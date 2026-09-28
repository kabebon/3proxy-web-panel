package handlers

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"panel/gen"
	"panel/models"
)

func UsersHandlers(pool *pgxpool.Pool) http.Handler {
	r := chi.NewRouter()

	// GET /users — list all users with listener info
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		users, err := loadUsers(r, pool)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		listeners, err := loadListeners(r, pool)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		render(w, r, "users/index.html", map[string]any{
			"Users":     users,
			"Listeners": listeners,
		})
	})

	// GET /users/new — create form with pre-generated credentials
	r.Get("/new", func(w http.ResponseWriter, r *http.Request) {
		listeners, _ := loadListeners(r, pool)
		renderTemplate(w, "user-form", map[string]any{
			"User":        models.ProxyUser{},
			"Listeners":   listeners,
			"IsEdit":      false,
			"GenUsername": gen.Username(),
			"GenPassword": gen.Password(),
		})
	})

	// POST /users — create user
	r.Post("/", func(w http.ResponseWriter, r *http.Request) {
		username := r.FormValue("username")
		password := r.FormValue("password")
		if username == "" {
			username = gen.Username() // server-side fallback for no-JS clients
		}
		if password == "" {
			password = gen.Password()
		}
		listenerID := nullableIntForm(r, "listener_id")
		bin, _ := strconv.Atoi(r.FormValue("bandwidth_in"))
		bout, _ := strconv.Atoi(r.FormValue("bandwidth_out"))
		enabled := r.FormValue("enabled") == "on"
		allowedIPs := r.FormValue("allowed_ips")
		expiresAt := parseExpiresAtForm(r.FormValue("expires_at"))
		limitBytes, err := parseTrafficLimitForm(r.FormValue("traffic_limit_gb"))
		if err != nil {
			triggerToast(w, "Error: traffic limit: "+err.Error(), "error")
			http.Error(w, err.Error(), 400)
			return
		}

		var uid int
		err = pool.QueryRow(r.Context(),
			`INSERT INTO proxy_users (username, password, listener_id, bandwidth_in, bandwidth_out, allowed_ips, enabled, expires_at, traffic_limit)
             VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id`,
			username, password, listenerID, bin, bout, allowedIPs, enabled, expiresAt, limitBytes,
		).Scan(&uid)
		if err != nil {
			triggerToast(w, "Error: "+err.Error(), "error")
			http.Error(w, err.Error(), 500)
			return
		}

		u, err := loadUser(r, pool, uid)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		triggerToast(w, "User created", "success")
		w.Header().Set("HX-Reswap", "afterbegin")
		w.Header().Set("HX-Retarget", "#users-tbody")
		renderTemplate(w, "user-row", u)
	})

	// GET /users/{id}/edit — edit form
	r.Get("/{id}/edit", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.Atoi(chi.URLParam(r, "id"))
		u, err := loadUser(r, pool, id)
		if err != nil {
			http.Error(w, "not found", 404)
			return
		}
		listeners, _ := loadListeners(r, pool)
		renderTemplate(w, "user-form", map[string]any{
			"User":      u,
			"Listeners": listeners,
			"IsEdit":    true,
		})
	})

	// PUT /users/{id} — update user
	r.Put("/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		listenerID := nullableIntForm(r, "listener_id")
		bin, _ := strconv.Atoi(r.FormValue("bandwidth_in"))
		bout, _ := strconv.Atoi(r.FormValue("bandwidth_out"))
		enabled := r.FormValue("enabled") == "on"
		expiresAt := parseExpiresAtForm(r.FormValue("expires_at"))
		limitBytes, err := parseTrafficLimitForm(r.FormValue("traffic_limit_gb"))
		if err != nil {
			triggerToast(w, "Error: traffic limit: "+err.Error(), "error")
			http.Error(w, err.Error(), 400)
			return
		}
		resetUsed := r.FormValue("reset_traffic_used") == "on"

		// Update password only if provided
		pass := r.FormValue("password")
		if pass != "" {
			_, err = pool.Exec(r.Context(),
				`UPDATE proxy_users SET username=$1, password=$2, listener_id=$3,
                 bandwidth_in=$4, bandwidth_out=$5, allowed_ips=$6, enabled=$7,
                 expires_at=$8, traffic_limit=$9 WHERE id=$10`,
				r.FormValue("username"), pass, listenerID, bin, bout, r.FormValue("allowed_ips"), enabled,
				expiresAt, limitBytes, id)
		} else {
			_, err = pool.Exec(r.Context(),
				`UPDATE proxy_users SET username=$1, listener_id=$2,
                 bandwidth_in=$3, bandwidth_out=$4, allowed_ips=$5, enabled=$6,
                 expires_at=$7, traffic_limit=$8 WHERE id=$9`,
				r.FormValue("username"), listenerID, bin, bout, r.FormValue("allowed_ips"), enabled,
				expiresAt, limitBytes, id)
		}
		if err != nil {
			triggerToast(w, "Error: "+err.Error(), "error")
			http.Error(w, err.Error(), 500)
			return
		}
		if resetUsed {
			if _, err := pool.Exec(r.Context(),
				`UPDATE proxy_users SET traffic_used=0 WHERE id=$1`, id); err != nil {
				triggerToast(w, "Error: "+err.Error(), "error")
				http.Error(w, err.Error(), 500)
				return
			}
		}

		uid, _ := strconv.Atoi(id)
		u, err := loadUser(r, pool, uid)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		triggerToast(w, "User updated — press Apply Config to activate", "success")
		renderTemplate(w, "user-row", u)
	})

	// DELETE /users/{id}
	r.Delete("/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if _, err := pool.Exec(r.Context(), "DELETE FROM proxy_users WHERE id=$1", id); err != nil {
			triggerToast(w, "Error: "+err.Error(), "error")
			http.Error(w, err.Error(), 500)
			return
		}
		triggerToast(w, "User deleted", "success")
		w.WriteHeader(200)
	})

	// POST /users/{id}/toggle — enable/disable
	r.Post("/{id}/toggle", func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if _, err := pool.Exec(r.Context(),
			"UPDATE proxy_users SET enabled = NOT enabled WHERE id=$1", id); err != nil {
			triggerToast(w, "Error: "+err.Error(), "error")
			http.Error(w, err.Error(), 500)
			return
		}
		uid, _ := strconv.Atoi(id)
		u, err := loadUser(r, pool, uid)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		triggerToast(w, "User updated", "success")
		renderTemplate(w, "user-row", u)
	})

	return r
}

