-- +goose Up
CREATE TABLE counters (
    id text PRIMARY KEY,
    user_id text NOT NULL DEFAULT '',
    name text NOT NULL DEFAULT '',
    n bigint NOT NULL DEFAULT 0,
    rates bigint NOT NULL DEFAULT 0
);
