package api

import (
	"crypto/rand"
	"math/big"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// credential alphabet: no look-alike chars, no specials (CL config + URL safe)
const (
	usernameAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"
	passwordAlphabet = "abcdefghjkmnpqrstuvwxyzABCDEFGHJKMNPQRSTUVWXYZ23456789"
)

func randString(alphabet string, n int) string {
	b := make([]byte, n)
	max := big.NewInt(int64(len(alphabet)))
	for i := range b {
		idx, err := rand.Int(rand.Reader, max)
		if err != nil {
			panic(err) // crypto/rand failure is unrecoverable
		}
		b[i] = alphabet[idx.Int64()]
	}
	return string(b)
}

// UserCreds is what the bot shows the customer after purchase: everything
// needed to connect plus the expiry the tariff bought.
type UserCreds struct {
	ID               int        `json:"id"`
	Username         string     `json:"username"`
	Password         string     `json:"password"`
	Host             string     `json:"host"`
	Port             int        `json:"port"`
	Protocol         string     `json:"protocol"`
	ListenerID       int        `json:"listener_id"`
	ExpiresAt        *time.Time `json:"expires_at"`
	Enabled          bool       `json:"enabled"`
	ConnectionString string     `json:"connection_string"`
}

type userRow struct {
	ID         int
	Username   string
	Password   string
	ListenerID *int
	Enabled    bool
	ExpiresAt  *time.Time
}

func (s *Server) loadUserByID(w http.ResponseWriter, r *http.Request) (userRow, bool) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil || id <= 0 {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return userRow{}, false
	}
	var u userRow
	err = s.pool.QueryRow(r.Context(),
		`SELECT id, username, password, listener_id, enabled, expires_at FROM proxy_users WHERE id=$1`, id).
		Scan(&u.ID, &u.Username, &u.Password, &u.ListenerID, &u.Enabled, &u.ExpiresAt)
	if err != nil {
		writeErr(w, http.StatusNotFound, "user not found")
		return userRow{}, false
	}
	return u, true
}

// creds enriches a user row with its listener endpoint for the bot.
func (s *Server) creds(r *http.Request, u userRow) (UserCreds, error) {
	c := UserCreds{
		ID: u.ID, Username: u.Username, Password: u.Password,
		ListenerID: 0, ExpiresAt: u.ExpiresAt, Enabled: u.Enabled,
		Host: s.cfg.PublicHost,
	}
	if u.ListenerID != nil {
		c.ListenerID = *u.ListenerID
		var protocol string
		var port int
		err := s.pool.QueryRow(r.Context(),
			`SELECT protocol, port FROM listeners WHERE id=$1`, *u.ListenerID).Scan(&protocol, &port)
		if err == nil {
			c.Protocol, c.Port = protocol, port
			if c.Host != "" && port != 0 {
				c.ConnectionString = protocol + "://" + u.Username + ":" + u.Password + "@" + c.Host + ":" + strconv.Itoa(port)
			}
		}
	}
	return c, nil
}

type createUserReq struct {
	Username   *string `json:"username"`
	Password   *string `json:"password"`
	ListenerID *int    `json:"listener_id"`
	Days       *int    `json:"days"` // subscription length; default 30
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	var req createUserReq
	if err := decodeBody(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}

	username := ""
	if req.Username != nil {
		username = *req.Username
	}
	if username == "" {
		username = "p" + randString(usernameAlphabet, 8)
	}
	password := ""
	if req.Password != nil {
		password = *req.Password
	}
	if password == "" {
		password = randString(passwordAlphabet, 12)
	}
	days := 30
	if req.Days != nil {
		days = *req.Days
	}
	if days < 0 || days > 3650 {
		writeErr(w, http.StatusBadRequest, "days must be 0..3650")
		return
	}

	// listener: explicit or first enabled (rotating/sticky product choice is
	// the bot's job; it knows the ports from GET /listeners)
	listenerID := req.ListenerID
	if listenerID == nil {
		var id int
		err := s.pool.QueryRow(r.Context(),
			`SELECT id FROM listeners WHERE enabled ORDER BY id LIMIT 1`).Scan(&id)
		if err != nil {
			writeErr(w, http.StatusConflict, "no enabled listener configured")
			return
		}
		listenerID = &id
	} else {
		var exists bool
		if err := s.pool.QueryRow(r.Context(),
			`SELECT EXISTS(SELECT 1 FROM listeners WHERE id=$1 AND enabled)`, *listenerID).Scan(&exists); err != nil || !exists {
			writeErr(w, http.StatusBadRequest, "listener_id not found or disabled")
			return
		}
	}

	var expires *time.Time
	if days > 0 {
		t := time.Now().UTC().AddDate(0, 0, days)
		expires = &t
	}

	var uid int
	err := s.pool.QueryRow(r.Context(),
		`INSERT INTO proxy_users (username, password, listener_id, expires_at, enabled)
		 VALUES ($1,$2,$3,$4,TRUE) RETURNING id`,
		username, password, listenerID, expires).Scan(&uid)
	if err != nil {
		if pgErr, ok := err.(*pgconn.PgError); ok && pgErr.Code == "23505" {
			writeErr(w, http.StatusConflict, "username already exists")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	if err := s.applyNow(r.Context()); err != nil {
		writeErr(w, http.StatusInternalServerError, "user created but apply failed: "+err.Error())
		return
	}

	var u userRow
	_ = s.pool.QueryRow(r.Context(),
		`SELECT id, username, password, listener_id, enabled, expires_at FROM proxy_users WHERE id=$1`, uid).
		Scan(&u.ID, &u.Username, &u.Password, &u.ListenerID, &u.Enabled, &u.ExpiresAt)
	creds, _ := s.creds(r, u)
	writeJSON(w, http.StatusCreated, creds)
}

func (s *Server) getUser(w http.ResponseWriter, r *http.Request) {
	u, ok := s.loadUserByID(w, r)
	if !ok {
		return
	}
	creds, _ := s.creds(r, u)
	creds.Password = u.Password // full visibility for the bot
	writeJSON(w, http.StatusOK, creds)
}

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(),
		`SELECT id, username, password, listener_id, enabled, expires_at FROM proxy_users ORDER BY id`)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	out := []UserCreds{}
	for rows.Next() {
		var u userRow
		if err := rows.Scan(&u.ID, &u.Username, &u.Password, &u.ListenerID, &u.Enabled, &u.ExpiresAt); err != nil {
			continue
		}
		c, _ := s.creds(r, u)
		out = append(out, c)
	}
	writeJSON(w, http.StatusOK, out)
}

