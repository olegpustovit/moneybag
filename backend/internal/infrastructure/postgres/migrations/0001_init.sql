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

CREATE TABLE shares (
    id text PRIMARY KEY,
    dashboard_id text NOT NULL DEFAULT '',
    owner_user text NOT NULL DEFAULT '',
    grantee_user text NOT NULL DEFAULT '',
    role text NOT NULL DEFAULT '',
    mode text NOT NULL DEFAULT '',
    sources text[],
    row_filter text NOT NULL DEFAULT '',
    refresh text NOT NULL DEFAULT '',
    expires_at timestamptz,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL,
    last_refresh timestamptz NOT NULL
);
CREATE TABLE dashboard_snapshots (
    id text PRIMARY KEY,
    grant_hash text NOT NULL DEFAULT '',
    owner_user text NOT NULL DEFAULT '',
    dashboard_id text NOT NULL DEFAULT '',
    version integer NOT NULL DEFAULT 0,
    title text NOT NULL DEFAULT '',
    rendered jsonb NOT NULL DEFAULT '{}',
    at timestamptz NOT NULL
);

CREATE TABLE events (
    user_id text NOT NULL DEFAULT '',
    id text PRIMARY KEY,
    seq bigint NOT NULL DEFAULT 0,
    at timestamptz NOT NULL,
    actor jsonb NOT NULL DEFAULT '{}',
    actor_id text NOT NULL DEFAULT '',
    tool text NOT NULL DEFAULT '',
    target jsonb NOT NULL DEFAULT '{}',
    target_id text NOT NULL DEFAULT '',
    -- Hashes bind these original JSON bytes, including their key order.
    before bytea,
    after bytea,
    reason text NOT NULL DEFAULT '',
    grant_id text NOT NULL DEFAULT '',
    grant_hash text NOT NULL DEFAULT '',
    summary text NOT NULL DEFAULT '',
    batch_id text NOT NULL DEFAULT '',
    routine boolean NOT NULL DEFAULT false,
    prev_hash text NOT NULL DEFAULT '',
    hash text NOT NULL DEFAULT '',
    UNIQUE (user_id,seq)
);
CREATE INDEX events_user_target ON events (user_id,target_id);
CREATE INDEX events_user_tool_seq ON events (user_id,tool,seq DESC);
CREATE INDEX events_user_actor_seq ON events (user_id,actor_id,seq DESC);
CREATE TABLE audit_outcomes (
    hash text NOT NULL DEFAULT '',
    id text PRIMARY KEY,
    user_id text NOT NULL DEFAULT '',
    status text NOT NULL DEFAULT '',
    before bytea,
    after bytea,
    at timestamptz NOT NULL
);

CREATE TABLE accounts (
    user_id text NOT NULL DEFAULT '',
    storage_key text PRIMARY KEY,
    id text NOT NULL DEFAULT '',
    type text NOT NULL DEFAULT '',
    name text NOT NULL DEFAULT '',
    pot text NOT NULL DEFAULT '',
    commodities text[],
    location text NOT NULL DEFAULT '',
    class text NOT NULL DEFAULT '',
    groups jsonb,
    closed_at timestamptz,
    display_name text NOT NULL DEFAULT '',
    note text NOT NULL DEFAULT '',
    estimated boolean,
    UNIQUE (user_id,id)
);
CREATE TABLE journal (
    user_id text NOT NULL DEFAULT '',
    id text PRIMARY KEY,
    seq bigint NOT NULL DEFAULT 0,
    date text NOT NULL DEFAULT '',
    kind text NOT NULL DEFAULT '',
    payee text NOT NULL DEFAULT '',
    postings jsonb NOT NULL DEFAULT '[]',
    tags text[],
    links jsonb NOT NULL DEFAULT '[]',
    status text NOT NULL DEFAULT '',
    actor jsonb NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT '0001-01-01T00:00:00Z',
    UNIQUE (user_id,seq)
);
CREATE INDEX journal_user_date_seq ON journal (user_id,date,seq);
CREATE INDEX journal_user_kind_status ON journal (user_id,kind,status);
CREATE TABLE entry_tags (
    id text PRIMARY KEY,
    user_id text NOT NULL DEFAULT '',
    entry_id text NOT NULL DEFAULT '',
    tags text[],
    UNIQUE (user_id,entry_id)
);
CREATE TABLE balances (
    pk bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id text NOT NULL DEFAULT '',
    dirty boolean NOT NULL DEFAULT false
);

