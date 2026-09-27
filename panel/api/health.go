package api

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/proxy"
	"panel/models"
)

// The health checker verifies that an upstream can actually reach the AI
// services the product is sold for — not merely that its TCP port answers.
// A probe opens the full chain (SOCKS5/HTTP-proxy CONNECT through the
// upstream, TLS handshake, HTTP request to each target domain); the upstream
// stays UP as long as at least one target answers with a non-blocked HTTP
// status. DOWN upstreams are removed from active routing (see config_gen.go
// for groups, failoverUpstream for sticky listeners) and the admin is alerted.

// StartHealthChecker runs the background prober loop. Mode "off" skips all
// checks; "monitor" only records status and alerts; "auto" additionally
// applies failover and config regeneration on state transitions.
func (s *Server) StartHealthChecker() {
	log.Printf("[health] checker started: mode=%s interval=%ds timeout=%ds fails=%d targets=%v",
		s.cfg.HealthcheckMode, s.cfg.HealthcheckSeconds, s.cfg.HealthcheckTimeout,
		s.cfg.HealthcheckFails, s.cfg.HealthcheckTargets)
	go func() {
		for {
			time.Sleep(time.Duration(s.cfg.HealthcheckSeconds) * time.Second)
			if s.cfg.HealthcheckMode == "off" {
				continue
			}
			s.healthRound()
		}
	}()
}

type checkResult struct {
	up       models.Upstream
	probeOK  bool
	probeErr string
}

func (s *Server) healthRound() {
	ctx := context.Background()
	rows, err := s.pool.Query(ctx,
		`SELECT id, name, type, host, port, username, password FROM upstreams WHERE enabled ORDER BY id`)
	if err != nil {
		log.Printf("[health] load upstreams: %v", err)
		return
	}
	var ups []models.Upstream
	for rows.Next() {
		var u models.Upstream
		if err := rows.Scan(&u.ID, &u.Name, &u.Type, &u.Host, &u.Port, &u.Username, &u.Password); err == nil {
			ups = append(ups, u)
		}
	}
	rows.Close()
	if len(ups) == 0 {
		return
	}

	timeout := time.Duration(s.cfg.HealthcheckTimeout) * time.Second
	results := make([]checkResult, len(ups))
	var wg sync.WaitGroup
	for i, u := range ups {
		wg.Add(1)
		go func(i int, u models.Upstream) {
			defer wg.Done()
			ok, errs := probeUpstream(ctx, u, s.cfg.HealthcheckTargets, timeout)
			results[i] = checkResult{up: u, probeOK: ok, probeErr: errs}
		}(i, u)
	}
	wg.Wait()

	for _, res := range results {
		s.processCheck(ctx, res)
	}
}

