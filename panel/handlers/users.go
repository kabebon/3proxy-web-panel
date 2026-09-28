package handlers

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
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

	// GET /users/new — empty create form
	r.Get("/new", func(w http.ResponseWriter, r *http.Request) {
		listeners, _ := loadListeners(r, pool)
		renderTemplate(w, "user-form", map[string]any{
			"User":      models.ProxyUser{},
			"Listeners": listeners,
			"IsEdit":    false,
		})
	})

	// POST /users — create user
	r.Post("/", func(w http.ResponseWriter, r *http.Request) {
		username := r.FormValue("username")
		password := r.FormValue("password")
		listenerID := nullableIntForm(r, "listener_id")
		bin, _ := strconv.Atoi(r.FormValue("bandwidth_in"))
		bout, _ := strconv.Atoi(r.FormValue("bandwidth_out"))
		enabled := r.FormValue("enabled") == "on"
		allowedIPs := r.FormValue("allowed_ips")
		
		trafficLimitMB, _ := strconv.ParseInt(r.FormValue("traffic_limit"), 10, 64)
		trafficLimit := trafficLimitMB * 1024 * 1024 // convert MB to bytes

		var uid int
		err := pool.QueryRow(r.Context(),
			`INSERT INTO proxy_users (username, password, listener_id, bandwidth_in, bandwidth_out, allowed_ips, enabled, traffic_limit)
             VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`,
			username, password, listenerID, bin, bout, allowedIPs, enabled, trafficLimit,
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
		
		trafficLimitMB, _ := strconv.ParseInt(r.FormValue("traffic_limit"), 10, 64)
		trafficLimit := trafficLimitMB * 1024 * 1024

		// Update password only if provided
		pass := r.FormValue("password")
		var err error
		if pass != "" {
			_, err = pool.Exec(r.Context(),
				`UPDATE proxy_users SET username=$1, password=$2, listener_id=$3,
                 bandwidth_in=$4, bandwidth_out=$5, allowed_ips=$6, enabled=$7, traffic_limit=$8 WHERE id=$9`,
				r.FormValue("username"), pass, listenerID, bin, bout, r.FormValue("allowed_ips"), enabled, trafficLimit, id)
		} else {
			_, err = pool.Exec(r.Context(),
				`UPDATE proxy_users SET username=$1, listener_id=$2,
                 bandwidth_in=$3, bandwidth_out=$4, allowed_ips=$5, enabled=$6, traffic_limit=$7 WHERE id=$8`,
				r.FormValue("username"), listenerID, bin, bout, r.FormValue("allowed_ips"), enabled, trafficLimit, id)
		}
		if err != nil {
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
