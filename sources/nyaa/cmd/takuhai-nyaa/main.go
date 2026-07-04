// Command takuhai-nyaa is the stateless Nyaa crawler.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/felixge/httpsnoop"

	// time/tzdata bakes the IANA zoneinfo database into the binary so
	// timezone-dependent parsing works in distroless/scratch images.
	_ "time/tzdata"

	"github.com/wyvernzora/takuhai/internal/metrics"
	"github.com/wyvernzora/takuhai/sources/nyaa"
)

var (
	version = "0.1.0"
	commit  = "unknown"
)

type ServeCmd struct {
	Addr     string
	BaseURL  string
	Query    string
	Category string
	Filter   string
	RateRPS  float64
	LogLevel string
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "takuhai-nyaa:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("missing command: serve")
	}
	switch args[0] {
	case "--version", "-version", "version":
		fmt.Fprintln(stdout, version)
		return nil
	case "serve":
		cmd, err := parseServe(args[1:])
		if err != nil {
			return err
		}
		return cmd.Run()
	default:
		return fmt.Errorf("unknown command %q: want serve", args[0])
	}
}

func parseServe(args []string) (ServeCmd, error) {
	cmd, err := serveDefaults()
	if err != nil {
		return ServeCmd{}, err
	}
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.StringVar(&cmd.Addr, "addr", cmd.Addr, "listen address")
	fs.StringVar(&cmd.BaseURL, "nyaa-base-url", cmd.BaseURL, "Nyaa base URL to crawl")
	fs.StringVar(&cmd.Query, "query", cmd.Query, "Nyaa listing search query")
	fs.StringVar(&cmd.Category, "category", cmd.Category, "Nyaa category id; empty means all categories")
	fs.StringVar(&cmd.Filter, "filter", cmd.Filter, "Nyaa filter id; 0=none, 1=no remakes, 2=trusted only")
	fs.Float64Var(&cmd.RateRPS, "rate-rps", cmd.RateRPS, "crawl rate limit in requests/sec; <=0 disables")
	fs.StringVar(&cmd.LogLevel, "log-level", cmd.LogLevel, "log level: debug, info, warn, error")
	if err := fs.Parse(args); err != nil {
		return ServeCmd{}, err
	}
	if fs.NArg() != 0 {
		return ServeCmd{}, fmt.Errorf("serve takes no positional args")
	}
	switch cmd.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return ServeCmd{}, fmt.Errorf("invalid --log-level %q", cmd.LogLevel)
	}
	return cmd, nil
}

func serveDefaults() (ServeCmd, error) {
	rateRPS, err := envFloat("TAKUHAI_NYAA_RATE_RPS", 0.5)
	if err != nil {
		return ServeCmd{}, err
	}
	return ServeCmd{
		Addr:     envString("TAKUHAI_NYAA_ADDR", ":8082"),
		BaseURL:  envString("TAKUHAI_NYAA_BASE_URL", "https://nyaa.si"),
		Query:    envString("TAKUHAI_NYAA_QUERY", ""),
		Category: envString("TAKUHAI_NYAA_CATEGORY", "1_0"),
		Filter:   envString("TAKUHAI_NYAA_FILTER", "0"),
		RateRPS:  rateRPS,
		LogLevel: envString("TAKUHAI_NYAA_LOG_LEVEL", "info"),
	}, nil
}

func envString(env, def string) string {
	if v, ok := os.LookupEnv(env); ok {
		return v
	}
	return def
}

func envFloat(env string, def float64) (float64, error) {
	v, ok := os.LookupEnv(env)
	if !ok {
		return def, nil
	}
	n, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", env, err)
	}
	return n, nil
}

func (c *ServeCmd) Run() error {
	level := slogLevel(c.LogLevel)
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	metricsSrv := metrics.NewCrawler("takuhai_nyaa", "Nyaa", version, commit)
	srv := nyaa.NewServerWithMetricsAndLogger(c.BaseURL, c.Query, c.Category, c.Filter, c.RateRPS, metricsSrv, logger.With("component", "crawler"))

	mux := http.NewServeMux()
	mux.Handle("/crawl", srv)
	mux.Handle("/metrics", metricsSrv.Handler())
	httpSrv := &http.Server{Addr: c.Addr, Handler: logHTTP(logger, metricsSrv.HTTP, metricsSrv.HTTP.Wrap(mux))}

	logger.Info("takuhai-nyaa starting",
		"version", version,
		"addr", c.Addr,
		"nyaa_base_url", c.BaseURL,
		"query", c.Query,
		"category", c.Category,
		"filter", c.Filter,
		"rate_rps", c.RateRPS,
	)

	errCh := make(chan error, 1)
	go func() {
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("server stopped with error", "err", err)
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	logger.Info("takuhai-nyaa shutting down")
	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutCtx); err != nil {
		logger.Warn("graceful shutdown timed out", "err", err)
		return err
	}
	logger.Info("takuhai-nyaa stopped")
	return nil
}

func logHTTP(logger *slog.Logger, routes interface{ Route(string) string }, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		captured := httpsnoop.CaptureMetrics(next, w, r)
		path := routes.Route(r.URL.Path)
		if path == "/metrics" {
			return
		}
		logger.InfoContext(r.Context(), "http request completed",
			"component", "http",
			"method", r.Method,
			"path", path,
			"status", captured.Code,
			"duration_ms", time.Since(start).Milliseconds(),
			"response_bytes", captured.Written,
		)
	})
}

func slogLevel(s string) slog.Level {
	switch s {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
