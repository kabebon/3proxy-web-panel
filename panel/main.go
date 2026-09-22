package main

import (
	"context"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
	chiMiddleware "github.com/go-chi/chi/v5/middleware"
	"panel/config"
	"panel/db"
	"panel/handlers"
	"panel/middleware"
)

func main() {
	cfg := config.Load()
	ctx := context.Background()

	pool, err := db.InitDB(ctx, cfg)
	if err != nil {
		log.Fatalf("Failed to connect to db: %v", err)
	}
	defer pool.Close()

	if err := db.RunMigrations(ctx, pool); err != nil {
		log.Fatalf("Failed to run migrations: %v", err)
	}

	if err := db.EnsureAdminUser(ctx, pool, cfg); err != nil {
		log.Fatalf("Failed to ensure admin user: %v", err)
	}

	// Initialize templates
	if err := handlers.InitTemplates("templates"); err != nil {
		log.Fatalf("Failed to load templates: %v", err)
	}

	r := chi.NewRouter()
	r.Use(chiMiddleware.Logger)
	r.Use(chiMiddleware.Recoverer)

	// Static files
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))

	// Auth routes (public)
	handlers.RegisterAuth(r, pool)

	// Protected routes
	r.Group(func(r chi.Router) {
		r.Use(middleware.Auth(pool))

		handlers.RegisterDashboard(r, pool, cfg)
		r.Mount("/users", handlers.UsersHandlers(pool))
		r.Mount("/upstreams", handlers.UpstreamsHandlers(pool))
		r.Mount("/groups", handlers.GroupsHandlers(pool))
		r.Mount("/listeners", handlers.ListenersHandlers(pool))
		r.Mount("/stats", handlers.StatsHandlers(pool, cfg))
	})

	log.Printf("🚀 3proxy Panel starting on :%s", cfg.Port)
	if err := http.ListenAndServe(":"+cfg.Port, r); err != nil {
		log.Fatal(err)
	}
}
