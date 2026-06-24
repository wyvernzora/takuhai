//go:build conformance

package conformance

import (
	"context"
	"fmt"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	dbmigrations "github.com/wyvernzora/takuhai/db/migrations"
	"github.com/wyvernzora/takuhai/internal/store"
	"github.com/wyvernzora/takuhai/internal/store/postgres"
)

// sharedPG is the single testcontainers-go Postgres container shared across every
// store in this test binary. It is started lazily on the first newConformanceStore*
// call (sync.Once) and torn down when the process exits — its admin connection string
// is the template each per-call fresh database is carved from.
var (
	sharedPGOnce sync.Once
	sharedPGDSN  string // admin DSN (points at the container's default database)
	sharedPGErr  error
	dbCounter    atomic.Uint64
)

// startSharedPG starts the shared Postgres container once and records its admin DSN.
// A 5-minute deadline covers the first-run image pull.
func startSharedPG() {
	sharedPGOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		container, err := tcpostgres.Run(ctx,
			"postgres:17-alpine",
			tcpostgres.WithDatabase("takuhai_admin"),
			tcpostgres.WithUsername("takuhai"),
			tcpostgres.WithPassword("takuhai"),
			tcpostgres.BasicWaitStrategies(),
		)
		if err != nil {
			sharedPGErr = fmt.Errorf("start shared postgres container: %w", err)
			return
		}
		dsn, err := container.ConnectionString(ctx, "sslmode=disable")
		if err != nil {
			sharedPGErr = fmt.Errorf("shared postgres connection string: %w", err)
			return
		}
		sharedPGDSN = dsn
		// The container is intentionally not Terminated here: it lives for the whole
		// test binary and the Docker daemon / Ryuk reaps it after the process exits.
	})
}

// dsnForDatabase rewrites the admin DSN's database path to dbName, yielding the DSN a
// per-call pool dials.
func dsnForDatabase(adminDSN, dbName string) (string, error) {
	u, err := url.Parse(adminDSN)
	if err != nil {
		return "", fmt.Errorf("parse admin DSN: %w", err)
	}
	u.Path = "/" + dbName
	return u.String(), nil
}

// newConformanceStore returns the Store the store-integration conformance tests run
// against: a fresh isolated database on the shared container, migrated to head, with
// a nil clock (defaulting to the real wall clock in postgres.NewStore).
func newConformanceStore(t *testing.T) store.Store {
	t.Helper()
	return newConformanceStoreWithClock(t, nil)
}

// newConformanceStoreWithClock is the clock-injecting variant of the harness seam: it
// threads the controllable fake clock into the real Postgres store so the
// time-dependent contract (lease expiry, first_matched_at, /healthz scrape-recency —
// design §7/§13) is deterministically testable. Each call gets its OWN fresh database
// on the shared container.
func newConformanceStoreWithClock(t *testing.T, clock store.Clock) store.Store {
	t.Helper()

	startSharedPG()
	if sharedPGErr != nil {
		t.Fatalf("shared postgres container: %v", sharedPGErr)
	}

	ctx := context.Background()
	dbName := fmt.Sprintf("takuhai_test_%d", dbCounter.Add(1))

	// Carve a fresh database on the shared container via a short-lived admin connection.
	adminPool, err := pgxpool.New(ctx, sharedPGDSN)
	if err != nil {
		t.Fatalf("admin pool: %v", err)
	}
	if _, err := adminPool.Exec(ctx, "CREATE DATABASE "+dbName); err != nil {
		adminPool.Close()
		t.Fatalf("create fresh database %q: %v", dbName, err)
	}
	adminPool.Close()

	dsn, err := dsnForDatabase(sharedPGDSN, dbName)
	if err != nil {
		t.Fatalf("derive per-call DSN: %v", err)
	}

	// Run the goose migrations into the fresh database over a database/sql handle (the
	// goose Go-library runner works over database/sql; the pgx/v5 stdlib driver bridges
	// the pgx connection string).
	sqlDB := stdlib.OpenDB(*mustParseConfig(t, dsn))
	if err := dbmigrations.Run(ctx, sqlDB); err != nil {
		sqlDB.Close()
		t.Fatalf("run migrations into %q: %v", dbName, err)
	}
	sqlDB.Close()

	// The store owns its own pool against the migrated fresh database.
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("store pool for %q: %v", dbName, err)
	}
	st := postgres.NewStore(pool, clock)

	t.Cleanup(func() {
		// Close the store's pool first (it is the only holder of connections to the
		// fresh database), then drop the database via a fresh admin connection.
		_ = st.Close()
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		admin, err := pgxpool.New(cleanupCtx, sharedPGDSN)
		if err != nil {
			t.Logf("cleanup: admin pool for drop %q: %v", dbName, err)
			return
		}
		defer admin.Close()
		if _, err := admin.Exec(cleanupCtx, "DROP DATABASE IF EXISTS "+dbName+" WITH (FORCE)"); err != nil {
			t.Logf("cleanup: drop database %q: %v", dbName, err)
		}
	})

	return st
}

// mustParseConfig parses a pgx connection config for the stdlib bridge, failing the
// test on a malformed DSN.
func mustParseConfig(t *testing.T, dsn string) *pgx.ConnConfig {
	t.Helper()
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse pgx config: %v", err)
	}
	return cfg
}
