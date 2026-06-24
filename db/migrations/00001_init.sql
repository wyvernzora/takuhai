-- +goose Up
-- +goose StatementBegin
CREATE TYPE match_status AS ENUM ('unmatched','matched','suppressed','exhausted');
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE releases (
    infohash         TEXT         PRIMARY KEY,
    title            TEXT         NOT NULL,
    magnet           TEXT,
    size_bytes       BIGINT,
    published_at     TIMESTAMPTZ  NOT NULL,
    sources          TEXT[]       NOT NULL DEFAULT '{}',

    match_status     match_status NOT NULL DEFAULT 'unmatched',
    ref              TEXT,
    confidence       DOUBLE PRECISION,
    first_matched_at TIMESTAMPTZ,

    attempt_count    INT          NOT NULL DEFAULT 0,
    claimed_at       TIMESTAMPTZ,
    lease_expires_at TIMESTAMPTZ,
    claim_token      BIGINT       NOT NULL DEFAULT 0,

    created_at       TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ  NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX releases_ref_idx ON releases (ref, published_at DESC, infohash DESC)
    WHERE match_status = 'matched';
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX releases_ref_matched_idx ON releases (ref, first_matched_at DESC, infohash DESC)
    WHERE match_status = 'matched';
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX releases_queue_idx ON releases (published_at DESC, infohash DESC)
    WHERE match_status = 'unmatched';
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE raw_items (
    id           BIGSERIAL   PRIMARY KEY,
    infohash     TEXT        NOT NULL REFERENCES releases(infohash),
    source       TEXT        NOT NULL,
    source_id    TEXT        NOT NULL,
    title        TEXT        NOT NULL,
    url          TEXT,
    published_at TIMESTAMPTZ NOT NULL,
    ingested_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (source, source_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX raw_items_infohash_idx ON raw_items (infohash);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE match_events (
    id         BIGSERIAL   PRIMARY KEY,
    infohash   TEXT        NOT NULL REFERENCES releases(infohash),
    status     match_status NOT NULL,
    ref        TEXT,
    confidence DOUBLE PRECISION,
    reason     TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX match_events_infohash_idx ON match_events (infohash, created_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS match_events;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS raw_items;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS releases;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TYPE IF EXISTS match_status;
-- +goose StatementEnd
