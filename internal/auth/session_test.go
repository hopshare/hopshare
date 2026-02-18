package auth

import (
	"testing"
	"time"
)

func TestSessionManagerRevokeAllForMember(t *testing.T) {
	sm := NewSessionManager()

	memberOneTokenA, err := sm.Create(101)
	if err != nil {
		t.Fatalf("create member one token A: %v", err)
	}
	memberOneTokenB, err := sm.Create(101)
	if err != nil {
		t.Fatalf("create member one token B: %v", err)
	}
	memberTwoToken, err := sm.Create(202)
	if err != nil {
		t.Fatalf("create member two token: %v", err)
	}

	if memberID, ok := sm.Get(memberOneTokenA); !ok || memberID != 101 {
		t.Fatalf("expected member one token A to resolve before revoke")
	}
	if memberID, ok := sm.Get(memberOneTokenB); !ok || memberID != 101 {
		t.Fatalf("expected member one token B to resolve before revoke")
	}
	if memberID, ok := sm.Get(memberTwoToken); !ok || memberID != 202 {
		t.Fatalf("expected member two token to resolve before revoke")
	}

	if revoked := sm.RevokeAllForMember(101); revoked != 2 {
		t.Fatalf("expected 2 revoked sessions for member one, got %d", revoked)
	}
	if _, ok := sm.Get(memberOneTokenA); ok {
		t.Fatalf("expected member one token A to be revoked")
	}
	if _, ok := sm.Get(memberOneTokenB); ok {
		t.Fatalf("expected member one token B to be revoked")
	}
	if memberID, ok := sm.Get(memberTwoToken); !ok || memberID != 202 {
		t.Fatalf("expected member two token to remain valid after member one revoke")
	}
	if revoked := sm.RevokeAllForMember(101); revoked != 0 {
		t.Fatalf("expected second revoke call to be idempotent with 0 revoked, got %d", revoked)
	}
}

func TestSessionManagerExpiry(t *testing.T) {
	sm := NewSessionManagerWithTTL(30 * time.Minute)
	now := time.Date(2026, time.January, 2, 15, 0, 0, 0, time.UTC)
	sm.now = func() time.Time { return now }

	token, err := sm.Create(303)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	if memberID, ok := sm.Get(token); !ok || memberID != 303 {
		t.Fatalf("expected token to be valid before expiry")
	}

	now = now.Add(31 * time.Minute)
	if _, ok := sm.Get(token); ok {
		t.Fatalf("expected token to be expired")
	}
	if revoked := sm.RevokeAllForMember(303); revoked != 0 {
		t.Fatalf("expected expired token cleanup to remove member mapping, revoked=%d", revoked)
	}
}

func TestSessionManagerWithoutExpiry(t *testing.T) {
	sm := NewSessionManagerWithTTL(0)
	now := time.Date(2026, time.January, 2, 15, 0, 0, 0, time.UTC)
	sm.now = func() time.Time { return now }

	token, err := sm.Create(404)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	now = now.Add(24 * time.Hour)
	if memberID, ok := sm.Get(token); !ok || memberID != 404 {
		t.Fatalf("expected token to remain valid when ttl disabled")
	}
}

func TestSessionManagerIdleTimeout(t *testing.T) {
	sm := NewSessionManagerWithConfig(SessionManagerConfig{
		IdleTimeout: 10 * time.Minute,
	})
	now := time.Date(2026, time.January, 2, 15, 0, 0, 0, time.UTC)
	sm.now = func() time.Time { return now }

	token, err := sm.Create(505)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	now = now.Add(9 * time.Minute)
	if memberID, ok := sm.Get(token); !ok || memberID != 505 {
		t.Fatalf("expected token to be valid before idle timeout")
	}

	now = now.Add(9 * time.Minute)
	if memberID, ok := sm.Get(token); !ok || memberID != 505 {
		t.Fatalf("expected token to remain valid after activity refresh")
	}

	now = now.Add(11 * time.Minute)
	if _, ok := sm.Get(token); ok {
		t.Fatalf("expected token to expire after idle timeout")
	}
}

func TestSessionManagerAbsoluteTTLWithActivity(t *testing.T) {
	sm := NewSessionManagerWithConfig(SessionManagerConfig{
		AbsoluteTTL: 30 * time.Minute,
		IdleTimeout: 10 * time.Minute,
	})
	now := time.Date(2026, time.January, 2, 15, 0, 0, 0, time.UTC)
	sm.now = func() time.Time { return now }

	token, err := sm.Create(606)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	now = now.Add(9 * time.Minute)
	if memberID, ok := sm.Get(token); !ok || memberID != 606 {
		t.Fatalf("expected token to be valid before absolute expiry")
	}

	now = now.Add(9 * time.Minute)
	if memberID, ok := sm.Get(token); !ok || memberID != 606 {
		t.Fatalf("expected token to be valid before absolute expiry with activity")
	}

	now = now.Add(13 * time.Minute)
	if _, ok := sm.Get(token); ok {
		t.Fatalf("expected token to expire at absolute ttl despite activity")
	}
}

func TestSessionManagerRotate(t *testing.T) {
	sm := NewSessionManager()
	token, err := sm.Create(707)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	newToken, memberID, ok := sm.Rotate(token)
	if !ok {
		t.Fatalf("expected rotate to succeed")
	}
	if memberID != 707 {
		t.Fatalf("rotate member id: got %d want %d", memberID, 707)
	}
	if newToken == "" || newToken == token {
		t.Fatalf("expected rotate to issue a distinct non-empty token")
	}
	if _, ok := sm.Get(token); ok {
		t.Fatalf("expected old token to be invalid after rotate")
	}
	if gotMemberID, ok := sm.Get(newToken); !ok || gotMemberID != 707 {
		t.Fatalf("expected new token to resolve for member 707")
	}
}
