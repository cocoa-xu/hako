CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE devices (
    id UUID PRIMARY KEY,
    token_hash TEXT NOT NULL CHECK (token_hash ~ '^[0-9a-f]{64}$'),
    display_name TEXT NOT NULL DEFAULT '' CHECK (char_length(display_name) <= 80),
    created_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ
);
CREATE TABLE enrollments (
    key_hash TEXT PRIMARY KEY CHECK (key_hash ~ '^[0-9a-f]{64}$'),
    id TEXT NOT NULL UNIQUE,
    label TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    device_id UUID REFERENCES devices(id),
    revoked BOOLEAN NOT NULL DEFAULT FALSE
);
CREATE TABLE sessions (
    token_hash TEXT PRIMARY KEY CHECK (token_hash ~ '^[0-9a-f]{64}$'),
    expires_at TIMESTAMPTZ NOT NULL
);
CREATE TABLE snapshots (
    device_id UUID PRIMARY KEY REFERENCES devices(id),
    payload JSONB NOT NULL
);
CREATE INDEX sessions_expiry ON sessions(expires_at);
CREATE INDEX enrollments_expiry ON enrollments(expires_at) WHERE device_id IS NULL AND NOT revoked;
