package rest

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/wyvernzora/takuhai/internal/infohash"
	"github.com/wyvernzora/takuhai/internal/store"
	"github.com/wyvernzora/takuhai/pkg/rawpost"
)

// maxBatchPosts is the hard cap on one POST /ingest body (an oversized batch -> 400
// rather than an unbounded transaction stream). n8n keeps batches modest; this is the
// boundary backstop.
const maxBatchPosts = 1000

// ingestStore is the narrow store seam POST /ingest needs.
type ingestStore interface {
	IngestN(ctx context.Context, p store.IngestParams) (store.IngestOutcome, error)
	QueueStats(ctx context.Context) (store.QueueStats, error)
}

// ingestRequest is the POST /ingest request body: a batch of raw crawled posts.
type ingestRequest struct {
	Posts []rawpost.RawPost `json:"posts"`
}

func (h *Handler) handleIngest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req ingestRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if len(req.Posts) > maxBatchPosts {
		http.Error(w, "batch too large", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	for i := range req.Posts {
		post := req.Posts[i]
		if strings.TrimSpace(post.Source) == "" || strings.TrimSpace(post.SourceID) == "" {
			http.Error(w, "post missing source or source_id", http.StatusBadRequest)
			return
		}
	}

	batch, status, msg := h.ingestBatch(ctx, req.Posts)
	if status != http.StatusOK {
		http.Error(w, msg, status)
		return
	}

	qs, err := h.ingest.QueueStats(ctx)
	if err != nil {
		http.Error(w, "queue stats failed", http.StatusInternalServerError)
		return
	}

	resp := rawpost.IngestSummary{
		Batch: batch,
		Queue: rawpost.QueueStats{
			Available: int64(qs.Available),
			Leased:    int64(qs.Leased),
			Exhausted: int64(qs.Exhausted),
		},
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

func (h *Handler) ingestBatch(ctx context.Context, posts []rawpost.RawPost) (batchOut rawpost.IngestBatch, status int, msg string) {
	var batch rawpost.IngestBatch
	for i := range posts {
		post := posts[i]

		ih, err := infohash.NormalizeInfohash(post.Magnet)
		if err != nil {
			if errors.Is(err, infohash.ErrSkipInfohash) {
				batch.Skipped++
				continue
			}
			return batch, http.StatusInternalServerError, "infohash normalization failed"
		}

		out, err := h.ingest.IngestN(ctx, ingestParams(post, ih))
		if err != nil {
			return batch, http.StatusInternalServerError, "ingest failed"
		}
		switch {
		case out.New:
			batch.New++
		case out.Updated:
			batch.Updated++
		case out.Duplicate:
			batch.Duplicate++
		case out.Conflict:
			batch.Conflict++
		}
	}
	return batch, http.StatusOK, ""
}

func ingestParams(p rawpost.RawPost, ih string) store.IngestParams {
	return store.IngestParams{
		Infohash:    ih,
		Source:      p.Source,
		SourceID:    p.SourceID,
		Title:       p.Title,
		URL:         p.URL,
		Magnet:      p.Magnet,
		SizeBytes:   p.SizeBytes,
		PublishedAt: p.PublishedAt,
	}
}
