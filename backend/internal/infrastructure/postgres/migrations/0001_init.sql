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

CREATE TABLE feedback (
    id text PRIMARY KEY,
    user_id text NOT NULL DEFAULT '',
    target jsonb NOT NULL DEFAULT '{}',
    note text NOT NULL DEFAULT '',
    status text NOT NULL DEFAULT '',
    resolution text NOT NULL DEFAULT '',
    answer text NOT NULL DEFAULT '',
    created_by jsonb NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL,
    resolved_by jsonb NOT NULL DEFAULT '{}',
    resolved_at timestamptz
);

CREATE TABLE saved_views (
    storage_key text PRIMARY KEY,
    user_id text NOT NULL DEFAULT '',
    id text NOT NULL DEFAULT '',
    name text NOT NULL DEFAULT '',
    page text NOT NULL DEFAULT '',
    state jsonb
);

CREATE TABLE usage (
    id text PRIMARY KEY,
    user_id text NOT NULL DEFAULT '',
    date text NOT NULL DEFAULT '',
    path text NOT NULL DEFAULT '',
    dashboard text NOT NULL DEFAULT '',
    views bigint NOT NULL DEFAULT 0,
    last_at text NOT NULL DEFAULT ''
);

CREATE TABLE users (
    id text PRIMARY KEY,
    email text NOT NULL DEFAULT '' UNIQUE,
    name text NOT NULL DEFAULT '',
    is_admin boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT '0001-01-01T00:00:00Z',
    disabled boolean NOT NULL DEFAULT false
);
CREATE TABLE credentials (
    id text PRIMARY KEY,
    hash bytea NOT NULL DEFAULT '',
    salt bytea NOT NULL DEFAULT ''
);
CREATE TABLE sessions (
    id text PRIMARY KEY,
    user_id text NOT NULL DEFAULT '',
    csrf text NOT NULL DEFAULT '',
    expires_at timestamptz NOT NULL
);
CREATE INDEX sessions_expiry ON sessions (expires_at);
CREATE TABLE invites (
    id text PRIMARY KEY,
    email text NOT NULL DEFAULT '',
    created_by text NOT NULL DEFAULT '',
    expires_at timestamptz NOT NULL,
    used_at timestamptz
);
CREATE INDEX invites_expiry ON invites (expires_at);
CREATE TABLE settings (
    id text PRIMARY KEY,
    "by" text NOT NULL DEFAULT '',
    at timestamptz NOT NULL DEFAULT '0001-01-01T00:00:00Z'
);

CREATE TABLE oauth_clients (
    id text PRIMARY KEY,
    name text NOT NULL DEFAULT '',
    redirect_uris text[] NOT NULL DEFAULT '{}',
    auth_method text NOT NULL DEFAULT '',
    secret_hash text NOT NULL DEFAULT '',
    fetched_until timestamptz NOT NULL DEFAULT '0001-01-01T00:00:00Z'
);
CREATE TABLE oauth_codes (
    id text PRIMARY KEY,
    client_id text NOT NULL DEFAULT '',
    user_id text NOT NULL DEFAULT '',
    redirect_uri text NOT NULL DEFAULT '',
    challenge text NOT NULL DEFAULT '',
    resource text NOT NULL DEFAULT '',
    scopes text[] NOT NULL DEFAULT '{}',
    expires_at timestamptz NOT NULL
);
CREATE INDEX oauth_codes_expiry ON oauth_codes (expires_at);
CREATE TABLE oauth_grants (
    id text PRIMARY KEY,
    user_id text NOT NULL DEFAULT '',
    client_id text NOT NULL DEFAULT '',
    client_name text NOT NULL DEFAULT '',
    resource text NOT NULL DEFAULT '',
    scopes text[] NOT NULL DEFAULT '{}',
    revoked boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT '0001-01-01T00:00:00Z',
    expires_at timestamptz NOT NULL
);
CREATE INDEX oauth_grants_user ON oauth_grants (user_id);
CREATE TABLE oauth_tokens (
    id text PRIMARY KEY,
    refresh_hash text NOT NULL DEFAULT '' UNIQUE,
    grant_id text NOT NULL DEFAULT '',
    access_expires_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL DEFAULT '0001-01-01T00:00:00Z',
    used boolean NOT NULL DEFAULT false
);
CREATE INDEX oauth_tokens_expiry ON oauth_tokens (expires_at);

CREATE TABLE admin_confirmations (
    id text PRIMARY KEY,
    user_id text NOT NULL DEFAULT '',
    action text NOT NULL DEFAULT '',
    target text NOT NULL DEFAULT '',
    target_label text NOT NULL DEFAULT '',
    reason text NOT NULL DEFAULT '',
    requested_by jsonb NOT NULL DEFAULT '{}',
    expires_at timestamptz NOT NULL,
    used_at timestamptz
);
CREATE INDEX admin_confirmations_expiry ON admin_confirmations (expires_at);