type extendReq struct {
	Days int `json:"days"`
}

func (s *Server) extendUser(w http.ResponseWriter, r *http.Request) {
	u, ok := s.loadUserByID(w, r)
	if !ok {
		return
	}
	var req extendReq
	if err := decodeBody(r, &req); err != nil || req.Days <= 0 || req.Days > 3650 {
		writeErr(w, http.StatusBadRequest, "days must be 1..3650")
		return
	}

	// extend from the LATER of now / current expiry (pauses don't stack free time)
	_, err := s.pool.Exec(r.Context(),
		`UPDATE proxy_users
		   SET expires_at = GREATEST(COALESCE(expires_at, NOW()), NOW()) + make_interval(days => $1),
		       enabled = TRUE
		 WHERE id = $2`, req.Days, u.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.applyNow(r.Context()); err != nil {
		writeErr(w, http.StatusInternalServerError, "extended but apply failed: "+err.Error())
		return
	}

	nu, _ := s.loadUserByID(w, r)
	creds, _ := s.creds(r, nu)
	writeJSON(w, http.StatusOK, creds)
}

type rotateReq struct {
	Password   *bool `json:"password"`    // rotate credentials (default true)
	ListenerID *int  `json:"listener_id"` // move to another exit/port (optional)
}

func (s *Server) rotateUser(w http.ResponseWriter, r *http.Request) {
	u, ok := s.loadUserByID(w, r)
	if !ok {
		return
	}
	var req rotateReq
	if err := decodeBody(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	doPassword := req.Password == nil || *req.Password

	if req.ListenerID != nil {
		var exists bool
		if err := s.pool.QueryRow(r.Context(),
			`SELECT EXISTS(SELECT 1 FROM listeners WHERE id=$1 AND enabled)`, *req.ListenerID).Scan(&exists); err != nil || !exists {
			writeErr(w, http.StatusBadRequest, "listener_id not found or disabled")
			return
		}
	}
	if !doPassword && req.ListenerID == nil {
		writeErr(w, http.StatusBadRequest, "nothing to rotate: password=false and no listener_id")
		return
	}

	if doPassword {
		if _, err := s.pool.Exec(r.Context(),
			`UPDATE proxy_users SET password=$1 WHERE id=$2`, randString(passwordAlphabet, 12), u.ID); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if req.ListenerID != nil {
		if _, err := s.pool.Exec(r.Context(),
			`UPDATE proxy_users SET listener_id=$1 WHERE id=$2`, req.ListenerID, u.ID); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	if err := s.applyNow(r.Context()); err != nil {
		writeErr(w, http.StatusInternalServerError, "rotated but apply failed: "+err.Error())
		return
	}
	nu, _ := s.loadUserByID(w, r)
	creds, _ := s.creds(r, nu)
	writeJSON(w, http.StatusOK, creds)
}

type patchUserReq struct {
	Enabled *bool `json:"enabled"`
}

func (s *Server) patchUser(w http.ResponseWriter, r *http.Request) {
	u, ok := s.loadUserByID(w, r)
	if !ok {
		return
	}
	var req patchUserReq
	if err := decodeBody(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if req.Enabled != nil {
		if _, err := s.pool.Exec(r.Context(), `UPDATE proxy_users SET enabled=$1 WHERE id=$2`, *req.Enabled, u.ID); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if err := s.applyNow(r.Context()); err != nil {
		writeErr(w, http.StatusInternalServerError, "patched but apply failed: "+err.Error())
		return
	}
	nu, _ := s.loadUserByID(w, r)
	creds, _ := s.creds(r, nu)
	writeJSON(w, http.StatusOK, creds)
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request) {
	u, ok := s.loadUserByID(w, r)
	if !ok {
		return
	}
	if _, err := s.pool.Exec(r.Context(), `DELETE FROM proxy_users WHERE id=$1`, u.ID); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.applyNow(r.Context()); err != nil {
		writeErr(w, http.StatusInternalServerError, "deleted but apply failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "id": u.ID})
}
