package rest

import (
	"encoding/json"
	"net/http"

	"github.com/wyvernzora/takuhai/internal/dispatch"
)

func (h *Handler) handleClaim(w http.ResponseWriter, r *http.Request) {
	body, ok := h.requirePost(w, r)
	if !ok {
		h.metrics.QueueClaim(0, "error")
		return
	}
	var req dispatch.ClaimRequest
	if err := json.Unmarshal(body, &req); err != nil {
		h.metrics.QueueClaim(0, "error")
		writeBadInput(w, "invalid request body")
		return
	}
	out, err := h.dispatch.ClaimTyped(r.Context(), req)
	if err != nil {
		h.metrics.QueueClaim(0, "error")
		h.writeDispatchError(w, "", err)
		return
	}
	if len(out.Items) == 0 {
		h.metrics.QueueClaim(0, "empty")
	} else {
		h.metrics.QueueClaim(len(out.Items), "claimed")
	}
	writeJSONValue(w, http.StatusOK, out)
}

func (h *Handler) handleQueueStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "", "", "method not allowed")
		return
	}
	out, err := h.dispatch.QueueStatsTyped(r.Context())
	if err != nil {
		h.writeDispatchError(w, "", err)
		return
	}
	writeJSONValue(w, http.StatusOK, out)
}
