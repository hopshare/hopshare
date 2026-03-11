package http_test

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"hopshare/internal/service"
)

func TestOrganizationHTTPMatrix(t *testing.T) {
	db := requireHTTPTestDB(t)

	t.Run("ORG-01 GET /organizations is public", func(t *testing.T) {
		server := newHTTPServer(t, db)
		anon := newTestActor(t, "anon", server.URL, "", "")
		body := requireStatus(t, anon.Get("/organizations"), 200)
		requireBodyContains(t, body, "Organizations")
	})

	t.Run("ORG-02 GET /organization?org_id redirects permanently to /organization/{url_name}", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		owner := createSeededMember(t, ctx, db, "org_redirect_owner", suffix)
		org, err := service.CreateOrganization(ctx, db, "Org Redirect "+suffix, "City", "ST", "Desc", owner.Member.ID)
		if err != nil {
			t.Fatalf("create org: %v", err)
		}
		server := newHTTPServer(t, db)
		anon := newTestActor(t, "anon", server.URL, "", "")
		resp := anon.Get("/organization?org_id=" + strconv.FormatInt(org.ID, 10))
		defer resp.Body.Close()
		if resp.StatusCode != 301 {
			t.Fatalf("expected status 301, got %d", resp.StatusCode)
		}
		loc, err := resp.Location()
		if err != nil {
			t.Fatalf("location: %v", err)
		}
		if loc.Path != "/organization/"+org.URLName {
			t.Fatalf("unexpected redirect path: %q", loc.Path)
		}
	})

	t.Run("ORG-03 GET /organization invalid org_id returns 404", func(t *testing.T) {
		server := newHTTPServer(t, db)
		anon := newTestActor(t, "anon", server.URL, "", "")
		requireStatus(t, anon.Get("/organization?org_id=invalid"), 404)
	})

	t.Run("ORG-04 GET /organization/{slug} unknown returns 404", func(t *testing.T) {
		server := newHTTPServer(t, db)
		anon := newTestActor(t, "anon", server.URL, "", "")
		requireStatus(t, anon.Get("/organization/does-not-exist-"+uniqueTestSuffix()), 404)
	})

	t.Run("ORG-04A organization activity marks departed members with left tooltip", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner", "helper")
		hop := createAcceptedHopViaOffer(t, ctx, db, org.ID, members["owner"].Member.ID, members["helper"].Member.ID, "Left avatar marker "+suffix)
		if err := service.CompleteHop(ctx, db, service.CompleteHopParams{
			OrganizationID: org.ID,
			HopID:          hop.ID,
			CompletedBy:    members["owner"].Member.ID,
			Comment:        "done",
			CompletedHours: 1,
		}); err != nil {
			t.Fatalf("complete hop: %v", err)
		}
		if err := service.RemoveOrganizationMember(ctx, db, org.ID, members["helper"].Member.ID, members["owner"].Member.ID); err != nil {
			t.Fatalf("remove helper membership: %v", err)
		}

		server := newHTTPServer(t, db)
		anon := newTestActor(t, "anon", server.URL, "", "")
		body := requireStatus(t, anon.Get("/organization/"+org.URLName), 200)
		requireBodyContains(t, body, "Left organization")
	})

	t.Run("ORG-05 POST /organizations/request submits membership request", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner")
		requester := createSeededMember(t, ctx, db, "org_request_member", suffix)

		server := newHTTPServer(t, db)
		actor := newTestActor(t, "requester", server.URL, requester.Member.Email, requester.Password)
		actor.Login()
		loc := requireRedirectPath(t, actor.PostForm("/organizations/request", formKV(
			"org_id", strconv.FormatInt(org.ID, 10),
		)), "/organization/"+org.URLName)
		requireQueryValue(t, loc, "success", "Requested membership in "+org.Name+".")

		requests, err := service.PendingMembershipRequests(ctx, db, org.ID)
		if err != nil {
			t.Fatalf("pending requests: %v", err)
		}
		found := false
		for _, req := range requests {
			if req.MemberID == requester.Member.ID {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected pending request for member %d", requester.Member.ID)
		}
		ownerID := members["owner"].Member.ID
		ownerMessages, err := service.ListMessages(ctx, db, ownerID)
		if err != nil {
			t.Fatalf("owner messages: %v", err)
		}
		foundOwnerMessage := false
		for _, msg := range ownerMessages {
			if msg.Subject == "New membership request" && strings.Contains(msg.Body, org.Name) {
				foundOwnerMessage = true
				break
			}
		}
		if !foundOwnerMessage {
			t.Fatalf("expected owner membership request message for org %q", org.Name)
		}

		var ownerNotificationCount int
		if err := db.QueryRowContext(ctx, `
			SELECT COUNT(*)
			FROM member_notifications
			WHERE member_id = $1
				AND text LIKE $2
		`, ownerID, "%requested to join "+org.Name+"%").Scan(&ownerNotificationCount); err != nil {
			t.Fatalf("count owner notifications: %v", err)
		}
		if ownerNotificationCount == 0 {
			t.Fatalf("expected owner membership request notification for org %q", org.Name)
		}
	})

	t.Run("ORG-06 duplicate membership request is rejected", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, _ := createOrganizationWithMembers(t, ctx, db, suffix, "owner")
		requester := createSeededMember(t, ctx, db, "org_dup_member", suffix)

		server := newHTTPServer(t, db)
		actor := newTestActor(t, "requester", server.URL, requester.Member.Email, requester.Password)
		actor.Login()
		requireRedirectPath(t, actor.PostForm("/organizations/request", formKV(
			"org_id", strconv.FormatInt(org.ID, 10),
		)), "/organization/"+org.URLName)
		loc := requireRedirectPath(t, actor.PostForm("/organizations/request", formKV(
			"org_id", strconv.FormatInt(org.ID, 10),
		)), "/organization/"+org.URLName)
		requireQueryValue(t, loc, "error", "You already have a pending request for this organization.")
	})

	t.Run("ORG-07 invalid org id membership request redirects with error", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		member := createSeededMember(t, ctx, db, "org_invalid_request", uniqueTestSuffix())
		server := newHTTPServer(t, db)
		actor := newTestActor(t, "member", server.URL, member.Member.Email, member.Password)
		actor.Login()
		loc := requireRedirectPath(t, actor.PostForm("/organizations/request", formKV(
			"org_id", "invalid",
		)), "/organizations")
		requireQueryValue(t, loc, "error", "Invalid organization.")
	})

	t.Run("ORG-08 GET /organizations/create blocked for member who already created an organization", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		owner := createSeededMember(t, ctx, db, "org_create_block_owner", suffix)
		if _, err := service.CreateOrganization(ctx, db, "Already Owns "+suffix, "City", "ST", "Desc", owner.Member.ID); err != nil {
			t.Fatalf("create org: %v", err)
		}
		server := newHTTPServer(t, db)
		actor := newTestActor(t, "owner", server.URL, owner.Member.Email, owner.Password)
		actor.Login()
		loc := requireRedirectPath(t, actor.Get("/organizations/create"), "/organizations/manage")
		requireQueryValue(t, loc, "error", "You have already created an organization.")
	})

	t.Run("ORG-09 POST /organizations/create success", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		member := createSeededMember(t, ctx, db, "org_create_success", suffix)
		server := newHTTPServer(t, db)
		actor := newTestActor(t, "member", server.URL, member.Member.Email, member.Password)
		actor.Login()

		loc := requireRedirectPath(t, actor.PostMultipart("/organizations/create", map[string]string{
			"name":        "Created Org " + suffix,
			"city":        "Nashville",
			"state":       "TN",
			"description": "Created through integration test.",
		}), "/my-hopshare")
		requireQueryValue(t, loc, "invite_prompt", "1")
		requireBodyContains(t, loc.Query().Get("success"), "Created")

		org, err := service.PrimaryOwnedOrganization(ctx, db, member.Member.ID)
		if err != nil {
			t.Fatalf("primary owned organization: %v", err)
		}
		if org.ID == 0 {
			t.Fatalf("expected created organization")
		}
	})

	t.Run("ORG-10 POST /organizations/create missing fields returns inline validation", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		member := createSeededMember(t, ctx, db, "org_create_missing", uniqueTestSuffix())
		server := newHTTPServer(t, db)
		actor := newTestActor(t, "member", server.URL, member.Member.Email, member.Password)
		actor.Login()
		body := requireStatus(t, actor.PostMultipart("/organizations/create", map[string]string{
			"name":        "",
			"description": "",
		}), 200)
		requireBodyContains(t, body, "Organization name is required.")
	})

	t.Run("ORG-11 POST /organizations/create invalid logo rejected", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		member := createSeededMember(t, ctx, db, "org_create_bad_logo", uniqueTestSuffix())
		server := newHTTPServer(t, db)
		actor := newTestActor(t, "member", server.URL, member.Member.Email, member.Password)
		actor.Login()
		body := requireStatus(t, actor.PostMultipartWithFiles("/organizations/create", map[string]string{
			"name":        "Logo Bad Org " + uniqueTestSuffix(),
			"description": "desc",
		}, []multipartFile{{
			FieldName:   "logo_file",
			FileName:    "logo.txt",
			ContentType: "text/plain",
			Data:        []byte("bad logo"),
		}}), 200)
		requireBodyContains(t, body, "logo must be a PNG or JPEG")
	})

	t.Run("ORG-12 GET /organizations/logo without uploaded logo redirects to default", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		owner := createSeededMember(t, ctx, db, "org_logo_default_owner", uniqueTestSuffix())
		org, err := service.CreateOrganization(ctx, db, "Logo Default Org "+uniqueTestSuffix(), "City", "ST", "Desc", owner.Member.ID)
		if err != nil {
			t.Fatalf("create org: %v", err)
		}
		server := newHTTPServer(t, db)
		anon := newTestActor(t, "anon", server.URL, "", "")
		resp := anon.Get("/organizations/logo?org_id=" + strconv.FormatInt(org.ID, 10))
		defer resp.Body.Close()
		if resp.StatusCode != 302 {
			t.Fatalf("expected status 302, got %d", resp.StatusCode)
		}
		loc, err := resp.Location()
		if err != nil {
			t.Fatalf("location: %v", err)
		}
		if loc.Path != "/static/assets/image/logo_blue_transparent.png" {
			t.Fatalf("unexpected logo fallback path: %q", loc.Path)
		}
	})

	t.Run("ORG-13 GET /organizations/logo with uploaded logo returns image", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		owner := createSeededMember(t, ctx, db, "org_logo_uploaded_owner", uniqueTestSuffix())
		org, err := service.CreateOrganization(ctx, db, "Logo Uploaded Org "+uniqueTestSuffix(), "City", "ST", "Desc", owner.Member.ID)
		if err != nil {
			t.Fatalf("create org: %v", err)
		}
		if err := service.SetOrganizationLogo(ctx, db, org.ID, "image/png", tinyPNGData()); err != nil {
			t.Fatalf("set logo: %v", err)
		}
		server := newHTTPServer(t, db)
		anon := newTestActor(t, "anon", server.URL, "", "")
		body := requireStatus(t, anon.Get("/organizations/logo?org_id="+strconv.FormatInt(org.ID, 10)), 200)
		if body == "" {
			t.Fatalf("expected image body")
		}
	})

	t.Run("ORG-14 GET /organizations/manage owner succeeds", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		owner := createSeededMember(t, ctx, db, "org_manage_owner", uniqueTestSuffix())
		org, err := service.CreateOrganization(ctx, db, "Manage Org "+uniqueTestSuffix(), "City", "ST", "Desc", owner.Member.ID)
		if err != nil {
			t.Fatalf("create org: %v", err)
		}
		server := newHTTPServer(t, db)
		actor := newTestActor(t, "owner", server.URL, owner.Member.Email, owner.Password)
		actor.Login()
		body := requireStatus(t, actor.Get("/organizations/manage?org_id="+strconv.FormatInt(org.ID, 10)), 200)
		requireBodyContains(t, body, "Manage your organization")
	})

	t.Run("ORG-15 GET /organizations/manage non-owner forbidden", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner", "member")
		server := newHTTPServer(t, db)
		actor := newTestActor(t, "member", server.URL, members["member"].Member.Email, members["member"].Password)
		actor.Login()
		requireStatus(t, actor.Get("/organizations/manage?org_id="+strconv.FormatInt(org.ID, 10)), 403)
	})

	t.Run("ORG-16 GET /organizations/manage invalid org_id returns 400", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		owner := createSeededMember(t, ctx, db, "org_manage_invalid_owner", uniqueTestSuffix())
		if _, err := service.CreateOrganization(ctx, db, "Invalid OrgID "+uniqueTestSuffix(), "City", "ST", "Desc", owner.Member.ID); err != nil {
			t.Fatalf("create org: %v", err)
		}
		server := newHTTPServer(t, db)
		actor := newTestActor(t, "owner", server.URL, owner.Member.Email, owner.Password)
		actor.Login()
		requireStatus(t, actor.Get("/organizations/manage?org_id=abc"), 400)
	})

	t.Run("ORG-17 POST /organizations/manage action=details success", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		owner := createSeededMember(t, ctx, db, "org_manage_details_owner", uniqueTestSuffix())
		org, err := service.CreateOrganization(ctx, db, "Details Org "+uniqueTestSuffix(), "City", "ST", "Desc", owner.Member.ID)
		if err != nil {
			t.Fatalf("create org: %v", err)
		}
		updatedName := "Updated Name " + uniqueTestSuffix()
		server := newHTTPServer(t, db)
		actor := newTestActor(t, "owner", server.URL, owner.Member.Email, owner.Password)
		actor.Login()
		loc := requireRedirectPath(t, actor.PostMultipart("/organizations/manage", map[string]string{
			"action":      "details",
			"org_id":      strconv.FormatInt(org.ID, 10),
			"name":        updatedName,
			"city":        "Seattle",
			"state":       "WA",
			"description": "Updated description",
		}), "/organizations/manage")
		requireQueryValue(t, loc, "success", "Organization updated.")

		updated, err := service.GetOrganizationByID(ctx, db, org.ID)
		if err != nil {
			t.Fatalf("get org: %v", err)
		}
		if updated.Name != updatedName {
			t.Fatalf("expected updated name, got %q", updated.Name)
		}
	})

	t.Run("ORG-18 POST /organizations/manage action=skills success", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		owner := createSeededMember(t, ctx, db, "org_manage_skills_owner", uniqueTestSuffix())
		org, err := service.CreateOrganization(ctx, db, "Skills Org "+uniqueTestSuffix(), "City", "ST", "Desc", owner.Member.ID)
		if err != nil {
			t.Fatalf("create org: %v", err)
		}
		server := newHTTPServer(t, db)
		actor := newTestActor(t, "owner", server.URL, owner.Member.Email, owner.Password)
		actor.Login()
		loc := requireRedirectPath(t, actor.PostForm("/organizations/manage", formKV(
			"action", "skills",
			"tab", "skills",
			"org_id", strconv.FormatInt(org.ID, 10),
			"skills_text", "Carpentry\nErrands\n",
		)), "/organizations/manage")
		requireQueryValue(t, loc, "tab", "skills")
		requireQueryValue(t, loc, "success", "Organization skills updated.")

		skills, err := service.ListOrganizationSkills(ctx, db, org.ID)
		if err != nil {
			t.Fatalf("list org skills: %v", err)
		}
		if len(skills) != 2 {
			t.Fatalf("expected 2 skills, got %d", len(skills))
		}
	})

	t.Run("ORG-18A POST /organizations/manage action=timebank success", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		owner := createSeededMember(t, ctx, db, "org_manage_timebank_owner", uniqueTestSuffix())
		org, err := service.CreateOrganization(ctx, db, "Timebank Org "+uniqueTestSuffix(), "City", "ST", "Desc", owner.Member.ID)
		if err != nil {
			t.Fatalf("create org: %v", err)
		}
		server := newHTTPServer(t, db)
		actor := newTestActor(t, "owner", server.URL, owner.Member.Email, owner.Password)
		actor.Login()
		loc := requireRedirectPath(t, actor.PostForm("/organizations/manage", formKV(
			"action", "timebank",
			"tab", "timebank",
			"org_id", strconv.FormatInt(org.ID, 10),
			"timebank_min_balance", "-4",
			"timebank_max_balance", "12",
			"timebank_starting_balance", "6",
		)), "/organizations/manage")
		requireQueryValue(t, loc, "tab", "timebank")
		requireQueryValue(t, loc, "success", "Time bank settings updated.")

		updated, err := service.GetOrganizationByID(ctx, db, org.ID)
		if err != nil {
			t.Fatalf("get org: %v", err)
		}
		if updated.TimebankMinBalance != -4 || updated.TimebankMaxBalance != 12 || updated.TimebankStartingBalance != 6 {
			t.Fatalf("unexpected timebank policy: min=%d max=%d start=%d", updated.TimebankMinBalance, updated.TimebankMaxBalance, updated.TimebankStartingBalance)
		}
	})

	t.Run("ORG-18B POST /organizations/manage action=timebank invalid minimum shows error", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		owner := createSeededMember(t, ctx, db, "org_manage_timebank_invalid_owner", uniqueTestSuffix())
		org, err := service.CreateOrganization(ctx, db, "Timebank Invalid Org "+uniqueTestSuffix(), "City", "ST", "Desc", owner.Member.ID)
		if err != nil {
			t.Fatalf("create org: %v", err)
		}
		server := newHTTPServer(t, db)
		actor := newTestActor(t, "owner", server.URL, owner.Member.Email, owner.Password)
		actor.Login()

		body := requireStatus(t, actor.PostForm("/organizations/manage", formKV(
			"action", "timebank",
			"org_id", strconv.FormatInt(org.ID, 10),
			"timebank_min_balance", "-12",
			"timebank_max_balance", "12",
			"timebank_starting_balance", "6",
		)), 200)
		requireBodyContains(t, body, "Minimum balance must be below zero and greater than -10.")
		requireBodyContains(t, body, "x-data=\"{ activeTab: &#39;timebank&#39; }\"")
	})

	t.Run("ORG-19 POST /organizations/manage unknown action returns 400", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		owner := createSeededMember(t, ctx, db, "org_manage_unknown_owner", uniqueTestSuffix())
		org, err := service.CreateOrganization(ctx, db, "Unknown Action Org "+uniqueTestSuffix(), "City", "ST", "Desc", owner.Member.ID)
		if err != nil {
			t.Fatalf("create org: %v", err)
		}
		server := newHTTPServer(t, db)
		actor := newTestActor(t, "owner", server.URL, owner.Member.Email, owner.Password)
		actor.Login()
		requireStatus(t, actor.PostForm("/organizations/manage", formKV(
			"action", "unknown",
			"org_id", strconv.FormatInt(org.ID, 10),
		)), 400)
	})

	t.Run("ORG-20 POST /organizations/manage/request accept approves membership", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner")
		requester := createSeededMember(t, ctx, db, "request_accept_member", suffix)
		if err := service.RequestMembership(ctx, db, requester.Member.ID, org.ID, nil); err != nil {
			t.Fatalf("request membership: %v", err)
		}
		reqID := requirePendingRequestID(t, ctx, db, org.ID, requester.Member.ID)

		server := newHTTPServer(t, db)
		ownerActor := newTestActor(t, "owner", server.URL, members["owner"].Member.Email, members["owner"].Password)
		ownerActor.Login()
		loc := requireRedirectPath(t, ownerActor.PostForm("/organizations/manage/request", formKV(
			"request_id", strconv.FormatInt(reqID, 10),
			"action", "accept",
		)), "/organizations/manage")
		requireQueryValue(t, loc, "success", "Membership approved.")

		hasMembership, err := service.MemberHasActiveMembership(ctx, db, requester.Member.ID, org.ID)
		if err != nil {
			t.Fatalf("check membership: %v", err)
		}
		if !hasMembership {
			t.Fatalf("expected approved membership")
		}
	})

	t.Run("ORG-21 POST /organizations/manage/request deny rejects membership", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner")
		requester := createSeededMember(t, ctx, db, "request_deny_member", suffix)
		if err := service.RequestMembership(ctx, db, requester.Member.ID, org.ID, nil); err != nil {
			t.Fatalf("request membership: %v", err)
		}
		reqID := requirePendingRequestID(t, ctx, db, org.ID, requester.Member.ID)

		server := newHTTPServer(t, db)
		ownerActor := newTestActor(t, "owner", server.URL, members["owner"].Member.Email, members["owner"].Password)
		ownerActor.Login()
		loc := requireRedirectPath(t, ownerActor.PostForm("/organizations/manage/request", formKV(
			"request_id", strconv.FormatInt(reqID, 10),
			"action", "deny",
		)), "/organizations/manage")
		requireQueryValue(t, loc, "success", "Membership denied.")

		requesterMessages, err := service.ListMessages(ctx, db, requester.Member.ID)
		if err != nil {
			t.Fatalf("requester messages: %v", err)
		}
		foundRequesterMessage := false
		for _, msg := range requesterMessages {
			if msg.Subject == "Membership request denied" && strings.Contains(msg.Body, org.Name) {
				foundRequesterMessage = true
				break
			}
		}
		if !foundRequesterMessage {
			t.Fatalf("expected requester denial message for org %q", org.Name)
		}

		var requesterNotificationCount int
		if err := db.QueryRowContext(ctx, `
			SELECT COUNT(*)
			FROM member_notifications
			WHERE member_id = $1
				AND text LIKE $2
		`, requester.Member.ID, "%request to join "+org.Name+" was denied%").Scan(&requesterNotificationCount); err != nil {
			t.Fatalf("count requester notifications: %v", err)
		}
		if requesterNotificationCount == 0 {
			t.Fatalf("expected requester denial notification for org %q", org.Name)
		}
	})

	t.Run("ORG-22 POST /organizations/manage/request non-owner is blocked", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner", "member")
		requester := createSeededMember(t, ctx, db, "request_non_owner_member", suffix)
		if err := service.RequestMembership(ctx, db, requester.Member.ID, org.ID, nil); err != nil {
			t.Fatalf("request membership: %v", err)
		}
		reqID := requirePendingRequestID(t, ctx, db, org.ID, requester.Member.ID)

		server := newHTTPServer(t, db)
		memberActor := newTestActor(t, "member", server.URL, members["member"].Member.Email, members["member"].Password)
		memberActor.Login()
		loc := requireRedirectPath(t, memberActor.PostForm("/organizations/manage/request", formKV(
			"request_id", strconv.FormatInt(reqID, 10),
			"action", "accept",
		)), "/organizations/manage")
		requireQueryValue(t, loc, "error", "You can only manage your organization's requests.")
	})

	t.Run("ORG-23 POST /organizations/manage action=delete_organization requires exact confirmation phrase", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner", "member")

		server := newHTTPServer(t, db)
		ownerActor := newTestActor(t, "owner", server.URL, members["owner"].Member.Email, members["owner"].Password)
		ownerActor.Login()

		body := requireStatus(t, ownerActor.PostForm("/organizations/manage", formKV(
			"action", "delete_organization",
			"org_id", strconv.FormatInt(org.ID, 10),
			"delete_organization_confirmation", "I want to remove something else",
		)), http.StatusOK)
		requireBodyContains(t, body, "Please type")
		requireBodyContains(t, body, "exactly to confirm organization deletion.")

		if _, err := service.GetEnabledOrganizationByID(ctx, db, org.ID); err != nil {
			t.Fatalf("expected organization to remain after failed delete confirmation: %v", err)
		}
	})

	t.Run("ORG-24 POST /organizations/manage action=delete_organization removes organization and notifies members", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner", "member")

		server := newHTTPServer(t, db)
		ownerActor := newTestActor(t, "owner", server.URL, members["owner"].Member.Email, members["owner"].Password)
		ownerActor.Login()

		loc := requireRedirectPath(t, ownerActor.PostForm("/organizations/manage", formKV(
			"action", "delete_organization",
			"org_id", strconv.FormatInt(org.ID, 10),
			"delete_organization_confirmation", "I want to remove "+org.Name,
		)), "/my-hopshare")
		requireQueryValue(t, loc, "success", "Organization permanently deleted.")

		if _, err := service.GetEnabledOrganizationByID(ctx, db, org.ID); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("expected deleted organization to be unavailable, got err=%v", err)
		}

		actorName := strings.TrimSpace(members["owner"].Member.FirstName + " " + members["owner"].Member.LastName)
		if actorName == "" {
			actorName = members["owner"].Member.Email
		}
		wantSubject := "Organization permanently removed"
		wantBody := "User " + actorName + " has permanently removed the Organization " + org.Name + "."

		for _, key := range []string{"owner", "member"} {
			msgs, err := service.ListMessages(ctx, db, members[key].Member.ID)
			if err != nil {
				t.Fatalf("list messages for %s: %v", key, err)
			}
			found := false
			for _, msg := range msgs {
				if msg.Subject == wantSubject && msg.Body == wantBody {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("expected organization delete message for %s with body %q", key, wantBody)
			}
		}
	})
}

