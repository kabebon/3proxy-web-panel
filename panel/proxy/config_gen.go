package proxy

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"panel/config"
	"panel/models"
)

func GenerateConfig(ctx context.Context, pool *pgxpool.Pool, cfg *config.Config) (string, error) {
	var sb strings.Builder

	sb.WriteString("# Global\n")
	sb.WriteString("nserver 8.8.8.8\n")
	sb.WriteString("nserver 8.8.4.4\n")
	sb.WriteString("nscache 65536\n")
	sb.WriteString("timeouts 1 5 30 60 180 1800 15 60\n")
	sb.WriteString(fmt.Sprintf("log %s D\n", cfg.ProxyLogPath))
	sb.WriteString("logformat \"- +_L%t.%. %N.%p %E %U %C:%c %R:%r %O %I %h %T\"\n")
	sb.WriteString("rotate 30\n")
	sb.WriteString("maxconn 100\n\n")

	// Users definition — expired subscriptions never reach the config
	rows, err := pool.Query(ctx, "SELECT username, password FROM proxy_users WHERE enabled = TRUE AND (expires_at IS NULL OR expires_at > NOW())")
	if err != nil {
		return "", err
	}
	defer rows.Close()

	sb.WriteString("# Users definition\n")
	var usersStr []string
	for rows.Next() {
		var u, p string
		if err := rows.Scan(&u, &p); err != nil {
			return "", err
		}
		// CL (cleartext): 3proxy 0.9.6 rejects the unsalted-md5 CR1 form this
		// panel used to emit — every login came back 407. Passwords are already
		// plaintext in the DB; harden (NT hash) separately later.
		usersStr = append(usersStr, fmt.Sprintf("%s:CL:%s", u, p))
	}
	if len(usersStr) > 0 {
		sb.WriteString("users " + strings.Join(usersStr, " ") + "\n")
	}
	sb.WriteString("\n")

	// Health failover (auto mode only): upstreams the checker marked DOWN.
	// They are excluded from active routing: group members drop to weight 0
	// (3proxy-native fallback — still reachable if everything else dies, but
	// never selected while a healthy parent exists). In monitor mode the
	// health table intentionally has zero effect on the generated config.
	downUpstreams := map[int]bool{}
	if cfg.HealthcheckMode == "auto" {
		hrows, err := pool.Query(ctx, "SELECT upstream_id FROM upstream_health WHERE status = 'down'")
		if err == nil {
			for hrows.Next() {
				var id int
				if err := hrows.Scan(&id); err == nil {
					downUpstreams[id] = true
				}
			}
			hrows.Close()
		}
	}

	// Listeners
	lrows, err := pool.Query(ctx, "SELECT id, name, protocol, port, bind_ip, upstream_id, upstream_group_id FROM listeners WHERE enabled = TRUE")
	if err != nil {
		return "", err
	}
	defer lrows.Close()

	var listeners []models.Listener
	for lrows.Next() {
		var l models.Listener
		if err := lrows.Scan(&l.ID, &l.Name, &l.Protocol, &l.Port, &l.BindIP, &l.UpstreamID, &l.UpstreamGroupID); err != nil {
			return "", err
		}
		listeners = append(listeners, l)
	}

	for _, l := range listeners {
		sb.WriteString(fmt.Sprintf("# Listener %s\n", l.Name))
		sb.WriteString("auth strong\n")
		sb.WriteString("flush\n")

		urow, err := pool.Query(ctx, "SELECT username, bandwidth_in, bandwidth_out FROM proxy_users WHERE enabled = TRUE AND (expires_at IS NULL OR expires_at > NOW()) AND listener_id = $1", l.ID)
		if err != nil {
			return "", err
		}

		var listenerUsers []string
		var bands []string
		for urow.Next() {
			var u string
			var bin, bout int
			if err := urow.Scan(&u, &bin, &bout); err != nil {
				urow.Close()
				return "", err
			}
			listenerUsers = append(listenerUsers, u)
			if bin > 0 || bout > 0 {
				bands = append(bands, fmt.Sprintf("bandlim %d %d %s", bin, bout, u))
			}
		}
		urow.Close()

		// ONE allow entry with the full user list: a 3proxy parent group binds
		// to the LAST ACL entry, so separate "allow user" lines would leave
		// everyone but the last-listed user bypassing the chain (direct exit).
		if len(listenerUsers) > 0 {
			sb.WriteString("allow " + strings.Join(listenerUsers, ",") + "\n")
		}

		// Parents must come directly after the allow rules: 3proxy attaches the
		// chain to the last "allow" ACL entry ("deny" in between is a chaining error).
		if l.UpstreamID != nil {
			var u models.Upstream
			err := pool.QueryRow(ctx, "SELECT type, host, port, username, password FROM upstreams WHERE id = $1 AND enabled = TRUE", *l.UpstreamID).Scan(&u.Type, &u.Host, &u.Port, &u.Username, &u.Password)
			if err == nil {
				sb.WriteString(parentLine(1000, u) + "\n")
			}
		} else if l.UpstreamGroupID != nil {
			grows, err := pool.Query(ctx, "SELECT u.id, u.type, u.host, u.port, u.username, u.password, m.weight FROM upstreams u JOIN upstream_group_members m ON u.id = m.upstream_id JOIN upstream_groups g ON g.id = m.group_id WHERE g.id = $1 AND g.enabled = TRUE AND u.enabled = TRUE ORDER BY m.id", *l.UpstreamGroupID)
			if err == nil {
				type member struct {
					up models.Upstream
					w  int
				}
				var members []member
				for grows.Next() {
					var m member
					if err := grows.Scan(&m.up.ID, &m.up.Type, &m.up.Host, &m.up.Port, &m.up.Username, &m.up.Password, &m.w); err == nil {
						if m.w < 0 {
							m.w = 0
						}
						members = append(members, m)
					}
				}
				grows.Close()

				// 3proxy groups parents by cumulative weight, a group ending
				// at exactly 1000. Normalize member weights to sum to 1000 so
				// the whole set forms ONE balancing group. Weight-0 members are
				// emitted as 0 = 3proxy-native fallback (used only when the
				// others fail) — the hook for health-check auto mode.
				var total int
				var actives int
				for _, m := range members {
					if m.w > 0 {
						total += m.w
						actives++
					}
				}
				if total == 0 && len(members) > 0 {
					// all members fallback/zero: degrade to equal split
					for i := range members {
						members[i].w = 1
					}
					total = len(members)
					actives = len(members)
				}

				// Health failover: DOWN actives leave the balancing group
				// (renormalize the survivors to 1000 among themselves, the
				// dead ones become weight-0 fallbacks). When NO active member
				// is healthy there is nothing to fall over to — keep the
				// original weights as best effort.
				liveActives := 0
				for _, m := range members {
					if m.w > 0 && !downUpstreams[m.up.ID] {
						liveActives++
					}
				}
				skip := actives > 0 && liveActives == 0 // keep best effort
				countSet := actives
				if !skip && liveActives < actives {
					total = 0
					countSet = 0
					for _, m := range members {
						if m.w > 0 && !downUpstreams[m.up.ID] {
							total += m.w
							countSet++
						}
					}
				}

				emitted := 0
				emittedSum := 0
				for _, m := range members {
					if m.w <= 0 || (downUpstreams[m.up.ID] && !skip) {
						sb.WriteString(parentLine(0, m.up) + "\n")
						continue
					}
					emitted++
					var w int
					if emitted == countSet {
						w = 1000 - emittedSum // last counted member absorbs the rounding remainder
					} else {
						w = 1000 * m.w / total
						emittedSum += w
					}
					sb.WriteString(parentLine(w, m.up) + "\n")
				}
			}
		}

		sb.WriteString("deny *\n")

		for _, b := range bands {
			sb.WriteString(b + "\n")
		}

		if l.Protocol == "http" {
			sb.WriteString(fmt.Sprintf("proxy -p%d -i%s\n", l.Port, l.BindIP))
		} else {
			sb.WriteString(fmt.Sprintf("socks -p%d -i%s\n", l.Port, l.BindIP))
		}
		sb.WriteString("\n")
	}

	return sb.String(), nil
}

// parentLine renders a 3proxy parent directive in the positional form understood
// by 3proxy 0.9.6 (alpine): parent <weight> <type> <host> <port> [user pass].
// The URL form "user:pass@host:port" is NOT parsed by this build.
func parentLine(weight int, u models.Upstream) string {
	ptype := "http"
	if u.Type == "socks5" {
		ptype = "socks5+"
	}
	line := fmt.Sprintf("parent %d %s %s %d", weight, ptype, u.Host, u.Port)
	if u.Username != "" {
		line += fmt.Sprintf(" %s %s", u.Username, u.Password)
	}
	return line
}

func WriteConfig(configPath string, content string) error {
	return os.WriteFile(configPath, []byte(content), 0644)
}
