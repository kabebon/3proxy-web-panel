// Package api implements the /api/v1 REST surface consumed by the bot:
// token-authenticated user/upstream/listener management with automatic
// config regeneration + 3proxy restart after every mutation, plus a
// background reaper enforcing proxy_users.expires_at.
package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"panel/config"
	"panel/proxy"
)

type Server struct {
	pool *pgxpool.Pool
	cfg  *config.Config

	applyMu sync.Mutex
	lastFP  string
}

func New(pool *pgxpool.Pool, cfg *config.Config) *Server {
	return &Server{pool: pool, cfg: cfg}
}

// Mount registers the API on the root router at /api/v1.
func (s *Server) Mount(r chi.Router) {
	a := chi.NewRouter()
	a.Use(s.requireToken)

	a.Get("/health", s.health)
	a.Post("/apply", s.apply)

	a.Route("/users", func(a chi.Router) {
		a.Get("/", s.listUsers)
		a.Post("/", s.createUser)
		a.Get("/{id}", s.getUser)
		a.Patch("/{id}", s.patchUser)
		a.Delete("/{id}", s.deleteUser)
		a.Post("/{id}/extend", s.extendUser)
		a.Post("/{id}/rotate", s.rotateUser)
	})

	a.Route("/upstreams", func(a chi.Router) {
		a.Get("/", s.listUpstreams)
		a.Post("/", s.createUpstream)
		a.Get("/{id}", s.getUpstream)
		a.Patch("/{id}", s.patchUpstream)
		a.Delete("/{id}", s.deleteUpstream)
	})

	a.Route("/groups", func(a chi.Router) {
		a.Get("/", s.listGroups)
		a.Post("/", s.createGroup)
		a.Delete("/{id}", s.deleteGroup)
	})

	a.Route("/listeners", func(a chi.Router) {
		a.Get("/", s.listListeners)
		a.Post("/", s.createListener)
		a.Patch("/{id}", s.patchListener)
		a.Delete("/{id}", s.deleteListener)
	})

	r.Mount("/api/v1", a)
}

// requireToken authenticates requests via "Authorization: Bearer <token>"
// against the configured API_TOKENS (constant-time compare). With no tokens
// configured the whole API answers 404 — it simply does not exist.
func (s *Server) requireToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(s.cfg.APITokens) == 0 {
			writeErr(w, http.StatusNotFound, "api disabled")
			return
		}
		h := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if len(h) <= len(prefix) || h[:len(prefix)] != prefix {
			writeErr(w, http.StatusUnauthorized, "missing bearer token")
			return
		}
		present := h[len(prefix):]
		ok := false
		for _, t := range s.cfg.APITokens {
			if len(t) == len(present) && subtle.ConstantTimeCompare([]byte(t), []byte(present)) == 1 {
				ok = true
				break
			}
		}
		if !ok {
			writeErr(w, http.StatusUnauthorized, "invalid token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// applyNow regenerates the 3proxy config from the DB, writes it and reloads
// 3proxy (supervisor restarts the process on SIGHUP). Serialized so
// concurrent API mutations cannot interleave config writes. The state
// fingerprint is captured AFTER a successful apply so the reaper does not
// re-apply changes already pushed by API mutations (no double restarts).
func (s *Server) applyNow(ctx context.Context) error {
	s.applyMu.Lock()
	defer s.applyMu.Unlock()
	content, err := proxy.GenerateConfig(ctx, s.pool, s.cfg.ProxyLogPath)
	if err != nil {
		return err
	}
	if err := proxy.WriteConfig(s.cfg.ProxyConfigPath, content); err != nil {
		return err
	}
	if err := proxy.ReloadProxy(s.cfg.ProxyContainerName); err != nil {
		return err
	}
	if fp, err := s.fingerprint(ctx); err == nil {
		s.lastFP = fp
	}
	return nil
}

func (s *Server) apply(w http.ResponseWriter, r *http.Request) {
	if err := s.applyNow(r.Context()); err != nil {
		writeErr(w, http.StatusInternalServerError, "apply failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"applied": true})
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	var users, listeners, upstreams int
	s.pool.QueryRow(r.Context(), "SELECT COUNT(*) FROM proxy_users WHERE enabled AND (expires_at IS NULL OR expires_at > NOW())").Scan(&users)
	s.pool.QueryRow(r.Context(), "SELECT COUNT(*) FROM listeners WHERE enabled").Scan(&listeners)
	s.pool.QueryRow(r.Context(), "SELECT COUNT(*) FROM upstreams WHERE enabled").Scan(&upstreams)
	writeJSON(w, http.StatusOK, map[string]any{
		"status":        "ok",
		"proxy_running": proxy.IsProxyRunning(s.cfg.ProxyContainerName),
		"users_active":  users,
		"listeners":     listeners,
		"upstreams":     upstreams,
	})
}

// StartReaper converges the live config when subscriptions expire without any
// API call: it fingerprints the set of users that should exist and regenerates
// the config whenever that fingerprint changes. Fingerprints are captured at
// apply time, so changes already applied by the API do not trigger it.
func (s *Server) StartReaper() {
	go func() {
		first := true
		for {
			time.Sleep(time.Duration(s.cfg.ReaperSeconds) * time.Second)
			fp, err := s.fingerprint(context.Background())
			if err != nil {
				continue
			}
			s.applyMu.Lock()
			changed := fp != s.lastFP
			s.applyMu.Unlock()
			if first || changed {
				_ = s.applyNow(context.Background())
				first = false
			}
		}
	}()
}

func (s *Server) fingerprint(ctx context.Context) (string, error) {
	var users, expired, listeners, upstreams int
	if err := s.pool.QueryRow(ctx, "SELECT COUNT(*) FROM proxy_users").Scan(&users); err != nil {
		return "", err
	}
	if err := s.pool.QueryRow(ctx, "SELECT COUNT(*) FROM proxy_users WHERE enabled AND expires_at IS NOT NULL AND expires_at <= NOW()").Scan(&expired); err != nil {
		return "", err
	}
	if err := s.pool.QueryRow(ctx, "SELECT COUNT(*) FROM listeners WHERE enabled").Scan(&listeners); err != nil {
		return "", err
	}
	if err := s.pool.QueryRow(ctx, "SELECT COUNT(*) FROM upstreams WHERE enabled").Scan(&upstreams); err != nil {
		return "", err
	}
	return fmtFingerprint(users, expired, listeners, upstreams), nil
}

func fmtFingerprint(users, expired, listeners, upstreams int) string {
	b, _ := json.Marshal([4]int{users, expired, listeners, upstreams})
	return string(b)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// decodeBody decodes a JSON body into dst; empty bodies decode as zero values.
func decodeBody(r *http.Request, dst any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		if err.Error() == "EOF" {
			return nil
		}
		return err
	}
	return nil
}
