ALTER TABLE daily_digests ADD COLUMN next_attempt_at timestamptz;
ALTER TABLE daily_digests ADD COLUMN attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0);
INSERT INTO schema_migrations(version) VALUES (4);
