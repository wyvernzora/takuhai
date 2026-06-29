package rest

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/wyvernzora/takuhai/internal/dispatch"
)

func (h *Handler) handleClaim(w http.ResponseWriter, r *http.Request) {
	body, ok := h.requirePost(w, r)
	if !ok {
		h.metrics.QueueClaim(0, "error")
		h.log(r, slog.LevelDebug, "queue claim rejected", "reason", "method_or_body")
		return
	}
	var req dispatch.ClaimRequest
	if err := json.Unmarshal(body, &req); err != nil {
		h.metrics.QueueClaim(0, "error")
		h.log(r, slog.LevelInfo, "queue claim rejected", "reason", "invalid_body", "err", err)
		writeBadInput(w, "invalid request body")
		return
	}
	out, err := h.dispatch.ClaimTyped(r.Context(), req)
	if err != nil {
		h.metrics.QueueClaim(0, "error")
		h.log(r, dispatchLogLevel(err), "queue claim failed",
			"limit", req.Limit,
			"lease_seconds", req.LeaseSeconds,
			"code", dispatch.WireCode(err),
			"err", err,
		)
		h.writeDispatchError(w, "", err)
		return
	}
	if len(out.Items) == 0 {
		h.metrics.QueueClaim(0, "empty")
	} else {
		h.metrics.QueueClaim(len(out.Items), "claimed")
	}
	h.log(r, slog.LevelInfo, "queue claim completed",
		"limit", req.Limit,
		"lease_seconds", req.LeaseSeconds,
		"claimed_count", len(out.Items),
	)
	writeJSONValue(w, http.StatusOK, out)
}

func (h *Handler) handleQueueStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		h.log(r, slog.LevelDebug, "queue stats rejected", "reason", "method_not_allowed", "method", r.Method)
		writeError(w, http.StatusMethodNotAllowed, "", "", "method not allowed")
		return
	}
	out, err := h.dispatch.QueueStatsTyped(r.Context())
	if err != nil {
		h.log(r, dispatchLogLevel(err), "queue stats failed", "code", dispatch.WireCode(err), "err", err)
		h.writeDispatchError(w, "", err)
		return
	}
	h.log(r, slog.LevelDebug, "queue stats completed",
		"available", out.Available,
		"leased", out.Leased,
		"unmatched", out.Unmatched,
		"matched", out.Matched,
		"suppressed", out.Suppressed,
		"exhausted", out.Exhausted,
	)
	writeJSONValue(w, http.StatusOK, out)
}
