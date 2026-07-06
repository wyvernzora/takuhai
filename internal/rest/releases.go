package rest

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/wyvernzora/takuhai/internal/dispatch"
	"github.com/wyvernzora/takuhai/internal/infohash"
)

func (h *Handler) handleGetRelease(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		h.log(r, slog.LevelDebug, "release lookup rejected", "reason", "method_not_allowed", "method", r.Method)
		writeError(w, http.StatusMethodNotAllowed, "", "", "method not allowed")
		return
	}

	raw := strings.TrimPrefix(r.URL.Path, "/releases/")
	if raw == "" || strings.Contains(raw, "/") {
		h.log(r, slog.LevelDebug, "release lookup rejected", "reason", "invalid_infohash")
		writeBadInput(w, "invalid infohash")
		return
	}
	ih, err := infohash.NormalizeInfohash(raw)
	if err != nil {
		h.log(r, slog.LevelDebug, "release lookup rejected", "reason", "invalid_infohash")
		writeBadInput(w, "invalid infohash")
		return
	}
	out, err := h.dispatch.GetReleaseTyped(r.Context(), dispatch.GetReleaseRequest{Infohash: ih})
	if err != nil {
		h.log(r, dispatchLogLevel(err), "release lookup failed",
			"infohash", ih,
			"code", dispatch.WireCode(err),
			"err", err,
		)
		h.writeDispatchError(w, ih, err)
		return
	}
	h.log(r, slog.LevelInfo, "release lookup completed", "infohash", out.Infohash)
	writeJSONValue(w, http.StatusOK, out)
}
