package auth

import "testing"

func TestNewPostgresSessionManagerRejectsNilDB(t *testing.T) {
	if _, err := NewPostgresSessionManager(nil, SessionManagerConfig{}); err == nil {
		t.Fatalf("expected nil db to be rejected")
	}
}

func TestSessionTokenRoundTrip(t *testing.T) {
	tokenID := "0123456789abcdef0123456789abcdef"
	tokenSecret := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	raw := buildSessionToken(tokenID, tokenSecret)

	gotID, gotSecret, ok := splitSessionToken(raw)
	if !ok {
		t.Fatalf("expected token to parse")
	}
	if gotID != tokenID {
		t.Fatalf("token id: got %q want %q", gotID, tokenID)
	}
	if gotSecret != tokenSecret {
		t.Fatalf("token secret: got %q want %q", gotSecret, tokenSecret)
	}
}

func TestSplitSessionTokenRejectsInvalidTokens(t *testing.T) {
	tests := []string{
		"",
		" ",
		"abc",
		"nothex.nothex",
		"0123456789abcdef0123456789abcdef.",
		".0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		"0123456789abcdef0123456789abcdef.0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdeg",
		"0123456789abcdef0123456789abcde.0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	}

	for _, raw := range tests {
		if _, _, ok := splitSessionToken(raw); ok {
			t.Fatalf("expected token %q to be rejected", raw)
		}
	}
}
