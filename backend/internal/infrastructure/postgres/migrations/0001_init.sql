-- +goose Up
CREATE TABLE counters (
    id text PRIMARY KEY,
    user_id text NOT NULL DEFAULT '',
    name text NOT NULL DEFAULT '',
    n bigint NOT NULL DEFAULT 0,
    rates bigint NOT NULL DEFAULT 0
);

CREATE TABLE prices (
    id text PRIMARY KEY,
    user_id text NOT NULL DEFAULT '',
    pair text NOT NULL DEFAULT '',
    base text NOT NULL DEFAULT '',
    quote text NOT NULL DEFAULT '',
    at timestamptz NOT NULL,
    value numeric NOT NULL DEFAULT 0,
    source text NOT NULL DEFAULT '',
    archived boolean NOT NULL DEFAULT false,
    corrected boolean NOT NULL DEFAULT false,
    UNIQUE (user_id, pair, at)
);
CREATE INDEX prices_user_at ON prices (user_id, at);

CREATE TABLE rate_sync (
    id text PRIMARY KEY,
    user_id text NOT NULL DEFAULT '',
    provider text NOT NULL DEFAULT '',
    status text NOT NULL DEFAULT '',
    last_attempt text NOT NULL DEFAULT '',
    last_success text NOT NULL DEFAULT '',
    error text NOT NULL DEFAULT '',
    consecutive_failures integer NOT NULL DEFAULT 0,
    newest_at text NOT NULL DEFAULT '',
    newest_value text NOT NULL DEFAULT ''
);
CREATE INDEX rate_sync_user ON rate_sync (user_id);
