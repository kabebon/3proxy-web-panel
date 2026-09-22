package handlers

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"panel/models"
)

func ListenersHandlers(pool *pgxpool.Pool) http.Handler {
	r := chi.NewRouter()

	// GET /listeners — list
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		listeners, err := loadListeners(r, pool)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		allUpstreams, _ := loadAllUpstreams(r, pool)
		allGroups, _ := loadGroups(r, pool)

		// Count users per listener
		userCounts := map[int]int{}
		rows, err := pool.Query(r.Context(), "SELECT listener_id, COUNT(*) FROM proxy_users WHERE listener_id IS NOT NULL GROUP BY listener_id")
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var lid, cnt int
				rows.Scan(&lid, &cnt)
				userCounts[lid] = cnt
			}
		}

		render(w, r, "listeners/index.html", map[string]any{
			"Listeners":  listeners,
			"Upstreams":  allUpstreams,
			"Groups":     allGroups,
			"UserCounts": userCounts,
		})
	})

	// GET /listeners/new
	r.Get("/new", func(w http.ResponseWriter, r *http.Request) {
		allUpstreams, _ := loadAllUpstreams(r, pool)
		allGroups, _ := loadGroups(r, pool)
		renderTemplate(w, "listener-form", map[string]any{
			"Listener":  models.Listener{BindIP: "0.0.0.0"},
			"Upstreams": allUpstreams,
			"Groups":    allGroups,
			"IsEdit":    false,
		})
	})

	// POST /listeners — create
	r.Post("/", func(w http.ResponseWriter, r *http.Request) {
		port, err := strconv.Atoi(r.FormValue("port"))
		if err != nil || port < 1 || port > 65535 {
			http.Error(w, "invalid port", 400)
			return
		}
		enabled := r.FormValue("enabled") == "on"
		bindIP := r.FormValue("bind_ip")
		if bindIP == "" {
			bindIP = "0.0.0.0"
		}
		upstreamID := nullableIntForm(r, "upstream_id")
		groupID := nullableIntForm(r, "upstream_group_id")
		// Mutually exclusive
		if upstreamID != nil && groupID != nil {
			groupID = nil
		}

		var l models.Listener
		err = pool.QueryRow(r.Context(),
			`INSERT INTO listeners (name, protocol, port, bind_ip, upstream_id, upstream_group_id, enabled)
             VALUES ($1,$2,$3,$4,$5,$6,$7)
             RETURNING id, name, protocol, port, bind_ip, upstream_id, upstream_group_id, enabled`,
			r.FormValue("name"), r.FormValue("protocol"), port, bindIP, upstreamID, groupID, enabled,
		).Scan(&l.ID, &l.Name, &l.Protocol, &l.Port, &l.BindIP, &l.UpstreamID, &l.UpstreamGroupID, &l.Enabled)
		if err != nil {
			triggerToast(w, "Error: "+err.Error(), "error")
			http.Error(w, err.Error(), 500)
			return
		}

		// Load upstream/group details for the row
		l = enrichListener(r, pool, l)

		triggerToast(w, "Listener created", "success")
		w.Header().Set("HX-Reswap", "afterbegin")
		w.Header().Set("HX-Retarget", "#listeners-tbody")
		renderTemplate(w, "listener-row", map[string]any{"Listener": l, "UserCount": 0})
	})

	// GET /listeners/{id}/edit
	r.Get("/{id}/edit", func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		listeners, err := loadListeners(r, pool)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		var found *models.Listener
		for i := range listeners {
			if strconv.Itoa(listeners[i].ID) == id {
				found = &listeners[i]
				break
			}
		}
		if found == nil {
			http.Error(w, "not found", 404)
			return
		}
		allUpstreams, _ := loadAllUpstreams(r, pool)
		allGroups, _ := loadGroups(r, pool)
		renderTemplate(w, "listener-form", map[string]any{
			"Listener":  *found,
			"Upstreams": allUpstreams,
			"Groups":    allGroups,
			"IsEdit":    true,
		})
	})

	// PUT /listeners/{id}
	r.Put("/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		port, err := strconv.Atoi(r.FormValue("port"))
		if err != nil || port < 1 || port > 65535 {
			http.Error(w, "invalid port", 400)
			return
		}
		enabled := r.FormValue("enabled") == "on"
		bindIP := r.FormValue("bind_ip")
		if bindIP == "" {
			bindIP = "0.0.0.0"
		}
		upstreamID := nullableIntForm(r, "upstream_id")
		groupID := nullableIntForm(r, "upstream_group_id")
		if upstreamID != nil && groupID != nil {
			groupID = nil
		}

		var l models.Listener
		err = pool.QueryRow(r.Context(),
			`UPDATE listeners SET name=$1, protocol=$2, port=$3, bind_ip=$4,
             upstream_id=$5, upstream_group_id=$6, enabled=$7
             WHERE id=$8
             RETURNING id, name, protocol, port, bind_ip, upstream_id, upstream_group_id, enabled`,
			r.FormValue("name"), r.FormValue("protocol"), port, bindIP, upstreamID, groupID, enabled, id,
		).Scan(&l.ID, &l.Name, &l.Protocol, &l.Port, &l.BindIP, &l.UpstreamID, &l.UpstreamGroupID, &l.Enabled)
		if err != nil {
			triggerToast(w, "Error: "+err.Error(), "error")
			http.Error(w, err.Error(), 500)
			return
		}

		l = enrichListener(r, pool, l)

		var userCount int
		pool.QueryRow(r.Context(), "SELECT COUNT(*) FROM proxy_users WHERE listener_id=$1", l.ID).Scan(&userCount)

		triggerToast(w, "Listener updated", "success")
		renderTemplate(w, "listener-row", map[string]any{"Listener": l, "UserCount": userCount})
	})

	// DELETE /listeners/{id}
	r.Delete("/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if _, err := pool.Exec(r.Context(), "DELETE FROM listeners WHERE id=$1", id); err != nil {
			triggerToast(w, "Error: "+err.Error(), "error")
			http.Error(w, err.Error(), 500)
			return
		}
		triggerToast(w, "Listener deleted", "success")
		w.WriteHeader(200)
	})

	return r
}

