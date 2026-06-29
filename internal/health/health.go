// Package health owns the standalone /healthz handler (design §10/§11).
//
// /healthz is NOT owned by internal/mcp: it is a single mountable http.Handler
// constructed from a Store, so both surfaces can mount the SAME handler — the
// plain net/http listener and the internal/mcp SDK server mount the identical
// handler. Its contract is a single DB-reachability check (design §10, as amended
// by workspace-migration §2): ingestion is external push now, so /healthz no longer
// carries a scrape-recency signal — liveness is purely "can we reach the DB".
package health

import (
	"log/slog"
	"net/http"

	"github.com/wyvernzora/takuhai/internal/store"
)

// handler is the /healthz http.Handler. It probes the Store's DB reachability and
// reports 200 when the ping succeeds, non-200 otherwise.
type handler struct {
	store  store.Store
	logger *slog.Logger
}

// NewHandler constructs the standalone /healthz handler from the Store (design
// §10/§11). The clock argument the old scrape-recency check needed is gone — the
// probe is a pure DB ping now.
func NewHandler(s store.Store) http.Handler {
	return &handler{store: s}
}

func NewHandlerWithLogger(s store.Store, logger *slog.Logger) http.Handler {
	return &handler{store: s, logger: logger}
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Single readiness check: a live DB round-trip. A closed/unreachable pool fails
	// the ping => non-200; never a bare 200-OK stub (§10/§11).
	if err := h.store.Ping(r.Context()); err != nil {
		if h.logger != nil {
			h.logger.WarnContext(r.Context(), "health check failed", "err", err)
		}
		http.Error(w, "unhealthy: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}
