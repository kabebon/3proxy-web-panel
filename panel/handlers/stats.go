package handlers

import (
	"bufio"
	"fmt"
	"net/http"
	"os"
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

// parseLast100Lines reads the last 100 lines from the log file and parses them.
// Optionally filters by username.
func parseLast100Lines(logPath, filterUser string) ([]LogEntry, error) {
	f, err := os.Open(logPath)
	if err != nil {
		// Log file may not exist yet — return empty, not an error
		return nil, nil
	}
	defer f.Close()

	// Read all lines into a ring buffer of last 100
	const maxLines = 100
	lines := make([]string, 0, maxLines)
	scanner := bufio.NewScanner(f)
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

// parseLogLine parses a 3proxy log line.
// Log format: "- +_L%t.%. %N.%p %E %U %C:%c %R:%r %O %I %h %T"
// Example: 2024-01-15 12:34:56.789 3 0 myuser 192.168.1.100:54321 example.com:443 1024 2048 - -
func parseLogLine(line string) LogEntry {
	e := LogEntry{Raw: line}
	parts := strings.Fields(line)
	if len(parts) < 10 {
		return e
	}

	// Date + time
	if len(parts) >= 2 {
		// Try to parse and format nicely
		t, err := time.Parse("2006-01-02 15:04:05.000", parts[0]+" "+strings.Split(parts[1], ".")[0])
		if err == nil {
			e.Time = t.Format("2006-01-02 15:04:05")
		} else {
			e.Time = parts[0] + " " + parts[1]
		}
	}

	// Fields: [date] [time] [pid] [errorcode] [user] [src:port] [dst:port] [bytes_out] [bytes_in] ...
	if len(parts) > 4 {
		e.User = parts[4]
	}
	if len(parts) > 5 {
		e.SourceIP = parts[5]
	}
	if len(parts) > 6 {
		e.Destination = parts[6]
	}
	if len(parts) > 7 {
		e.BytesSent = formatBytes(parts[7])
	}
	if len(parts) > 8 {
		e.BytesRecv = formatBytes(parts[8])
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