// loadUsers loads all proxy users with listener details.
func loadUsers(r *http.Request, pool *pgxpool.Pool) ([]models.ProxyUser, error) {
	rows, err := pool.Query(r.Context(),
		`SELECT u.id, u.username, u.password, u.listener_id, u.bandwidth_in, u.bandwidth_out,
                u.allowed_ips, u.enabled, u.traffic_limit, u.traffic_used, u.expires_at
         FROM proxy_users u ORDER BY u.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []models.ProxyUser
	for rows.Next() {
		var u models.ProxyUser
		if err := rows.Scan(&u.ID, &u.Username, &u.Password, &u.ListenerID,
			&u.BandwidthIn, &u.BandwidthOut, &u.AllowedIPs, &u.Enabled, &u.TrafficLimit, &u.TrafficUsed, &u.ExpiresAt); err != nil {
			return nil, err
		}
		users = append(users, u)
	}

	// Enrich with listener info
	for i := range users {
		if users[i].ListenerID != nil {
			l := enrichListenerByID(r, pool, *users[i].ListenerID)
			users[i].Listener = l
		}
	}
	return users, nil
}

// loadUser loads a single proxy user with listener details.
func loadUser(r *http.Request, pool *pgxpool.Pool, id int) (models.ProxyUser, error) {
	var u models.ProxyUser
	err := pool.QueryRow(r.Context(),
		`SELECT id, username, password, listener_id, bandwidth_in, bandwidth_out,
                allowed_ips, enabled, traffic_limit, traffic_used, expires_at
         FROM proxy_users WHERE id=$1`, id).
		Scan(&u.ID, &u.Username, &u.Password, &u.ListenerID,
			&u.BandwidthIn, &u.BandwidthOut, &u.AllowedIPs, &u.Enabled, &u.TrafficLimit, &u.TrafficUsed, &u.ExpiresAt)
	if err != nil {
		return u, err
	}
	if u.ListenerID != nil {
		u.Listener = enrichListenerByID(r, pool, *u.ListenerID)
	}
	return u, nil
}

// parseExpiresAtForm converts the datetime-local form value ("2006-01-02T15:04")
// into a *time.Time; empty string means "no expiry".
func parseExpiresAtForm(v string) *time.Time {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	t, err := time.Parse("2006-01-02T15:04", v)
	if err != nil {
		return nil
	}
	return &t
}

// parseTrafficLimitForm converts the "Traffic Limit (GB)" form value to
// bytes; empty or 0 = unlimited.
func parseTrafficLimitForm(v string) (int64, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, nil
	}
	gb, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid number %q", v)
	}
	if gb < 0 || gb > 1e6 {
		return 0, fmt.Errorf("must be 0..1000000 GB")
	}
	return int64(gb * 1073741824), nil
}

// enrichListenerByID loads a Listener by ID without its upstream details.
func enrichListenerByID(r *http.Request, pool *pgxpool.Pool, id int) *models.Listener {
	var l models.Listener
	err := pool.QueryRow(r.Context(),
		`SELECT id, name, protocol, port, bind_ip, upstream_id, upstream_group_id, enabled
         FROM listeners WHERE id=$1`, id).
		Scan(&l.ID, &l.Name, &l.Protocol, &l.Port, &l.BindIP, &l.UpstreamID, &l.UpstreamGroupID, &l.Enabled)
	if err != nil {
		return nil
	}
	l = enrichListener(r, pool, l)
	return &l
}
