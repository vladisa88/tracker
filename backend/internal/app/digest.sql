CREATE TABLE daily_digests (
 business_date date PRIMARY KEY,
 chat_id bigint NOT NULL CHECK (chat_id > 0),
 message text NOT NULL,
 status text NOT NULL CHECK (status IN ('pending', 'empty', 'sent')),
 created_at timestamptz NOT NULL,
 sent_at timestamptz,
 CHECK ((status = 'empty') = (message = '')),
 CHECK ((status = 'sent') = (sent_at IS NOT NULL))
);
INSERT INTO schema_migrations(version) VALUES (3);
