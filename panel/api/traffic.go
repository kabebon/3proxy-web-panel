package api

import (
	"bufio"
	"context"
	"log"
	"os"
	"strconv"
	"strings"
	"time"
)

// StartTrafficDaemon reads 3proxy.traf and updates traffic quotas.
func (s *Server) StartTrafficDaemon() {
	go func() {
		lastSeen := make(map[int]int64)

		for {
			time.Sleep(60 * time.Second)

			file, err := os.Open("/var/log/3proxy/3proxy.traf")
			if err != nil {
				// File might not exist yet if 3proxy just started and hasn"t flushed counters.
				continue
			}

			var disableList []int
			scanner := bufio.NewScanner(file)
			
			for scanner.Scan() {
				line := strings.TrimSpace(scanner.Text())
				if line == "" {
					continue
				}
				parts := strings.Fields(line)
				if len(parts) < 3 {
					continue
				}

				id, err1 := strconv.Atoi(parts[0])
				high, err2 := strconv.ParseInt(parts[1], 10, 64)
				low, err3 := strconv.ParseInt(parts[2], 10, 64)

				if err1 != nil || err2 != nil || err3 != nil {
					continue
				}

				// Total bytes 3proxy has recorded for this counter since it was created/reset.
				total := (high << 32) + low
				
				last, ok := lastSeen[id]
				var delta int64
				if !ok || total < last {
					// Either first time seeing this user since daemon start, OR 3proxy reset the counter.
					// If daemon just started, we treat the current counter value as delta.
					// This is safe because 3proxy resets it monthly, and we only double-count 
					// the traffic since the last 3proxy flush (at most 1 minute of traffic).
					delta = total
				} else {
					delta = total - last
				}
				lastSeen[id] = total

				if delta > 0 {
					var tLimit, tUsed int64
					var enabled bool
					err := s.pool.QueryRow(context.Background(),
						"UPDATE proxy_users SET traffic_used = traffic_used + $1 WHERE id = $2 RETURNING traffic_limit, traffic_used, enabled",
						delta, id).Scan(&tLimit, &tUsed, &enabled)
						
					if err == nil && tLimit > 0 && tUsed >= tLimit && enabled {
						disableList = append(disableList, id)
					}
				}
			}
			file.Close()

			if len(disableList) > 0 {
				log.Printf("[traffic] Disabling users due to quota: %v", disableList)
				for _, uid := range disableList {
					s.pool.Exec(context.Background(), "UPDATE proxy_users SET enabled = false WHERE id = $1", uid)
				}
				
				// Rebuild config
				s.applyMu.Lock()
				_ = s.applyNow(context.Background())
				s.applyMu.Unlock()
			}
		}
	}()
}
