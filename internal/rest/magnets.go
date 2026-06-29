package rest

import (
	"log/slog"
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
		h.log(r, slog.LevelDebug, "magnet lookup rejected", "reason", "method_not_allowed", "method", r.Method)
		writeError(w, http.StatusMethodNotAllowed, "", "", "method not allowed")
		return
	}

	raw := strings.TrimPrefix(r.URL.Path, "/magnets/")
	if raw == "" || strings.Contains(raw, "/") {
		h.log(r, slog.LevelDebug, "magnet lookup rejected", "reason", "invalid_infohash")
		writeBadInput(w, "invalid infohash")
		return
	}

	ih, err := infohash.NormalizeInfohash(raw)
	if err != nil {
		h.log(r, slog.LevelDebug, "magnet lookup rejected", "reason", "invalid_infohash")
		writeBadInput(w, "invalid infohash")
		return
	}
	out, err := h.dispatch.ResolveMagnetsTyped(r.Context(), dispatch.ResolveMagnetsRequest{Infohashes: []string{ih}})
	if err != nil {
		h.log(r, dispatchLogLevel(err), "magnet lookup failed",
			"infohash", ih,
			"code", dispatch.WireCode(err),
			"err", err,
		)
		h.writeDispatchError(w, ih, err)
		return
	}
	magnet, ok := out.Magnets[ih]
	if !ok {
		h.log(r, slog.LevelInfo, "magnet lookup missed", "infohash", ih)
		writeError(w, http.StatusNotFound, "no_such_release", ih, store.ErrNoSuchRelease.Error())
		return
	}
	h.log(r, slog.LevelInfo, "magnet lookup completed", "infohash", ih)
	writeJSONValue(w, http.StatusOK, magnetResponse{Infohash: ih, Magnet: magnet})
}
