-- +goose Up
-- +goose StatementBegin
CREATE INDEX releases_recent_idx ON releases (published_at DESC, infohash DESC)
    WHERE match_status = 'matched';
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX releases_recent_matched_idx ON releases (first_matched_at DESC, infohash DESC)
    WHERE match_status = 'matched';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS releases_recent_matched_idx;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS releases_recent_idx;
-- +goose StatementEnd