CREATE TABLE budgets (
    storage_key text PRIMARY KEY,
    user_id text NOT NULL DEFAULT '',
    id text NOT NULL DEFAULT '',
    name text NOT NULL DEFAULT '',
    revision integer NOT NULL DEFAULT 0,
    "limit" numeric NOT NULL DEFAULT 0,
    limit_lexeme text,
    accounts text[],
    group_ref jsonb,
    "order" integer NOT NULL DEFAULT 0,
    archived boolean NOT NULL DEFAULT false,
    UNIQUE (user_id,id)
);
CREATE INDEX budgets_user_order ON budgets (user_id,"order",id);
CREATE TABLE rules (
    storage_key text PRIMARY KEY,
    user_id text NOT NULL DEFAULT '',
    id text NOT NULL DEFAULT '',
    priority integer NOT NULL DEFAULT 0,
    match text NOT NULL DEFAULT '',
    "set" jsonb NOT NULL DEFAULT '{}',
    note text NOT NULL DEFAULT '',
    enabled boolean NOT NULL DEFAULT false,
    version integer NOT NULL DEFAULT 0,
    UNIQUE (user_id,id)
);
CREATE INDEX rules_user_priority ON rules (user_id,priority,id);

CREATE TABLE balance_assertions (
 id text PRIMARY KEY,
 user_id text NOT NULL DEFAULT '',
 account text NOT NULL DEFAULT '',
 date text NOT NULL DEFAULT '',
 amount jsonb NOT NULL DEFAULT '{"v":"0","c":""}',
 source text NOT NULL DEFAULT '',
 ok boolean NOT NULL DEFAULT false,
 diff numeric NOT NULL DEFAULT 0,
 archived boolean NOT NULL DEFAULT false
);
CREATE INDEX assertions_user_account ON balance_assertions (user_id,account);
CREATE TABLE discrepancies (
 id text PRIMARY KEY,
 user_id text NOT NULL DEFAULT '',
 assertion_id text NOT NULL DEFAULT '',
 account text NOT NULL DEFAULT '',
 diff jsonb NOT NULL DEFAULT '{"v":"0","c":""}',
 status text NOT NULL DEFAULT '',
 resolution text NOT NULL DEFAULT '',
 reason text NOT NULL DEFAULT '',
 adjustment_id text NOT NULL DEFAULT '',
 archived boolean NOT NULL DEFAULT false
);
CREATE INDEX discrepancies_user_status_account ON discrepancies (user_id,status,account);

CREATE TABLE scenarios (
 storage_key text PRIMARY KEY,
 user_id text NOT NULL DEFAULT '',
 id text NOT NULL DEFAULT '',
 name text NOT NULL DEFAULT '',
 revision integer NOT NULL DEFAULT 0,
 overrides jsonb NOT NULL DEFAULT '[]',
 events jsonb,
 legacy jsonb,
 archived boolean NOT NULL DEFAULT false,
 UNIQUE (user_id,id)
);
CREATE TABLE income_models (
 id text PRIMARY KEY,
 user_id text NOT NULL DEFAULT '',
 streams jsonb NOT NULL DEFAULT '[]'
);

