package handlers

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"panel/config"
	"panel/models"
)

func RegisterClientPortal(r chi.Router, pool *pgxpool.Pool, cfg *config.Config) {
	// clientToken binds the session cookie to the exact credentials: a
	// forged cookie with just a username cannot read someone's usage page,
	// and rotating the proxy password invalidates old portal sessions.
	clientToken := func(username, password string) string {
		m := hmac.New(sha256.New, []byte(cfg.SecretKey))
		m.Write([]byte(username))
		m.Write([]byte{0})
		m.Write([]byte(password))
		return hex.EncodeToString(m.Sum(nil))
	}
	r.Get("/client/login", func(w http.ResponseWriter, r *http.Request) {
		renderTemplate(w, "client/login.html", nil)
	})

	r.Post("/client/login", func(w http.ResponseWriter, r *http.Request) {
		user := r.FormValue("username")
		pass := r.FormValue("password")

		var u models.ProxyUser
		err := pool.QueryRow(r.Context(),
			"SELECT id, username, password FROM proxy_users WHERE username=$1", user).
			Scan(&u.ID, &u.Username, &u.Password)

		if err != nil || u.Password != pass {
			renderTemplate(w, "client/login.html", map[string]any{"Error": "Invalid credentials"})
			return
		}

		http.SetCookie(w, &http.Cookie{
			Name:     "client_auth",
			Value:    u.Username + ":" + clientToken(u.Username, u.Password),
			Path:     "/client",
			HttpOnly: true,
			Expires:  time.Now().Add(24 * time.Hour),
		})
		
		if r.Header.Get("HX-Request") == "true" {
			w.Header().Set("HX-Redirect", "/client")
			w.WriteHeader(200)
		} else {
			http.Redirect(w, r, "/client", 302)
		}
	})
	
	r.Get("/client/logout", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{
			Name:     "client_auth",
			Value:    "",
			Path:     "/client",
			HttpOnly: true,
			Expires:  time.Now().Add(-1 * time.Hour),
		})
		http.Redirect(w, r, "/client/login", 302)
	})

	r.Get("/client", func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("client_auth")
		if err != nil || cookie.Value == "" {
			http.Redirect(w, r, "/client/login", 302)
			return
		}
		name, token, ok := strings.Cut(cookie.Value, ":")
		if !ok {
			http.Redirect(w, r, "/client/login", 302)
			return
		}

		var u models.ProxyUser
		err = pool.QueryRow(r.Context(),
			`SELECT id, username, password, listener_id, bandwidth_in, bandwidth_out, allowed_ips, enabled, traffic_limit, traffic_used, expires_at 
			 FROM proxy_users WHERE username=$1`, name).
			Scan(&u.ID, &u.Username, &u.Password, &u.ListenerID, &u.BandwidthIn, &u.BandwidthOut, &u.AllowedIPs, &u.Enabled, &u.TrafficLimit, &u.TrafficUsed, &u.ExpiresAt)

		if err != nil || !hmac.Equal([]byte(token), []byte(clientToken(u.Username, u.Password))) {
			http.Redirect(w, r, "/client/login", 302)
			return
		}

		if u.ListenerID != nil {
			u.Listener = enrichListenerByID(r, pool, *u.ListenerID)
		}

		renderTemplate(w, "client/dashboard.html", map[string]any{"User": u})
	})
}
