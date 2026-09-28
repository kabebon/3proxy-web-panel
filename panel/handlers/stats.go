package handlers

import (
	"bufio"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"panel/config"
)

// LogEntry represents a parsed 3proxy log line.
type LogEntry struct {
	Time        string
	User        string
	SourceIP    string
	Destination string
	BytesSent   string
	BytesRecv   string
	Raw         string
}

func StatsHandlers(pool *pgxpool.Pool, cfg *config.Config) http.Handler {
	r := chi.NewRouter()

	// GET /stats — main stats page
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		entries, _ := parseLast100Lines(cfg.ProxyLogPath, r.URL.Query().Get("user"))
		render(w, r, "stats/index.html", map[string]any{
			"Entries": entries,
		})
	})

	// GET /stats/live — htmx polling endpoint (returns just the table rows)
	r.Get("/live", func(w http.ResponseWriter, r *http.Request) {
		filterUser := r.URL.Query().Get("user")
		entries, _ := parseLast100Lines(cfg.ProxyLogPath, filterUser)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		renderTemplate(w, "stats-table", map[string]any{"Entries": entries})
	})

	return r
}

// parseLast100Lines reads the last 100 lines from the newest 3proxy log file
// (logtype D rotates daily: 3proxy.log.YYYY.MM.DD next to the configured
// path) and parses them. Optionally filters by username.
func parseLast100Lines(logPath, filterUser string) ([]LogEntry, error) {
	path := newestLogFile(logPath)
	if path == "" {
		return nil, nil
	}
	f, err := os.Open(path)
	if err != nil {
		// Log file may not exist yet — return empty, not an error
		return nil, nil
	}
	defer f.Close()

	// Read all lines into a ring buffer of last 100
	const maxLines = 100
	lines := make([]string, 0, maxLines)
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		lines = append(lines, line)
		if len(lines) > maxLines {
			lines = lines[1:]
		}
	}

	// Parse in reverse so newest entries appear first
	entries := make([]LogEntry, 0, len(lines))
	for i := len(lines) - 1; i >= 0; i-- {
		e := parseLogLine(lines[i])
		if filterUser != "" && !strings.EqualFold(e.User, filterUser) {
			continue
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// newestLogFile returns the newest existing log file for a configured path:
// the dated rotation file with the greatest name (3proxy.log.YYYY.MM.DD sorts
// chronologically), or the plain path itself when no rotation files exist.
func newestLogFile(logPath string) string {
	dir := filepath.Dir(logPath)
	base := filepath.Base(logPath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	best := ""
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() {
			continue
		}
		if n == base || strings.HasPrefix(n, base+".") {
			if n > best {
				best = n
			}
		}
	}
	if best == "" {
		return ""
	}
	return filepath.Join(dir, best)
}

// parseLogLine parses a 3proxy log line produced by the generator's
// logformat "- +_L%t.%. %N.%p %E %U %C:%c %R:%r %O %I %h %T":
//
//	1790206426.565 SOCK5.21080 00000 myuser 192.168.1.100:54321 example.com:443 1024 2048 1 CONNECT_...
//	  unix.ts       svc.port    err   user    src                    dst              out  in
func parseLogLine(line string) LogEntry {
	e := LogEntry{Raw: line}
	parts := strings.Fields(line)
	if len(parts) < 6 {
		return e
	}

	// Unix timestamp with milliseconds
	if sec, err := strconv.ParseFloat(parts[0], 64); err == nil {
		t := time.Unix(int64(sec), int64((sec-float64(int64(sec)))*1e9))
		e.Time = t.Format("2006-01-02 15:04:05")
	} else {
		e.Time = parts[0]
	}

	e.User = parts[3]
	e.SourceIP = parts[4]
	e.Destination = parts[5]
	if len(parts) > 7 {
		e.BytesSent = formatBytes(parts[6])
	}
	if len(parts) > 8 {
		e.BytesRecv = formatBytes(parts[7])
	}

	return e
}

func formatBytes(s string) string {
	n := int64(0)
	fmt.Sscanf(s, "%d", &n)
	if n < 0 {
		return "-"
	}
	switch {
	case n >= 1024*1024*1024:
		return fmt.Sprintf("%.1f GB", float64(n)/float64(1024*1024*1024))
	case n >= 1024*1024:
		return fmt.Sprintf("%.1f MB", float64(n)/float64(1024*1024))
	case n >= 1024:
		return fmt.Sprintf("%.1f KB", float64(n)/float64(1024))
	default:
		return fmt.Sprintf("%d B", n)
	}
}