func TestOrganizationMemberRoleHTTPMatrix(t *testing.T) {
	db := requireHTTPTestDB(t)

	t.Run("ORG-M-01 remove member success", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner", "member")
		server := newHTTPServer(t, db)
		ownerActor := newTestActor(t, "owner", server.URL, members["owner"].Member.Email, members["owner"].Password)
		ownerActor.Login()

		loc := requireRedirectPath(t, ownerActor.PostForm("/organizations/manage/member/remove", formKV(
			"org_id", strconv.FormatInt(org.ID, 10),
			"member_id", strconv.FormatInt(members["member"].Member.ID, 10),
		)), "/organizations/manage")
		requireQueryValue(t, loc, "success", "Member removed.")

		hasMembership, err := service.MemberHasActiveMembership(ctx, db, members["member"].Member.ID, org.ID)
		if err != nil {
			t.Fatalf("check membership: %v", err)
		}
		if hasMembership {
			t.Fatalf("expected member to be removed")
		}
	})

	t.Run("ORG-M-02 remove member invalid id returns error", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner")
		server := newHTTPServer(t, db)
		ownerActor := newTestActor(t, "owner", server.URL, members["owner"].Member.Email, members["owner"].Password)
		ownerActor.Login()

		loc := requireRedirectPath(t, ownerActor.PostForm("/organizations/manage/member/remove", formKV(
			"org_id", strconv.FormatInt(org.ID, 10),
			"member_id", "invalid",
		)), "/organizations/manage")
		requireQueryValue(t, loc, "error", "Invalid member.")
	})

	t.Run("ORG-M-03 remove non-existent membership returns error", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner")
		other := createSeededMember(t, ctx, db, "non_member", suffix)
		server := newHTTPServer(t, db)
		ownerActor := newTestActor(t, "owner", server.URL, members["owner"].Member.Email, members["owner"].Password)
		ownerActor.Login()
		loc := requireRedirectPath(t, ownerActor.PostForm("/organizations/manage/member/remove", formKV(
			"org_id", strconv.FormatInt(org.ID, 10),
			"member_id", strconv.FormatInt(other.Member.ID, 10),
		)), "/organizations/manage")
		requireQueryValue(t, loc, "error", "Membership not found.")
	})

	t.Run("ORG-M-04 remove member non-owner blocked", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner", "member", "other")
		server := newHTTPServer(t, db)
		memberActor := newTestActor(t, "member", server.URL, members["member"].Member.Email, members["member"].Password)
		memberActor.Login()
		requireStatus(t, memberActor.PostForm("/organizations/manage/member/remove", formKV(
			"org_id", strconv.FormatInt(org.ID, 10),
			"member_id", strconv.FormatInt(members["other"].Member.ID, 10),
		)), 403)
	})

	t.Run("ORG-M-05 make owner success", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner", "member")
		server := newHTTPServer(t, db)
		ownerActor := newTestActor(t, "owner", server.URL, members["owner"].Member.Email, members["owner"].Password)
		ownerActor.Login()
		loc := requireRedirectPath(t, ownerActor.PostForm("/organizations/manage/member/role", formKV(
			"org_id", strconv.FormatInt(org.ID, 10),
			"member_id", strconv.FormatInt(members["member"].Member.ID, 10),
			"action", "make_owner",
		)), "/organizations/manage")
		requireQueryValue(t, loc, "success", "Member promoted to owner.")
	})

	t.Run("ORG-M-06 revoke owner success", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner", "member")
		if err := service.UpdateOrganizationMemberRole(ctx, db, org.ID, members["member"].Member.ID, true); err != nil {
			t.Fatalf("promote setup: %v", err)
		}
		server := newHTTPServer(t, db)
		ownerActor := newTestActor(t, "owner", server.URL, members["owner"].Member.Email, members["owner"].Password)
		ownerActor.Login()
		loc := requireRedirectPath(t, ownerActor.PostForm("/organizations/manage/member/role", formKV(
			"org_id", strconv.FormatInt(org.ID, 10),
			"member_id", strconv.FormatInt(members["member"].Member.ID, 10),
			"action", "revoke_owner",
		)), "/organizations/manage")
		requireQueryValue(t, loc, "success", "Owner role revoked.")
	})

	t.Run("ORG-M-07 member role unknown action returns error", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner", "member")
		server := newHTTPServer(t, db)
		ownerActor := newTestActor(t, "owner", server.URL, members["owner"].Member.Email, members["owner"].Password)
		ownerActor.Login()
		loc := requireRedirectPath(t, ownerActor.PostForm("/organizations/manage/member/role", formKV(
			"org_id", strconv.FormatInt(org.ID, 10),
			"member_id", strconv.FormatInt(members["member"].Member.ID, 10),
			"action", "bogus",
		)), "/organizations/manage")
		requireQueryValue(t, loc, "error", "Unknown action.")
	})

	t.Run("ORG-M-08 role change invalid member id returns error", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner")
		server := newHTTPServer(t, db)
		ownerActor := newTestActor(t, "owner", server.URL, members["owner"].Member.Email, members["owner"].Password)
		ownerActor.Login()
		loc := requireRedirectPath(t, ownerActor.PostForm("/organizations/manage/member/role", formKV(
			"org_id", strconv.FormatInt(org.ID, 10),
			"member_id", "invalid",
			"action", "make_owner",
		)), "/organizations/manage")
		requireQueryValue(t, loc, "error", "Invalid member.")
	})

	t.Run("ORG-M-09 role change non-owner blocked", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner", "member", "other")
		server := newHTTPServer(t, db)
		memberActor := newTestActor(t, "member", server.URL, members["member"].Member.Email, members["member"].Password)
		memberActor.Login()
		requireStatus(t, memberActor.PostForm("/organizations/manage/member/role", formKV(
			"org_id", strconv.FormatInt(org.ID, 10),
			"member_id", strconv.FormatInt(members["other"].Member.ID, 10),
			"action", "make_owner",
		)), 403)
	})

	t.Run("ORG-M-10 removed member loses hop access immediately", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner", "member")
		hop, err := service.CreateHop(ctx, db, service.CreateHopParams{
			OrganizationID: org.ID,
			MemberID:       members["owner"].Member.ID,
			Title:          "Access Loss Hop " + suffix,
			Details:        "Testing access removal.",
			EstimatedHours: 1,
			NeededByKind:   "anytime",
			IsPrivate:      false,
		})
		if err != nil {
			t.Fatalf("create hop: %v", err)
		}

		server := newHTTPServer(t, db)
		ownerActor := newTestActor(t, "owner", server.URL, members["owner"].Member.Email, members["owner"].Password)
		memberActor := newTestActor(t, "member", server.URL, members["member"].Member.Email, members["member"].Password)
		ownerActor.Login()
		memberActor.Login()

		requireRedirectPath(t, ownerActor.PostForm("/organizations/manage/member/remove", formKV(
			"org_id", strconv.FormatInt(org.ID, 10),
			"member_id", strconv.FormatInt(members["member"].Member.ID, 10),
		)), "/organizations/manage")

		requireStatus(t, memberActor.Get("/hops/view?org_id="+strconv.FormatInt(org.ID, 10)+"&hop_id="+strconv.FormatInt(hop.ID, 10)), 403)
	})

	t.Run("ORG-M-11 removing the last owner is forbidden", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner", "member")

		server := newHTTPServer(t, db)
		ownerActor := newTestActor(t, "owner", server.URL, members["owner"].Member.Email, members["owner"].Password)
		ownerActor.Login()

		loc := requireRedirectPath(t, ownerActor.PostForm("/organizations/manage/member/remove", formKV(
			"org_id", strconv.FormatInt(org.ID, 10),
			"member_id", strconv.FormatInt(members["owner"].Member.ID, 10),
		)), "/organizations/manage")
		requireQueryValue(t, loc, "error", "Cannot remove the last owner from an organization.")

		hasMembership, err := service.MemberHasActiveMembership(ctx, db, members["owner"].Member.ID, org.ID)
		if err != nil {
			t.Fatalf("check owner membership: %v", err)
		}
		if !hasMembership {
			t.Fatalf("expected owner membership to remain active")
		}
	})

	t.Run("ORG-M-12 remove button is not shown for self", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner", "member")
		if err := service.UpdateOrganizationMemberRole(ctx, db, org.ID, members["member"].Member.ID, true); err != nil {
			t.Fatalf("promote setup: %v", err)
		}

		server := newHTTPServer(t, db)
		secondaryOwnerActor := newTestActor(t, "member", server.URL, members["member"].Member.Email, members["member"].Password)
		secondaryOwnerActor.Login()

		body := requireStatus(t, secondaryOwnerActor.Get("/organizations/manage?org_id="+strconv.FormatInt(org.ID, 10)), 200)
		requireBodyNotContains(t, body, "name=\"member_id\" value=\""+strconv.FormatInt(members["member"].Member.ID, 10)+"\"")
	})

	t.Run("ORG-I-01 owner invite blast sends valid emails and skips existing members", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner", "member")
		disabled := createSeededMember(t, ctx, db, "disabled_invite", suffix)
		if err := service.SetMemberEnabled(ctx, db, disabled.Member.ID, false); err != nil {
			t.Fatalf("disable invitee member: %v", err)
		}
		sender := &recordingPasswordResetEmailSender{}
		server := newHTTPServerWithPasswordResetEmailSender(t, db, sender)
		ownerActor := newTestActor(t, "owner", server.URL, members["owner"].Member.Email, members["owner"].Password)
		ownerActor.Login()

		loc := requireRedirectPath(t, ownerActor.PostForm("/organizations/manage", formKV(
			"action", "invites",
			"org_id", strconv.FormatInt(org.ID, 10),
			"invite_emails", "not-an-email, "+members["member"].Member.Email+", "+disabled.Member.Email+", invite_"+suffix+"@example.com",
		)), "/organizations/manage")
		requireQueryValue(t, loc, "tab", "invite")

		if len(sender.inviteEmails) != 1 {
			t.Fatalf("expected one invite email, got %d", len(sender.inviteEmails))
		}
		if !strings.EqualFold(sender.inviteEmails[0].ToEmail, "invite_"+suffix+"@example.com") {
			t.Fatalf("invite email recipient mismatch: got=%q", sender.inviteEmails[0].ToEmail)
		}

		invitations, err := service.ListOrganizationInvitations(ctx, db, org.ID, 50)
		if err != nil {
			t.Fatalf("list organization invitations: %v", err)
		}
		if len(invitations) == 0 {
			t.Fatalf("expected at least one invitation row")
		}
		if invitations[0].SentAt == nil {
			t.Fatalf("expected sent_at to be populated for latest invite")
		}
	})

	t.Run("ORG-I-01A owner invite blast can redirect to my-hopshare", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner")
		sender := &recordingPasswordResetEmailSender{}
		server := newHTTPServerWithPasswordResetEmailSender(t, db, sender)
		ownerActor := newTestActor(t, "owner", server.URL, members["owner"].Member.Email, members["owner"].Password)
		ownerActor.Login()

		loc := requireRedirectPath(t, ownerActor.PostForm("/organizations/manage", formKV(
			"action", "invites",
			"org_id", strconv.FormatInt(org.ID, 10),
			"invite_emails", "invite_redirect_"+suffix+"@example.com",
			"post_invite_redirect", "my-hopshare",
		)), "/my-hopshare")
		requireQueryValue(t, loc, "org_id", strconv.FormatInt(org.ID, 10))
		requireBodyContains(t, loc.Query().Get("success"), "Invite blast complete")

		if len(sender.inviteEmails) != 1 {
			t.Fatalf("expected one invite email, got %d", len(sender.inviteEmails))
		}
	})

	t.Run("ORG-I-02 logged-in invited member can accept invitation link", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner")
		invitee := createSeededMember(t, ctx, db, "invitee", suffix)
		sender := &recordingPasswordResetEmailSender{}
		server := newHTTPServerWithPasswordResetEmailSender(t, db, sender)

		ownerActor := newTestActor(t, "owner", server.URL, members["owner"].Member.Email, members["owner"].Password)
		ownerActor.Login()
		requireRedirectPath(t, ownerActor.PostForm("/organizations/manage", formKV(
			"action", "invites",
			"org_id", strconv.FormatInt(org.ID, 10),
			"invite_emails", invitee.Member.Email,
		)), "/organizations/manage")

		if len(sender.inviteEmails) != 1 {
			t.Fatalf("expected one invite email, got %d", len(sender.inviteEmails))
		}
		token := extractInviteTokenFromURL(t, sender.inviteEmails[0].InviteURL)
		if token == "" {
			t.Fatalf("expected invite token")
		}

		inviteeActor := newTestActor(t, "invitee", server.URL, invitee.Member.Email, invitee.Password)
		inviteeActor.Login()
		loc := requireRedirectPath(t, inviteeActor.Get("/invite?token="+url.QueryEscape(token)), "/organization/"+org.URLName)
		requireQueryValue(t, loc, "success", "Invitation accepted.")

		hasMembership, err := service.MemberHasActiveMembership(ctx, db, invitee.Member.ID, org.ID)
		if err != nil {
			t.Fatalf("check invitee membership: %v", err)
		}
		if !hasMembership {
			t.Fatalf("expected invitee to have active membership after accepting invite")
		}
	})

	t.Run("ORG-I-03 invite tab hidden when feature email is disabled", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner")
		sender := &recordingPasswordResetEmailSender{}
		server := newHTTPServerWithFeatureEmailAndPasswordResetEmailSender(t, db, false, sender)
		ownerActor := newTestActor(t, "owner", server.URL, members["owner"].Member.Email, members["owner"].Password)
		ownerActor.Login()

		body := requireStatus(t, ownerActor.Get("/organizations/manage?org_id="+strconv.FormatInt(org.ID, 10)), 200)
		requireBodyNotContains(t, body, "Send invite blast")
	})
}

func extractInviteTokenFromURL(t *testing.T, inviteURL string) string {
	t.Helper()
	parsed, err := url.Parse(strings.TrimSpace(inviteURL))
	if err != nil {
		t.Fatalf("parse invite url %q: %v", inviteURL, err)
	}
	token := strings.TrimSpace(parsed.Query().Get("token"))
	if token == "" {
		t.Fatalf("could not find invite token in url %q", inviteURL)
	}
	return token
}

func requirePendingRequestID(t *testing.T, ctx context.Context, db *sql.DB, orgID, memberID int64) int64 {
	t.Helper()
	requests, err := service.PendingMembershipRequests(ctx, db, orgID)
	if err != nil {
		t.Fatalf("pending requests: %v", err)
	}
	for _, req := range requests {
		if req.MemberID == memberID {
			return req.ID
		}
	}
	t.Fatalf("could not find pending request for member=%d org=%d", memberID, orgID)
	return 0
}
