package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wyvernzora/takuhai/internal/cursor"
	"github.com/wyvernzora/takuhai/internal/store"
)

const (
	defaultClaimLimit   = 1
	maxClaimLimit       = 500
	defaultLeaseSeconds = 300

	defaultReleasesLimit = 50
	maxReleasesLimit     = 500
	maxMagnetsBatch      = 500
)

type StoreConfig struct {
	QueueMaxAttempts int
}

type Store struct {
	pool  *pgxpool.Pool
	clock store.Clock
	cfg   StoreConfig
}

var (
	_ store.Store = (*Store)(nil)
)

func NewStore(pool *pgxpool.Pool, clock store.Clock) *Store {
	return NewStoreWithConfig(pool, clock, StoreConfig{})
}

func NewStoreWithConfig(pool *pgxpool.Pool, clock store.Clock, cfg StoreConfig) *Store {
	if clock == nil {
		clock = store.RealClock{}
	}
	if cfg.QueueMaxAttempts <= 0 {
		cfg.QueueMaxAttempts = 3
	}
	return &Store{pool: pool, clock: clock, cfg: cfg}
}

func isCanonicalInfohash(s string) bool {
	if len(s) != 40 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func ingestSeeds(p store.IngestParams) (magnet *string, sizeBytes *int64) {
	if p.Magnet != "" {
		m := p.Magnet
		magnet = &m
	}
	if p.SizeBytes != 0 {
		sz := p.SizeBytes
		sizeBytes = &sz
	}
	return magnet, sizeBytes
}

func (s *Store) IngestN(ctx context.Context, p store.IngestParams) (store.IngestOutcome, error) {
	if !isCanonicalInfohash(p.Infohash) {
		return store.IngestOutcome{Duplicate: true}, nil
	}

	now := s.clock.Now()
	magnet, sizeBytes := ingestSeeds(p)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return store.IngestOutcome{}, fmt.Errorf("ingest: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // best-effort cleanup after commit/error.

	created, inserted, err := s.upsertTx(ctx, tx, p, magnet, sizeBytes, now)
	if err != nil {
		return store.IngestOutcome{}, err
	}

	if created && !inserted {
		return store.IngestOutcome{Conflict: true}, nil
	}
	if err := tx.Commit(ctx); err != nil {
		return store.IngestOutcome{}, fmt.Errorf("ingest: commit: %w", err)
	}
	switch {
	case created:
		return store.IngestOutcome{New: true}, nil
	case inserted:
		return store.IngestOutcome{Updated: true}, nil
	default:
		return store.IngestOutcome{Duplicate: true}, nil
	}
}

func (s *Store) upsertTx(ctx context.Context, tx pgx.Tx, p store.IngestParams, magnet *string, sizeBytes *int64, now time.Time) (created, inserted bool, err error) {
	err = tx.QueryRow(ctx, `
		INSERT INTO releases (
			infohash, title, magnet, size_bytes, published_at, sources,
			match_status, attempt_count, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, ARRAY[$6]::text[],
			'unmatched', 0, $7, $7)
		ON CONFLICT (infohash) DO UPDATE SET infohash = releases.infohash
		RETURNING (xmax = 0) AS created
	`, p.Infohash, p.Title, magnet, sizeBytes, p.PublishedAt, p.Source, now).Scan(&created)
	if err != nil {
		return false, false, fmt.Errorf("ingest: upsert release: %w", err)
	}

	tag, err := tx.Exec(ctx, `
		INSERT INTO raw_items (
			infohash, source, source_id, title, url, published_at, ingested_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (source, source_id) DO NOTHING
	`, p.Infohash, p.Source, p.SourceID, p.Title, p.URL, p.PublishedAt, now)
	if err != nil {
		return false, false, fmt.Errorf("ingest: insert raw_item: %w", err)
	}
	inserted = tag.RowsAffected() > 0

	if !created && inserted {
		if err := s.recomputeRepresentative(ctx, tx, p.Infohash, magnet, sizeBytes, now); err != nil {
			return false, false, err
		}
	}
	return created, inserted, nil
}

func (s *Store) recomputeRepresentative(ctx context.Context, tx pgx.Tx, infohash string, magnet *string, sizeBytes *int64, now time.Time) error {
	_, err := tx.Exec(ctx, `
		UPDATE releases r SET
			title = (
				SELECT ri.title FROM raw_items ri
				WHERE ri.infohash = r.infohash
				ORDER BY ri.published_at DESC, ri.id DESC
				LIMIT 1
			),
			published_at = (
				SELECT min(ri.published_at) FROM raw_items ri
				WHERE ri.infohash = r.infohash
			),
			sources = (
				SELECT array_agg(DISTINCT ri.source ORDER BY ri.source) FROM raw_items ri
				WHERE ri.infohash = r.infohash
			),
			magnet = COALESCE(r.magnet, $2),
			size_bytes = COALESCE(r.size_bytes, $3),
			updated_at = $4
		WHERE r.infohash = $1
	`, infohash, magnet, sizeBytes, now)
	if err != nil {
		return fmt.Errorf("ingest: recompute representative fields: %w", err)
	}
	return nil
}

func (s *Store) Claim(ctx context.Context, p store.ClaimParams) (store.ClaimResult, error) {
	now := s.clock.Now()
	limit := p.Limit
	if limit <= 0 {
		limit = defaultClaimLimit
	}
	if limit > maxClaimLimit {
		limit = maxClaimLimit
	}
	leaseSeconds := p.LeaseSeconds
	if leaseSeconds <= 0 {
		leaseSeconds = defaultLeaseSeconds
	}
	leaseExpires := now.Add(time.Duration(leaseSeconds) * time.Second)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return store.ClaimResult{}, fmt.Errorf("claim: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // best-effort cleanup after commit/error.

	if _, err := tx.Exec(ctx, `
		UPDATE releases
		SET match_status = 'exhausted',
			claimed_at = NULL,
			lease_expires_at = NULL,
			updated_at = $1
		WHERE match_status = 'unmatched'
			AND attempt_count >= $2
			AND (lease_expires_at IS NULL OR lease_expires_at <= $1)
	`, now, s.cfg.QueueMaxAttempts); err != nil {
		return store.ClaimResult{}, fmt.Errorf("claim: exhaust old attempts: %w", err)
	}

	rows, err := tx.Query(ctx, `
		WITH claimable AS (
			SELECT infohash
			FROM releases
			WHERE match_status = 'unmatched'
				AND attempt_count < $4
				AND (lease_expires_at IS NULL OR lease_expires_at <= $1)
			ORDER BY published_at DESC, infohash DESC
			LIMIT $2
			FOR UPDATE SKIP LOCKED
		),
		leased AS (
			UPDATE releases r SET
				claimed_at = $1,
				lease_expires_at = $3,
				claim_token = r.claim_token + 1,
				updated_at = $1
			FROM claimable c
			WHERE r.infohash = c.infohash
			RETURNING r.infohash, r.claim_token, r.attempt_count, r.lease_expires_at, r.published_at
		)
		SELECT infohash, claim_token, attempt_count, lease_expires_at
		FROM leased
		ORDER BY published_at DESC, infohash DESC
	`, now, limit, leaseExpires, s.cfg.QueueMaxAttempts)
	if err != nil {
		return store.ClaimResult{}, fmt.Errorf("claim: select+lease: %w", err)
	}

	type claimedRow struct {
		infohash     string
		token        int64
		attemptCount int
		leaseExpires time.Time
	}
	claimed, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (claimedRow, error) {
		var cr claimedRow
		err := row.Scan(&cr.infohash, &cr.token, &cr.attemptCount, &cr.leaseExpires)
		return cr, err
	})
	if err != nil {
		return store.ClaimResult{}, fmt.Errorf("claim: collect leased rows: %w", err)
	}

	items := make([]store.ClaimedRelease, 0, len(claimed))
	for _, cr := range claimed {
		raw, err := s.rawItemsFor(ctx, tx, cr.infohash)
		if err != nil {
			return store.ClaimResult{}, err
		}
		items = append(items, store.ClaimedRelease{
			Infohash:     cr.infohash,
			ClaimToken:   cr.token,
			AttemptCount: cr.attemptCount,
			LeaseExpires: cr.leaseExpires,
			RawItems:     raw,
		})
	}

	if err := tx.Commit(ctx); err != nil {
		return store.ClaimResult{}, fmt.Errorf("claim: commit: %w", err)
	}
	return store.ClaimResult{Items: items}, nil
}

func (s *Store) rawItemsFor(ctx context.Context, tx pgx.Tx, infohash string) ([]store.RawItemEvent, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, source, source_id, title, COALESCE(url, ''), published_at
		FROM raw_items
		WHERE infohash = $1
		ORDER BY id
	`, infohash)
	if err != nil {
		return nil, fmt.Errorf("claim: load raw_items for %s: %w", infohash, err)
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (store.RawItemEvent, error) {
		var ev store.RawItemEvent
		err := row.Scan(&ev.ID, &ev.Source, &ev.SourceID, &ev.Title, &ev.URL, &ev.PublishedAt)
		return ev, err
	})
	if err != nil {
		return nil, fmt.Errorf("claim: collect raw_items for %s: %w", infohash, err)
	}
	return items, nil
}

func (s *Store) Submit(ctx context.Context, p store.SubmitParams) error { //nolint:cyclop // Small status transition table; splitting hides the flow.
	if err := validateSubmit(p); err != nil {
		return err
	}
	now := s.clock.Now()

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("submit: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // best-effort cleanup after commit/error.

	var currentStatus string
	var currentToken int64
	var leaseExpires *time.Time
	var attemptCount int
	err = tx.QueryRow(ctx, `
		SELECT match_status, claim_token, lease_expires_at, attempt_count
		FROM releases
		WHERE infohash = $1
		FOR UPDATE
	`, p.Infohash).Scan(&currentStatus, &currentToken, &leaseExpires, &attemptCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("submit %s: %w", p.Infohash, store.ErrNoSuchRelease)
	}
	if err != nil {
		return fmt.Errorf("submit %s: load lease: %w", p.Infohash, err)
	}
	if currentToken != p.ClaimToken || leaseExpires == nil || !leaseExpires.After(now) {
		return fmt.Errorf("submit %s: %w", p.Infohash, store.ErrStaleLease)
	}
	if currentStatus != "unmatched" {
		return fmt.Errorf("submit %s: %w", p.Infohash, store.ErrNoActiveLease)
	}

	newAttemptCount := attemptCount
	if p.Status == "unmatched" {
		newAttemptCount++
	}
	finalStatus := p.Status
	clearLease := p.Status == "matched" || p.Status == "suppressed"
	if p.Status == "unmatched" && newAttemptCount >= s.cfg.QueueMaxAttempts {
		finalStatus = "exhausted"
		clearLease = true
	}

	switch finalStatus {
	case "matched":
		_, err = tx.Exec(ctx, `
			UPDATE releases SET
				match_status = 'matched',
				ref = $2,
				confidence = $3,
				first_matched_at = COALESCE(first_matched_at, $4),
				claimed_at = NULL,
				lease_expires_at = NULL,
				updated_at = $4
			WHERE infohash = $1
		`, p.Infohash, p.Ref, p.Confidence, now)
	case "suppressed", "exhausted":
		_, err = tx.Exec(ctx, `
			UPDATE releases SET
				match_status = $2,
				attempt_count = $3,
				ref = NULL,
				confidence = NULL,
				claimed_at = NULL,
				lease_expires_at = NULL,
				updated_at = $4
			WHERE infohash = $1
		`, p.Infohash, finalStatus, newAttemptCount, now)
	case "unmatched":
		_, err = tx.Exec(ctx, `
			UPDATE releases SET
				attempt_count = $2,
				updated_at = $3
			WHERE infohash = $1
		`, p.Infohash, newAttemptCount, now)
	default:
		return fmt.Errorf("submit: invalid status %q", p.Status)
	}
	if err != nil {
		return fmt.Errorf("submit %s: update: %w", p.Infohash, err)
	}
	if !clearLease && finalStatus != "unmatched" {
		return fmt.Errorf("submit: impossible status %q", finalStatus)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO match_events (infohash, status, ref, confidence, reason, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, p.Infohash, finalStatus, nullableRef(finalStatus, p.Ref), nullableConfidence(finalStatus, p.Confidence), p.Reason, now); err != nil {
		return fmt.Errorf("submit %s: append match_event: %w", p.Infohash, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("submit %s: commit: %w", p.Infohash, err)
	}
	return nil
}

func validateSubmit(p store.SubmitParams) error {
	switch p.Status {
	case "matched":
		return cursor.ValidateRef(p.Ref)
	case "unmatched", "suppressed":
		return nil
	default:
		return fmt.Errorf("submit: invalid status %q", p.Status)
	}
}

func nullableRef(status, ref string) any {
	if status == "matched" {
		return ref
	}
	return nil
}

func nullableConfidence(status string, confidence float64) any {
	if status == "matched" {
		return confidence
	}
	return nil
}

func (s *Store) QueueStats(ctx context.Context) (store.QueueStats, error) {
	now := s.clock.Now()
	var qs store.QueueStats
	err := s.pool.QueryRow(ctx, `
		SELECT
			count(*) FILTER (
				WHERE match_status = 'unmatched'
					AND attempt_count < $2
					AND (lease_expires_at IS NULL OR lease_expires_at <= $1)
			),
			count(*) FILTER (
				WHERE match_status = 'unmatched'
					AND lease_expires_at IS NOT NULL
					AND lease_expires_at > $1
			),
			count(*) FILTER (WHERE match_status = 'unmatched'),
			count(*) FILTER (WHERE match_status = 'matched'),
			count(*) FILTER (WHERE match_status = 'suppressed'),
			count(*) FILTER (WHERE match_status = 'exhausted')
		FROM releases
	`, now, s.cfg.QueueMaxAttempts).Scan(&qs.Available, &qs.Leased, &qs.Unmatched, &qs.Matched, &qs.Suppressed, &qs.Exhausted)
	if err != nil {
		return store.QueueStats{}, fmt.Errorf("queue stats: %w", err)
	}
	return qs, nil
}

func (s *Store) ListReleases(ctx context.Context, q store.ReleaseQuery) (store.ReleasePage, error) {
	if err := cursor.ValidateRef(q.Ref); err != nil {
		return store.ReleasePage{}, err
	}
	limit := q.Limit
	if limit <= 0 {
		limit = defaultReleasesLimit
	}
	if limit > maxReleasesLimit {
		limit = maxReleasesLimit
	}

	path := cursor.PathCatalog
	if q.Since != nil {
		path = cursor.PathDelta
	}
	var seek *cursor.Cursor
	if q.Cursor != "" {
		c, err := cursor.Decode(q.Cursor, q.Ref, path)
		if err != nil {
			return store.ReleasePage{}, cursor.ErrInvalidCursor
		}
		seek = &c
	}
	fetch := limit + 1
	seekKey, seekHash, hasSeek := time.Time{}, "", false
	if seek != nil {
		seekKey, seekHash, hasSeek = seek.Key, seek.Infohash, true
	}

	var rows pgx.Rows
	var err error
	if path == cursor.PathCatalog {
		rows, err = s.pool.Query(ctx, `
			SELECT infohash, title, COALESCE(size_bytes, 0), published_at,
				COALESCE(confidence, 0), sources, published_at
			FROM releases
			WHERE match_status = 'matched' AND ref = $1
				AND (NOT $4 OR (published_at, infohash) < ($2, $3))
			ORDER BY published_at DESC, infohash DESC
			LIMIT $5
		`, q.Ref, seekKey, seekHash, hasSeek, fetch)
	} else {
		rows, err = s.pool.Query(ctx, `
			SELECT infohash, title, COALESCE(size_bytes, 0), published_at,
				COALESCE(confidence, 0), sources, first_matched_at
			FROM releases
			WHERE match_status = 'matched' AND ref = $1
				AND first_matched_at > $2
				AND (NOT $5 OR (first_matched_at, infohash) < ($3, $4))
			ORDER BY first_matched_at DESC, infohash DESC
			LIMIT $6
		`, q.Ref, *q.Since, seekKey, seekHash, hasSeek, fetch)
	}
	if err != nil {
		return store.ReleasePage{}, fmt.Errorf("list_releases %s: %w", q.Ref, err)
	}

	type keyedRow struct {
		item store.ReleaseItem
		key  time.Time
	}
	keyed, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (keyedRow, error) {
		var kr keyedRow
		err := row.Scan(&kr.item.Infohash, &kr.item.Title, &kr.item.SizeBytes,
			&kr.item.PublishedAt, &kr.item.Confidence, &kr.item.Sources, &kr.key)
		return kr, err
	})
	if err != nil {
		return store.ReleasePage{}, fmt.Errorf("list_releases %s: collect: %w", q.Ref, err)
	}

	hasMore := len(keyed) > limit
	if hasMore {
		keyed = keyed[:limit]
	}
	items := make([]store.ReleaseItem, len(keyed))
	for i := range keyed {
		items[i] = keyed[i].item
	}
	var next string
	if hasMore {
		last := keyed[len(keyed)-1]
		enc, err := cursor.Encode(cursor.Cursor{
			Ref: q.Ref, Path: path, Key: last.key, Infohash: last.item.Infohash,
		})
		if err != nil {
			return store.ReleasePage{}, fmt.Errorf("list_releases %s: encode cursor: %w", q.Ref, err)
		}
		next = enc
	}
	return store.ReleasePage{Releases: items, NextCursor: next}, nil
}

func (s *Store) ResolveMagnets(ctx context.Context, infohashes []string) (map[string]string, error) {
	if len(infohashes) > maxMagnetsBatch {
		return nil, fmt.Errorf("resolve_magnets: batch of %d exceeds hard cap %d", len(infohashes), maxMagnetsBatch)
	}
	out := make(map[string]string, len(infohashes))
	if len(infohashes) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT infohash, magnet
		FROM releases
		WHERE infohash = ANY($1) AND magnet IS NOT NULL
	`, infohashes)
	if err != nil {
		return nil, fmt.Errorf("resolve_magnets: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var ih, magnet string
		if err := rows.Scan(&ih, &magnet); err != nil {
			return nil, fmt.Errorf("resolve_magnets: scan: %w", err)
		}
		out[ih] = magnet
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("resolve_magnets: rows: %w", err)
	}
	return out, nil
}

func (s *Store) Ping(ctx context.Context) error {
	if s.pool == nil {
		return errors.New("takuhai/postgres: nil pool")
	}
	return s.pool.Ping(ctx)
}

func (s *Store) Close() error {
	if s.pool != nil {
		s.pool.Close()
	}
	return nil
}
