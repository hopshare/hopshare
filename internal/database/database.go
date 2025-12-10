package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// New opens and pings a Postgres database.
// Note: a Postgres driver (e.g. pgx stdlib) must be imported in main to register "postgres".
func New(ctx context.Context, url string) (*sql.DB, error) {
	if url == "" {
		return nil, errors.New("database url is required (set HOPSHARE_DB_URL)")
	}

	db, err := sql.Open("postgres", url)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	return db, nil
}
