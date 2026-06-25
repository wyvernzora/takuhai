// Command takuhai-dmhy is the stateless DMHY crawler.
//
//	takuhai-dmhy serve   serves POST /crawl: given {page_size, cursor?, lookback?} it
//	                     walks the DMHY HTML archive newest→oldest (page 1 is the latest)
//	                     and returns {posts, next_cursor, has_more}.
//	takuhai-dmhy parse   parses locally-saved DMHY archive HTML into RawPost JSONL — the
//	                     same parser the live crawl uses, fed from a file instead of the
//	                     network — for offline backfill (POST the JSONL into /ingest).
//
// The crawler is DUMB: it emits raw posts (title+magnet+metadata+size) and does NOT
// normalize the infohash or dedup; takuhai derives the dedup key on /ingest. It holds
// no state across requests — n8n passes the cursor in and stores next_cursor.
//
// serve flags each honor a TAKUHAI_DMHY_-prefixed environment-variable fallback so
// container deployments configure the binary without crafting an args list.
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

	// time/tzdata bakes the IANA zoneinfo database into the binary so
	// timezone-dependent parsing works in distroless/scratch images.
	_ "time/tzdata"

	"github.com/wyvernzora/takuhai/internal/metrics"
	"github.com/wyvernzora/takuhai/sources/dmhy"
)

// version and commit are overridable at link time via -ldflags.
var (
	version = "0.1.0"
	commit  = "unknown"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "takuhai-dmhy:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("missing command: serve or parse")
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
	case "parse":
		cmd, err := parseParse(args[1:])
		if err != nil {
			return err
		}
		return cmd.Run()
	default:
		return fmt.Errorf("unknown command %q: want serve or parse", args[0])
	}
}

// ServeCmd serves POST /crawl. Its flags honor TAKUHAI_DMHY_-prefixed env fallbacks.
type ServeCmd struct {
	Addr     string
	BaseURL  string
	SortID   int
	RateRPS  float64
	CacheTTL time.Duration
	LogLevel string
}

func parseServe(args []string) (ServeCmd, error) {
	cmd, err := serveDefaults()
	if err != nil {
		return ServeCmd{}, err
	}
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.StringVar(&cmd.Addr, "addr", cmd.Addr, "listen address")
	fs.StringVar(&cmd.BaseURL, "dmhy-base-url", cmd.BaseURL, "DMHY base URL to crawl")
	fs.IntVar(&cmd.SortID, "sort-id", cmd.SortID, "DMHY sort_id to crawl; <=0 uses bare archive path")
	fs.Float64Var(&cmd.RateRPS, "rate-rps", cmd.RateRPS, "crawl rate limit in requests/sec; <=0 disables")
	fs.DurationVar(&cmd.CacheTTL, "cache-ttl", cmd.CacheTTL, "in-memory page cache TTL; 0 disables")
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
	sortID, err := envInt("TAKUHAI_DMHY_SORT_ID", 2)
	if err != nil {
		return ServeCmd{}, err
	}
	rateRPS, err := envFloat("TAKUHAI_DMHY_RATE_RPS", 0.5)
	if err != nil {
		return ServeCmd{}, err
	}
	cacheTTL, err := envDuration("TAKUHAI_DMHY_CACHE_TTL", 10*time.Minute)
	if err != nil {
		return ServeCmd{}, err
	}
	return ServeCmd{
		Addr:     envString("TAKUHAI_DMHY_ADDR", ":8081"),
		BaseURL:  envString("TAKUHAI_DMHY_BASE_URL", "https://share.dmhy.org"),
		SortID:   sortID,
		RateRPS:  rateRPS,
		CacheTTL: cacheTTL,
		LogLevel: envString("TAKUHAI_DMHY_LOG_LEVEL", "info"),
	}, nil
}

func parseParse(args []string) (ParseCmd, error) {
	cmd := ParseCmd{Out: "-"}
	fs := flag.NewFlagSet("parse", flag.ContinueOnError)
	fs.StringVar(&cmd.Out, "out", cmd.Out, "write JSONL here; '-' = stdout")
	fs.StringVar(&cmd.Out, "o", cmd.Out, "write JSONL here; '-' = stdout")
	fs.BoolVar(&cmd.KeepGoing, "keep-going", false, "continue after per-file parse failures")
	if err := fs.Parse(args); err != nil {
		return ParseCmd{}, err
	}
	cmd.Files = fs.Args()
	if len(cmd.Files) == 0 {
		return ParseCmd{}, fmt.Errorf("parse needs at least one file or '-'")
	}
	return cmd, nil
}

func envString(env, def string) string {
	if v, ok := os.LookupEnv(env); ok {
		return v
	}
	return def
}

func envInt(env string, def int) (int, error) {
	v, ok := os.LookupEnv(env)
	if !ok {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", env, err)
	}
	return n, nil
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

func envDuration(env string, def time.Duration) (time.Duration, error) {
	v, ok := os.LookupEnv(env)
	if !ok {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", env, err)
	}
	return d, nil
}

// Run boots the crawl server and blocks until SIGINT/SIGTERM, then drains.
func (c *ServeCmd) Run() error {
	level := slogLevel(c.LogLevel)
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	metricsSrv := metrics.NewDMHY(version, commit)
	srv := dmhy.NewServerWithMetrics(c.BaseURL, c.SortID, c.RateRPS, c.CacheTTL, metricsSrv)

	mux := http.NewServeMux()
	mux.Handle("/crawl", srv)
	mux.Handle("/metrics", metricsSrv.Handler())
	httpSrv := &http.Server{Addr: c.Addr, Handler: metricsSrv.HTTP.Wrap(mux)}

	logger.Info("takuhai-dmhy starting",
		"version", version,
		"addr", c.Addr,
		"dmhy_base_url", c.BaseURL,
		"sort_id", c.SortID,
		"rate_rps", c.RateRPS,
		"cache_ttl", c.CacheTTL.String(),
	)

	errCh := make(chan error, 1)
	go func() {
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	logger.Info("takuhai-dmhy shutting down")
	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return httpSrv.Shutdown(shutCtx)
}

// slogLevel maps a validated --log-level to a slog.Level.
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
