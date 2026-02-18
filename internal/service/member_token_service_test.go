package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"hopshare/internal/types"
)

func TestMemberTokenConsumeValidOnce(t *testing.T) {
	db := require_db(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	member := createTokenTestMember(t, ctx, db, "consume_once")
	token, err := IssueMemberToken(ctx, db, IssueMemberTokenParams{
		MemberID: member.ID,
		Purpose:  MemberTokenPurposePasswordReset,
		TTL:      time.Hour,
	})
	if err != nil {
		t.Fatalf("issue member token: %v", err)
	}

	tokenID, _, ok := splitIssuedMemberToken(token)
	if !ok {
		t.Fatalf("expected issued token to parse")
	}

	consumedMemberID, err := ConsumeMemberToken(ctx, db, MemberTokenPurposePasswordReset, token)
	if err != nil {
		t.Fatalf("consume member token: %v", err)
	}
	if consumedMemberID != member.ID {
		t.Fatalf("consumed member id: got %d want %d", consumedMemberID, member.ID)
	}

	var usedAt time.Time
	if err := db.QueryRowContext(ctx, `
		SELECT used_at
		FROM member_tokens
		WHERE token_id = $1
	`, tokenID).Scan(&usedAt); err != nil {
		t.Fatalf("load used_at for consumed token: %v", err)
	}
	if usedAt.IsZero() {
		t.Fatalf("expected used_at to be set")
	}

	_, err = ConsumeMemberToken(ctx, db, MemberTokenPurposePasswordReset, token)
	if !errors.Is(err, ErrTokenUsed) {
		t.Fatalf("expected ErrTokenUsed on token reuse, got %v", err)
	}
}

func TestMemberTokenConsumeExpiredFails(t *testing.T) {
	db := require_db(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	member := createTokenTestMember(t, ctx, db, "consume_expired")
	token, err := IssueMemberToken(ctx, db, IssueMemberTokenParams{
		MemberID: member.ID,
		Purpose:  MemberTokenPurposePasswordReset,
		TTL:      time.Hour,
	})
	if err != nil {
		t.Fatalf("issue member token: %v", err)
	}
	tokenID, _, ok := splitIssuedMemberToken(token)
	if !ok {
		t.Fatalf("expected issued token to parse")
	}

	if _, err := db.ExecContext(ctx, `
		UPDATE member_tokens
		SET expires_at = NOW() - INTERVAL '1 minute'
		WHERE token_id = $1
	`, tokenID); err != nil {
		t.Fatalf("expire token: %v", err)
	}

	_, err = ConsumeMemberToken(ctx, db, MemberTokenPurposePasswordReset, token)
	if !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("expected ErrTokenExpired, got %v", err)
	}
}

func TestMemberTokenConsumeWrongTokenFails(t *testing.T) {
	db := require_db(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	member := createTokenTestMember(t, ctx, db, "consume_wrong")
	token, err := IssueMemberToken(ctx, db, IssueMemberTokenParams{
		MemberID: member.ID,
		Purpose:  MemberTokenPurposePasswordReset,
		TTL:      time.Hour,
	})
	if err != nil {
		t.Fatalf("issue member token: %v", err)
	}
	tokenID, _, ok := splitIssuedMemberToken(token)
	if !ok {
		t.Fatalf("expected issued token to parse")
	}

	wrongToken := buildIssuedMemberToken(tokenID, strings.Repeat("0", 64))
	_, err = ConsumeMemberToken(ctx, db, MemberTokenPurposePasswordReset, wrongToken)
	if !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("expected ErrTokenInvalid, got %v", err)
	}

	consumedMemberID, err := ConsumeMemberToken(ctx, db, MemberTokenPurposePasswordReset, token)
	if err != nil {
		t.Fatalf("consume original token after invalid attempt: %v", err)
	}
	if consumedMemberID != member.ID {
		t.Fatalf("consumed member id: got %d want %d", consumedMemberID, member.ID)
	}
}

func TestMemberTokenEmailVerificationPurpose(t *testing.T) {
	db := require_db(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	member := createTokenTestMember(t, ctx, db, "email_verification")
	token, err := IssueMemberToken(ctx, db, IssueMemberTokenParams{
		MemberID: member.ID,
		Purpose:  MemberTokenPurposeEmailVerification,
		TTL:      time.Hour,
	})
	if err != nil {
		t.Fatalf("issue email verification token: %v", err)
	}

	if _, err := ValidateMemberToken(ctx, db, MemberTokenPurposeEmailVerification, token); err != nil {
		t.Fatalf("validate email verification token: %v", err)
	}

	if _, err := ConsumeMemberToken(ctx, db, MemberTokenPurposePasswordReset, token); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("expected ErrTokenInvalid when consuming with wrong purpose, got %v", err)
	}

	consumedMemberID, err := ConsumeMemberToken(ctx, db, MemberTokenPurposeEmailVerification, token)
	if err != nil {
		t.Fatalf("consume email verification token: %v", err)
	}
	if consumedMemberID != member.ID {
		t.Fatalf("consumed member id: got %d want %d", consumedMemberID, member.ID)
	}
}

func createTokenTestMember(t *testing.T, ctx context.Context, db *sql.DB, label string) types.Member {
	t.Helper()
	suffix := fmt.Sprintf("%s_%d", label, time.Now().UnixNano())
	input := types.Member{
		FirstName:              "Token",
		LastName:               "Tester",
		Username:               "token_" + suffix,
		Email:                  "token_" + suffix + "@example.com",
		PasswordHash:           "hashed_password",
		PreferredContactMethod: types.ContactMethodEmail,
		PreferredContact:       "token_" + suffix + "@example.com",
		Enabled:                true,
		Verified:               false,
	}

	member, err := CreateMember(ctx, db, input)
	if err != nil {
		t.Fatalf("create token test member: %v", err)
	}
	return member
}