CREATE TABLE tables (
 storage_key text PRIMARY KEY,
 user_id text NOT NULL DEFAULT '',
 id text NOT NULL DEFAULT '',
 name text NOT NULL DEFAULT '',
 columns jsonb NOT NULL DEFAULT '[]',
 keys jsonb,
 checks jsonb,
 ledger jsonb,
 description text NOT NULL DEFAULT '',
 archived boolean NOT NULL DEFAULT false,
 UNIQUE(user_id,id)
);
CREATE INDEX tables_user_name ON tables(user_id,name);
CREATE TABLE rows (
 storage_key text NOT NULL UNIQUE,
 user_id text NOT NULL,
 table_id text NOT NULL,
 id text NOT NULL,
 cells jsonb NOT NULL DEFAULT '{}',
 version integer NOT NULL DEFAULT 0,
 key_hashes text[],
 decimal_cells text[] NOT NULL DEFAULT '{}',
 archived boolean NOT NULL DEFAULT false,
 PRIMARY KEY(user_id,table_id,id)
);
CREATE TABLE row_keys (
 user_id text NOT NULL,
 table_id text NOT NULL,
 key_hash text NOT NULL,
 row_id text NOT NULL,
 PRIMARY KEY(user_id,table_id,key_hash),
 FOREIGN KEY(user_id,table_id,row_id) REFERENCES rows(user_id,table_id,id) ON DELETE CASCADE
);
CREATE INDEX row_keys_row ON row_keys(user_id,table_id,row_id);
CREATE TABLE import_batches (
 id text PRIMARY KEY,
 user_id text NOT NULL DEFAULT '',
 account text NOT NULL DEFAULT '',
 format text NOT NULL DEFAULT '',
 file_sha256 text NOT NULL DEFAULT '',
 status text NOT NULL DEFAULT '',
 candidates jsonb NOT NULL DEFAULT '[]',
 "from" text NOT NULL DEFAULT '',
 "to" text NOT NULL DEFAULT '',
 closing_balance jsonb,
 created_at timestamptz NOT NULL DEFAULT '0001-01-01T00:00:00Z'
);
CREATE INDEX import_batches_user_created ON import_batches(user_id,created_at DESC);
CREATE TABLE import_rows (
 id text PRIMARY KEY,
 user_id text NOT NULL DEFAULT '',
 batch_id text NOT NULL DEFAULT '',
 account text NOT NULL DEFAULT '',
 dedupe_key text NOT NULL DEFAULT '',
 raw jsonb,
 proposed jsonb NOT NULL DEFAULT '{}',
 kind text NOT NULL DEFAULT '',
 entry_id text NOT NULL DEFAULT '',
 reversal_id text NOT NULL DEFAULT '',
 proposed_id text NOT NULL DEFAULT '',
 skip boolean NOT NULL DEFAULT false,
 UNIQUE(user_id,account,dedupe_key)
);
CREATE INDEX import_rows_user_batch ON import_rows(user_id,batch_id);
CREATE TABLE import_files (
 id text PRIMARY KEY,
 user_id text NOT NULL DEFAULT '',
 content bytea,
 sha256 text NOT NULL DEFAULT ''
);
CREATE TABLE feeds (
 storage_key text PRIMARY KEY,
 user_id text NOT NULL DEFAULT '',
 id text NOT NULL DEFAULT '',
 url text NOT NULL DEFAULT '',
 account text NOT NULL DEFAULT '',
 format text NOT NULL DEFAULT '',
 date_format text NOT NULL DEFAULT '',
 token_encrypted text NOT NULL DEFAULT ''
);
CREATE INDEX feeds_user ON feeds(user_id);
CREATE TABLE dashboards (
 storage_key text PRIMARY KEY,
 user_id text NOT NULL DEFAULT '',
 id text NOT NULL DEFAULT '',
 title text NOT NULL DEFAULT '',
 published_version integer NOT NULL DEFAULT 0,
 draft_version integer NOT NULL DEFAULT 0,
 revision integer NOT NULL DEFAULT 0,
 home boolean NOT NULL DEFAULT false,
 archived boolean NOT NULL DEFAULT false,
 published_by jsonb NOT NULL DEFAULT '{}',
 published_at timestamptz,
 publish_note text NOT NULL DEFAULT '',
 UNIQUE(user_id,id)
);
CREATE TABLE dashboard_versions (
 storage_key text PRIMARY KEY,
 user_id text NOT NULL DEFAULT '',
 dashboard_id text NOT NULL DEFAULT '',
 version integer NOT NULL DEFAULT 0,
 spec jsonb NOT NULL DEFAULT '{}',
 author jsonb NOT NULL DEFAULT '{}',
 created_at timestamptz NOT NULL DEFAULT '0001-01-01T00:00:00Z',
 note text NOT NULL DEFAULT '',
 UNIQUE(user_id,dashboard_id,version)
);
CREATE TABLE views (
 storage_key text PRIMARY KEY,
 user_id text NOT NULL DEFAULT '',
 id text NOT NULL DEFAULT '',
 query jsonb NOT NULL DEFAULT '{}',
 UNIQUE(user_id,id)
);
