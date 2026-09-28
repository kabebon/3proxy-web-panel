package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"panel/gen"
)

// UserCreds is what the bot shows the customer after purchase: everything
// needed to connect plus the expiry the tariff bought and the traffic state.
type UserCreds struct {
	ID                    int        `json:"id"`
	Username              string     `json:"username"`
	Password              string     `json:"password"`
	Host                  string     `json:"host"`
	Port                  int        `json:"port"`
	Protocol              string     `json:"protocol"`
	ListenerID            int        `json:"listener_id"`
	ExpiresAt             *time.Time `json:"expires_at"`
	Enabled               bool       `json:"enabled"`
	TrafficLimit          int64      `json:"traffic_limit_bytes"`      // 0 = unlimited
	TrafficUsed           int64      `json:"traffic_used_bytes"`
	TrafficRemainingBytes int64      `json:"traffic_remaining_bytes"` // -1 = unlimited
	TrafficExhausted      bool       `json:"traffic_exhausted"`       // true = cut off by quota
	ConnectionString      string     `json:"connection_string"`
}

type userRow struct {
	ID                int
	Username          string
	Password          string
	ListenerID        *int
	Enabled           bool
	ExpiresAt         *time.Time
	TrafficLimit     int64
	TrafficUsed      int64
}

const userCols = "id, username, password, listener_id, enabled, expires_at, traffic_limit, traffic_used"

func (s *Server) loadUserByID(w http.ResponseWriter, r *http.Request) (userRow, bool) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil || id <= 0 {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return userRow{}, false
	}
	var u userRow
	err = s.pool.QueryRow(r.Context(),
		`SELECT `+userCols+` FROM proxy_users WHERE id=$1`, id).
		Scan(&u.ID, &u.Username, &u.Password, &u.ListenerID, &u.Enabled, &u.ExpiresAt,
			&u.TrafficLimit, &u.TrafficUsed)
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
		TrafficLimit: u.TrafficLimit, TrafficUsed: u.TrafficUsed,
	}
	if u.TrafficLimit > 0 {
		c.TrafficRemainingBytes = u.TrafficLimit - u.TrafficUsed
		if c.TrafficRemainingBytes < 0 {
			c.TrafficRemainingBytes = 0
		}
		c.TrafficExhausted = u.TrafficUsed >= u.TrafficLimit
	} else {
		c.TrafficRemainingBytes = -1
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
	Username       *string  `json:"username"`
	Password       *string  `json:"password"`
	ListenerID     *int     `json:"listener_id"`
	Days           *int     `json:"days"`            // subscription length; default 30
	TrafficLimitGB *float64 `json:"traffic_limit_gb"` // total quota; 0/null = unlimited
}

// gbToBytes converts gigabytes (as sold to customers) to bytes.
func gbToBytes(gb float64) int64 { return int64(gb * 1073741824) }

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
		username = gen.Username()
	}
	password := ""
	if req.Password != nil {
		password = *req.Password
	}
	if password == "" {
		password = gen.Password()
	}
	days := 30
	if req.Days != nil {
		days = *req.Days
	}
	if days < 0 || days > 3650 {
		writeErr(w, http.StatusBadRequest, "days must be 0..3650")
		return
	}
	var limitBytes int64
	if req.TrafficLimitGB != nil {
		if *req.TrafficLimitGB < 0 || *req.TrafficLimitGB > 1e6 {
			writeErr(w, http.StatusBadRequest, "traffic_limit_gb must be 0..1000000")
			return
		}
		limitBytes = gbToBytes(*req.TrafficLimitGB)
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
		`INSERT INTO proxy_users (username, password, listener_id, expires_at, traffic_limit, enabled)
		 VALUES ($1,$2,$3,$4,$5,TRUE) RETURNING id`,
		username, password, listenerID, expires, limitBytes).Scan(&uid)
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
		`SELECT `+userCols+` FROM proxy_users WHERE id=$1`, uid).
		Scan(&u.ID, &u.Username, &u.Password, &u.ListenerID, &u.Enabled, &u.ExpiresAt,
			&u.TrafficLimit, &u.TrafficUsed)
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
		`SELECT `+userCols+` FROM proxy_users ORDER BY id`)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	out := []UserCreds{}
	for rows.Next() {
		var u userRow
		if err := rows.Scan(&u.ID, &u.Username, &u.Password, &u.ListenerID, &u.Enabled, &u.ExpiresAt,
			&u.TrafficLimit, &u.TrafficUsed); err != nil {
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

// trafficReq drives the quota from the bot: top-up after a customer buys
// more traffic (add_gb), a hard re-set of the quota (set_gb), or resetting
// the spent counter (reset_used, e.g. start of a new billing period).
type trafficReq struct {
	AddGB     *float64 `json:"add_gb"`
	SetGB     *float64 `json:"set_gb"`
	ResetUsed *bool    `json:"reset_used"`
}

func (s *Server) trafficUser(w http.ResponseWriter, r *http.Request) {
	u, ok := s.loadUserByID(w, r)
	if !ok {
		return
	}
	var req trafficReq
	if err := decodeBody(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if req.AddGB == nil && req.SetGB == nil && req.ResetUsed == nil {
		writeErr(w, http.StatusBadRequest, "nothing to do: pass add_gb, set_gb or reset_used")
		return
	}
	for _, v := range []*float64{req.AddGB, req.SetGB} {
		if v != nil && (*v < 0 || *v > 1e6) {
			writeErr(w, http.StatusBadRequest, "gb values must be 0..1000000")
			return
		}
	}
	if req.AddGB != nil && *req.AddGB == 0 {
		writeErr(w, http.StatusBadRequest, "add_gb must be > 0")
		return
	}

	newLimit := u.TrafficLimit
	if req.SetGB != nil {
		newLimit = gbToBytes(*req.SetGB)
	}
	if req.AddGB != nil {
		newLimit += gbToBytes(*req.AddGB)
	}
	// Only the limit is written; the used counter keeps accumulating on its
	// own (the accounter may add bytes between our read and this write).
	_, err := s.pool.Exec(r.Context(),
		`UPDATE proxy_users SET traffic_limit=$1 WHERE id=$2`, newLimit, u.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if req.ResetUsed != nil && *req.ResetUsed {
		if _, err := s.pool.Exec(r.Context(),
			`UPDATE proxy_users SET traffic_used=0 WHERE id=$1`, u.ID); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if err := s.applyNow(r.Context()); err != nil {
		writeErr(w, http.StatusInternalServerError, "traffic updated but apply failed: "+err.Error())
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
			`UPDATE proxy_users SET password=$1 WHERE id=$2`, gen.Password(), u.ID); err != nil {
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
