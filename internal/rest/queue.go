package rest

import (
	"encoding/json"
	"net/http"

	"github.com/wyvernzora/takuhai/internal/dispatch"
)

func (h *Handler) handleClaim(w http.ResponseWriter, r *http.Request) {
	body, ok := h.requirePost(w, r)
	if !ok {
		return
	}
	var req dispatch.ClaimRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeBadInput(w, "invalid request body")
		return
	}
	out, err := h.dispatch.ClaimTyped(r.Context(), req)
	if err != nil {
		h.writeDispatchError(w, "", err)
		return
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
