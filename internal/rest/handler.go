// Package rest wires takuhai's HTTP REST surfaces: pushed crawl ingest, queue
// claiming/stats, and matcher disposition submit.
package rest

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/wyvernzora/takuhai/internal/dispatch"
	"github.com/wyvernzora/takuhai/internal/store"
)

type Handler struct {
	dispatch *dispatch.Dispatcher
	ingest   ingestStore
	mux      *http.ServeMux
}

func New(s store.Store) *Handler {
	h := &Handler{
		dispatch: dispatch.New(s),
		ingest:   s,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/ingest", h.handleIngest)
	mux.HandleFunc("/queue/claim", h.handleClaim)
	mux.HandleFunc("/queue/stats", h.handleQueueStats)
	mux.HandleFunc("/submit", h.handleSubmit)
	h.mux = mux
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) { h.mux.ServeHTTP(w, r) }

type errorResponse struct {
	Code     string `json:"code,omitempty"`
	Infohash string `json:"infohash,omitempty"`
	Message  string `json:"message"`
}

func (h *Handler) requirePost(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "", "", "method not allowed")
		return nil, false
	}
	body, err := readAll(r)
	if err != nil {
		writeBadInput(w, "unreadable request body")
		return nil, false
	}
	return body, true
}

func (h *Handler) writeDispatchError(w http.ResponseWriter, infohash string, err error) {
	code := dispatch.WireCode(err)
	switch code {
	case "no_such_release":
		writeError(w, http.StatusNotFound, code, infohash, err.Error())
	case "no_active_lease", "stale_lease":
		writeError(w, http.StatusConflict, code, infohash, err.Error())
	case "invalid_ref":
		writeError(w, http.StatusBadRequest, code, infohash, err.Error())
	case "invalid_input":
		writeError(w, http.StatusBadRequest, code, infohash, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "", infohash, "internal error")
	}
}

func writeBadInput(w http.ResponseWriter, msg string) {
	writeError(w, http.StatusBadRequest, "", "", msg)
}

func writeJSON(w http.ResponseWriter, status int, raw []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(raw)
}

func writeJSONValue(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, infohash, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorResponse{Code: code, Infohash: infohash, Message: msg})
}

func readAll(r *http.Request) ([]byte, error) {
	defer r.Body.Close()
	return io.ReadAll(r.Body)
}
