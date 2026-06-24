package rest

import (
	"encoding/json"
	"net/http"

	"github.com/wyvernzora/takuhai/internal/dispatch"
)

func (h *Handler) handleSubmit(w http.ResponseWriter, r *http.Request) {
	body, ok := h.requirePost(w, r)
	if !ok {
		return
	}
	var req dispatch.SubmitRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeBadInput(w, "invalid request body")
		return
	}
	if err := h.dispatch.SubmitTyped(r.Context(), req); err != nil {
		h.writeDispatchError(w, req.Infohash, err)
		return
	}
	writeJSON(w, http.StatusOK, []byte(`{"ok":true}`))
}
