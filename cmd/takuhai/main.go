// Command takuhai is the release-indexer service entrypoint.
//
// takuhai (宅配, "home delivery / courier") continuously ingests anime
// releases from pluggable sources (DMHY first), stores them immutably,
// dedups them into a queryable catalog, and exposes both a consumer query
// API and a worker work-queue over MCP. The actual identity matching is
// performed by an external agent; the indexer only records what that agent
// reports.
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
	"os"
	"os/signal"
	"syscall"

	// time/tzdata bakes the IANA zoneinfo database into the binary so
	// timezone-dependent parsing works in distroless/scratch images that
	// lack /usr/share/zoneinfo.
	_ "time/tzdata"
)

// version is overridable at link time via -ldflags="-X main.version=...".
var version = "0.1.0"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "takuhai:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		showVersion = flag.Bool("version", false, "print version and exit")
		transport   = stringFlag("transport", "TAKUHAI_TRANSPORT", "stdio", "transport: stdio or http")
		addr        = stringFlag("addr", "TAKUHAI_ADDR", ":8080", "listen address (http transport only)")
		databaseURL = stringFlag("database-url", "TAKUHAI_DATABASE_URL", "", "PostgreSQL connection string")
		logLevel    = stringFlag("log-level", "TAKUHAI_LOG_LEVEL", "info", "log level: debug, info, warn, error")
	)
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return nil
	}

	level, err := parseLogLevel(*logLevel)
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	switch *transport {
	case "stdio", "http":
	default:
		return fmt.Errorf("invalid --transport %q (want stdio or http)", *transport)
	}

	// TODO(phase-1): wire the Postgres store, ingestion core, DMHY source,
	// and the MCP server (consumer + worker tool groups). The skeleton only
	// validates configuration and the shutdown path for now.
	logger.Info("takuhai starting",
		"version", version,
		"transport", *transport,
		"addr", *addr,
		"database_configured", *databaseURL != "",
	)
	logger.Warn("server not yet implemented; this is a bootstrap skeleton (see docs/indexer-handover.md §13)")

	<-ctx.Done()
	logger.Info("takuhai shutting down")
	return nil
}

func stringFlag(name, env, def, usage string) *string {
	if v := os.Getenv(env); v != "" {
		def = v
	}
	return flag.String(name, def, fmt.Sprintf("%s (env %s)", usage, env))
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
