package rest

import (
	"net/http"
	"strings"

	"github.com/wyvernzora/takuhai/internal/dispatch"
	"github.com/wyvernzora/takuhai/internal/infohash"
	"github.com/wyvernzora/takuhai/internal/store"
)

type magnetResponse struct {
	Infohash string `json:"infohash"`
	Magnet   string `json:"magnet"`
}

func (h *Handler) handleGetMagnet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "", "", "method not allowed")
		return
	}

	raw := strings.TrimPrefix(r.URL.Path, "/magnets/")
	if raw == "" || strings.Contains(raw, "/") {
		writeBadInput(w, "invalid infohash")
		return
	}

	ih, err := infohash.NormalizeInfohash(raw)
	if err != nil {
		writeBadInput(w, "invalid infohash")
		return
	}
	out, err := h.dispatch.ResolveMagnetsTyped(r.Context(), dispatch.ResolveMagnetsRequest{Infohashes: []string{ih}})
	if err != nil {
		h.writeDispatchError(w, ih, err)
		return
	}
	magnet, ok := out.Magnets[ih]
	if !ok {
		writeError(w, http.StatusNotFound, "no_such_release", ih, store.ErrNoSuchRelease.Error())
		return
	}
	writeJSONValue(w, http.StatusOK, magnetResponse{Infohash: ih, Magnet: magnet})
}
