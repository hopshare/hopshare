package http_test

import "testing"

func TestHTTPMethodGuards(t *testing.T) {
	db := requireHTTPTestDB(t)
	ctx, cancel := newTestContext(t)
	defer cancel()

	member := createSeededMember(t, ctx, db, "method_guard_member", uniqueTestSuffix())
	server := newHTTPServer(t, db)
	actor := newTestActor(t, "member", server.URL, member.Member.Username, member.Password)
	actor.Login()

	cases := []struct {
		name      string
		path      string
		method    string
		wantAllow string
	}{
		{"landing", "/", "POST", "GET"},
		{"login", "/login", "PUT", "GET, POST"},
		{"signup", "/signup", "PUT", "GET, POST"},
		{"signup-success", "/signup-success", "POST", "GET"},
		{"learn-more", "/learn-more", "POST", "GET"},
		{"terms", "/terms", "POST", "GET"},
		{"privacy", "/privacy", "POST", "GET"},
		{"help", "/help", "POST", "GET"},
		{"forgot-password", "/forgot-password", "PUT", "GET, POST"},
		{"reset-password", "/reset-password", "PUT", "GET, POST"},
		{"my-hopshare", "/my-hopshare", "POST", "GET"},
		{"my-hops", "/my-hops", "POST", "GET"},
		{"profile", "/profile", "PUT", "GET, POST"},
		{"members-avatar", "/members/avatar", "POST", "GET"},
		{"members-avatar-public", "/members/avatar/public", "POST", "GET"},
		{"messages", "/messages", "POST", "GET"},
		{"messages-unread-count", "/messages/unread-count", "POST", "GET"},
		{"messages-delete", "/messages/delete", "GET", "POST"},
		{"messages-reply", "/messages/reply", "GET", "POST"},
		{"messages-action", "/messages/action", "GET", "POST"},
		{"hops-create", "/hops/create", "GET", "POST"},
		{"hops-view", "/hops/view", "POST", "GET"},
		{"hops-privacy", "/hops/privacy", "GET", "POST"},
		{"hops-comments-create", "/hops/comments/create", "GET", "POST"},
		{"hops-images-upload", "/hops/images/upload", "GET", "POST"},
		{"hops-images-delete", "/hops/images/delete", "GET", "POST"},
		{"hops-image", "/hops/image", "POST", "GET"},
		{"hops-offer", "/hops/offer", "GET", "POST"},
		{"hops-cancel", "/hops/cancel", "GET", "POST"},
		{"hops-complete", "/hops/complete", "GET", "POST"},
		{"organizations", "/organizations", "POST", "GET"},
		{"organization", "/organization", "POST", "GET"},
		{"organization-slug", "/organization/test-slug", "POST", "GET"},
		{"organizations-logo", "/organizations/logo", "POST", "GET"},
		{"organizations-create", "/organizations/create", "PUT", "GET, POST"},
		{"organizations-manage", "/organizations/manage", "PUT", "GET, POST"},
		{"organizations-manage-request", "/organizations/manage/request", "GET", "POST"},
		{"organizations-manage-member-remove", "/organizations/manage/member/remove", "GET", "POST"},
		{"organizations-manage-member-role", "/organizations/manage/member/role", "GET", "POST"},
		{"organizations-request", "/organizations/request", "GET", "POST"},
		{"logout", "/logout", "PUT", "POST"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			resp := actor.Request(tc.method, tc.path, nil, nil)
			defer resp.Body.Close()
			if resp.StatusCode != 405 {
				t.Fatalf("expected 405 for %s %s, got %d", tc.method, tc.path, resp.StatusCode)
			}
			if got := resp.Header.Get("Allow"); got != tc.wantAllow {
				t.Fatalf("expected Allow=%q for %s %s, got %q", tc.wantAllow, tc.method, tc.path, got)
			}
		})
	}
}
