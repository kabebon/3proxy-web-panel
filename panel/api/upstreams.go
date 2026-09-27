package api

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
)

type upstreamJSON struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	Host      string `json:"host"`
	Port      int    `json:"port"`
	Username  string `json:"username"`
	Enabled   bool   `json:"enabled"`
	HasSecret bool   `json:"has_password"`
}

type upstreamReq struct {
	Name     *string `json:"name"`
	Type     *string `json:"type"` // "http" | "socks5"
	Host     *string `json:"host"`
	Port     *int    `json:"port"`
	Username *string `json:"username"`
	Password *string `json:"password"`
	Enabled  *bool   `json:"enabled"`
}

func validUpstreamType(t string) bool { return t == "http" || t == "socks5" }

func (s *Server) listUpstreams(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(),
		`SELECT id, name, type, host, port, username, password <> '' AS has_secret, enabled FROM upstreams ORDER BY id`)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []upstreamJSON{}
	for rows.Next() {
		var u upstreamJSON
		if err := rows.Scan(&u.ID, &u.Name, &u.Type, &u.Host, &u.Port, &u.Username, &u.HasSecret, &u.Enabled); err != nil {
			continue
		}
		out = append(out, u)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) getUpstream(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil || id <= 0 {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var u upstreamJSON
	err = s.pool.QueryRow(r.Context(),
		`SELECT id, name, type, host, port, username, password <> '' AS has_secret, enabled FROM upstreams WHERE id=$1`, id).
		Scan(&u.ID, &u.Name, &u.Type, &u.Host, &u.Port, &u.Username, &u.HasSecret, &u.Enabled)
	if err != nil {
		writeErr(w, http.StatusNotFound, "upstream not found")
		return
	}
	writeJSON(w, http.StatusOK, u)
}

func (s *Server) createUpstream(w http.ResponseWriter, r *http.Request) {
	var req upstreamReq
	if err := decodeBody(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if req.Type == nil || req.Host == nil || req.Port == nil || !validUpstreamType(*req.Type) ||
		*req.Host == "" || *req.Port < 1 || *req.Port > 65535 {
		writeErr(w, http.StatusBadRequest, "type (http|socks5), host and port (1..65535) are required")
		return
	}
	name := ""
	if req.Name != nil {
		name = *req.Name
	}
	if name == "" {
		name = "up-" + strconv.Itoa(*req.Port)
	}
	user, pass := "", ""
	if req.Username != nil {
		user = *req.Username
	}
	if req.Password != nil {
		pass = *req.Password
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	var id int
	err := s.pool.QueryRow(r.Context(),
		`INSERT INTO upstreams (name, type, host, port, username, password, enabled)
		 VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id`,
		name, *req.Type, *req.Host, *req.Port, user, pass, enabled).Scan(&id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.applyNow(r.Context()); err != nil {
		writeErr(w, http.StatusInternalServerError, "created but apply failed: "+err.Error())
		return
	}
	w.WriteHeader(http.StatusCreated)
	s.getUpstreamByID(w, r, id)
}

func (s *Server) getUpstreamByID(w http.ResponseWriter, r *http.Request, id int) {
	var u upstreamJSON
	err := s.pool.QueryRow(r.Context(),
		`SELECT id, name, type, host, port, username, password <> '' AS has_secret, enabled FROM upstreams WHERE id=$1`, id).
		Scan(&u.ID, &u.Name, &u.Type, &u.Host, &u.Port, &u.Username, &u.HasSecret, &u.Enabled)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, u)
}

// patchUpstream replaces a dead/rotated purchased proxy in place: group
// membership and listeners keep pointing at the same id.
func (s *Server) patchUpstream(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil || id <= 0 {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req upstreamReq
	if err := decodeBody(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if req.Type != nil && !validUpstreamType(*req.Type) {
		writeErr(w, http.StatusBadRequest, "type must be http|socks5")
		return
	}
	if req.Port != nil && (*req.Port < 1 || *req.Port > 65535) {
		writeErr(w, http.StatusBadRequest, "port must be 1..65535")
		return
	}

	sets := map[string]any{}
	if req.Name != nil {
		sets["name"] = *req.Name
	}
	if req.Type != nil {
		sets["type"] = *req.Type
	}
	if req.Host != nil {
		sets["host"] = *req.Host
	}
	if req.Port != nil {
		sets["port"] = *req.Port
	}
	if req.Username != nil {
		sets["username"] = *req.Username
	}
	if req.Password != nil {
		sets["password"] = *req.Password
	}
	if req.Enabled != nil {
		sets["enabled"] = *req.Enabled
	}
	if len(sets) == 0 {
		writeErr(w, http.StatusBadRequest, "no fields to update")
		return
	}

	query, args := buildUpdate("upstreams", id, sets)
	if _, err := s.pool.Exec(r.Context(), query, args...); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.applyNow(r.Context()); err != nil {
		writeErr(w, http.StatusInternalServerError, "updated but apply failed: "+err.Error())
		return
	}
	s.getUpstreamByID(w, r, id)
}

func (s *Server) deleteUpstream(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil || id <= 0 {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	if _, err := s.pool.Exec(r.Context(), `DELETE FROM upstreams WHERE id=$1`, id); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.applyNow(r.Context()); err != nil {
		writeErr(w, http.StatusInternalServerError, "deleted but apply failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "id": id})
}
