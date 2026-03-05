package http_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	apphttp "hopshare/internal/http"
	"hopshare/internal/service"
	"hopshare/internal/types"
)

type seededMember struct {
	Member   types.Member
	Password string
}

func TestHopLifecycleWorkflow_MultiUserHTTP(t *testing.T) {
	db := requireHTTPTestDB(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	suffix := uniqueTestSuffix()
	owner := createSeededMember(t, ctx, db, "owner", suffix)
	requester := createSeededMember(t, ctx, db, "requester", suffix)
	helper := createSeededMember(t, ctx, db, "helper", suffix)

	org, err := service.CreateOrganization(
		ctx,
		db,
		"HTTP Workflow Org "+suffix,
		"Test City",
		"TS",
		"Organization used by HTTP workflow integration tests.",
		owner.Member.ID,
	)
	if err != nil {
		t.Fatalf("create organization: %v", err)
	}

	approveMemberForOrganization(t, ctx, db, org.ID, owner.Member.ID, requester.Member.ID)
	approveMemberForOrganization(t, ctx, db, org.ID, owner.Member.ID, helper.Member.ID)

	cookieSecure := false
	server := httptest.NewServer(apphttp.NewRouterWithOptions(db, apphttp.RouterOptions{
		CookieSecure: &cookieSecure,
	}))
	defer server.Close()

	ownerActor := newTestActor(t, "owner", server.URL, owner.Member.Email, owner.Password)
	requesterActor := newTestActor(t, "requester", server.URL, requester.Member.Email, requester.Password)
	helperActor := newTestActor(t, "helper", server.URL, helper.Member.Email, helper.Password)

	ownerActor.Login()
	requesterActor.Login()
	helperActor.Login()

	hopTitle := "Need a ride to an appointment " + suffix
	createLoc := requireRedirectPath(t, requesterActor.PostForm("/hops/create", url.Values{
		"org_id":          {strconv.FormatInt(org.ID, 10)},
		"title":           {hopTitle},
		"details":         {"Need a round trip ride to the clinic."},
		"estimated_hours": {"2"},
		"needed_by_kind":  {types.HopNeededByAnytime},
	}), "/my-hopshare")
	requireQueryValue(t, createLoc, "org_id", strconv.FormatInt(org.ID, 10))
	requireQueryValue(t, createLoc, "success", "Hop created.")

	hop := findRequestedHopByTitle(t, ctx, db, org.ID, requester.Member.ID, hopTitle)
	if hop.Status != types.HopStatusOpen {
		t.Fatalf("expected created hop status %q, got %q", types.HopStatusOpen, hop.Status)
	}

	offerLoc := requireRedirectPath(t, helperActor.PostForm("/hops/offer", url.Values{
		"org_id": {strconv.FormatInt(org.ID, 10)},
		"hop_id": {strconv.FormatInt(hop.ID, 10)},
	}), "/my-hopshare")
	requireQueryValue(t, offerLoc, "org_id", strconv.FormatInt(org.ID, 10))
	requireQueryValue(t, offerLoc, "success", "Offer sent.")

	actionMsg := findPendingActionMessageForHop(t, ctx, db, requester.Member.ID, hop.ID)

	acceptLoc := requireRedirectPath(t, requesterActor.PostForm("/messages/action", url.Values{
		"message_id": {strconv.FormatInt(actionMsg.ID, 10)},
		"action":     {"accept"},
		"body":       {"I can do Tuesday afternoon. Thank you!"},
	}), "/messages")
	requireQueryValue(t, acceptLoc, "message_id", strconv.FormatInt(actionMsg.ID, 10))
	requireQueryValue(t, acceptLoc, "success", "Offer accepted.")

	actionMsg, err = service.GetMessageForMember(ctx, db, actionMsg.ID, requester.Member.ID)
	if err != nil {
		t.Fatalf("reload action message: %v", err)
	}
	if actionMsg.ActionStatus == nil || *actionMsg.ActionStatus != types.MessageActionAccepted {
		t.Fatalf("expected action message status %q, got %+v", types.MessageActionAccepted, actionMsg.ActionStatus)
	}

	hop, err = service.GetHopByID(ctx, db, org.ID, hop.ID)
	if err != nil {
		t.Fatalf("load hop after accept: %v", err)
	}
	if hop.Status != types.HopStatusAccepted {
		t.Fatalf("expected hop status %q after accept, got %q", types.HopStatusAccepted, hop.Status)
	}
	if hop.AcceptedBy == nil || *hop.AcceptedBy != helper.Member.ID {
		t.Fatalf("expected hop accepted_by=%d, got %v", helper.Member.ID, hop.AcceptedBy)
	}

	completionComment := "Completed successfully via multi-user HTTP integration test."
	completeLoc := requireRedirectPath(t, helperActor.PostForm("/hops/complete", url.Values{
		"org_id":             {strconv.FormatInt(org.ID, 10)},
		"hop_id":             {strconv.FormatInt(hop.ID, 10)},
		"completed_hours":    {"2"},
		"completion_comment": {completionComment},
	}), "/my-hopshare")
	requireQueryValue(t, completeLoc, "org_id", strconv.FormatInt(org.ID, 10))
	requireQueryValue(t, completeLoc, "success", "Hop completed.")

	hop, err = service.GetHopByID(ctx, db, org.ID, hop.ID)
	if err != nil {
		t.Fatalf("load hop after complete: %v", err)
	}
	if hop.Status != types.HopStatusCompleted {
		t.Fatalf("expected hop status %q after complete, got %q", types.HopStatusCompleted, hop.Status)
	}
	if hop.CompletedBy == nil || *hop.CompletedBy != helper.Member.ID {
		t.Fatalf("expected hop completed_by=%d, got %v", helper.Member.ID, hop.CompletedBy)
	}
	if hop.CompletedHours == nil || *hop.CompletedHours != 2 {
		t.Fatalf("expected completed_hours=2, got %v", hop.CompletedHours)
	}
	if hop.CompletionComment == nil || *hop.CompletionComment != completionComment {
		t.Fatalf("unexpected completion comment: got=%v want=%q", hop.CompletionComment, completionComment)
	}

	var transactionCount int
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM hop_transactions
		WHERE organization_id = $1 AND hop_id = $2
	`, org.ID, hop.ID).Scan(&transactionCount); err != nil {
		t.Fatalf("count hop transactions: %v", err)
	}
	if transactionCount != 1 {
		t.Fatalf("expected exactly 1 hop transaction, got %d", transactionCount)
	}

	helperMessages, err := service.ListMessages(ctx, db, helper.Member.ID)
	if err != nil {
		t.Fatalf("list helper messages: %v", err)
	}
	foundAcceptedInfo := false
	for _, msg := range helperMessages {
		if msg.MessageType != types.MessageTypeInformation {
			continue
		}
		if msg.SenderID == nil || *msg.SenderID != requester.Member.ID {
			continue
		}
		if strings.HasPrefix(msg.Subject, "Accepted:") {
			foundAcceptedInfo = true
			break
		}
	}
	if !foundAcceptedInfo {
		t.Fatalf("expected helper to receive acceptance info message")
	}
}

func TestHopLifecycleWorkflow_DeclineOfferKeepsHopOpen(t *testing.T) {
	db := requireHTTPTestDB(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	suffix := uniqueTestSuffix()
	owner := createSeededMember(t, ctx, db, "owner", suffix)
	requester := createSeededMember(t, ctx, db, "requester", suffix)
	helper := createSeededMember(t, ctx, db, "helper", suffix)

	org, err := service.CreateOrganization(
		ctx,
		db,
		"HTTP Decline Org "+suffix,
		"Test City",
		"TS",
		"Organization used by decline-path integration tests.",
		owner.Member.ID,
	)
	if err != nil {
		t.Fatalf("create organization: %v", err)
	}

	approveMemberForOrganization(t, ctx, db, org.ID, owner.Member.ID, requester.Member.ID)
	approveMemberForOrganization(t, ctx, db, org.ID, owner.Member.ID, helper.Member.ID)

	cookieSecure := false
	server := httptest.NewServer(apphttp.NewRouterWithOptions(db, apphttp.RouterOptions{
		CookieSecure: &cookieSecure,
	}))
	defer server.Close()

	requesterActor := newTestActor(t, "requester", server.URL, requester.Member.Email, requester.Password)
	helperActor := newTestActor(t, "helper", server.URL, helper.Member.Email, helper.Password)
	requesterActor.Login()
	helperActor.Login()

	hopTitle := "Decline-path test hop " + suffix
	createLoc := requireRedirectPath(t, requesterActor.PostForm("/hops/create", url.Values{
		"org_id":          {strconv.FormatInt(org.ID, 10)},
		"title":           {hopTitle},
		"details":         {"Need a helper but declining first offer."},
		"estimated_hours": {"1"},
		"needed_by_kind":  {types.HopNeededByAnytime},
	}), "/my-hopshare")
	requireQueryValue(t, createLoc, "success", "Hop created.")

	hop := findRequestedHopByTitle(t, ctx, db, org.ID, requester.Member.ID, hopTitle)

	offerLoc := requireRedirectPath(t, helperActor.PostForm("/hops/offer", url.Values{
		"org_id": {strconv.FormatInt(org.ID, 10)},
		"hop_id": {strconv.FormatInt(hop.ID, 10)},
	}), "/my-hopshare")
	requireQueryValue(t, offerLoc, "success", "Offer sent.")

	actionMsg := findPendingActionMessageForHop(t, ctx, db, requester.Member.ID, hop.ID)

	declineLoc := requireRedirectPath(t, requesterActor.PostForm("/messages/action", url.Values{
		"message_id": {strconv.FormatInt(actionMsg.ID, 10)},
		"action":     {"decline"},
		"body":       {"Thanks for offering, but I need to pass for now."},
	}), "/messages")
	requireQueryValue(t, declineLoc, "message_id", strconv.FormatInt(actionMsg.ID, 10))
	requireQueryValue(t, declineLoc, "success", "Offer declined.")

	actionMsg, err = service.GetMessageForMember(ctx, db, actionMsg.ID, requester.Member.ID)
	if err != nil {
		t.Fatalf("reload action message: %v", err)
	}
	if actionMsg.ActionStatus == nil || *actionMsg.ActionStatus != types.MessageActionDeclined {
		t.Fatalf("expected action message status %q, got %+v", types.MessageActionDeclined, actionMsg.ActionStatus)
	}

	hop, err = service.GetHopByID(ctx, db, org.ID, hop.ID)
	if err != nil {
		t.Fatalf("load hop after decline: %v", err)
	}
	if hop.Status != types.HopStatusOpen {
		t.Fatalf("expected hop status %q after decline, got %q", types.HopStatusOpen, hop.Status)
	}
	if hop.AcceptedBy != nil {
		t.Fatalf("expected hop accepted_by to remain nil after decline, got %v", hop.AcceptedBy)
	}

	helperMessages, err := service.ListMessages(ctx, db, helper.Member.ID)
	if err != nil {
		t.Fatalf("list helper messages: %v", err)
	}
	foundDeclinedInfo := false
	for _, msg := range helperMessages {
		if msg.MessageType != types.MessageTypeInformation {
			continue
		}
		if msg.SenderID == nil || *msg.SenderID != requester.Member.ID {
			continue
		}
		if strings.HasPrefix(msg.Subject, "Declined:") {
			foundDeclinedInfo = true
			break
		}
	}
	if !foundDeclinedInfo {
		t.Fatalf("expected helper to receive declined info message")
	}
	var declinedNotificationCount int
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM member_notifications
		WHERE member_id = $1 AND href = $2 AND text LIKE $3
	`, helper.Member.ID, "/messages", "%was declined.%").Scan(&declinedNotificationCount); err != nil {
		t.Fatalf("count declined helper notifications: %v", err)
	}
	if declinedNotificationCount == 0 {
		t.Fatalf("expected helper to receive declined notification with /messages link")
	}

	offerAgainLoc := requireRedirectPath(t, helperActor.PostForm("/hops/offer", url.Values{
		"org_id": {strconv.FormatInt(org.ID, 10)},
		"hop_id": {strconv.FormatInt(hop.ID, 10)},
	}), "/my-hopshare")
	requireQueryValue(t, offerAgainLoc, "success", "Offer sent.")

	pendingCount := countPendingActionMessagesForHop(t, ctx, db, requester.Member.ID, hop.ID)
	if pendingCount != 1 {
		t.Fatalf("expected one pending action message after declined offer retry, got %d", pendingCount)
	}
}

