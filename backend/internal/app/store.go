package app

import (
	"context"
	"database/sql"
	_ "embed"
)

//go:embed schema.sql
var schema string

//go:embed subscriptions.sql
var subscriptionsSchema string

//go:embed digest.sql
var digestSchema string

const schemaVersion = 3

func Migrate(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(78264001)"); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version integer PRIMARY KEY)`); err != nil {
		return err
	}
	var version int
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0) FROM schema_migrations`).Scan(&version); err != nil {
		return err
	}
	if version == 0 {
		if _, err = tx.ExecContext(ctx, schema); err != nil {
			return err
		}
	}
	if version < 2 {
		if _, err = tx.ExecContext(ctx, subscriptionsSchema); err != nil {
			return err
		}
	}
	if version < 3 {
		if _, err = tx.ExecContext(ctx, digestSchema); err != nil {
			return err
		}
	}
	if version > schemaVersion {
		return ErrSchema
	}
	if err = migrateBot(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}
