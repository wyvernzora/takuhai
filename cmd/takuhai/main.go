// Command takuhai is the release-indexer service entrypoint.
//
// takuhai (宅配, "home delivery / courier") is a passive store + work queue +
// query API for anime releases. Ingestion is external push: n8n drives the
// stateless crawler and POSTs raw posts to /ingest, then drives the match loop
// over the queue REST API. Consumer agents read the catalog over the /mcp
// endpoint. The actual identity matching is performed by an external agent; the
// indexer only records what that agent reports.
//
// All configuration is via flags, each honoring a TAKUHAI_-prefixed
// environment-variable fallback so container deployments can configure the
// binary without crafting an args list.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"

	dbmigrations "github.com/wyvernzora/takuhai/db/migrations"
	"github.com/wyvernzora/takuhai/internal/config"
	"github.com/wyvernzora/takuhai/internal/health"
	"github.com/wyvernzora/takuhai/internal/mcp"
	"github.com/wyvernzora/takuhai/internal/metrics"
	"github.com/wyvernzora/takuhai/internal/rest"
	"github.com/wyvernzora/takuhai/internal/store/postgres"

	// time/tzdata bakes the IANA zoneinfo database into the binary so
	// timezone-dependent parsing works in distroless/scratch images that
	// lack /usr/share/zoneinfo.
	_ "time/tzdata"
)

// version and commit are overridable at link time via -ldflags.
var (
	version = "0.1.0"
	commit  = "unknown"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "takuhai:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		showVersion      = flag.Bool("version", false, "print version and exit")
		addr             = stringFlag("addr", "TAKUHAI_ADDR", ":8080", "listen address")
		databaseURL      = stringFlag("database-url", "TAKUHAI_DATABASE_URL", "", "PostgreSQL connection string")
		logLevel         = stringFlag("log-level", "TAKUHAI_LOG_LEVEL", "info", "log level: debug, info, warn, error")
		queueMaxAttempts = intFlag("queue-max-attempts", "TAKUHAI_QUEUE_MAX_ATTEMPTS", 3, "max unmatched submits before a release becomes exhausted")
	)
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return nil
	}

	cfg := config.Config{
		Addr:             *addr,
		DatabaseURL:      *databaseURL,
		LogLevel:         *logLevel,
		QueueMaxAttempts: *queueMaxAttempts,
	}
	if err := cfg.Validate(); err != nil {
		return err
	}

	level, err := parseLogLevel(cfg.LogLevel)
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Bring the schema to head BEFORE serving (the embedded goose migrations are
	// idempotent — a database already at head is a no-op). A migration failure aborts
	// startup with a non-zero exit; we never serve against an unmigrated database.
	if err := runMigrations(ctx, cfg.DatabaseURL); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}

	// Construct the Postgres store.
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connect database: %w", err)
	}
	st := postgres.NewStoreWithConfig(pool, nil, postgres.StoreConfig{
		QueueMaxAttempts: cfg.QueueMaxAttempts,
	})
	defer st.Close() //nolint:errcheck // best-effort cleanup at process exit.

	// The single mountable /healthz handler (DB ping only — design §10). Both the HTTP
	// listener and the MCP server mount the SAME handler.
	healthz := health.NewHandler(st)
	metricsSrv := metrics.NewTakuhai(version, commit, st)

	// The consumer-only MCP server (list_releases / resolve_magnets). Its Handler() serves
	// /mcp + /healthz.
	mcpSrv := mcp.NewServerWithMetrics(st, healthz, metricsSrv)

	logger.Info("takuhai starting",
		"version", version,
		"addr", cfg.Addr,
	)

	return runHTTP(ctx, logger, cfg.Addr, st, mcpSrv, healthz, metricsSrv)
}

// runHTTP mounts every HTTP route — /ingest (push), /queue/* + /submit (match loop), /mcp +
// /healthz (consumer + health) — on one listener.
func runHTTP(
	ctx context.Context,
	logger *slog.Logger,
	addr string,
	st *postgres.Store,
	mcpSrv *mcp.Server,
	healthz http.Handler,
	metricsSrv *metrics.Takuhai,
) error {
	mux := http.NewServeMux()
	// The consumer /mcp endpoint + /healthz (the MCP server owns this mux).
	mux.Handle("/mcp", mcpSrv.Handler())
	mux.Handle("/healthz", healthz)
	mux.Handle("/metrics", metricsSrv.Handler())
	// The REST push-ingestion and match-loop surfaces.
	restAPI := rest.NewWithMetrics(st, metricsSrv)
	mux.Handle("/ingest", restAPI)
	mux.Handle("/queue/", restAPI)
	mux.Handle("/submit", restAPI)

	srv := &http.Server{Addr: addr, Handler: metricsSrv.HTTP.Wrap(mux)}

	// Bind SYNCHRONOUSLY so a failed bind (e.g. the port is already in use) fails fast:
	// run() returns the error promptly with a non-zero exit instead of leaving a process
	// "up" but serving nothing until SIGTERM (F16). Only after the listener is accepting
	// do we hand off to the background serve loop.
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("bind %s: %w", addr, err)
	}
	logger.Info("takuhai listening", "addr", ln.Addr().String())

	// Serve in the background. serveErr carries the loop's single terminal error —
	// ErrServerClosed after a clean Shutdown, or the fail-fast cause if the listener
	// dies on its own.
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()

	select {
	case <-ctx.Done():
		logger.Info("takuhai shutting down")
		shutCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), drainTimeout)
		defer cancel()
		if err := srv.Shutdown(shutCtx); err != nil {
			_ = srv.Close()
		}
		if err := <-serveErr; err != nil && err != http.ErrServerClosed {
			return err
		}
		return nil
	case err := <-serveErr:
		if err != nil && err != http.ErrServerClosed {
			return err
		}
		return nil
	}
}

// drainTimeout bounds the graceful HTTP shutdown. A consumer holding an open /mcp
// standalone SSE GET stream blocks server-side until its request context is cancelled,
// which http.Server.Shutdown does NOT do — so an unbounded Shutdown would hang the whole
// drain forever on a steady-state SIGTERM. The deadline caps in-flight wait; on expiry
// srv.Close force-closes lingering connections so shutdown can finish.
const drainTimeout = 10 * time.Second

// runMigrations brings the target database to head over a short-lived database/sql
// handle (the embedded goose runner works over database/sql; the pgx/v5 stdlib driver
// bridges the pgx connection string). It is idempotent and closes the handle before
// the service opens its own pool.
func runMigrations(ctx context.Context, databaseURL string) error {
	cfg, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		return fmt.Errorf("parse database url: %w", err)
	}
	sqlDB := stdlib.OpenDB(*cfg)
	defer sqlDB.Close()
	return dbmigrations.Run(ctx, sqlDB)
}

func stringFlag(name, env, def, usage string) *string {
	if v := os.Getenv(env); v != "" {
		def = v
	}
	return flag.String(name, def, fmt.Sprintf("%s (env %s)", usage, env))
}

func intFlag(name, env string, def int, usage string) *int {
	if v := os.Getenv(env); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			def = n
		}
	}
	return flag.Int(name, def, fmt.Sprintf("%s (env %s)", usage, env))
}

func parseLogLevel(s string) (slog.Level, error) {
	switch s {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("invalid --log-level %q", s)
	}
}
