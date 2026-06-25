package rest

import (
	"encoding/json"
	"net/http"

	"github.com/wyvernzora/takuhai/internal/dispatch"
)

func (h *Handler) handleSubmit(w http.ResponseWriter, r *http.Request) {
	body, ok := h.requirePost(w, r)
	if !ok {
		h.metrics.Submit("invalid", "error")
		return
	}
	var req dispatch.SubmitRequest
	if err := json.Unmarshal(body, &req); err != nil {
		h.metrics.Submit("invalid", "error")
		writeBadInput(w, "invalid request body")
		return
	}
	if err := h.dispatch.SubmitTyped(r.Context(), req); err != nil {
		result := "error"
		if code := dispatch.WireCode(err); code == "no_active_lease" || code == "stale_lease" {
			result = "conflict"
		}
		h.metrics.Submit(req.Status, result)
		h.writeDispatchError(w, req.Infohash, err)
		return
	}
	h.metrics.Submit(req.Status, "ok")
	writeJSON(w, http.StatusOK, []byte(`{"ok":true}`))
}
