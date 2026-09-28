package api

// Per-user traffic accounting from the 3proxy access log (stage 3).
//
// The log is written with logtype D: one file per day next to
// PROXY_LOG_PATH (3proxy.log.YYYY.MM.DD, lexicographic order = chronological).
// Every finished connection line carries the username and byte counters, so a
// tail-following parser with persisted per-file offsets is enough for exact
// per-user totals. Offsets are committed in the SAME transaction as the
// counters — a crash can neither lose nor double-count bytes.
//
// Enforcement is indirect: the config generator drops users whose
// traffic_used >= traffic_limit (see proxy.activeUserWhere), so
// cutting a user off means regenerating the config — which the accounter
// triggers as soon as the set of exhausted users changes.

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// rowQuerier is satisfied by both *pgxpool.Pool and pgx.Tx.
type rowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// StartAccounter runs the accounting loop in the background, one tick every
// TrafficSeconds.
func (s *Server) StartAccounter() {
	go func() {
		for {
			s.accountOnce(context.Background())
			time.Sleep(time.Duration(s.cfg.TrafficSeconds) * time.Second)
		}
	}()
}

func (s *Server) accountOnce(ctx context.Context) {
	dir := filepath.Dir(s.cfg.ProxyLogPath)
	base := filepath.Base(s.cfg.ProxyLogPath)

	entries, err := os.ReadDir(dir)
	if err != nil {
		return // no logs yet — nothing to account
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if n == base || (strings.HasPrefix(n, base+".") && len(n) > len(base)+1) {
			files = append(files, n)
		}
	}
	sort.Strings(files)
	if len(files) == 0 {
		return
	}

	// First ever run (empty state): seed every existing file at EOF so
	// history that predates the accounter is not billed against quotas.
	firstRun := s.accounterStateEmpty(ctx)

	deltas := map[string]int64{} // username -> new bytes
	offsets := map[string]int64{}
	for _, name := range files {
		full := filepath.Join(dir, name)
		st, err := os.Stat(full)
		if err != nil {
			continue
		}
		off := int64(0)
		if firstRun {
			// Seed at EOF — this marks the state table non-empty and starts
			// counting from the deployment moment.
			off = st.Size()
			offsets[name] = off
			continue
		}
		off = s.readOffset(ctx, name)
		if st.Size() < off {
			off = 0 // truncated or replaced — reread the whole file
		}
		noff, d := parseLogFile(full, off)
		if len(d) > 0 || noff != off {
			offsets[name] = noff
			for u, b := range d {
				deltas[u] += b
			}
		}
	}
	if len(offsets) == 0 {
		return
	}

	if s.commitDeltas(ctx, deltas, offsets) {
		// Someone crossed their quota: regenerate the config so 3proxy cuts
		// them off (un-exhausting happens via the traffic API, which applies
		// on its own).
		_ = s.applyNow(ctx)
	}
}

// parseLogFile reads the file from start and consumes whole lines only. It
// returns the new offset (start + bytes of complete lines) and per-user
// byte deltas.
func parseLogFile(path string, start int64) (int64, map[string]int64) {
	deltas := map[string]int64{}
	f, err := os.Open(path)
	if err != nil {
		return start, deltas
	}
	defer f.Close()
	if _, err := f.Seek(start, 0); err != nil {
		return start, deltas
	}
	buf, err := io.ReadAll(f)
	if err != nil {
		return start, deltas
	}
	lastNL := bytes.LastIndexByte(buf, '\n')
	if lastNL < 0 {
		return start, deltas // no complete line since last time
	}
	for _, line := range bytes.Split(buf[:lastNL], []byte("\n")) {
		if u, n := parseTrafficLine(string(line)); n != 0 {
			deltas[u] += n
		}
	}
	return start + int64(lastNL+1), deltas
}

// parseTrafficLine extracts (username, bytes_out+bytes_in) from one access
// log line. Log format set in the generator:
//
//	"- +_L%t.%. %N.%p %E %U %C:%c %R:%r %O %I %h %T"
//	1790206426.565 SOCK5.21080 00000 u-rot 127.0.0.1:42862 172.28.77.11:21081 76 192 1 CONNECT_...
//	  ts            svc.port    err   user  src               dst                    out in
func parseTrafficLine(line string) (string, int64) {
	p := strings.Fields(line)
	if len(p) < 8 {
		return "", 0
	}
	user := p[3]
	if user == "" || user == "-" {
		return "", 0 // service/accept lines and anonymous noise
	}
	out, err1 := strconv.ParseInt(p[6], 10, 64)
	in, err2 := strconv.ParseInt(p[7], 10, 64)
	if err1 != nil || err2 != nil {
		return "", 0
	}
	if out < 0 {
		out = 0
	}
	if in < 0 {
		in = 0
	}
	return user, out + in
}

func (s *Server) accounterStateEmpty(ctx context.Context) bool {
	var n int
	if err := s.pool.QueryRow(ctx, "SELECT COUNT(*) FROM traffic_accounting_state").Scan(&n); err != nil {
		return false
	}
	return n == 0
}

func (s *Server) readOffset(ctx context.Context, fileName string) int64 {
	var off int64
	if err := s.pool.QueryRow(ctx,
		"SELECT read_offset FROM traffic_accounting_state WHERE file_name=$1", fileName).Scan(&off); err != nil {
		return 0
	}
	return off
}

// commitDeltas applies counter increments and parser offsets atomically and
// reports whether the SET of quota-exhausted users changed.
func (s *Server) commitDeltas(ctx context.Context, deltas, offsets map[string]int64) bool {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false
	}
	defer tx.Rollback(ctx)

	before := exhaustedSet(ctx, tx)

	for u, d := range deltas {
		if d == 0 {
			continue
		}
		if _, err := tx.Exec(ctx,
			"UPDATE proxy_users SET traffic_used = traffic_used + $1 WHERE username = $2", d, u); err != nil {
			return false
		}
	}
	for name, off := range offsets {
		if _, err := tx.Exec(ctx,
			`INSERT INTO traffic_accounting_state (file_name, read_offset) VALUES ($1,$2)
			 ON CONFLICT (file_name) DO UPDATE SET read_offset = EXCLUDED.read_offset, updated_at = NOW()`,
			name, off); err != nil {
			return false
		}
	}

	after := exhaustedSet(ctx, tx)
	if err := tx.Commit(ctx); err != nil {
		return false
	}
	return before != after
}

// exhaustedSet returns the comma-joined usernames currently cut off by quota
// (empty string = none). Used both to trigger config regeneration and inside
// the reaper fingerprint.
func exhaustedSet(ctx context.Context, q rowQuerier) string {
	var set string
	_ = q.QueryRow(ctx,
		`SELECT COALESCE(string_agg(username, ',' ORDER BY username), '')
		 FROM proxy_users WHERE enabled AND traffic_limit > 0 AND traffic_used >= traffic_limit`).Scan(&set)
	return set
}
