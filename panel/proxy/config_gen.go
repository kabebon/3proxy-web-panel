package proxy

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"panel/models"
)

func GenerateConfig(ctx context.Context, pool *pgxpool.Pool, logPath string) (string, error) {
	var sb strings.Builder

	sb.WriteString("# Global\n")
	sb.WriteString("nserver 8.8.8.8\n")
	sb.WriteString("nserver 8.8.4.4\n")
	sb.WriteString("nscache 65536\n")
	sb.WriteString("timeouts 1 5 30 60 180 1800 15 60\n")
	sb.WriteString(fmt.Sprintf("log %s D\n", logPath))
	sb.WriteString("logformat \"- +_L%t.%. %N.%p %E %U %C:%c %R:%r %O %I %h %T\"\n")
	sb.WriteString("rotate 30\n")
	sb.WriteString("maxconn 100\n\n")

	// Users definition
	rows, err := pool.Query(ctx, "SELECT username, password FROM proxy_users WHERE enabled = TRUE")
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
		hash := md5.Sum([]byte(p))
		hashHex := hex.EncodeToString(hash[:])
		usersStr = append(usersStr, fmt.Sprintf("%s:CR1:%s", u, hashHex))
	}
	if len(usersStr) > 0 {
		sb.WriteString("users " + strings.Join(usersStr, " ") + "\n")
	}
	sb.WriteString("\n")

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

		urow, err := pool.Query(ctx, "SELECT username, bandwidth_in, bandwidth_out FROM proxy_users WHERE enabled = TRUE AND listener_id = $1", l.ID)
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

		for _, u := range listenerUsers {
			sb.WriteString(fmt.Sprintf("allow %s\n", u))
		}
		sb.WriteString("deny *\n")

		for _, b := range bands {
			sb.WriteString(b + "\n")
		}

		if l.UpstreamID != nil {
			var u models.Upstream
			err := pool.QueryRow(ctx, "SELECT type, host, port, username, password FROM upstreams WHERE id = $1 AND enabled = TRUE", *l.UpstreamID).Scan(&u.Type, &u.Host, &u.Port, &u.Username, &u.Password)
			if err == nil {
				prefix := "http+"
				if u.Type == "socks5" {
					prefix = "socks5+"
				}
				auth := ""
				if u.Username != "" {
					auth = fmt.Sprintf("%s:%s@", u.Username, u.Password)
				}
				sb.WriteString(fmt.Sprintf("parent 1000 %s %s%s:%d\n", prefix, auth, u.Host, u.Port))
			}
		} else if l.UpstreamGroupID != nil {
			grows, err := pool.Query(ctx, "SELECT u.type, u.host, u.port, u.username, u.password FROM upstreams u JOIN upstream_group_members m ON u.id = m.upstream_id JOIN upstream_groups g ON g.id = m.group_id WHERE g.id = $1 AND g.enabled = TRUE AND u.enabled = TRUE", *l.UpstreamGroupID)
			if err == nil {
				for grows.Next() {
					var u models.Upstream
					if err := grows.Scan(&u.Type, &u.Host, &u.Port, &u.Username, &u.Password); err == nil {
						prefix := "http+"
						if u.Type == "socks5" {
							prefix = "socks5+"
						}
						auth := ""
						if u.Username != "" {
							auth = fmt.Sprintf("%s:%s@", u.Username, u.Password)
						}
						sb.WriteString(fmt.Sprintf("parent 1000 %s %s%s:%d\n", prefix, auth, u.Host, u.Port))
					}
				}
				grows.Close()
			}
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

func WriteConfig(configPath string, content string) error {
	return os.WriteFile(configPath, []byte(content), 0644)
}
