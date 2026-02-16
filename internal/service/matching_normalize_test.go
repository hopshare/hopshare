package service

import "testing"

func TestTokenizeMatchTextMoveCouch(t *testing.T) {
	tokens := tokenizeMatchText("Need someone to help move a couch.")
	if len(tokens) == 0 {
		t.Fatalf("expected tokens, got none")
	}
	assertContainsToken(t, tokens, "move")
	assertContainsToken(t, tokens, "couch")
	assertNotContainsToken(t, tokens, "need")
	assertNotContainsToken(t, tokens, "help")
	assertNotContainsToken(t, tokens, "someone")
}

func TestStemMatchToken(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "moving", want: "move"},
		{in: "moved", want: "move"},
		{in: "boxes", want: "box"},
		{in: "drives", want: "drive"},
	}

	for _, tt := range tests {
		got := stemMatchToken(tt.in)
		if got != tt.want {
			t.Fatalf("stemMatchToken(%q)=%q, want %q", tt.in, got, tt.want)
		}
	}
}

func assertContainsToken(t *testing.T, tokens []string, target string) {
	t.Helper()
	for _, tok := range tokens {
		if tok == target {
			return
		}
	}
	t.Fatalf("expected token %q in %v", target, tokens)
}

func assertNotContainsToken(t *testing.T, tokens []string, target string) {
	t.Helper()
	for _, tok := range tokens {
		if tok == target {
			t.Fatalf("unexpected token %q in %v", target, tokens)
		}
	}
}