// probeUpstream runs the AI-reachability probe through the upstream itself.
// Returns (true, "") when at least one target is reachable; otherwise
// (false, per-target failure reasons for the health record / alert). All
// probes are bounded by the per-request context: connect through the proxy,
// TLS handshake and response headers must each fit inside the timeout.
func probeUpstream(ctx context.Context, u models.Upstream, targets []string, timeout time.Duration) (bool, string) {
	transport, err := proxyTransport(u)
	if err != nil {
		return false, "dialer: " + err.Error()
	}
	client := &http.Client{Transport: transport, Timeout: timeout}
	var fails []string
	for _, t := range targets {
		rctx, cancel := context.WithTimeout(ctx, timeout)
		req, rerr := http.NewRequestWithContext(rctx, http.MethodGet, "https://"+t+"/", nil)
		if rerr != nil {
			cancel()
			fails = append(fails, t+": "+rerr.Error())
			continue
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 HealthCheck")
		resp, derr := client.Do(req)
		cancel()
		if derr != nil {
			fails = append(fails, t+": "+derr.Error())
			continue
		}
		resp.Body.Close()
		// 403/451 from the AI edge means this exit IP is blocked — the proxy
		// is useless for the product even though the chain itself works.
		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnavailableForLegalReasons {
			fails = append(fails, fmt.Sprintf("%s: blocked (HTTP %d)", t, resp.StatusCode))
			continue
		}
		// Any other status (401/404/redirect...) proves TCP + proxy chain +
		// TLS handshake + a real HTTP response from the target domain.
		return true, ""
	}
	return false, strings.Join(fails, "; ")
}

// proxyTransport builds an http.RoundTripper whose connections are dialed
// through the upstream under test (SOCKS5 or HTTP proxy).
func proxyTransport(u models.Upstream) (http.RoundTripper, error) {
	if u.Type == "socks5" {
		var auth *proxy.Auth
		if u.Username != "" {
			auth = &proxy.Auth{User: u.Username, Password: u.Password}
		}
		d, err := proxy.SOCKS5("tcp", net.JoinHostPort(u.Host, strconv.Itoa(u.Port)), auth, proxy.Direct)
		if err != nil {
			return nil, err
		}
		cd, ok := d.(proxy.ContextDialer)
		if !ok {
			return nil, fmt.Errorf("socks5 dialer without context support")
		}
		return &http.Transport{
			DialContext:         cd.DialContext,
			DisableKeepAlives:   true,
			TLSHandshakeTimeout: timeoutForHandshake,
			ForceAttemptHTTP2:   false,
			TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
		}, nil
	}
	pu := &url.URL{Scheme: "http", Host: net.JoinHostPort(u.Host, strconv.Itoa(u.Port))}
	if u.Username != "" {
		pu.User = url.UserPassword(u.Username, u.Password)
	}
	return &http.Transport{
		Proxy:               http.ProxyURL(pu),
		DisableKeepAlives:   true,
		TLSHandshakeTimeout: timeoutForHandshake,
		ForceAttemptHTTP2:   false,
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
	}, nil
}

const timeoutForHandshake = 15 * time.Second

func truncStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// processCheck updates upstream_health and acts on state transitions:
// DOWN -> failover (auto mode) + alert; recovery -> re-apply + alert.
func (s *Server) processCheck(ctx context.Context, res checkResult) {
	var prevStatus string
	var prevFails int
	err := s.pool.QueryRow(ctx,
		`SELECT status, fail_count FROM upstream_health WHERE upstream_id=$1`, res.up.ID).
		Scan(&prevStatus, &prevFails)
	if err != nil {
		// never checked before: presumed healthy until failures cross the
		// configured threshold
		prevStatus, prevFails = "up", 0
	}

	newStatus, fails := prevStatus, prevFails
	if res.probeOK {
		newStatus, fails = "up", 0
	} else {
		fails = prevFails + 1
		if fails >= s.cfg.HealthcheckFails {
			newStatus = "down"
		}
	}

	now := time.Now().UTC()
	var upAt, downAt *time.Time
	if newStatus == "up" {
		upAt = &now
	} else if newStatus == "down" {
		downAt = &now
	}
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO upstream_health (upstream_id, status, fail_count, last_checked_at, last_up_at, last_down_at, last_error)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (upstream_id) DO UPDATE SET
			status = EXCLUDED.status,
			fail_count = EXCLUDED.fail_count,
			last_checked_at = EXCLUDED.last_checked_at,
			last_up_at = COALESCE(EXCLUDED.last_up_at, upstream_health.last_up_at),
			last_down_at = COALESCE(EXCLUDED.last_down_at, upstream_health.last_down_at),
			last_error = EXCLUDED.last_error`,
		res.up.ID, newStatus, fails, now, upAt, downAt, truncStr(res.probeErr, 400)); err != nil {
		log.Printf("[health] upstream %d: save state: %v", res.up.ID, err)
		return
	}

	wentDown := newStatus == "down" && prevStatus != "down"
	recovered := newStatus == "up" && prevStatus == "down"
	if !wentDown && !recovered {
		return
	}

	if wentDown {
		switched := ""
		if s.cfg.HealthcheckMode == "auto" {
			switched = s.failoverUpstream(ctx, res.up.ID)
			if err := s.applyNow(ctx); err != nil {
				log.Printf("[health] apply after upstream %d DOWN failed: %v", res.up.ID, err)
			}
		}
		s.alertDown(res.up, res.probeErr, switched)
		return
	}

	// Recovery: group weights are restored by regeneration (DB weights were
	// never modified). Sticky listeners are intentionally NOT switched back —
	// that would flap the customer's exit IP; rotate via the API instead.
	if s.cfg.HealthcheckMode == "auto" {
		if err := s.applyNow(ctx); err != nil {
			log.Printf("[health] apply after upstream %d recovery failed: %v", res.up.ID, err)
		}
	}
	s.alertUp(res.up)
}

// failoverUpstream re-points enabled listeners bound directly to the dead
// upstream at a healthy replacement (same type preferred). Balancing groups
// need no rewrite: the config generator already emits DOWN members with
// weight 0 (3proxy fallback), instantly removing them from routing.
func (s *Server) failoverUpstream(ctx context.Context, deadID int) string {
	var deadType string
	_ = s.pool.QueryRow(ctx, `SELECT type FROM upstreams WHERE id=$1`, deadID).Scan(&deadType)

	rows, err := s.pool.Query(ctx,
		`SELECT id, name FROM listeners WHERE enabled AND upstream_id = $1 ORDER BY id`, deadID)
	if err != nil {
		return ""
	}
	type boundListener struct {
		id   int
		name string
	}
	var toMove []boundListener
	for rows.Next() {
		var b boundListener
		if err := rows.Scan(&b.id, &b.name); err == nil {
			toMove = append(toMove, b)
		}
	}
	rows.Close()

	var moved []string
	for _, b := range toMove {
		var repl int
		err := s.pool.QueryRow(ctx, `
			SELECT u.id FROM upstreams u
			JOIN upstream_health h ON h.upstream_id = u.id AND h.status = 'up'
			WHERE u.enabled AND u.id <> $1
			ORDER BY (u.type = $2) DESC, u.id
			LIMIT 1`, deadID, deadType).Scan(&repl)
		if err != nil {
			continue // no healthy replacement available
		}
		if _, err := s.pool.Exec(ctx,
			`UPDATE listeners SET upstream_id=$1, upstream_group_id=NULL WHERE id=$2`, repl, b.id); err == nil {
			moved = append(moved, fmt.Sprintf("%s→upstream#%d", b.name, repl))
		}
	}
	return strings.Join(moved, ", ")
}
