package handlers

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"panel/models"
)

func GroupsHandlers(pool *pgxpool.Pool) http.Handler {
	r := chi.NewRouter()

	// GET /groups — list all groups with their members
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		groups, err := loadGroups(r, pool)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		// Also load all upstreams for the form
		allUpstreams, err := loadAllUpstreams(r, pool)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		render(w, r, "groups/index.html", map[string]any{
			"Groups":    groups,
			"Upstreams": allUpstreams,
		})
	})

	// GET /groups/new — empty form
	r.Get("/new", func(w http.ResponseWriter, r *http.Request) {
		allUpstreams, _ := loadAllUpstreams(r, pool)
		renderTemplate(w, "group-form", map[string]any{
			"Group":     models.UpstreamGroup{},
			"Upstreams": allUpstreams,
			"IsEdit":    false,
		})
	})

	// POST /groups — create group + add members
	r.Post("/", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		name := r.FormValue("name")
		enabled := r.FormValue("enabled") == "on"
		upstreamIDs := r.Form["upstream_ids[]"]

		tx, err := pool.Begin(r.Context())
		if err != nil {
			triggerToast(w, "Error: "+err.Error(), "error")
			http.Error(w, err.Error(), 500)
			return
		}
		defer tx.Rollback(r.Context())

		var groupID int
		err = tx.QueryRow(r.Context(),
			"INSERT INTO upstream_groups (name, enabled) VALUES ($1, $2) RETURNING id",
			name, enabled).Scan(&groupID)
		if err != nil {
			triggerToast(w, "Error: "+err.Error(), "error")
			http.Error(w, err.Error(), 500)
			return
		}

		for _, uid := range upstreamIDs {
			tx.Exec(r.Context(),
				"INSERT INTO upstream_group_members (group_id, upstream_id) VALUES ($1, $2) ON CONFLICT DO NOTHING",
				groupID, uid)
		}

		if err := tx.Commit(r.Context()); err != nil {
			triggerToast(w, "Error: "+err.Error(), "error")
			http.Error(w, err.Error(), 500)
			return
		}

		group, _ := loadGroup(r, pool, groupID)
		triggerToast(w, "Group created", "success")
		w.Header().Set("HX-Reswap", "afterbegin")
		w.Header().Set("HX-Retarget", "#groups-tbody")
		renderTemplate(w, "group-row", group)
	})

	// GET /groups/{id}/edit
	r.Get("/{id}/edit", func(w http.ResponseWriter, r *http.Request) {
		gid, err := strconv.Atoi(chi.URLParam(r, "id"))
		if err != nil {
			http.Error(w, "invalid id", 400)
			return
		}
		group, err := loadGroup(r, pool, gid)
		if err != nil {
			http.Error(w, "not found", 404)
			return
		}
		allUpstreams, _ := loadAllUpstreams(r, pool)
		// Build set of selected upstream IDs
		selectedIDs := map[int]bool{}
		for _, m := range group.Members {
			selectedIDs[m.UpstreamID] = true
		}
		renderTemplate(w, "group-form", map[string]any{
			"Group":       group,
			"Upstreams":   allUpstreams,
			"SelectedIDs": selectedIDs,
			"IsEdit":      true,
		})
	})

	// PUT /groups/{id} — update name/enabled + replace members
	r.Put("/{id}", func(w http.ResponseWriter, r *http.Request) {
		gid, err := strconv.Atoi(chi.URLParam(r, "id"))
		if err != nil {
			http.Error(w, "invalid id", 400)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		name := r.FormValue("name")
		enabled := r.FormValue("enabled") == "on"
		upstreamIDs := r.Form["upstream_ids[]"]

		tx, err := pool.Begin(r.Context())
		if err != nil {
			triggerToast(w, "Error: "+err.Error(), "error")
			http.Error(w, err.Error(), 500)
			return
		}
		defer tx.Rollback(r.Context())

		if _, err := tx.Exec(r.Context(),
			"UPDATE upstream_groups SET name=$1, enabled=$2 WHERE id=$3",
			name, enabled, gid); err != nil {
			triggerToast(w, "Error: "+err.Error(), "error")
			http.Error(w, err.Error(), 500)
			return
		}

		tx.Exec(r.Context(), "DELETE FROM upstream_group_members WHERE group_id=$1", gid)
		for _, uid := range upstreamIDs {
			tx.Exec(r.Context(),
				"INSERT INTO upstream_group_members (group_id, upstream_id) VALUES ($1, $2) ON CONFLICT DO NOTHING",
				gid, uid)
		}

		if err := tx.Commit(r.Context()); err != nil {
			triggerToast(w, "Error: "+err.Error(), "error")
			http.Error(w, err.Error(), 500)
			return
		}

		group, _ := loadGroup(r, pool, gid)
		triggerToast(w, "Group updated", "success")
		renderTemplate(w, "group-row", group)
	})

	// DELETE /groups/{id}
	r.Delete("/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if _, err := pool.Exec(r.Context(), "DELETE FROM upstream_groups WHERE id=$1", id); err != nil {
			triggerToast(w, "Error: "+err.Error(), "error")
			http.Error(w, err.Error(), 500)
			return
		}
		triggerToast(w, "Group deleted", "success")
		w.WriteHeader(200)
	})

	return r
}

