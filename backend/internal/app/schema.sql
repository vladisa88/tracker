CREATE TABLE owner_sessions (
 token_hash text PRIMARY KEY,
 owner_id bigint NOT NULL CHECK (owner_id > 0),
 csrf_token text NOT NULL,
 expires_at timestamptz NOT NULL
);
CREATE INDEX owner_sessions_expiry ON owner_sessions(expires_at);
CREATE TABLE login_requests (
 state_hash text PRIMARY KEY,
 browser_hash text NOT NULL,
 verifier text NOT NULL,
 nonce text NOT NULL,
 expires_at timestamptz NOT NULL
);
CREATE INDEX login_requests_expiry ON login_requests(expires_at);
INSERT INTO schema_migrations(version) VALUES (1);
