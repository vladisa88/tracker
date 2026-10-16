package app

import (
	"context"
	"database/sql"
)

// Telegram has its own version ledger so independent subscription migrations
// and Telegram migrations can ship without imposing an order on those features.
func migrateBot(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS telegram_schema_migrations(version integer PRIMARY KEY)`); err != nil {
		return err
	}
	var version int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0) FROM telegram_schema_migrations`).Scan(&version); err != nil {
		return err
	}
	if version > 2 {
		return ErrSchema
	}
	if version == 0 {
		_, err := tx.ExecContext(ctx, `CREATE TABLE telegram_connections(
   owner_id bigint PRIMARY KEY CHECK(owner_id>0),
   chat_id bigint CHECK(chat_id>0),
   status text NOT NULL DEFAULT 'disconnected' CHECK(status IN ('disconnected','checking','connected','error')),
   next_update_id bigint NOT NULL DEFAULT 0 CHECK(next_update_id>=0)
  ); INSERT INTO telegram_schema_migrations VALUES(1)`)
		if err != nil {
			return err
		}
	}
	if version < 2 {
		_, err := tx.ExecContext(ctx, `ALTER TABLE telegram_connections ADD COLUMN connection_revision bigint NOT NULL DEFAULT 0; INSERT INTO telegram_schema_migrations VALUES(2)`)
		return err
	}
	return nil
}
func botSchemaReady(ctx context.Context, db *sql.DB) bool {
	var version int
	return db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0) FROM telegram_schema_migrations`).Scan(&version) == nil && version == 2
}
