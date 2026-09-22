package handlers

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"panel/models"
)

func UpstreamsHandlers(pool *pgxpool.Pool) http.Handler {
	r := chi.NewRouter()

	// GET /upstreams — list
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		rows, err := pool.Query(r.Context(),
			"SELECT id, name, type, host, port, username, password, enabled FROM upstreams ORDER BY id")
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		defer rows.Close()
		var upstreams []models.Upstream
		for rows.Next() {
			var u models.Upstream
			if err := rows.Scan(&u.ID, &u.Name, &u.Type, &u.Host, &u.Port, &u.Username, &u.Password, &u.Enabled); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			upstreams = append(upstreams, u)
		}
		render(w, r, "upstreams/index.html", map[string]any{"Upstreams": upstreams})
	})

	// GET /upstreams/new — empty form
	r.Get("/new", func(w http.ResponseWriter, r *http.Request) {
		renderTemplate(w, "upstream-form", map[string]any{"Upstream": models.Upstream{}, "IsEdit": false})
	})

	// POST /upstreams — create
	r.Post("/", func(w http.ResponseWriter, r *http.Request) {
		port, _ := strconv.Atoi(r.FormValue("port"))
		enabled := r.FormValue("enabled") == "on"
		var u models.Upstream
		err := pool.QueryRow(r.Context(),
			`INSERT INTO upstreams (name, type, host, port, username, password, enabled)
             VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id, name, type, host, port, username, password, enabled`,
			r.FormValue("name"), r.FormValue("type"), r.FormValue("host"), port,
			r.FormValue("username"), r.FormValue("password"), enabled,
		).Scan(&u.ID, &u.Name, &u.Type, &u.Host, &u.Port, &u.Username, &u.Password, &u.Enabled)
		if err != nil {
			triggerToast(w, "Error: "+err.Error(), "error")
			http.Error(w, err.Error(), 500)
			return
		}
		triggerToast(w, "Upstream created", "success")
		w.Header().Set("HX-Reswap", "afterbegin")
		w.Header().Set("HX-Retarget", "#upstreams-tbody")
		renderTemplate(w, "upstream-row", u)
	})

	// GET /upstreams/{id}/edit — edit form
	r.Get("/{id}/edit", func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		var u models.Upstream
		err := pool.QueryRow(r.Context(),
			"SELECT id, name, type, host, port, username, password, enabled FROM upstreams WHERE id = $1", id).
			Scan(&u.ID, &u.Name, &u.Type, &u.Host, &u.Port, &u.Username, &u.Password, &u.Enabled)
		if err != nil {
			http.Error(w, "not found", 404)
			return
		}
		renderTemplate(w, "upstream-form", map[string]any{"Upstream": u, "IsEdit": true})
	})

	// PUT /upstreams/{id} — update
	r.Put("/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		port, _ := strconv.Atoi(r.FormValue("port"))
		enabled := r.FormValue("enabled") == "on"
		var u models.Upstream
		err := pool.QueryRow(r.Context(),
			`UPDATE upstreams SET name=$1, type=$2, host=$3, port=$4, username=$5, password=$6, enabled=$7
             WHERE id=$8 RETURNING id, name, type, host, port, username, password, enabled`,
			r.FormValue("name"), r.FormValue("type"), r.FormValue("host"), port,
			r.FormValue("username"), r.FormValue("password"), enabled, id,
		).Scan(&u.ID, &u.Name, &u.Type, &u.Host, &u.Port, &u.Username, &u.Password, &u.Enabled)
		if err != nil {
			triggerToast(w, "Error: "+err.Error(), "error")
			http.Error(w, err.Error(), 500)
			return
		}
		triggerToast(w, "Upstream updated", "success")
		renderTemplate(w, "upstream-row", u)
	})

	// DELETE /upstreams/{id}
	r.Delete("/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if _, err := pool.Exec(r.Context(), "DELETE FROM upstreams WHERE id = $1", id); err != nil {
			triggerToast(w, "Error: "+err.Error(), "error")
			http.Error(w, err.Error(), 500)
			return
		}
		triggerToast(w, "Upstream deleted", "success")
		w.WriteHeader(200)
	})

	return r
}
