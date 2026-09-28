package db

import (
	"context"
	_ "embed"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
	"panel/config"
)

//go:embed migrations/001_init.sql
var initSQL string

//go:embed migrations/002_upstream_health.sql
var upstreamHealthSQL string

//go:embed migrations/003_traffic_limits.sql
var trafficLimitsSQL string

//go:embed migrations/004_traffic_accounting.sql
var trafficAccountingSQL string

func InitDB(ctx context.Context, cfg *config.Config) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, cfg.DBUrl)
	if err != nil {
		return nil, err
	}
	return pool, nil
}

func RunMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, initSQL); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, upstreamHealthSQL); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, trafficLimitsSQL); err != nil {
		return err
	}
	_, err := pool.Exec(ctx, trafficAccountingSQL)
	return err
}

func EnsureAdminUser(ctx context.Context, pool *pgxpool.Pool, cfg *config.Config) error {
	var count int
	err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM admin_users").Scan(&count)
	if err != nil {
		return err
	}
	if count == 0 {
		hash, err := bcrypt.GenerateFromPassword([]byte(cfg.AdminPassword), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
		_, err = pool.Exec(ctx, "INSERT INTO admin_users (username, password_hash) VALUES ($1, $2)", cfg.AdminUser, string(hash))
		return err
	}
	return nil
}
