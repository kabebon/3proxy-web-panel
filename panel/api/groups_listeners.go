package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
)

// buildUpdate constructs "UPDATE tbl SET k1=$1,... WHERE id=$n" from a map.
func buildUpdate(table string, id int, sets map[string]any) (string, []any) {
	keys := make([]string, 0, len(sets))
	for k := range sets {
		keys = append(keys, k)
	}
	// deterministic order
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[j] < keys[i] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	parts := make([]string, 0, len(keys))
	args := make([]any, 0, len(keys)+1)
	for i, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=$%d", k, i+1))
		args = append(args, sets[k])
	}
	args = append(args, id)
	return fmt.Sprintf("UPDATE %s SET %s WHERE id=$%d", table, strings.Join(parts, ", "), len(args)), args
}

type groupMemberJSON struct {
	UpstreamID int `json:"upstream_id"`
	Weight     int `json:"weight"`
}

type groupJSON struct {
	ID      int               `json:"id"`
	Name    string            `json:"name"`
	Enabled bool              `json:"enabled"`
	Members []groupMemberJSON `json:"members"`
}

type groupReq struct {
	Name    *string           `json:"name"`
	Members []groupMemberJSON `json:"members"`
}

func (s *Server) listGroups(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(), `SELECT id, name, enabled FROM upstream_groups ORDER BY id`)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	groups := []groupJSON{}
	ids := []int{}
	for rows.Next() {
		var g groupJSON
		if err := rows.Scan(&g.ID, &g.Name, &g.Enabled); err != nil {
			continue
		}
		g.Members = []groupMemberJSON{}
		groups = append(groups, g)
		ids = append(ids, g.ID)
	}
	for i := range groups {
		mrows, err := s.pool.Query(r.Context(),
			`SELECT upstream_id, weight FROM upstream_group_members WHERE group_id=$1 ORDER BY id`, groups[i].ID)
		if err != nil {
			continue
		}
		for mrows.Next() {
			var m groupMemberJSON
			if err := mrows.Scan(&m.UpstreamID, &m.Weight); err == nil {
				groups[i].Members = append(groups[i].Members, m)
			}
		}
		mrows.Close()
	}
	writeJSON(w, http.StatusOK, groups)
}

