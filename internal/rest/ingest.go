package rest

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

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
	start := time.Now()
	if r.Method != http.MethodPost {
		h.log(r, slog.LevelDebug, "ingest rejected", "reason", "method_not_allowed", "method", r.Method)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req ingestRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		h.metrics.IngestBatch(0, "error")
		h.log(r, slog.LevelInfo, "ingest rejected", "reason", "invalid_body", "err", err)
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if len(req.Posts) > maxBatchPosts {
		h.metrics.IngestBatch(len(req.Posts), "error")
		h.log(r, slog.LevelInfo, "ingest rejected",
			"reason", "batch_too_large",
			"post_count", len(req.Posts),
			"source_counts", sourceCounts(req.Posts),
			"max_post_count", maxBatchPosts,
		)
		http.Error(w, "batch too large", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	for i := range req.Posts {
		post := req.Posts[i]
		if strings.TrimSpace(post.Source) == "" || strings.TrimSpace(post.SourceID) == "" {
			h.metrics.IngestBatch(len(req.Posts), "error")
			h.log(r, slog.LevelInfo, "ingest rejected",
				"reason", "missing_source",
				"post_count", len(req.Posts),
				"post_index", i,
			)
			http.Error(w, "post missing source or source_id", http.StatusBadRequest)
			return
		}
	}

	batch, failure, status, msg := h.ingestBatch(ctx, req.Posts)
	if status != http.StatusOK {
		h.metrics.IngestBatch(len(req.Posts), "error")
		h.log(r, slog.LevelError, "ingest failed",
			"post_count", len(req.Posts),
			"source_counts", sourceCounts(req.Posts),
			"post_index", failure.index,
			"source", failure.source,
			"source_id", failure.sourceID,
			"status", status,
			"duration_ms", time.Since(start).Milliseconds(),
			"err", failure.err,
		)
		http.Error(w, msg, status)
		return
	}

	qs, err := h.ingest.QueueStats(ctx)
	if err != nil {
		h.metrics.IngestBatch(len(req.Posts), "error")
		h.log(r, slog.LevelError, "ingest queue stats failed",
			"post_count", len(req.Posts),
			"duration_ms", time.Since(start).Milliseconds(),
			"err", err,
		)
		http.Error(w, "queue stats failed", http.StatusInternalServerError)
		return
	}
	h.metrics.IngestBatch(len(req.Posts), "ok")
	h.log(r, slog.LevelInfo, "ingest completed",
		"post_count", len(req.Posts),
		"source_counts", sourceCounts(req.Posts),
		"new_count", batch.New,
		"updated_count", batch.Updated,
		"duplicate_count", batch.Duplicate,
		"conflict_count", batch.Conflict,
		"skipped_count", batch.Skipped,
		"queue_available", qs.Available,
		"queue_leased", qs.Leased,
		"queue_exhausted", qs.Exhausted,
		"duration_ms", time.Since(start).Milliseconds(),
	)

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

type ingestFailure struct {
	index    int
	source   string
	sourceID string
	err      error
}

func (h *Handler) ingestBatch(ctx context.Context, posts []rawpost.RawPost) (batchOut rawpost.IngestBatch, failure ingestFailure, status int, msg string) {
	var batch rawpost.IngestBatch
	for i := range posts {
		post := posts[i]

		ih, err := infohash.NormalizeInfohash(post.Magnet)
		if err != nil {
			if errors.Is(err, infohash.ErrSkipInfohash) {
				batch.Skipped++
				h.metrics.IngestPost(post.Source, "skipped")
				continue
			}
			h.metrics.IngestPost(post.Source, "error")
			return batch, ingestFailure{
				index:    i,
				source:   post.Source,
				sourceID: post.SourceID,
				err:      err,
			}, http.StatusInternalServerError, "infohash normalization failed"
		}

		out, err := h.ingest.IngestN(ctx, ingestParams(post, ih))
		if err != nil {
			h.metrics.IngestPost(post.Source, "error")
			return batch, ingestFailure{
				index:    i,
				source:   post.Source,
				sourceID: post.SourceID,
				err:      err,
			}, http.StatusInternalServerError, "ingest failed"
		}
		switch {
		case out.New:
			batch.New++
			h.metrics.IngestPost(post.Source, "new")
		case out.Updated:
			batch.Updated++
			h.metrics.IngestPost(post.Source, "updated")
		case out.Duplicate:
			batch.Duplicate++
			h.metrics.IngestPost(post.Source, "duplicate")
		case out.Conflict:
			batch.Conflict++
			h.metrics.IngestPost(post.Source, "conflict")
		}
	}
	return batch, ingestFailure{}, http.StatusOK, ""
}

func sourceCounts(posts []rawpost.RawPost) map[string]int {
	counts := make(map[string]int)
	for i := range posts {
		source := strings.TrimSpace(posts[i].Source)
		if source == "" {
			source = "unknown"
		}
		counts[source]++
	}
	return counts
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
