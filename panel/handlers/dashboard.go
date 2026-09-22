package handlers

import (
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"panel/config"
	"panel/proxy"
)

func DashboardHandlers(pool *pgxpool.Pool, cfg *config.Config) http.Handler {
	r := chi.NewRouter()

	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		var userCount, upstreamCount, listenerCount, groupCount int
		pool.QueryRow(r.Context(), "SELECT COUNT(*) FROM proxy_users").Scan(&userCount)
		pool.QueryRow(r.Context(), "SELECT COUNT(*) FROM upstreams").Scan(&upstreamCount)
		pool.QueryRow(r.Context(), "SELECT COUNT(*) FROM listeners WHERE enabled=true").Scan(&listenerCount)
		pool.QueryRow(r.Context(), "SELECT COUNT(*) FROM upstream_groups").Scan(&groupCount)

		// Check if 3proxy container is running
		isRunning := proxy.IsProxyRunning(cfg.ProxyContainerName)

		render(w, r, "dashboard.html", map[string]any{
			"UserCount":     userCount,
			"UpstreamCount": upstreamCount,
			"ListenerCount": listenerCount,
			"GroupCount":    groupCount,
			"IsRunning":     isRunning,
		})
	})

	r.Post("/proxy/apply", func(w http.ResponseWriter, r *http.Request) {
		cfgContent, err := proxy.GenerateConfig(r.Context(), pool, cfg.ProxyLogPath)
		if err != nil {
			triggerToast(w, "Config generation failed: "+err.Error(), "error")
			w.WriteHeader(500)
			fmt.Fprintf(w, `<div class="bg-red-900/50 border border-red-700 text-red-300 px-4 py-3 rounded-md text-sm">Error: %s</div>`, err.Error())
			return
		}
		if err = proxy.WriteConfig(cfg.ProxyConfigPath, cfgContent); err != nil {
			triggerToast(w, "Failed to write config: "+err.Error(), "error")
			w.WriteHeader(500)
			fmt.Fprintf(w, `<div class="bg-red-900/50 border border-red-700 text-red-300 px-4 py-3 rounded-md text-sm">Error: %s</div>`, err.Error())
			return
		}
		if err = proxy.ReloadProxy(cfg.ProxyContainerName); err != nil {
			triggerToast(w, "Config written but reload failed: "+err.Error(), "error")
			w.WriteHeader(500)
			fmt.Fprintf(w, `<div class="bg-yellow-900/50 border border-yellow-700 text-yellow-300 px-4 py-3 rounded-md text-sm">Config saved but reload failed: %s</div>`, err.Error())
			return
		}
		triggerToast(w, "Config applied successfully!", "success")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<div class="bg-green-900/50 border border-green-700 text-green-300 px-4 py-3 rounded-md text-sm flex items-center space-x-2"><span>✓ Config applied and 3proxy reloaded successfully.</span></div>`)
	})

	return r
}