func TestHopLifecycleWorkflow_PrivateHopBlocksNonAssociatedCommentAndImage(t *testing.T) {
	db := requireHTTPTestDB(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	suffix := uniqueTestSuffix()
	owner := createSeededMember(t, ctx, db, "owner", suffix)
	requester := createSeededMember(t, ctx, db, "requester", suffix)
	outsider := createSeededMember(t, ctx, db, "outsider", suffix)

	org, err := service.CreateOrganization(
		ctx,
		db,
		"HTTP Private-Hop Org "+suffix,
		"Test City",
		"TS",
		"Organization used by private-hop permission integration tests.",
		owner.Member.ID,
	)
	if err != nil {
		t.Fatalf("create organization: %v", err)
	}

	approveMemberForOrganization(t, ctx, db, org.ID, owner.Member.ID, requester.Member.ID)
	approveMemberForOrganization(t, ctx, db, org.ID, owner.Member.ID, outsider.Member.ID)

	hop, err := service.CreateHop(ctx, db, service.CreateHopParams{
		OrganizationID: org.ID,
		MemberID:       requester.Member.ID,
		Title:          "Private hop permission test " + suffix,
		Details:        "Only associated members should be able to add content.",
		EstimatedHours: 1,
		NeededByKind:   types.HopNeededByAnytime,
		IsPrivate:      true,
	})
	if err != nil {
		t.Fatalf("create private hop: %v", err)
	}

	cookieSecure := false
	server := httptest.NewServer(apphttp.NewRouterWithOptions(db, apphttp.RouterOptions{
		CookieSecure: &cookieSecure,
	}))
	defer server.Close()

	outsiderActor := newTestActor(t, "outsider", server.URL, outsider.Member.Email, outsider.Password)
	outsiderActor.Login()

	commentBody := requireStatus(t, outsiderActor.PostForm("/hops/comments/create", url.Values{
		"org_id": {strconv.FormatInt(org.ID, 10)},
		"hop_id": {strconv.FormatInt(hop.ID, 10)},
		"body":   {"I should not be allowed to comment here."},
	}), http.StatusForbidden)
	if !strings.Contains(commentBody, "Unauthorized") {
		t.Fatalf("expected unauthorized body for comment endpoint, got %q", commentBody)
	}

	imageBody := requireStatus(t, outsiderActor.PostMultipart("/hops/images/upload", map[string]string{
		"org_id": strconv.FormatInt(org.ID, 10),
		"hop_id": strconv.FormatInt(hop.ID, 10),
	}), http.StatusForbidden)
	if !strings.Contains(imageBody, "Unauthorized") {
		t.Fatalf("expected unauthorized body for image upload endpoint, got %q", imageBody)
	}

	comments, err := service.ListHopComments(ctx, db, hop.ID)
	if err != nil {
		t.Fatalf("list hop comments: %v", err)
	}
	if len(comments) != 0 {
		t.Fatalf("expected no comments on private hop, got %d", len(comments))
	}

	images, err := service.ListHopImages(ctx, db, hop.ID)
	if err != nil {
		t.Fatalf("list hop images: %v", err)
	}
	if len(images) != 0 {
		t.Fatalf("expected no images on private hop, got %d", len(images))
	}
}

func createSeededMember(t *testing.T, ctx context.Context, db *sql.DB, role, suffix string) seededMember {
	t.Helper()

	username := fmt.Sprintf("http_%s_%s", role, suffix)
	email := username + "@example.com"
	password := "Password123!"
	hash, err := service.HashPassword(password)
	if err != nil {
		t.Fatalf("hash password for %s: %v", role, err)
	}

	member, err := service.CreateMember(ctx, db, types.Member{
		FirstName:        role,
		LastName:         "Integration",
		Email:            email,
		PasswordHash:     hash,
		PreferredContact: email,
		Enabled:          true,
		Verified:         true,
	})
	if err != nil {
		t.Fatalf("create %s member: %v", role, err)
	}

	return seededMember{
		Member:   member,
		Password: password,
	}
}

func approveMemberForOrganization(t *testing.T, ctx context.Context, db *sql.DB, orgID, ownerID, memberID int64) {
	t.Helper()

	if err := service.RequestMembership(ctx, db, memberID, orgID, nil); err != nil {
		t.Fatalf("request membership member=%d org=%d: %v", memberID, orgID, err)
	}

	requests, err := service.PendingMembershipRequests(ctx, db, orgID)
	if err != nil {
		t.Fatalf("load pending requests org=%d: %v", orgID, err)
	}

	var requestID int64
	for _, req := range requests {
		if req.MemberID == memberID {
			requestID = req.ID
			break
		}
	}
	if requestID == 0 {
		t.Fatalf("could not find pending request for member=%d org=%d", memberID, orgID)
	}

	if err := service.ApproveMembershipRequest(ctx, db, requestID, ownerID); err != nil {
		t.Fatalf("approve membership request %d: %v", requestID, err)
	}

	hasMembership, err := service.MemberHasActiveMembership(ctx, db, memberID, orgID)
	if err != nil {
		t.Fatalf("check approved membership member=%d org=%d: %v", memberID, orgID, err)
	}
	if !hasMembership {
		t.Fatalf("expected member=%d to have active membership in org=%d", memberID, orgID)
	}
}

func countPendingActionMessagesForHop(t *testing.T, ctx context.Context, db *sql.DB, recipientID, hopID int64) int {
	t.Helper()

	messages, err := service.ListMessages(ctx, db, recipientID)
	if err != nil {
		t.Fatalf("list messages for recipient=%d: %v", recipientID, err)
	}

	count := 0
	for _, msg := range messages {
		if msg.MessageType != types.MessageTypeAction {
			continue
		}
		if msg.HopID == nil || *msg.HopID != hopID {
			continue
		}
		if msg.ActionStatus == nil {
			count++
		}
	}

	return count
}

func findRequestedHopByTitle(t *testing.T, ctx context.Context, db *sql.DB, orgID, memberID int64, title string) types.Hop {
	t.Helper()

	hops, err := service.ListRequestedHops(ctx, db, orgID, memberID)
	if err != nil {
		t.Fatalf("list requested hops member=%d org=%d: %v", memberID, orgID, err)
	}

	for _, hop := range hops {
		if hop.Title == title {
			return hop
		}
	}
	t.Fatalf("could not find requested hop with title %q", title)
	return types.Hop{}
}

func findPendingActionMessageForHop(t *testing.T, ctx context.Context, db *sql.DB, recipientID, hopID int64) types.Message {
	t.Helper()

	messages, err := service.ListMessages(ctx, db, recipientID)
	if err != nil {
		t.Fatalf("list messages for member=%d: %v", recipientID, err)
	}

	for _, msg := range messages {
		if msg.MessageType != types.MessageTypeAction {
			continue
		}
		if msg.HopID == nil || *msg.HopID != hopID {
			continue
		}
		if msg.ActionStatus != nil {
			continue
		}
		return msg
	}

	t.Fatalf("could not find pending action message for recipient=%d hop=%d", recipientID, hopID)
	return types.Message{}
}
