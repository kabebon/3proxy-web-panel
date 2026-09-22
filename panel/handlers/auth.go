package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

func RegisterAuth(r chi.Router, pool *pgxpool.Pool) {

	// GET /login
	r.Get("/login", func(w http.ResponseWriter, r *http.Request) {
		// If already logged in, redirect to dashboard
		if cookie, err := r.Cookie("panel_session"); err == nil {
			var count int
			pool.QueryRow(r.Context(),
				"SELECT COUNT(*) FROM sessions WHERE id=$1 AND expires_at > NOW()", cookie.Value).Scan(&count)
			if count > 0 {
				http.Redirect(w, r, "/", http.StatusFound)
				return
			}
		}
		renderTemplate(w, "login.html", nil)
	})

	// POST /login
	r.Post("/login", func(w http.ResponseWriter, r *http.Request) {
		username := r.FormValue("username")
		password := r.FormValue("password")

		var id int
		var hash string
		err := pool.QueryRow(r.Context(),
			"SELECT id, password_hash FROM admin_users WHERE username=$1", username).Scan(&id, &hash)
		if err != nil || bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
			w.WriteHeader(http.StatusUnauthorized)
			renderTemplate(w, "login.html", map[string]string{"Error": "Invalid username or password"})
			return
		}

		// Generate session token
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			http.Error(w, "internal error", 500)
			return
		}
		token := hex.EncodeToString(b)
		expires := time.Now().Add(24 * time.Hour)

		if _, err = pool.Exec(r.Context(),
			"INSERT INTO sessions (id, admin_user_id, expires_at) VALUES ($1,$2,$3)",
			token, id, expires); err != nil {
			http.Error(w, "internal error", 500)
			return
		}

		http.SetCookie(w, &http.Cookie{
			Name:     "panel_session",
			Value:    token,
			Expires:  expires,
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		})

		if r.Header.Get("HX-Request") == "true" {
			w.Header().Set("HX-Redirect", "/")
			w.WriteHeader(200)
			return
		}
		http.Redirect(w, r, "/", http.StatusFound)
	})

	// POST /logout
	r.Post("/logout", func(w http.ResponseWriter, r *http.Request) {
		if cookie, err := r.Cookie("panel_session"); err == nil {
			pool.Exec(r.Context(), "DELETE FROM sessions WHERE id=$1", cookie.Value)
		}
		http.SetCookie(w, &http.Cookie{
			Name:     "panel_session",
			Value:    "",
			Expires:  time.Unix(0, 0),
			MaxAge:   -1,
			Path:     "/",
			HttpOnly: true,
		})
		http.Redirect(w, r, "/login", http.StatusFound)
	})
}