func (s *Server) createGroup(w http.ResponseWriter, r *http.Request) {
	var req groupReq
	if err := decodeBody(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if req.Name == nil || *req.Name == "" || len(req.Members) == 0 {
		writeErr(w, http.StatusBadRequest, "name and at least one member required")
		return
	}
	for _, m := range req.Members {
		var exists bool
		if err := s.pool.QueryRow(r.Context(),
			`SELECT EXISTS(SELECT 1 FROM upstreams WHERE id=$1)`, m.UpstreamID).Scan(&exists); err != nil || !exists {
			writeErr(w, http.StatusBadRequest, fmt.Sprintf("upstream %d not found", m.UpstreamID))
			return
		}
	}

	var gid int
	err := s.pool.QueryRow(r.Context(),
		`INSERT INTO upstream_groups (name, enabled) VALUES ($1, TRUE) RETURNING id`, *req.Name).Scan(&gid)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, m := range req.Members {
		mw := m.Weight
		if mw < 0 {
			mw = 0
		}
		if _, err := s.pool.Exec(r.Context(),
			`INSERT INTO upstream_group_members (group_id, upstream_id, weight) VALUES ($1,$2,$3)`,
			gid, m.UpstreamID, mw); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if err := s.applyNow(r.Context()); err != nil {
		writeErr(w, http.StatusInternalServerError, "created but apply failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": gid, "name": *req.Name, "members": len(req.Members)})
}

func (s *Server) deleteGroup(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil || id <= 0 {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	if _, err := s.pool.Exec(r.Context(), `DELETE FROM upstream_groups WHERE id=$1`, id); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.applyNow(r.Context()); err != nil {
		writeErr(w, http.StatusInternalServerError, "deleted but apply failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "id": id})
}

type listenerJSON struct {
	ID              int    `json:"id"`
	Name            string `json:"name"`
	Protocol        string `json:"protocol"`
	Port            int    `json:"port"`
	BindIP          string `json:"bind_ip"`
	UpstreamID      *int   `json:"upstream_id"`
	UpstreamGroupID *int   `json:"upstream_group_id"`
	Enabled         bool   `json:"enabled"`
}

type listenerReq struct {
	Name            *string `json:"name"`
	Protocol        *string `json:"protocol"` // "http" | "socks5"
	Port            *int    `json:"port"`
	BindIP          *string `json:"bind_ip"`
	UpstreamID      *int    `json:"upstream_id"`
	UpstreamGroupID *int    `json:"upstream_group_id"`
	Enabled         *bool   `json:"enabled"`
}

func validProtocol(p string) bool { return p == "http" || p == "socks5" }

func (s *Server) listListeners(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(),
		`SELECT id, name, protocol, port, bind_ip, upstream_id, upstream_group_id, enabled FROM listeners ORDER BY id`)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []listenerJSON{}
	for rows.Next() {
		var l listenerJSON
		if err := rows.Scan(&l.ID, &l.Name, &l.Protocol, &l.Port, &l.BindIP, &l.UpstreamID, &l.UpstreamGroupID, &l.Enabled); err != nil {
			continue
		}
		out = append(out, l)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) createListener(w http.ResponseWriter, r *http.Request) {
	var req listenerReq
	if err := decodeBody(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if req.Protocol == nil || req.Port == nil || !validProtocol(*req.Protocol) || *req.Port < 1 || *req.Port > 65535 {
		writeErr(w, http.StatusBadRequest, "protocol (http|socks5) and port (1..65535) are required")
		return
	}
	if req.UpstreamID == nil && req.UpstreamGroupID == nil {
		writeErr(w, http.StatusBadRequest, "upstream_id or upstream_group_id required")
		return
	}
	if req.UpstreamID != nil {
		var exists bool
		if err := s.pool.QueryRow(r.Context(),
			`SELECT EXISTS(SELECT 1 FROM upstreams WHERE id=$1)`, *req.UpstreamID).Scan(&exists); err != nil || !exists {
			writeErr(w, http.StatusBadRequest, "upstream_id not found")
			return
		}
	}
	if req.UpstreamGroupID != nil {
		var exists bool
		if err := s.pool.QueryRow(r.Context(),
			`SELECT EXISTS(SELECT 1 FROM upstream_groups WHERE id=$1)`, *req.UpstreamGroupID).Scan(&exists); err != nil || !exists {
			writeErr(w, http.StatusBadRequest, "upstream_group_id not found")
			return
		}
	}
	name := ""
	if req.Name != nil {
		name = *req.Name
	}
	if name == "" {
		name = *req.Protocol + "-" + strconv.Itoa(*req.Port)
	}
	bind := "0.0.0.0"
	if req.BindIP != nil && *req.BindIP != "" {
		bind = *req.BindIP
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	var id int
	err := s.pool.QueryRow(r.Context(),
		`INSERT INTO listeners (name, protocol, port, bind_ip, upstream_id, upstream_group_id, enabled)
		 VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id`,
		name, *req.Protocol, *req.Port, bind, req.UpstreamID, req.UpstreamGroupID, enabled).Scan(&id)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") {
			writeErr(w, http.StatusConflict, "port already in use")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.applyNow(r.Context()); err != nil {
		writeErr(w, http.StatusInternalServerError, "created but apply failed: "+err.Error())
		return
	}
	s.getListenerByID(w, r, id)
}

func (s *Server) getListenerByID(w http.ResponseWriter, r *http.Request, id int) {
	var l listenerJSON
	err := s.pool.QueryRow(r.Context(),
		`SELECT id, name, protocol, port, bind_ip, upstream_id, upstream_group_id, enabled FROM listeners WHERE id=$1`, id).
		Scan(&l.ID, &l.Name, &l.Protocol, &l.Port, &l.BindIP, &l.UpstreamID, &l.UpstreamGroupID, &l.Enabled)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, l)
}

func (s *Server) patchListener(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil || id <= 0 {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req listenerReq
	if err := decodeBody(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	sets := map[string]any{}
	if req.Name != nil {
		sets["name"] = *req.Name
	}
	if req.Protocol != nil {
		if !validProtocol(*req.Protocol) {
			writeErr(w, http.StatusBadRequest, "protocol must be http|socks5")
			return
		}
		sets["protocol"] = *req.Protocol
	}
	if req.Port != nil {
		if *req.Port < 1 || *req.Port > 65535 {
			writeErr(w, http.StatusBadRequest, "port must be 1..65535")
			return
		}
		sets["port"] = *req.Port
	}
	if req.BindIP != nil {
		sets["bind_ip"] = *req.BindIP
	}
	if req.UpstreamID != nil {
		sets["upstream_id"] = *req.UpstreamID
		sets["upstream_group_id"] = nil
	}
	if req.UpstreamGroupID != nil {
		sets["upstream_group_id"] = *req.UpstreamGroupID
		sets["upstream_id"] = nil
	}
	if req.Enabled != nil {
		sets["enabled"] = *req.Enabled
	}
	if len(sets) == 0 {
		writeErr(w, http.StatusBadRequest, "no fields to update")
		return
	}
	query, args := buildUpdate("listeners", id, sets)
	if _, err := s.pool.Exec(r.Context(), query, args...); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.applyNow(r.Context()); err != nil {
		writeErr(w, http.StatusInternalServerError, "patched but apply failed: "+err.Error())
		return
	}
	s.getListenerByID(w, r, id)
}

func (s *Server) deleteListener(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil || id <= 0 {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	// users on this listener lose it (ON DELETE SET NULL) and get kicked from
	// the config — the bot should move or delete them first
	if _, err := s.pool.Exec(r.Context(), `DELETE FROM listeners WHERE id=$1`, id); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.applyNow(r.Context()); err != nil {
		writeErr(w, http.StatusInternalServerError, "deleted but apply failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "id": id})
}
