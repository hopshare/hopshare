package http_test

import (
	"net/http"
	"net/url"
	"strconv"
	"testing"

	"hopshare/internal/service"
)

func TestProfileHTTPMatrix(t *testing.T) {
	db := requireHTTPTestDB(t)

	t.Run("PROF-01 GET /profile authenticated renders page", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		member := createSeededMember(t, ctx, db, "profile_get", uniqueTestSuffix())
		server := newHTTPServer(t, db)
		actor := newTestActor(t, "member", server.URL, member.Member.Email, member.Password)
		actor.Login()
		body := requireStatus(t, actor.Get("/profile"), 200)
		requireBodyContains(t, body, "My Profile")
		requireBodyContains(t, body, "Maximum avatar file size: 2 MB.")
	})

	t.Run("PROF-01A GET /profile renders My Account danger zone controls", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		member := createSeededMember(t, ctx, db, "profile_get_account_tab", uniqueTestSuffix())
		server := newHTTPServer(t, db)
		actor := newTestActor(t, "member", server.URL, member.Member.Email, member.Password)
		actor.Login()

		body := requireStatus(t, actor.Get("/profile"), 200)
		requireBodyContains(t, body, "My Account")
		requireBodyContains(t, body, "Danger Zone!")
		requireBodyContains(t, body, "Delete my Account")
		requireBodyContains(t, body, "I want to leave hopShare")
	})

	t.Run("PROF-02 POST /profile action=profile updates member details", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		member := createSeededMember(t, ctx, db, "profile_update", uniqueTestSuffix())
		server := newHTTPServer(t, db)
		actor := newTestActor(t, "member", server.URL, member.Member.Email, member.Password)
		actor.Login()

		loc := requireRedirectPath(t, actor.PostMultipart("/profile", map[string]string{
			"action":            "profile",
			"first_name":        "UpdatedFirst",
			"last_name":         "UpdatedLast",
			"email":             member.Member.Email,
			"preferred_contact": member.Member.Email,
			"city":              "Portland",
			"state":             "OR",
		}), "/profile")
		requireQueryValue(t, loc, "success", "Profile updated.")

		updated, err := service.GetMemberByID(ctx, db, member.Member.ID)
		if err != nil {
			t.Fatalf("load updated member: %v", err)
		}
		if updated.FirstName != "UpdatedFirst" || updated.LastName != "UpdatedLast" {
			t.Fatalf("profile names not updated: got %q %q", updated.FirstName, updated.LastName)
		}
	})

	t.Run("PROF-03 POST /profile action=profile missing preferred contact is rejected", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		member := createSeededMember(t, ctx, db, "profile_invalid_contact", uniqueTestSuffix())
		server := newHTTPServer(t, db)
		actor := newTestActor(t, "member", server.URL, member.Member.Email, member.Password)
		actor.Login()

		loc := requireRedirectPath(t, actor.PostMultipart("/profile", map[string]string{
			"action":            "profile",
			"first_name":        "F",
			"last_name":         "L",
			"email":             member.Member.Email,
			"preferred_contact": "",
		}), "/profile")
		requireQueryValue(t, loc, "error", "Name, email, and preferred contact are required.")
	})

	t.Run("PROF-04 POST /profile action=profile missing fields is rejected", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		member := createSeededMember(t, ctx, db, "profile_missing_fields", uniqueTestSuffix())
		server := newHTTPServer(t, db)
		actor := newTestActor(t, "member", server.URL, member.Member.Email, member.Password)
		actor.Login()

		loc := requireRedirectPath(t, actor.PostMultipart("/profile", map[string]string{
			"action":            "profile",
			"first_name":        "",
			"last_name":         "",
			"email":             member.Member.Email,
			"preferred_contact": member.Member.Email,
		}), "/profile")
		requireQueryValue(t, loc, "error", "Name, email, and preferred contact are required.")
	})

	t.Run("PROF-04A POST /profile action=profile duplicate email is rejected with explicit message", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		member := createSeededMember(t, ctx, db, "profile_duplicate_email_member", suffix)
		other := createSeededMember(t, ctx, db, "profile_duplicate_email_other", suffix)
		server := newHTTPServer(t, db)
		actor := newTestActor(t, "member", server.URL, member.Member.Email, member.Password)
		actor.Login()

		loc := requireRedirectPath(t, actor.PostMultipart("/profile", map[string]string{
			"action":            "profile",
			"first_name":        "Duplicate",
			"last_name":         "Email",
			"email":             other.Member.Email,
			"preferred_contact": member.Member.Email,
		}), "/profile")
		requireQueryValue(t, loc, "error", "The email provided is already being used. Please choose another one.")
	})

	t.Run("PROF-05 POST /profile action=password success updates password", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		member := createSeededMember(t, ctx, db, "profile_password_success", uniqueTestSuffix())
		server := newHTTPServer(t, db)
		actor := newTestActor(t, "member", server.URL, member.Member.Email, member.Password)
		actor.Login()
		oldSessionToken := actor.cookieValue("hopshare_session")
		if oldSessionToken == "" {
			t.Fatalf("expected existing session token before password update")
		}

		loc := requireRedirectPath(t, actor.PostForm("/profile", formKV(
			"action", "password",
			"current_password", member.Password,
			"new_password", "UpdatedPassword123!",
			"confirm_password", "UpdatedPassword123!",
		)), "/profile")
		requireQueryValue(t, loc, "success", "Password updated.")
		newSessionToken := actor.cookieValue("hopshare_session")
		if newSessionToken == "" {
			t.Fatalf("expected rotated session token after password update")
		}
		if newSessionToken == oldSessionToken {
			t.Fatalf("expected rotated session token to differ from previous token")
		}

		newActor := newTestActor(t, "member_new_pass", server.URL, member.Member.Email, "UpdatedPassword123!")
		newActor.Login()

		oldSessionActor := newTestActor(t, "member_old_session", server.URL, "", "")
		baseURL, err := url.Parse(server.URL)
		if err != nil {
			t.Fatalf("parse server url: %v", err)
		}
		oldSessionActor.client.Jar.SetCookies(baseURL, []*http.Cookie{{
			Name:  "hopshare_session",
			Value: oldSessionToken,
			Path:  "/",
		}})
		requireRedirectPath(t, oldSessionActor.Get("/my-hopshare"), "/login")
	})

	t.Run("PROF-06 POST /profile action=password wrong current password is rejected", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		member := createSeededMember(t, ctx, db, "profile_password_wrong_current", uniqueTestSuffix())
		server := newHTTPServer(t, db)
		actor := newTestActor(t, "member", server.URL, member.Member.Email, member.Password)
		actor.Login()

		loc := requireRedirectPath(t, actor.PostForm("/profile", formKV(
			"action", "password",
			"current_password", "incorrect",
			"new_password", "UpdatedPassword123!",
			"confirm_password", "UpdatedPassword123!",
		)), "/profile")
		requireQueryValue(t, loc, "error", "Current password is incorrect.")
	})

	t.Run("PROF-07 POST /profile action=password mismatch is rejected", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		member := createSeededMember(t, ctx, db, "profile_password_mismatch", uniqueTestSuffix())
		server := newHTTPServer(t, db)
		actor := newTestActor(t, "member", server.URL, member.Member.Email, member.Password)
		actor.Login()

		loc := requireRedirectPath(t, actor.PostForm("/profile", formKV(
			"action", "password",
			"current_password", member.Password,
			"new_password", "UpdatedPassword123!",
			"confirm_password", "DifferentPassword123!",
		)), "/profile")
		requireQueryValue(t, loc, "error", "New passwords do not match.")
	})

	t.Run("PROF-08 POST /profile action=skills succeeds for allowed skill ids", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		member := createSeededMember(t, ctx, db, "profile_skills_allowed", uniqueTestSuffix())
		server := newHTTPServer(t, db)
		actor := newTestActor(t, "member", server.URL, member.Member.Email, member.Password)
		actor.Login()

		available, err := service.ListAvailableSkillsForMember(ctx, db, member.Member.ID)
		if err != nil {
			t.Fatalf("list available skills: %v", err)
		}
		if len(available) == 0 {
			t.Fatalf("expected at least one available skill")
		}
		skillID := available[0].ID

		loc := requireRedirectPath(t, actor.PostForm("/profile", formKV(
			"action", "skills",
			"skill_ids", strconv.FormatInt(skillID, 10),
		)), "/profile")
		requireQueryValue(t, loc, "success", "Skills updated.")

		selected, err := service.ListSelectedSkillIDsForMember(ctx, db, member.Member.ID)
		if err != nil {
			t.Fatalf("list selected skills: %v", err)
		}
		if len(selected) != 1 || selected[0] != skillID {
			t.Fatalf("expected selected skills [%d], got %v", skillID, selected)
		}
	})

	t.Run("PROF-09 POST /profile action=skills invalid skill id parse is rejected", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		member := createSeededMember(t, ctx, db, "profile_skills_invalid", uniqueTestSuffix())
		server := newHTTPServer(t, db)
		actor := newTestActor(t, "member", server.URL, member.Member.Email, member.Password)
		actor.Login()

		loc := requireRedirectPath(t, actor.PostForm("/profile", formKV(
			"action", "skills",
			"skill_ids", "not_a_number",
		)), "/profile")
		requireQueryValue(t, loc, "error", "Invalid skill selection.")
	})

	t.Run("PROF-10 POST /profile action=skills forbidden org skill is rejected", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		ownerA := createSeededMember(t, ctx, db, "profile_skill_owner_a", suffix)
		memberA := createSeededMember(t, ctx, db, "profile_skill_member_a", suffix)
		ownerB := createSeededMember(t, ctx, db, "profile_skill_owner_b", suffix)

		orgA, err := service.CreateOrganization(ctx, db, "Profile Skill Org A "+suffix, "Test City", "TS", "Org A", ownerA.Member.ID)
		if err != nil {
			t.Fatalf("create orgA: %v", err)
		}
		approveMemberForOrganization(t, ctx, db, orgA.ID, ownerA.Member.ID, memberA.Member.ID)

		orgB, err := service.CreateOrganization(ctx, db, "Profile Skill Org B "+suffix, "Test City", "TS", "Org B", ownerB.Member.ID)
		if err != nil {
			t.Fatalf("create orgB: %v", err)
		}
		if err := service.ReplaceOrganizationSkills(ctx, db, orgB.ID, ownerB.Member.ID, []string{"Forbidden Skill"}); err != nil {
			t.Fatalf("replace orgB skills: %v", err)
		}
		orgBSkills, err := service.ListOrganizationSkills(ctx, db, orgB.ID)
		if err != nil {
			t.Fatalf("list orgB skills: %v", err)
		}
		if len(orgBSkills) == 0 {
			t.Fatalf("expected at least one orgB skill")
		}

		server := newHTTPServer(t, db)
		actor := newTestActor(t, "memberA", server.URL, memberA.Member.Email, memberA.Password)
		actor.Login()
		loc := requireRedirectPath(t, actor.PostForm("/profile", formKV(
			"action", "skills",
			"skill_ids", strconv.FormatInt(orgBSkills[0].ID, 10),
		)), "/profile")
		requireQueryValue(t, loc, "error", "One or more selected skills are not available to your account.")
	})

	t.Run("PROF-11 POST /profile action=profile with valid avatar uploads avatar", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		member := createSeededMember(t, ctx, db, "profile_avatar_valid", uniqueTestSuffix())
		server := newHTTPServer(t, db)
		actor := newTestActor(t, "member", server.URL, member.Member.Email, member.Password)
		actor.Login()

		loc := requireRedirectPath(t, actor.PostMultipartWithFiles("/profile", map[string]string{
			"action":            "profile",
			"first_name":        member.Member.FirstName,
			"last_name":         member.Member.LastName,
			"email":             member.Member.Email,
			"preferred_contact": member.Member.Email,
		}, []multipartFile{{
			FieldName:   "avatar_file",
			FileName:    "avatar.png",
			ContentType: "image/png",
			Data:        tinyPNGData(),
		}}), "/profile")
		requireQueryValue(t, loc, "success", "Profile updated.")

		resp := actor.Get("/members/avatar")
		body := requireStatus(t, resp, 200)
		if body == "" {
			t.Fatalf("expected non-empty avatar response body")
		}
	})

	t.Run("PROF-12 POST /profile action=profile with invalid avatar type is rejected", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		member := createSeededMember(t, ctx, db, "profile_avatar_invalid", uniqueTestSuffix())
		server := newHTTPServer(t, db)
		actor := newTestActor(t, "member", server.URL, member.Member.Email, member.Password)
		actor.Login()

		loc := requireRedirectPath(t, actor.PostMultipartWithFiles("/profile", map[string]string{
			"action":            "profile",
			"first_name":        member.Member.FirstName,
			"last_name":         member.Member.LastName,
			"email":             member.Member.Email,
			"preferred_contact": member.Member.Email,
		}, []multipartFile{{
			FieldName:   "avatar_file",
			FileName:    "avatar.txt",
			ContentType: "text/plain",
			Data:        []byte("not an image"),
		}}), "/profile")
		requireQueryValue(t, loc, "error", "avatar must be a PNG or JPEG")
	})

	t.Run("PROF-12C POST /profile action=profile with oversized avatar is rejected", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		member := createSeededMember(t, ctx, db, "profile_avatar_oversized", uniqueTestSuffix())
		server := newHTTPServer(t, db)
		actor := newTestActor(t, "member", server.URL, member.Member.Email, member.Password)
		actor.Login()

		loc := requireRedirectPath(t, actor.PostMultipartWithFiles("/profile", map[string]string{
			"action":            "profile",
			"first_name":        member.Member.FirstName,
			"last_name":         member.Member.LastName,
			"email":             member.Member.Email,
			"preferred_contact": member.Member.Email,
		}, []multipartFile{{
			FieldName:   "avatar_file",
			FileName:    "avatar.png",
			ContentType: "image/png",
			Data:        oversizedPNGData((2 << 20) + 1),
		}}), "/profile")
		requireQueryValue(t, loc, "error", "avatar file too large (max 2 MB)")
	})

	t.Run("PROF-12A POST /profile action=delete_account requires exact confirmation phrase", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		member := createSeededMember(t, ctx, db, "profile_delete_account_phrase", uniqueTestSuffix())
		server := newHTTPServer(t, db)
		actor := newTestActor(t, "member", server.URL, member.Member.Email, member.Password)
		actor.Login()

		loc := requireRedirectPath(t, actor.PostForm("/profile", formKV(
			"action", "delete_account",
			"delete_account_confirmation", "i want to leave hopShare",
		)), "/profile")
		requireQueryValue(t, loc, "tab", "account")
		requireQueryValue(t, loc, "error", "Please type \"I want to leave hopShare\" exactly to confirm account deletion.")

		stillEnabled, err := service.GetMemberByID(ctx, db, member.Member.ID)
		if err != nil {
			t.Fatalf("load member after rejected delete action: %v", err)
		}
		if !stillEnabled.Enabled {
			t.Fatalf("expected member to remain enabled after rejected delete action")
		}
		requireStatus(t, actor.Get("/my-hopshare"), http.StatusOK)
	})

	t.Run("PROF-12B POST /profile action=delete_account disables account and logs out all sessions", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		member := createSeededMember(t, ctx, db, "profile_delete_account_success", uniqueTestSuffix())
		server := newHTTPServer(t, db)
		primaryActor := newTestActor(t, "primary_member", server.URL, member.Member.Email, member.Password)
		secondaryActor := newTestActor(t, "secondary_member", server.URL, member.Member.Email, member.Password)
		primaryActor.Login()
		secondaryActor.Login()

		requireRedirectPath(t, primaryActor.PostForm("/profile", formKV(
			"action", "delete_account",
			"delete_account_confirmation", "I want to leave hopShare",
		)), "/farewell")

		updated, err := service.GetMemberByID(ctx, db, member.Member.ID)
		if err != nil {
			t.Fatalf("load member after delete action: %v", err)
		}
		if updated.Enabled {
			t.Fatalf("expected member to be disabled after delete action")
		}

		farewellBody := requireStatus(t, primaryActor.Get("/farewell"), http.StatusOK)
		requireBodyContains(t, farewellBody, "Thanks for being part of hopShare.")

		requireRedirectPath(t, primaryActor.Get("/my-hopshare"), "/login")
		requireRedirectPath(t, secondaryActor.Get("/my-hopshare"), "/login")

		loginBody := requireStatus(t, primaryActor.PostForm("/login", formKV(
			"email", member.Member.Email,
			"password", member.Password,
		)), http.StatusOK)
		requireBodyContains(t, loginBody, "Invalid email or password.")
	})

	t.Run("PROF-13 GET /members/avatar shared-org member is visible", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner", "member_a", "member_b")
		_ = org

		server := newHTTPServer(t, db)
		actorA := newTestActor(t, "member_a", server.URL, members["member_a"].Member.Email, members["member_a"].Password)
		actorA.Login()

		requireStatus(t, actorA.Get("/members/avatar?member_id="+strconv.FormatInt(members["member_b"].Member.ID, 10)), 200)
	})

	t.Run("PROF-14 GET /members/avatar non-shared member is 404", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		_, membersA := createOrganizationWithMembers(t, ctx, db, suffix+"a", "owner", "member_a")
		_, membersB := createOrganizationWithMembers(t, ctx, db, suffix+"b", "owner", "member_b")

		server := newHTTPServer(t, db)
		actorA := newTestActor(t, "member_a", server.URL, membersA["member_a"].Member.Email, membersA["member_a"].Password)
		actorA.Login()

		requireStatus(t, actorA.Get("/members/avatar?member_id="+strconv.FormatInt(membersB["member_b"].Member.ID, 10)), 404)
	})
}

func tinyPNGData() []byte {
	return []byte{
		0x89, 0x50, 0x4E, 0x47,
		0x0D, 0x0A, 0x1A, 0x0A,
		0x00, 0x00, 0x00, 0x0D,
		0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01,
		0x00, 0x00, 0x00, 0x01,
		0x08, 0x02, 0x00, 0x00,
		0x00, 0x90, 0x77, 0x53,
		0xDE, 0x00, 0x00, 0x00,
		0x0A, 0x49, 0x44, 0x41,
		0x54, 0x78, 0x9C, 0x63,
		0x00, 0x01, 0x00, 0x00,
		0x05, 0x00, 0x01, 0x0D,
		0x0A, 0x2D, 0xB4, 0x00,
		0x00, 0x00, 0x00, 0x49,
		0x45, 0x4E, 0x44, 0xAE,
		0x42, 0x60, 0x82,
	}
}

func oversizedPNGData(size int) []byte {
	data := make([]byte, size)
	copy(data, tinyPNGData())
	return data
}
