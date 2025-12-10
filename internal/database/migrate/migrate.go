package migrate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"hopshare/deploy/migrations"
)

// Run applies all pending migrations in order.
func Run(ctx context.Context, db *sql.DB) error {
	migs, err := migrations.List()
	if err != nil {
		return fmt.Errorf("load migrations: %w", err)
	}
	if len(migs) == 0 {
		return errors.New("no migrations found")
	}

	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`); err != nil {
		return fmt.Errorf("ensure schema_migrations: %w", err)
	}

	for _, mig := range migs {
		applied, err := isApplied(ctx, db, mig.Version)
		if err != nil {
			return err
		}
		if applied {
			continue
		}
		if err := applyMigration(ctx, db, mig); err != nil {
			return err
		}
	}

	return nil
}

func isApplied(ctx context.Context, db *sql.DB, version string) (bool, error) {
	var exists bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)`, version).Scan(&exists); err != nil {
		return false, fmt.Errorf("check migration %s: %w", version, err)
	}
	return exists, nil
}

func applyMigration(ctx context.Context, db *sql.DB, mig migrations.Migration) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration %s: %w", mig.Version, err)
	}

	if _, err := tx.ExecContext(ctx, mig.SQL); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("apply migration %s: %w", mig.Version, err)
	}

	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, mig.Version); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("record migration %s: %w", mig.Version, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration %s: %w", mig.Version, err)
	}

	return nil
}
