package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type contextKey string

const UserIDKey = contextKey("user_id")

func Auth(pool *pgxpool.Pool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie("panel_session")
			if err != nil {
				if r.Header.Get("HX-Request") == "true" {
					w.Header().Set("HX-Redirect", "/login")
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				http.Redirect(w, r, "/login", http.StatusFound)
				return
			}

			var adminID int
			var expiresAt time.Time
			err = pool.QueryRow(r.Context(), "SELECT admin_user_id, expires_at FROM sessions WHERE id = $1", cookie.Value).Scan(&adminID, &expiresAt)
			if err != nil || time.Now().After(expiresAt) {
				if err == nil {
					pool.Exec(r.Context(), "DELETE FROM sessions WHERE id = $1", cookie.Value)
				}
				if r.Header.Get("HX-Request") == "true" {
					w.Header().Set("HX-Redirect", "/login")
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				http.Redirect(w, r, "/login", http.StatusFound)
				return
			}

			// CSRF check for mutations
			if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
				csrfCookie, err := r.Cookie("csrf_token")
				csrfHeader := r.Header.Get("X-CSRF-Token")
				if err != nil || csrfHeader == "" || csrfCookie.Value != csrfHeader {
					http.Error(w, "Invalid CSRF token", http.StatusForbidden)
					return
				}
			}

			// Ensure CSRF token exists
			_, err = r.Cookie("csrf_token")
			if err != nil {
				b := make([]byte, 32)
				rand.Read(b)
				token := hex.EncodeToString(b)
				http.SetCookie(w, &http.Cookie{
					Name:     "csrf_token",
					Value:    token,
					Path:     "/",
					HttpOnly: false, // Accessible by JS for htmx if needed
					SameSite: http.SameSiteLaxMode,
				})
			}

			ctx := context.WithValue(r.Context(), UserIDKey, adminID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
