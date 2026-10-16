ALTER TABLE daily_digests ADD COLUMN next_attempt_at timestamptz;
ALTER TABLE daily_digests ADD COLUMN attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0);
ALTER TABLE daily_digests ADD COLUMN last_result text CHECK (last_result IN ('sent', 'unknown', 'temporary_error', 'rate_limited', 'permanent_error'));
-- Earlier releases recorded confirmed success, but not the number of attempts.
UPDATE daily_digests SET attempts=1,last_result='sent' WHERE status='sent';
INSERT INTO schema_migrations(version) VALUES (4);
