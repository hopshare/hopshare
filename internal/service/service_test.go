package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	_ "github.com/lib/pq"

	"hopshare/internal/database"
	"hopshare/internal/database/migrate"
	"hopshare/internal/types"
)

var (
	dbOnce        sync.Once
	sharedDB      *sql.DB
	dbSetupErr    error
	errMissingURL = errors.New("HOPSHARE_DB_URL or DATABASE_URL not set")
)

// require_db returns a live database connection and ensures migrations run once.
func require_db(t *testing.T) *sql.DB {
	t.Helper()

	dbOnce.Do(func() {
		dbURL := os.Getenv("HOPSHARE_DB_URL")
		if dbURL == "" {
			dbURL = os.Getenv("DATABASE_URL")
		}
		if dbURL == "" {
			dbSetupErr = errMissingURL
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		sharedDB, dbSetupErr = database.New(ctx, dbURL)
		if dbSetupErr != nil {
			return
		}

		dbSetupErr = migrate.Run(ctx, sharedDB)
	})

	if errors.Is(dbSetupErr, errMissingURL) {
		t.Skip(errMissingURL.Error())
	}
	if dbSetupErr != nil {
		t.Fatalf("database setup failed: %v", dbSetupErr)
	}
	return sharedDB
}

func TestCreateMember(t *testing.T) {
	db := require_db(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	username := fmt.Sprintf("inttest_%d", time.Now().UnixNano())
	email := fmt.Sprintf("%s@example.com", username)

	input := types.Member{
		Username:               username,
		Email:                  email,
		PasswordHash:           "hashed_password",
		PreferredContactMethod: types.ContactMethodEmail,
		PreferredContact:       email,
		Enabled:                true,
		Verified:               true,
	}

	member, err := CreateMember(ctx, db, input)
	if err != nil {
		t.Fatalf("CreateMember returned error: %v", err)
	}

	if member.ID == 0 {
		t.Fatalf("expected member ID to be set")
	}
	if member.Username != input.Username || member.Email != input.Email || member.PasswordHash != input.PasswordHash {
		t.Fatalf("returned member does not match input")
	}
	if member.CreatedAt.IsZero() || member.UpdatedAt.IsZero() {
		t.Fatalf("expected timestamps to be set, got created_at=%v updated_at=%v", member.CreatedAt, member.UpdatedAt)
	}
}
