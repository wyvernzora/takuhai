package rest

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/wyvernzora/takuhai/internal/dispatch"
)

func (h *Handler) handleSubmit(w http.ResponseWriter, r *http.Request) {
	body, ok := h.requirePost(w, r)
	if !ok {
		h.metrics.Submit("invalid", "error", nil)
		h.log(r, slog.LevelDebug, "submit rejected", "reason", "method_or_body")
		return
	}
	var req dispatch.SubmitRequest
	if err := json.Unmarshal(body, &req); err != nil {
		h.metrics.Submit("invalid", "error", nil)
		h.log(r, slog.LevelInfo, "submit rejected", "reason", "invalid_body", "err", err)
		writeBadInput(w, "invalid request body")
		return
	}
	if err := h.dispatch.SubmitTyped(r.Context(), req); err != nil {
		result := "error"
		if code := dispatch.WireCode(err); code == "no_active_lease" || code == "stale_lease" {
			result = "conflict"
		}
		h.metrics.Submit(req.Status, result, nil)
		h.log(r, dispatchLogLevel(err), "submit failed",
			"infohash", req.Infohash,
			"claim_token", req.ClaimToken,
			"status", req.Status,
			"ref", req.Ref,
			"has_confidence", req.Confidence != nil,
			"reason_len", len(req.Reason),
			"result", result,
			"code", dispatch.WireCode(err),
			"err", err,
		)
		h.writeDispatchError(w, req.Infohash, err)
		return
	}
	h.metrics.Submit(req.Status, "ok", req.Confidence)
	h.log(r, slog.LevelInfo, "submit completed",
		"infohash", req.Infohash,
		"claim_token", req.ClaimToken,
		"status", req.Status,
		"ref", req.Ref,
		"has_confidence", req.Confidence != nil,
		"reason_len", len(req.Reason),
	)
	writeJSON(w, http.StatusOK, []byte(`{"ok":true}`))
}