// loadListeners loads all listeners from DB with upstream/group info.
func loadListeners(r *http.Request, pool *pgxpool.Pool) ([]models.Listener, error) {
	rows, err := pool.Query(r.Context(),
		`SELECT id, name, protocol, port, bind_ip, upstream_id, upstream_group_id, enabled
         FROM listeners ORDER BY port`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var listeners []models.Listener
	for rows.Next() {
		var l models.Listener
		if err := rows.Scan(&l.ID, &l.Name, &l.Protocol, &l.Port, &l.BindIP, &l.UpstreamID, &l.UpstreamGroupID, &l.Enabled); err != nil {
			return nil, err
		}
		l = enrichListener(r, pool, l)
		listeners = append(listeners, l)
	}
	return listeners, nil
}

// enrichListener loads upstream or group details into a Listener.
func enrichListener(r *http.Request, pool *pgxpool.Pool, l models.Listener) models.Listener {
	if l.UpstreamID != nil {
		var u models.Upstream
		err := pool.QueryRow(r.Context(),
			"SELECT id, name, type, host, port, username, enabled FROM upstreams WHERE id=$1",
			*l.UpstreamID).Scan(&u.ID, &u.Name, &u.Type, &u.Host, &u.Port, &u.Username, &u.Enabled)
		if err == nil {
			l.Upstream = &u
		}
	} else if l.UpstreamGroupID != nil {
		var g models.UpstreamGroup
		pool.QueryRow(r.Context(),
			"SELECT id, name, enabled FROM upstream_groups WHERE id=$1",
			*l.UpstreamGroupID).Scan(&g.ID, &g.Name, &g.Enabled)
		l.UpstreamGroup = &g
	}
	return l
}

// nullableIntForm parses a form field as *int; returns nil if empty or 0.
func nullableIntForm(r *http.Request, field string) *int {
	v := r.FormValue(field)
	if v == "" || v == "0" {
		return nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n == 0 {
		return nil
	}
	return &n
}
