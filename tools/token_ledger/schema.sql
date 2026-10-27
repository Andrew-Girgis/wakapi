-- Additive only. No changes to upstream Wakapi tables or heartbeat identity.
CREATE TABLE IF NOT EXISTS token_usage_events (
    user_id TEXT NOT NULL,
    source TEXT NOT NULL,
    source_key TEXT NOT NULL,
    session_key TEXT NOT NULL DEFAULT '',
    occurred_ms INTEGER NOT NULL,
    machine TEXT NOT NULL,
    project TEXT NOT NULL DEFAULT '',
    model TEXT NOT NULL DEFAULT '',
    input_tokens INTEGER NOT NULL CHECK(input_tokens >= 0),
    cache_read_tokens INTEGER NOT NULL CHECK(cache_read_tokens >= 0),
    cache_write_tokens INTEGER NOT NULL CHECK(cache_write_tokens >= 0),
    unknown_input_tokens INTEGER NOT NULL DEFAULT 0 CHECK(unknown_input_tokens >= 0),
    output_tokens INTEGER NOT NULL CHECK(output_tokens >= 0),
    reasoning_tokens INTEGER NOT NULL CHECK(reasoning_tokens >= 0),
    parser_version INTEGER NOT NULL,
    raw_sha256 TEXT NOT NULL,
    raw_usage_json TEXT NOT NULL DEFAULT '{}',
    observed_ms INTEGER NOT NULL,
    PRIMARY KEY (user_id, source, source_key)
);
CREATE INDEX IF NOT EXISTS idx_token_usage_range ON token_usage_events(user_id, occurred_ms);
CREATE INDEX IF NOT EXISTS idx_token_usage_session ON token_usage_events(user_id, session_key, source);
CREATE INDEX IF NOT EXISTS idx_token_usage_filters ON token_usage_events(user_id, machine, project, occurred_ms);
CREATE TABLE IF NOT EXISTS token_usage_session_refs (
    user_id TEXT NOT NULL,
    source TEXT NOT NULL,
    source_key TEXT NOT NULL,
    session_key TEXT NOT NULL,
    PRIMARY KEY (user_id, source, source_key, session_key)
);
CREATE INDEX IF NOT EXISTS idx_token_usage_refs_session
    ON token_usage_session_refs(user_id, session_key, source);
CREATE TABLE IF NOT EXISTS token_usage_quarantined_sessions (
    user_id TEXT NOT NULL,
    source TEXT NOT NULL,
    session_key TEXT NOT NULL,
    reason TEXT NOT NULL,
    status TEXT NOT NULL CHECK(status IN ('open','resolved')),
    PRIMARY KEY (user_id, source, session_key)
);
CREATE TABLE IF NOT EXISTS token_usage_accounting_modes (
    user_id TEXT NOT NULL,
    source TEXT NOT NULL,
    session_key TEXT NOT NULL,
    mode TEXT NOT NULL,
    PRIMARY KEY (user_id,source,session_key)
);
CREATE TABLE IF NOT EXISTS token_usage_conflicts (
    user_id TEXT NOT NULL,
    source TEXT NOT NULL,
    source_key TEXT NOT NULL,
    previous_sha256 TEXT NOT NULL,
    incoming_sha256 TEXT NOT NULL,
    reason TEXT NOT NULL,
    observed_ms INTEGER NOT NULL,
    PRIMARY KEY (user_id, source, source_key, incoming_sha256)
);
