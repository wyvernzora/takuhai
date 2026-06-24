// Package migrations embeds takuhai's goose SQL migrations and runs them via the
// goose Go library (no goose CLI). The schema is the §3 DDL; the first migration
// creates the core tables, the match_status enum, and the hot-path partial
// indexes. The conformance harness and cmd/takuhai call Run to bring a fresh
// database up to head before serving.
package migrations

import (
	"context"
	"database/sql"
	"embed"
	"fmt"

	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

//go:embed *.sql
var fsys embed.FS

// newProvider builds the goose Provider: the Postgres dialect, the embedded SQL files,
// and a session-level advisory lock that serializes concurrent runners.
func newProvider(db *sql.DB) (*goose.Provider, error) {
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return nil, fmt.Errorf("migrations: new session locker: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, fsys, goose.WithSessionLocker(locker))
	if err != nil {
		return nil, fmt.Errorf("migrations: new provider: %w", err)
	}
	return provider, nil
}

// Run applies all pending migrations up to head against db.
func Run(ctx context.Context, db *sql.DB) error {
	provider, err := newProvider(db)
	if err != nil {
		return err
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("migrations: up: %w", err)
	}
	return nil
}
