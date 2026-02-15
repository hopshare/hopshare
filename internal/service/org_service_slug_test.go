package service

import "testing"

func TestNormalizeOrganizationURLName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "simple", input: "My Organization", want: "my-organization"},
		{name: "punctuation", input: "Acme, Inc. (North)", want: "acme-inc-north"},
		{name: "only symbols", input: "!!!", want: "organization"},
		{name: "trim separators", input: "  --Alpha--  ", want: "alpha"},
		{name: "max length", input: "abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyz", want: "abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyzabcdefghijk"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := normalizeOrganizationURLName(tc.input)
			if got != tc.want {
				t.Fatalf("normalizeOrganizationURLName(%q) = %q, want %q", tc.input, got, tc.want)
			}
			if len(got) > organizationURLNameMaxLen {
				t.Fatalf("normalizeOrganizationURLName(%q) exceeded max len: %d", tc.input, len(got))
			}
		})
	}
}

func TestOrganizationURLNameWithOrdinal(t *testing.T) {
	t.Parallel()

	base := "my-organization"
	if got := organizationURLNameWithOrdinal(base, 1); got != "my-organization" {
		t.Fatalf("ordinal=1 got %q", got)
	}
	if got := organizationURLNameWithOrdinal(base, 2); got != "my-organization-2" {
		t.Fatalf("ordinal=2 got %q", got)
	}

	longBase := "abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyzabcdefghijk"
	got := organizationURLNameWithOrdinal(longBase, 27)
	if got != "abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyzabcdefgh-27" {
		t.Fatalf("truncated ordinal slug = %q", got)
	}
	if len(got) > organizationURLNameMaxLen {
		t.Fatalf("ordinal slug exceeded max len: %d", len(got))
	}
}