// loadGroups loads all groups with their upstream members.
func loadGroups(r *http.Request, pool *pgxpool.Pool) ([]models.UpstreamGroup, error) {
	rows, err := pool.Query(r.Context(),
		"SELECT id, name, enabled FROM upstream_groups ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var groups []models.UpstreamGroup
	for rows.Next() {
		var g models.UpstreamGroup
		if err := rows.Scan(&g.ID, &g.Name, &g.Enabled); err != nil {
			return nil, err
		}
		groups = append(groups, g)
	}

	for i := range groups {
		members, err := loadGroupMembers(r, pool, groups[i].ID)
		if err != nil {
			return nil, err
		}
		groups[i].Members = members
	}
	return groups, nil
}

func loadGroup(r *http.Request, pool *pgxpool.Pool, id int) (models.UpstreamGroup, error) {
	var g models.UpstreamGroup
	err := pool.QueryRow(r.Context(),
		"SELECT id, name, enabled FROM upstream_groups WHERE id=$1", id).
		Scan(&g.ID, &g.Name, &g.Enabled)
	if err != nil {
		return g, err
	}
	members, err := loadGroupMembers(r, pool, id)
	if err != nil {
		return g, err
	}
	g.Members = members
	return g, nil
}

func loadGroupMembers(r *http.Request, pool *pgxpool.Pool, groupID int) ([]models.UpstreamGroupMember, error) {
	mrows, err := pool.Query(r.Context(),
		`SELECT m.id, m.group_id, m.upstream_id, m.weight,
                u.id, u.name, u.type, u.host, u.port, u.username, u.enabled
         FROM upstream_group_members m
         JOIN upstreams u ON u.id = m.upstream_id
         WHERE m.group_id = $1 ORDER BY m.id`, groupID)
	if err != nil {
		return nil, err
	}
	defer mrows.Close()

	var members []models.UpstreamGroupMember
	for mrows.Next() {
		var m models.UpstreamGroupMember
		var u models.Upstream
		if err := mrows.Scan(&m.ID, &m.GroupID, &m.UpstreamID, &m.Weight,
			&u.ID, &u.Name, &u.Type, &u.Host, &u.Port, &u.Username, &u.Enabled); err != nil {
			return nil, err
		}
		m.Upstream = &u
		members = append(members, m)
	}
	return members, nil
}

// loadAllUpstreams fetches all upstreams for use in group/listener forms.
func loadAllUpstreams(r *http.Request, pool *pgxpool.Pool) ([]models.Upstream, error) {
	rows, err := pool.Query(r.Context(),
		"SELECT id, name, type, host, port, username, enabled FROM upstreams ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ups []models.Upstream
	for rows.Next() {
		var u models.Upstream
		if err := rows.Scan(&u.ID, &u.Name, &u.Type, &u.Host, &u.Port, &u.Username, &u.Enabled); err != nil {
			return nil, err
		}
		ups = append(ups, u)
	}
	return ups, nil
}
