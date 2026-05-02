// Package store handles all Postgres persistence for the agent.
package store

import (
	"context"
	"embed"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrations embed.FS

// DB wraps a pgxpool.Pool and provides typed query helpers.
type DB struct {
	Pool *pgxpool.Pool
}

// Open connects to Postgres and runs any pending migrations.
func Open(ctx context.Context, dsn string) (*DB, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("store: connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("store: ping: %w", err)
	}

	db := &DB{Pool: pool}
	if err := db.migrate(); err != nil {
		return nil, fmt.Errorf("store: migrate: %w", err)
	}
	return db, nil
}

// Close releases pool connections.
func (db *DB) Close() { db.Pool.Close() }

func (db *DB) migrate() error {
	goose.SetBaseFS(migrations)
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	sqlDB := stdlib.OpenDBFromPool(db.Pool)
	defer sqlDB.Close()
	return goose.Up(sqlDB, "migrations")
}
