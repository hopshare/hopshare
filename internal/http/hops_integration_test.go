package http_test

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"testing"
	"time"

	"hopshare/internal/service"
	"hopshare/internal/types"
)

func TestHopsHTTPMatrix(t *testing.T) {
	db := requireHTTPTestDB(t)

	t.Run("HOP-01 create hop success", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner", "requester")
		server := newHTTPServer(t, db)
		requester := newTestActor(t, "requester", server.URL, members["requester"].Member.Email, members["requester"].Password)
		requester.Login()
		loc := requireRedirectPath(t, requester.PostForm("/hops/create", formKV(
			"org_id", strconv.FormatInt(org.ID, 10),
			"title", "Create Hop Success "+suffix,
			"details", "Need help.",
			"estimated_hours", "2",
			"needed_by_kind", types.HopNeededByAnytime,
		)), "/my-hopshare")
		requireQueryValue(t, loc, "success", "Hop created.")

		hops, err := service.ListRequestedHops(ctx, db, org.ID, members["requester"].Member.ID)
		if err != nil {
			t.Fatalf("list requested hops: %v", err)
		}
		var created *types.Hop
		for i := range hops {
			if hops[i].Title == "Create Hop Success "+suffix {
				created = &hops[i]
				break
			}
		}
		if created == nil {
			t.Fatalf("created hop not found in requested list")
		}
		if created.NeededByDate != nil {
			t.Fatalf("expected needed_by_date to be nil for anytime hop, got %v", created.NeededByDate)
		}
		if created.ExpiresAt == nil {
			t.Fatalf("expected expires_at to be set for anytime hop")
		}
		want := created.CreatedAt.AddDate(0, 0, 90)
		diff := created.ExpiresAt.Sub(want)
		if diff < -2*time.Second || diff > 2*time.Second {
			t.Fatalf("expected expires_at about 90 days after created_at, created_at=%v expires_at=%v diff=%v", created.CreatedAt, *created.ExpiresAt, diff)
		}
	})

	t.Run("HOP-02 create hop invalid inputs are rejected", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner", "requester")
		server := newHTTPServer(t, db)
		requester := newTestActor(t, "requester", server.URL, members["requester"].Member.Email, members["requester"].Password)
		requester.Login()

		loc := requireRedirectPath(t, requester.PostForm("/hops/create", formKV(
			"org_id", strconv.FormatInt(org.ID, 10),
			"title", "Invalid Hours Hop "+suffix,
			"estimated_hours", "bad",
			"needed_by_kind", types.HopNeededByAnytime,
		)), "/my-hopshare")
		requireQueryValue(t, loc, "error", "Invalid hours.")

		loc = requireRedirectPath(t, requester.PostForm("/hops/create", formKV(
			"org_id", strconv.FormatInt(org.ID, 10),
			"title", "Invalid Date Hop "+suffix,
			"estimated_hours", "2",
			"needed_by_kind", types.HopNeededByOn,
			"needed_by_date", "bad-date",
		)), "/my-hopshare")
		requireQueryValue(t, loc, "error", "Invalid date.")

		loc = requireRedirectPath(t, requester.PostForm("/hops/create", formKV(
			"org_id", strconv.FormatInt(org.ID, 10),
			"title", "Anytime Ignores Date "+suffix,
			"estimated_hours", "2",
			"needed_by_kind", types.HopNeededByAnytime,
			"needed_by_date", "bad-date",
		)), "/my-hopshare")
		requireQueryValue(t, loc, "success", "Hop created.")
	})

	t.Run("HOP-02b request hop page renders for active members", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner", "requester")
		server := newHTTPServer(t, db)
		requester := newTestActor(t, "requester", server.URL, members["requester"].Member.Email, members["requester"].Password)
		requester.Login()

		body := requireStatus(t, requester.Get("/hops/request?org_id="+strconv.FormatInt(org.ID, 10)), 200)
		requireBodyContains(t, body, "Request a hop")
		requireBodyContains(t, body, `action="/hops/create"`)
		requireBodyContains(t, body, `name="org_id" value="`+strconv.FormatInt(org.ID, 10)+`"`)
		requireBodyContains(t, body, "Back to My hopShare")

		dashboardBody := requireStatus(t, requester.Get("/my-hopshare?org_id="+strconv.FormatInt(org.ID, 10)), 200)
		requireBodyContains(t, dashboardBody, `href="/hops/request?org_id=`+strconv.FormatInt(org.ID, 10)+`"`)
		requireBodyNotContains(t, dashboardBody, `aria-label="Request a hop"`)
	})

	t.Run("HOP-03 create hop by non-member fails", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, _ := createOrganizationWithMembers(t, ctx, db, suffix, "owner")
		outsider := createSeededMember(t, ctx, db, "hop_non_member", suffix)
		server := newHTTPServer(t, db)
		actor := newTestActor(t, "outsider", server.URL, outsider.Member.Email, outsider.Password)
		actor.Login()
		loc := requireRedirectPath(t, actor.PostForm("/hops/create", formKV(
			"org_id", strconv.FormatInt(org.ID, 10),
			"title", "Should Not Create "+suffix,
			"estimated_hours", "1",
			"needed_by_kind", types.HopNeededByAnytime,
		)), "/my-hopshare")
		requireQueryValue(t, loc, "error", "Could not create hop.")
	})

	t.Run("HOP-03A create hop blocked when requester is at minimum balance", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner", "requester")
		if err := service.AdjustMemberHourBalance(ctx, db, service.AdjustMemberHourBalanceParams{
			OrganizationID: org.ID,
			MemberID:       members["requester"].Member.ID,
			AdminMemberID:  members["owner"].Member.ID,
			HoursDelta:     -10, // requester starts at 5, so this moves them to -5 (default min)
			Reason:         "test minimum balance request block",
		}); err != nil {
			t.Fatalf("adjust requester balance: %v", err)
		}

		server := newHTTPServer(t, db)
		requester := newTestActor(t, "requester", server.URL, members["requester"].Member.Email, members["requester"].Password)
		requester.Login()
		loc := requireRedirectPath(t, requester.PostForm("/hops/create", formKV(
			"org_id", strconv.FormatInt(org.ID, 10),
			"title", "Min Blocked "+suffix,
			"estimated_hours", "1",
			"needed_by_kind", types.HopNeededByAnytime,
		)), "/my-hopshare")
		requireQueryValue(t, loc, "error", "You're at this organization's minimum balance (-5). Complete a hop first to earn hours before requesting another.")
	})

	t.Run("HOP-04 view hop as org member succeeds", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner", "member")
		hop, err := service.CreateHop(ctx, db, service.CreateHopParams{
			OrganizationID: org.ID,
			MemberID:       members["owner"].Member.ID,
			Title:          "Viewable Hop " + suffix,
			Details:        "Visible to members.",
			EstimatedHours: 1,
			NeededByKind:   types.HopNeededByAnytime,
			IsPrivate:      false,
		})
		if err != nil {
			t.Fatalf("create hop: %v", err)
		}
		server := newHTTPServer(t, db)
		member := newTestActor(t, "member", server.URL, members["member"].Member.Email, members["member"].Password)
		member.Login()
		body := requireStatus(t, member.Get("/hops/view?org_id="+strconv.FormatInt(org.ID, 10)+"&hop_id="+strconv.FormatInt(hop.ID, 10)), 200)
		requireBodyContains(t, body, "Viewable Hop")
		requireBodyContains(t, body, "Is asking for help")
		requireBodyNotContains(t, body, ">Unknown<")
	})

	t.Run("HOP-05 view hop as non-member returns forbidden", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner")
		hop, err := service.CreateHop(ctx, db, service.CreateHopParams{
			OrganizationID: org.ID,
			MemberID:       members["owner"].Member.ID,
			Title:          "Protected Hop " + suffix,
			Details:        "Not visible to non-members.",
			EstimatedHours: 1,
			NeededByKind:   types.HopNeededByAnytime,
			IsPrivate:      false,
		})
		if err != nil {
			t.Fatalf("create hop: %v", err)
		}
		outsider := createSeededMember(t, ctx, db, "hop_view_outsider", suffix)
		server := newHTTPServer(t, db)
		actor := newTestActor(t, "outsider", server.URL, outsider.Member.Email, outsider.Password)
		actor.Login()
		requireStatus(t, actor.Get("/hops/view?org_id="+strconv.FormatInt(org.ID, 10)+"&hop_id="+strconv.FormatInt(hop.ID, 10)), 403)
	})

	t.Run("HOP-06 accepted hop details show complete action only for requester/helper", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner", "helper", "member")
		hop := createAcceptedHopViaOffer(t, ctx, db, org.ID, members["owner"].Member.ID, members["helper"].Member.ID, "Detail completion visibility "+suffix)

		server := newHTTPServer(t, db)
		owner := newTestActor(t, "owner", server.URL, members["owner"].Member.Email, members["owner"].Password)
		helper := newTestActor(t, "helper", server.URL, members["helper"].Member.Email, members["helper"].Password)
		member := newTestActor(t, "member", server.URL, members["member"].Member.Email, members["member"].Password)
		owner.Login()
		helper.Login()
		member.Login()

		ownerBody := requireStatus(t, owner.Get("/hops/view?org_id="+strconv.FormatInt(org.ID, 10)+"&hop_id="+strconv.FormatInt(hop.ID, 10)), 200)
		requireBodyContains(t, ownerBody, "Mark complete")
		requireBodyContains(t, ownerBody, "data-hop-requester=\"true\"")
		requireBodyContains(t, ownerBody, "action=\"/hops/cancel\"")

		helperBody := requireStatus(t, helper.Get("/hops/view?org_id="+strconv.FormatInt(org.ID, 10)+"&hop_id="+strconv.FormatInt(hop.ID, 10)), 200)
		requireBodyContains(t, helperBody, "Mark complete")
		requireBodyContains(t, helperBody, "data-hop-requester=\"false\"")
		requireBodyNotContains(t, helperBody, "action=\"/hops/cancel\"")

		memberBody := requireStatus(t, member.Get("/hops/view?org_id="+strconv.FormatInt(org.ID, 10)+"&hop_id="+strconv.FormatInt(hop.ID, 10)), 200)
		requireBodyNotContains(t, memberBody, "data-hop-requester=\"true\"")
		requireBodyNotContains(t, memberBody, "data-hop-requester=\"false\"")
		requireBodyNotContains(t, memberBody, "action=\"/hops/cancel\"")
	})

	t.Run("HOP-06b open hop details show cancel action only for requester", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner", "helper", "member")
		hop, err := service.CreateHop(ctx, db, service.CreateHopParams{
			OrganizationID: org.ID,
			MemberID:       members["owner"].Member.ID,
			Title:          "Open detail cancel visibility " + suffix,
			Details:        "Only requester should see cancel on open hop details.",
			EstimatedHours: 1,
			NeededByKind:   types.HopNeededByAnytime,
			IsPrivate:      false,
		})
		if err != nil {
			t.Fatalf("create hop: %v", err)
		}

		server := newHTTPServer(t, db)
		owner := newTestActor(t, "owner", server.URL, members["owner"].Member.Email, members["owner"].Password)
		helper := newTestActor(t, "helper", server.URL, members["helper"].Member.Email, members["helper"].Password)
		member := newTestActor(t, "member", server.URL, members["member"].Member.Email, members["member"].Password)
		owner.Login()
		helper.Login()
		member.Login()

		ownerBody := requireStatus(t, owner.Get("/hops/view?org_id="+strconv.FormatInt(org.ID, 10)+"&hop_id="+strconv.FormatInt(hop.ID, 10)), 200)
		requireBodyContains(t, ownerBody, "action=\"/hops/cancel\"")

		helperBody := requireStatus(t, helper.Get("/hops/view?org_id="+strconv.FormatInt(org.ID, 10)+"&hop_id="+strconv.FormatInt(hop.ID, 10)), 200)
		requireBodyNotContains(t, helperBody, "action=\"/hops/cancel\"")

		memberBody := requireStatus(t, member.Get("/hops/view?org_id="+strconv.FormatInt(org.ID, 10)+"&hop_id="+strconv.FormatInt(hop.ID, 10)), 200)
		requireBodyNotContains(t, memberBody, "action=\"/hops/cancel\"")
	})

	t.Run("HOP-06c pending hop details show offers only to requester", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner", "helper", "member")
		hop, err := service.CreateHop(ctx, db, service.CreateHopParams{
			OrganizationID: org.ID,
			MemberID:       members["owner"].Member.ID,
			Title:          "Offer detail visibility " + suffix,
			Details:        "Only the requester should see pending offers.",
			EstimatedHours: 1,
			NeededByKind:   types.HopNeededByAnytime,
			IsPrivate:      false,
		})
		if err != nil {
			t.Fatalf("create hop: %v", err)
		}
		if err := service.OfferHopHelp(ctx, db, service.OfferHopParams{
			OrganizationID: org.ID,
			HopID:          hop.ID,
			OffererID:      members["helper"].Member.ID,
			OffererName:    "Helper Person",
		}); err != nil {
			t.Fatalf("offer hop: %v", err)
		}

		server := newHTTPServer(t, db)
		owner := newTestActor(t, "owner", server.URL, members["owner"].Member.Email, members["owner"].Password)
		helper := newTestActor(t, "helper", server.URL, members["helper"].Member.Email, members["helper"].Password)
		member := newTestActor(t, "member", server.URL, members["member"].Member.Email, members["member"].Password)
		owner.Login()
		helper.Login()
		member.Login()

		helperDisplayName := strings.TrimSpace(members["helper"].Member.FirstName + " " + members["helper"].Member.LastName)
		ownerBody := requireStatus(t, owner.Get("/hops/view?org_id="+strconv.FormatInt(org.ID, 10)+"&hop_id="+strconv.FormatInt(hop.ID, 10)), 200)
		requireBodyContains(t, ownerBody, "Offers to help")
		requireBodyContains(t, ownerBody, helperDisplayName)
		requireBodyContains(t, ownerBody, "action=\"/hops/offers/accept\"")
		requireBodyContains(t, ownerBody, "action=\"/hops/offers/decline\"")

		helperBody := requireStatus(t, helper.Get("/hops/view?org_id="+strconv.FormatInt(org.ID, 10)+"&hop_id="+strconv.FormatInt(hop.ID, 10)), 200)
		requireBodyNotContains(t, helperBody, "Offers to help")
		requireBodyNotContains(t, helperBody, "action=\"/hops/offers/accept\"")

		memberBody := requireStatus(t, member.Get("/hops/view?org_id="+strconv.FormatInt(org.ID, 10)+"&hop_id="+strconv.FormatInt(hop.ID, 10)), 200)
		requireBodyNotContains(t, memberBody, "Offers to help")
		requireBodyNotContains(t, memberBody, "action=\"/hops/offers/accept\"")
	})

	t.Run("HOP-06d accepting an offer from hop details accepts one helper and declines the rest", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner", "helper", "member")
		hop, err := service.CreateHop(ctx, db, service.CreateHopParams{
			OrganizationID: org.ID,
			MemberID:       members["owner"].Member.ID,
			Title:          "Offer detail accept " + suffix,
			Details:        "Accepting from details should deny the other offers.",
			EstimatedHours: 1,
			NeededByKind:   types.HopNeededByAnytime,
			IsPrivate:      false,
		})
		if err != nil {
			t.Fatalf("create hop: %v", err)
		}
		if err := service.OfferHopHelp(ctx, db, service.OfferHopParams{
			OrganizationID: org.ID,
			HopID:          hop.ID,
			OffererID:      members["helper"].Member.ID,
			OffererName:    "Helper One",
		}); err != nil {
			t.Fatalf("offer hop helper: %v", err)
		}
		if err := service.OfferHopHelp(ctx, db, service.OfferHopParams{
			OrganizationID: org.ID,
			HopID:          hop.ID,
			OffererID:      members["member"].Member.ID,
			OffererName:    "Helper Two",
		}); err != nil {
			t.Fatalf("offer hop member: %v", err)
		}

		server := newHTTPServer(t, db)
		owner := newTestActor(t, "owner", server.URL, members["owner"].Member.Email, members["owner"].Password)
		owner.Login()

		redirectTo := "/hops/view?org_id=" + strconv.FormatInt(org.ID, 10) + "&hop_id=" + strconv.FormatInt(hop.ID, 10)
		loc := requireRedirectPath(t, owner.PostForm("/hops/offers/accept", formKV(
			"org_id", strconv.FormatInt(org.ID, 10),
			"hop_id", strconv.FormatInt(hop.ID, 10),
			"offer_member_id", strconv.FormatInt(members["helper"].Member.ID, 10),
			"redirect_to", redirectTo,
		)), "/hops/view")
		requireQueryValue(t, loc, "success", "Offer accepted.")

		hop, err = service.GetHopByID(ctx, db, org.ID, hop.ID)
		if err != nil {
			t.Fatalf("load hop after accept: %v", err)
		}
		if hop.Status != types.HopStatusAccepted {
			t.Fatalf("expected hop status %q, got %q", types.HopStatusAccepted, hop.Status)
		}
		if hop.AcceptedBy == nil || *hop.AcceptedBy != members["helper"].Member.ID {
			t.Fatalf("expected accepted_by=%d, got %v", members["helper"].Member.ID, hop.AcceptedBy)
		}

		pending, err := service.HasPendingHopOffer(ctx, db, hop.ID, members["member"].Member.ID)
		if err != nil {
			t.Fatalf("check remaining pending offer: %v", err)
		}
		if pending {
			t.Fatalf("expected non-selected offer to be denied")
		}

		helperMessages, err := service.ListMessages(ctx, db, members["helper"].Member.ID)
		if err != nil {
			t.Fatalf("list accepted helper messages: %v", err)
		}
		foundAccepted := false
		for _, msg := range helperMessages {
			if strings.HasPrefix(msg.Subject, "Accepted:") {
				foundAccepted = true
				break
			}
		}
		if !foundAccepted {
			t.Fatalf("expected accepted helper to receive acceptance message")
		}

		memberMessages, err := service.ListMessages(ctx, db, members["member"].Member.ID)
		if err != nil {
			t.Fatalf("list declined helper messages: %v", err)
		}
		foundDeclined := false
		for _, msg := range memberMessages {
			if strings.HasPrefix(msg.Subject, "Declined:") {
				foundDeclined = true
				break
			}
		}
		if !foundDeclined {
			t.Fatalf("expected non-selected helper to receive decline message")
		}
	})

	t.Run("HOP-06e declining an offer from hop details keeps the hop open", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner", "helper")
		hop, err := service.CreateHop(ctx, db, service.CreateHopParams{
			OrganizationID: org.ID,
			MemberID:       members["owner"].Member.ID,
			Title:          "Offer detail decline " + suffix,
			Details:        "Declining from details should keep the hop open.",
			EstimatedHours: 1,
			NeededByKind:   types.HopNeededByAnytime,
			IsPrivate:      false,
		})
		if err != nil {
			t.Fatalf("create hop: %v", err)
		}
		if err := service.OfferHopHelp(ctx, db, service.OfferHopParams{
			OrganizationID: org.ID,
			HopID:          hop.ID,
			OffererID:      members["helper"].Member.ID,
			OffererName:    "Helper Person",
		}); err != nil {
			t.Fatalf("offer hop: %v", err)
		}

		server := newHTTPServer(t, db)
		owner := newTestActor(t, "owner", server.URL, members["owner"].Member.Email, members["owner"].Password)
		owner.Login()

		redirectTo := "/hops/view?org_id=" + strconv.FormatInt(org.ID, 10) + "&hop_id=" + strconv.FormatInt(hop.ID, 10)
		loc := requireRedirectPath(t, owner.PostForm("/hops/offers/decline", formKV(
			"org_id", strconv.FormatInt(org.ID, 10),
			"hop_id", strconv.FormatInt(hop.ID, 10),
			"offer_member_id", strconv.FormatInt(members["helper"].Member.ID, 10),
			"redirect_to", redirectTo,
		)), "/hops/view")
		requireQueryValue(t, loc, "success", "Offer declined.")

		hop, err = service.GetHopByID(ctx, db, org.ID, hop.ID)
		if err != nil {
			t.Fatalf("load hop after decline: %v", err)
		}
		if hop.Status != types.HopStatusOpen {
			t.Fatalf("expected hop to remain open, got %q", hop.Status)
		}

		pending, err := service.HasPendingHopOffer(ctx, db, hop.ID, members["helper"].Member.ID)
		if err != nil {
			t.Fatalf("check declined pending offer: %v", err)
		}
		if pending {
			t.Fatalf("expected helper offer to be declined")
		}

		body := requireStatus(t, owner.Get(redirectTo), 200)
		requireBodyContains(t, body, "No one has offered help yet.")
	})

	t.Run("HOP-07 duplicate offer is rejected and HOP-08 offering own hop is rejected", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner", "helper")
		hop, err := service.CreateHop(ctx, db, service.CreateHopParams{
			OrganizationID: org.ID,
			MemberID:       members["owner"].Member.ID,
			Title:          "Offer Hop " + suffix,
			Details:        "Offer flow test.",
			EstimatedHours: 1,
			NeededByKind:   types.HopNeededByAnytime,
			IsPrivate:      false,
		})
		if err != nil {
			t.Fatalf("create hop: %v", err)
		}
		server := newHTTPServer(t, db)
		helper := newTestActor(t, "helper", server.URL, members["helper"].Member.Email, members["helper"].Password)
		owner := newTestActor(t, "owner", server.URL, members["owner"].Member.Email, members["owner"].Password)
		helper.Login()
		owner.Login()

		requireRedirectPath(t, helper.PostForm("/hops/offer", formKV(
			"org_id", strconv.FormatInt(org.ID, 10),
			"hop_id", strconv.FormatInt(hop.ID, 10),
		)), "/my-hopshare")

		loc := requireRedirectPath(t, helper.PostForm("/hops/offer", formKV(
			"org_id", strconv.FormatInt(org.ID, 10),
			"hop_id", strconv.FormatInt(hop.ID, 10),
		)), "/my-hopshare")
		requireQueryValue(t, loc, "error", "You've already offered to help with this hop.")

		loc = requireRedirectPath(t, owner.PostForm("/hops/offer", formKV(
			"org_id", strconv.FormatInt(org.ID, 10),
			"hop_id", strconv.FormatInt(hop.ID, 10),
		)), "/my-hopshare")
		requireQueryValue(t, loc, "error", "Could not send offer.")
	})

	t.Run("HOP-15 cancel open hop by creator succeeds", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner")
		hop, err := service.CreateHop(ctx, db, service.CreateHopParams{
			OrganizationID: org.ID,
			MemberID:       members["owner"].Member.ID,
			Title:          "Cancel Open Hop " + suffix,
			Details:        "Cancel open flow.",
			EstimatedHours: 1,
			NeededByKind:   types.HopNeededByAnytime,
			IsPrivate:      false,
		})
		if err != nil {
			t.Fatalf("create hop: %v", err)
		}
		server := newHTTPServer(t, db)
		owner := newTestActor(t, "owner", server.URL, members["owner"].Member.Email, members["owner"].Password)
		owner.Login()
		loc := requireRedirectPath(t, owner.PostForm("/hops/cancel", formKV(
			"org_id", strconv.FormatInt(org.ID, 10),
			"hop_id", strconv.FormatInt(hop.ID, 10),
		)), "/my-hopshare")
		requireQueryValue(t, loc, "success", "Hop canceled.")
	})

	t.Run("HOP-15b cancel open hop clears pending offers and action messages", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner", "helper")
		hop, err := service.CreateHop(ctx, db, service.CreateHopParams{
			OrganizationID: org.ID,
			MemberID:       members["owner"].Member.ID,
			Title:          "Cancel open clears offers " + suffix,
			Details:        "Cancel should clear pending offers and actions.",
			EstimatedHours: 1,
			NeededByKind:   types.HopNeededByAnytime,
			IsPrivate:      false,
		})
		if err != nil {
			t.Fatalf("create hop: %v", err)
		}

		server := newHTTPServer(t, db)
		helper := newTestActor(t, "helper", server.URL, members["helper"].Member.Email, members["helper"].Password)
		owner := newTestActor(t, "owner", server.URL, members["owner"].Member.Email, members["owner"].Password)
		helper.Login()
		owner.Login()

		offerLoc := requireRedirectPath(t, helper.PostForm("/hops/offer", formKV(
			"org_id", strconv.FormatInt(org.ID, 10),
			"hop_id", strconv.FormatInt(hop.ID, 10),
		)), "/my-hopshare")
		requireQueryValue(t, offerLoc, "success", "Offer sent.")

		cancelLoc := requireRedirectPath(t, owner.PostForm("/hops/cancel", formKV(
			"org_id", strconv.FormatInt(org.ID, 10),
			"hop_id", strconv.FormatInt(hop.ID, 10),
		)), "/my-hopshare")
		requireQueryValue(t, cancelLoc, "success", "Hop canceled.")

		var offerStatus sql.NullString
		if err := db.QueryRowContext(ctx, `
			SELECT status
			FROM hop_help_offers
			WHERE hop_id = $1 AND member_id = $2
		`, hop.ID, members["helper"].Member.ID).Scan(&offerStatus); err != nil {
			t.Fatalf("load hop offer status after cancel: %v", err)
		}
		if !offerStatus.Valid || offerStatus.String != types.HopOfferStatusDenied {
			t.Fatalf("expected offer status %q after cancel, got %+v", types.HopOfferStatusDenied, offerStatus)
		}

		ownerName := strings.TrimSpace(members["owner"].Member.FirstName + " " + members["owner"].Member.LastName)
		if ownerName == "" {
			ownerName = members["owner"].Member.Email
		}
		wantSubject := ownerName + " has canceled their Hop, " + hop.Title
		helperMessages, err := service.ListMessages(ctx, db, members["helper"].Member.ID)
		if err != nil {
			t.Fatalf("list helper messages after cancel: %v", err)
		}
		var foundCancelInfo bool
		for _, msg := range helperMessages {
			if msg.Subject == wantSubject {
				foundCancelInfo = true
				break
			}
		}
		if !foundCancelInfo {
			t.Fatalf("expected helper cancellation message with subject %q", wantSubject)
		}

		var helperCancelNotificationCount int
		if err := db.QueryRowContext(ctx, `
			SELECT COUNT(*)
			FROM member_notifications
			WHERE member_id = $1 AND href = $2 AND text LIKE $3
		`, members["helper"].Member.ID, "/messages", "%offer is no longer needed.%").Scan(&helperCancelNotificationCount); err != nil {
			t.Fatalf("count helper cancel notifications: %v", err)
		}
		if helperCancelNotificationCount == 0 {
			t.Fatalf("expected helper cancellation notification with /messages link")
		}
	})

	t.Run("HOP-15c cancel open hop notifies all pending offerers", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner", "helper1", "helper2")
		hop, err := service.CreateHop(ctx, db, service.CreateHopParams{
			OrganizationID: org.ID,
			MemberID:       members["owner"].Member.ID,
			Title:          "Cancel open notifies all helpers " + suffix,
			Details:        "All pending offerers should be notified when requester cancels.",
			EstimatedHours: 1,
			NeededByKind:   types.HopNeededByAnytime,
			IsPrivate:      false,
		})
		if err != nil {
			t.Fatalf("create hop: %v", err)
		}

		server := newHTTPServer(t, db)
		owner := newTestActor(t, "owner", server.URL, members["owner"].Member.Email, members["owner"].Password)
		helper1 := newTestActor(t, "helper1", server.URL, members["helper1"].Member.Email, members["helper1"].Password)
		helper2 := newTestActor(t, "helper2", server.URL, members["helper2"].Member.Email, members["helper2"].Password)
		owner.Login()
		helper1.Login()
		helper2.Login()

		loc := requireRedirectPath(t, helper1.PostForm("/hops/offer", formKV(
			"org_id", strconv.FormatInt(org.ID, 10),
			"hop_id", strconv.FormatInt(hop.ID, 10),
		)), "/my-hopshare")
		requireQueryValue(t, loc, "success", "Offer sent.")
		loc = requireRedirectPath(t, helper2.PostForm("/hops/offer", formKV(
			"org_id", strconv.FormatInt(org.ID, 10),
			"hop_id", strconv.FormatInt(hop.ID, 10),
		)), "/my-hopshare")
		requireQueryValue(t, loc, "success", "Offer sent.")

		cancelLoc := requireRedirectPath(t, owner.PostForm("/hops/cancel", formKV(
			"org_id", strconv.FormatInt(org.ID, 10),
			"hop_id", strconv.FormatInt(hop.ID, 10),
		)), "/my-hopshare")
		requireQueryValue(t, cancelLoc, "success", "Hop canceled.")

		ownerName := strings.TrimSpace(members["owner"].Member.FirstName + " " + members["owner"].Member.LastName)
		if ownerName == "" {
			ownerName = members["owner"].Member.Email
		}
		wantSubject := ownerName + " has canceled their Hop, " + hop.Title

		helperIDs := []int64{members["helper1"].Member.ID, members["helper2"].Member.ID}
		for _, helperID := range helperIDs {
			msgs, listErr := service.ListMessages(ctx, db, helperID)
			if listErr != nil {
				t.Fatalf("list helper messages helper=%d: %v", helperID, listErr)
			}
			var hasCancelInfo bool
			for _, msg := range msgs {
				if msg.Subject == wantSubject {
					hasCancelInfo = true
					break
				}
			}
			if !hasCancelInfo {
				t.Fatalf("expected helper=%d cancellation message with subject %q", helperID, wantSubject)
			}

			var notificationCount int
			if err := db.QueryRowContext(ctx, `
				SELECT COUNT(*)
				FROM member_notifications
				WHERE member_id = $1 AND href = $2 AND text LIKE $3
			`, helperID, "/messages", "%offer is no longer needed.%").Scan(&notificationCount); err != nil {
				t.Fatalf("count helper cancellation notifications helper=%d: %v", helperID, err)
			}
			if notificationCount == 0 {
				t.Fatalf("expected helper=%d cancellation notification with /messages link", helperID)
			}
		}
	})

	t.Run("HOP-16 cancel accepted hop by creator succeeds", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner", "helper")
		hop := createAcceptedHopViaOffer(t, ctx, db, org.ID, members["owner"].Member.ID, members["helper"].Member.ID, "Cancel Accepted Hop "+suffix)
		server := newHTTPServer(t, db)
		owner := newTestActor(t, "owner", server.URL, members["owner"].Member.Email, members["owner"].Password)
		owner.Login()
		loc := requireRedirectPath(t, owner.PostForm("/hops/cancel", formKV(
			"org_id", strconv.FormatInt(org.ID, 10),
			"hop_id", strconv.FormatInt(hop.ID, 10),
		)), "/my-hopshare")
		requireQueryValue(t, loc, "success", "Hop canceled.")

		ownerName := strings.TrimSpace(members["owner"].Member.FirstName + " " + members["owner"].Member.LastName)
		if ownerName == "" {
			ownerName = members["owner"].Member.Email
		}
		wantSubject := ownerName + " has canceled their Hop, " + hop.Title
		wantBody := "We wanted to let you know that " + ownerName + " has canceled their Hop titled, " + hop.Title + ". Thanks anyway for the offer to help! Why not go check for some other Hops that need help?"

		helperMessages, err := service.ListMessages(ctx, db, members["helper"].Member.ID)
		if err != nil {
			t.Fatalf("list helper messages: %v", err)
		}

		var cancelNoticeID int64
		for _, msg := range helperMessages {
			if msg.Subject == wantSubject {
				cancelNoticeID = msg.ID
				break
			}
		}
		if cancelNoticeID == 0 {
			t.Fatalf("expected helper cancellation message with subject %q", wantSubject)
		}

		cancelNotice, err := service.GetMessageForMember(ctx, db, cancelNoticeID, members["helper"].Member.ID)
		if err != nil {
			t.Fatalf("load helper cancellation message: %v", err)
		}
		if cancelNotice.Body != wantBody {
			t.Fatalf("unexpected helper cancellation body: got %q want %q", cancelNotice.Body, wantBody)
		}
	})

	t.Run("HOP-17 cancel by non-creator is rejected", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner", "helper")
		hop, err := service.CreateHop(ctx, db, service.CreateHopParams{
			OrganizationID: org.ID,
			MemberID:       members["owner"].Member.ID,
			Title:          "Cancel Non Creator " + suffix,
			Details:        "Cancel forbidden test.",
			EstimatedHours: 1,
			NeededByKind:   types.HopNeededByAnytime,
			IsPrivate:      false,
		})
		if err != nil {
			t.Fatalf("create hop: %v", err)
		}
		server := newHTTPServer(t, db)
		helper := newTestActor(t, "helper", server.URL, members["helper"].Member.Email, members["helper"].Password)
		helper.Login()
		loc := requireRedirectPath(t, helper.PostForm("/hops/cancel", formKV(
			"org_id", strconv.FormatInt(org.ID, 10),
			"hop_id", strconv.FormatInt(hop.ID, 10),
		)), "/my-hopshare")
		requireQueryValue(t, loc, "error", "Could not cancel hop.")
	})

	t.Run("HOP-12 complete accepted hop by requester succeeds", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner", "helper")
		hop := createAcceptedHopViaOffer(t, ctx, db, org.ID, members["owner"].Member.ID, members["helper"].Member.ID, "Complete by requester "+suffix)
		server := newHTTPServer(t, db)
		owner := newTestActor(t, "owner", server.URL, members["owner"].Member.Email, members["owner"].Password)
		owner.Login()
		loc := requireRedirectPath(t, owner.PostForm("/hops/complete", formKV(
			"org_id", strconv.FormatInt(org.ID, 10),
			"hop_id", strconv.FormatInt(hop.ID, 10),
			"completed_hours", "1",
			"completion_comment", "Requester completed.",
		)), "/my-hopshare")
		requireQueryValue(t, loc, "success", "Hop completed.")
	})

	t.Run("HOP-12b helper cannot increase awarded hours when completing hop", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner", "helper")
		hopTitle := "Helper cannot set completion hours " + suffix
		hop, err := service.CreateHop(ctx, db, service.CreateHopParams{
			OrganizationID: org.ID,
			MemberID:       members["owner"].Member.ID,
			Title:          hopTitle,
			Details:        "Estimated one hour.",
			EstimatedHours: 1,
			NeededByKind:   types.HopNeededByAnytime,
			IsPrivate:      false,
		})
		if err != nil {
			t.Fatalf("create hop: %v", err)
		}
		if err := service.OfferHopHelp(ctx, db, service.OfferHopParams{
			OrganizationID: org.ID,
			HopID:          hop.ID,
			OffererID:      members["helper"].Member.ID,
			OffererName:    "helper",
		}); err != nil {
			t.Fatalf("offer hop help: %v", err)
		}
		if err := service.AcceptPendingHopOffer(ctx, db, hop.ID, members["owner"].Member.ID, members["helper"].Member.ID, "owner", "accepted"); err != nil {
			t.Fatalf("accept pending hop offer: %v", err)
		}

		server := newHTTPServer(t, db)
		helper := newTestActor(t, "helper", server.URL, members["helper"].Member.Email, members["helper"].Password)
		helper.Login()
		loc := requireRedirectPath(t, helper.PostForm("/hops/complete", formKV(
			"org_id", strconv.FormatInt(org.ID, 10),
			"hop_id", strconv.FormatInt(hop.ID, 10),
			"completed_hours", "8",
			"completion_comment", "Helper completed.",
		)), "/my-hopshare")
		requireQueryValue(t, loc, "success", "Hop completed.")

		updated, err := service.GetHopByID(ctx, db, org.ID, hop.ID)
		if err != nil {
			t.Fatalf("load completed hop: %v", err)
		}
		if updated.CompletedHours == nil || *updated.CompletedHours != 1 {
			t.Fatalf("expected completed_hours to stay at estimated 1, got %v", updated.CompletedHours)
		}
	})

	t.Run("HOP-12c requester can set custom awarded hours when completing hop", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner", "helper")
		hopTitle := "Requester sets completion hours " + suffix
		hop, err := service.CreateHop(ctx, db, service.CreateHopParams{
			OrganizationID: org.ID,
			MemberID:       members["owner"].Member.ID,
			Title:          hopTitle,
			Details:        "Requester should be able to override hours.",
			EstimatedHours: 1,
			NeededByKind:   types.HopNeededByAnytime,
			IsPrivate:      false,
		})
		if err != nil {
			t.Fatalf("create hop: %v", err)
		}
		if err := service.OfferHopHelp(ctx, db, service.OfferHopParams{
			OrganizationID: org.ID,
			HopID:          hop.ID,
			OffererID:      members["helper"].Member.ID,
			OffererName:    "helper",
		}); err != nil {
			t.Fatalf("offer hop help: %v", err)
		}
		if err := service.AcceptPendingHopOffer(ctx, db, hop.ID, members["owner"].Member.ID, members["helper"].Member.ID, "owner", "accepted"); err != nil {
			t.Fatalf("accept pending hop offer: %v", err)
		}

		server := newHTTPServer(t, db)
		owner := newTestActor(t, "owner", server.URL, members["owner"].Member.Email, members["owner"].Password)
		owner.Login()
		loc := requireRedirectPath(t, owner.PostForm("/hops/complete", formKV(
			"org_id", strconv.FormatInt(org.ID, 10),
			"hop_id", strconv.FormatInt(hop.ID, 10),
			"completed_hours", "4",
			"completion_comment", "Requester completed.",
		)), "/my-hopshare")
		requireQueryValue(t, loc, "success", "Hop completed.")

		updated, err := service.GetHopByID(ctx, db, org.ID, hop.ID)
		if err != nil {
			t.Fatalf("load completed hop: %v", err)
		}
		if updated.CompletedHours == nil || *updated.CompletedHours != 4 {
			t.Fatalf("expected completed_hours=4 for requester completion, got %v", updated.CompletedHours)
		}
	})

	t.Run("HOP-12d complete from hop details redirects back to hop details", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner", "helper")
		hop := createAcceptedHopViaOffer(t, ctx, db, org.ID, members["owner"].Member.ID, members["helper"].Member.ID, "Complete from details "+suffix)

		server := newHTTPServer(t, db)
		helper := newTestActor(t, "helper", server.URL, members["helper"].Member.Email, members["helper"].Password)
		helper.Login()

		redirectTo := "/hops/view?org_id=" + strconv.FormatInt(org.ID, 10) + "&hop_id=" + strconv.FormatInt(hop.ID, 10)
		loc := requireRedirectPath(t, helper.PostForm("/hops/complete", formKV(
			"org_id", strconv.FormatInt(org.ID, 10),
			"hop_id", strconv.FormatInt(hop.ID, 10),
			"redirect_to", redirectTo,
			"completion_comment", "Completed from details.",
		)), "/hops/view")
		requireQueryValue(t, loc, "org_id", strconv.FormatInt(org.ID, 10))
		requireQueryValue(t, loc, "hop_id", strconv.FormatInt(hop.ID, 10))
		requireQueryValue(t, loc, "success", "Hop completed.")
	})

	t.Run("HOP-12e completion transfer is capped at organization maximum balance", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner", "helper")
		hopTitle := "Completion capped by max balance " + suffix
		hop, err := service.CreateHop(ctx, db, service.CreateHopParams{
			OrganizationID: org.ID,
			MemberID:       members["owner"].Member.ID,
			Title:          hopTitle,
			Details:        "Cap completion hours by helper max balance.",
			EstimatedHours: 8,
			NeededByKind:   types.HopNeededByAnytime,
			IsPrivate:      false,
		})
		if err != nil {
			t.Fatalf("create hop: %v", err)
		}
		if err := service.OfferHopHelp(ctx, db, service.OfferHopParams{
			OrganizationID: org.ID,
			HopID:          hop.ID,
			OffererID:      members["helper"].Member.ID,
			OffererName:    "helper",
		}); err != nil {
			t.Fatalf("offer hop help: %v", err)
		}
		if err := service.AcceptPendingHopOffer(ctx, db, hop.ID, members["owner"].Member.ID, members["helper"].Member.ID, "owner", "accepted"); err != nil {
			t.Fatalf("accept pending hop offer: %v", err)
		}

		server := newHTTPServer(t, db)
		owner := newTestActor(t, "owner", server.URL, members["owner"].Member.Email, members["owner"].Password)
		owner.Login()
		loc := requireRedirectPath(t, owner.PostForm("/hops/complete", formKV(
			"org_id", strconv.FormatInt(org.ID, 10),
			"hop_id", strconv.FormatInt(hop.ID, 10),
			"completed_hours", "8",
			"completion_comment", "Requester completed with cap.",
		)), "/my-hopshare")
		requireQueryValue(t, loc, "success", "Hop completed. 5 hour(s) were transferred instead of 8 to keep the helper below the organization's maximum balance (10).")

		updated, err := service.GetHopByID(ctx, db, org.ID, hop.ID)
		if err != nil {
			t.Fatalf("load completed hop: %v", err)
		}
		if updated.CompletedHours == nil || *updated.CompletedHours != 5 {
			t.Fatalf("expected completed_hours=5 after cap, got %v", updated.CompletedHours)
		}
	})

	t.Run("HOP-13 complete hop missing comment is rejected", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner", "helper")
		hop := createAcceptedHopViaOffer(t, ctx, db, org.ID, members["owner"].Member.ID, members["helper"].Member.ID, "Complete missing comment "+suffix)
		server := newHTTPServer(t, db)
		helper := newTestActor(t, "helper", server.URL, members["helper"].Member.Email, members["helper"].Password)
		helper.Login()
		loc := requireRedirectPath(t, helper.PostForm("/hops/complete", formKV(
			"org_id", strconv.FormatInt(org.ID, 10),
			"hop_id", strconv.FormatInt(hop.ID, 10),
			"completed_hours", "1",
			"completion_comment", "",
		)), "/my-hopshare")
		requireQueryValue(t, loc, "error", "Could not complete hop.")
	})

	t.Run("HOP-14 complete hop invalid state is rejected", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner")
		hop, err := service.CreateHop(ctx, db, service.CreateHopParams{
			OrganizationID: org.ID,
			MemberID:       members["owner"].Member.ID,
			Title:          "Complete invalid state " + suffix,
			Details:        "Open hop cannot complete.",
			EstimatedHours: 1,
			NeededByKind:   types.HopNeededByAnytime,
			IsPrivate:      false,
		})
		if err != nil {
			t.Fatalf("create hop: %v", err)
		}
		server := newHTTPServer(t, db)
		owner := newTestActor(t, "owner", server.URL, members["owner"].Member.Email, members["owner"].Password)
		owner.Login()
		loc := requireRedirectPath(t, owner.PostForm("/hops/complete", formKV(
			"org_id", strconv.FormatInt(org.ID, 10),
			"hop_id", strconv.FormatInt(hop.ID, 10),
			"completion_comment", "Trying to complete open hop.",
		)), "/my-hopshare")
		requireQueryValue(t, loc, "error", "Could not complete hop.")
	})

	t.Run("HOP-18 privacy toggle normal form redirects and updates", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner", "helper")
		hop := createAcceptedHopViaOffer(t, ctx, db, org.ID, members["owner"].Member.ID, members["helper"].Member.ID, "Privacy hop "+suffix)
		server := newHTTPServer(t, db)
		owner := newTestActor(t, "owner", server.URL, members["owner"].Member.Email, members["owner"].Password)
		owner.Login()
		loc := requireRedirectPath(t, owner.PostForm("/hops/privacy", formKV(
			"org_id", strconv.FormatInt(org.ID, 10),
			"hop_id", strconv.FormatInt(hop.ID, 10),
			"is_private", "true",
		)), "/hops/view")
		requireQueryValue(t, loc, "hop_id", strconv.FormatInt(hop.ID, 10))

		updated, err := service.GetHopByID(ctx, db, org.ID, hop.ID)
		if err != nil {
			t.Fatalf("load hop: %v", err)
		}
		if !updated.IsPrivate {
			t.Fatalf("expected hop privacy to be true")
		}
	})

	t.Run("HOP-19 privacy toggle with HX-Request returns 204", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner", "helper")
		hop := createAcceptedHopViaOffer(t, ctx, db, org.ID, members["owner"].Member.ID, members["helper"].Member.ID, "HX privacy hop "+suffix)
		server := newHTTPServer(t, db)
		owner := newTestActor(t, "owner", server.URL, members["owner"].Member.Email, members["owner"].Password)
		owner.Login()
		resp := owner.Request("POST", "/hops/privacy", strings.NewReader(formKV(
			"org_id", strconv.FormatInt(org.ID, 10),
			"hop_id", strconv.FormatInt(hop.ID, 10),
			"is_private", "false",
			"csrf_token", owner.ensureCSRFToken(),
		).Encode()), map[string]string{
			"Content-Type": "application/x-www-form-urlencoded",
			"HX-Request":   "true",
		})
		requireStatus(t, resp, 204)
	})

	t.Run("HOP-20 privacy toggle non-associated member is forbidden", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner", "helper", "member")
		hop := createAcceptedHopViaOffer(t, ctx, db, org.ID, members["owner"].Member.ID, members["helper"].Member.ID, "Privacy forbidden hop "+suffix)
		server := newHTTPServer(t, db)
		member := newTestActor(t, "member", server.URL, members["member"].Member.Email, members["member"].Password)
		member.Login()
		requireStatus(t, member.PostForm("/hops/privacy", formKV(
			"org_id", strconv.FormatInt(org.ID, 10),
			"hop_id", strconv.FormatInt(hop.ID, 10),
			"is_private", "false",
		)), 403)
	})

	t.Run("HOP-21 privacy toggle invalid value returns 400", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner", "helper")
		hop := createAcceptedHopViaOffer(t, ctx, db, org.ID, members["owner"].Member.ID, members["helper"].Member.ID, "Privacy invalid value "+suffix)
		server := newHTTPServer(t, db)
		owner := newTestActor(t, "owner", server.URL, members["owner"].Member.Email, members["owner"].Password)
		owner.Login()
		requireStatus(t, owner.PostForm("/hops/privacy", formKV(
			"org_id", strconv.FormatInt(org.ID, 10),
			"hop_id", strconv.FormatInt(hop.ID, 10),
			"is_private", "invalid",
		)), 400)
	})

	t.Run("HOP-22 public hop comment by any org member succeeds and HOP-24 private associated comment succeeds", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner", "member")
		publicHop, err := service.CreateHop(ctx, db, service.CreateHopParams{
			OrganizationID: org.ID,
			MemberID:       members["owner"].Member.ID,
			Title:          "Public comment hop " + suffix,
			Details:        "Public.",
			EstimatedHours: 1,
			NeededByKind:   types.HopNeededByAnytime,
			IsPrivate:      false,
		})
		if err != nil {
			t.Fatalf("create public hop: %v", err)
		}
		privateHop, err := service.CreateHop(ctx, db, service.CreateHopParams{
			OrganizationID: org.ID,
			MemberID:       members["owner"].Member.ID,
			Title:          "Private comment hop " + suffix,
			Details:        "Private.",
			EstimatedHours: 1,
			NeededByKind:   types.HopNeededByAnytime,
			IsPrivate:      true,
		})
		if err != nil {
			t.Fatalf("create private hop: %v", err)
		}
		server := newHTTPServer(t, db)
		member := newTestActor(t, "member", server.URL, members["member"].Member.Email, members["member"].Password)
		owner := newTestActor(t, "owner", server.URL, members["owner"].Member.Email, members["owner"].Password)
		member.Login()
		owner.Login()

		requireRedirectPath(t, member.PostForm("/hops/comments/create", formKV(
			"org_id", strconv.FormatInt(org.ID, 10),
			"hop_id", strconv.FormatInt(publicHop.ID, 10),
			"body", "Public hop comment from org member.",
		)), "/hops/view")

		requireRedirectPath(t, owner.PostForm("/hops/comments/create", formKV(
			"org_id", strconv.FormatInt(org.ID, 10),
			"hop_id", strconv.FormatInt(privateHop.ID, 10),
			"body", "Private hop comment from associated member.",
		)), "/hops/view")
	})

	t.Run("HOP-25 image upload by associated member succeeds", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner")
		hop, err := service.CreateHop(ctx, db, service.CreateHopParams{
			OrganizationID: org.ID,
			MemberID:       members["owner"].Member.ID,
			Title:          "Image upload hop " + suffix,
			Details:        "Image upload test.",
			EstimatedHours: 1,
			NeededByKind:   types.HopNeededByAnytime,
			IsPrivate:      false,
		})
		if err != nil {
			t.Fatalf("create hop: %v", err)
		}
		server := newHTTPServer(t, db)
		owner := newTestActor(t, "owner", server.URL, members["owner"].Member.Email, members["owner"].Password)
		owner.Login()
		requireRedirectPath(t, owner.PostMultipartWithFiles("/hops/images/upload", map[string]string{
			"org_id": strconv.FormatInt(org.ID, 10),
			"hop_id": strconv.FormatInt(hop.ID, 10),
		}, []multipartFile{{
			FieldName:   "image",
			FileName:    "hop.png",
			ContentType: "image/png",
			Data:        tinyPNGData(),
		}}), "/hops/view")
		images, err := service.ListHopImages(ctx, db, hop.ID)
		if err != nil {
			t.Fatalf("list hop images: %v", err)
		}
		if len(images) != 1 {
			t.Fatalf("expected 1 image, got %d", len(images))
		}
	})

	t.Run("HOP-26 invalid image upload cases are rejected", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner")
		hop, err := service.CreateHop(ctx, db, service.CreateHopParams{
			OrganizationID: org.ID,
			MemberID:       members["owner"].Member.ID,
			Title:          "Image invalid hop " + suffix,
			Details:        "Invalid image test.",
			EstimatedHours: 1,
			NeededByKind:   types.HopNeededByAnytime,
			IsPrivate:      false,
		})
		if err != nil {
			t.Fatalf("create hop: %v", err)
		}
		server := newHTTPServer(t, db)
		owner := newTestActor(t, "owner", server.URL, members["owner"].Member.Email, members["owner"].Password)
		owner.Login()

		requireStatus(t, owner.PostMultipartWithFiles("/hops/images/upload", map[string]string{
			"org_id": strconv.FormatInt(org.ID, 10),
			"hop_id": strconv.FormatInt(hop.ID, 10),
		}, []multipartFile{{
			FieldName:   "image",
			FileName:    "bad.txt",
			ContentType: "text/plain",
			Data:        []byte("bad image"),
		}}), 400)

		requireStatus(t, owner.PostMultipart("/hops/images/upload", map[string]string{
			"org_id": strconv.FormatInt(org.ID, 10),
			"hop_id": strconv.FormatInt(hop.ID, 10),
		}), 400)
	})

	t.Run("HOP-28 delete image by associated member succeeds and HOP-29 non-associated delete forbidden", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner", "member")
		hop, err := service.CreateHop(ctx, db, service.CreateHopParams{
			OrganizationID: org.ID,
			MemberID:       members["owner"].Member.ID,
			Title:          "Image delete hop " + suffix,
			Details:        "Delete image test.",
			EstimatedHours: 1,
			NeededByKind:   types.HopNeededByAnytime,
			IsPrivate:      false,
		})
		if err != nil {
			t.Fatalf("create hop: %v", err)
		}
		server := newHTTPServer(t, db)
		owner := newTestActor(t, "owner", server.URL, members["owner"].Member.Email, members["owner"].Password)
		member := newTestActor(t, "member", server.URL, members["member"].Member.Email, members["member"].Password)
		owner.Login()
		member.Login()

		requireRedirectPath(t, owner.PostMultipartWithFiles("/hops/images/upload", map[string]string{
			"org_id": strconv.FormatInt(org.ID, 10),
			"hop_id": strconv.FormatInt(hop.ID, 10),
		}, []multipartFile{{
			FieldName:   "image",
			FileName:    "hop.png",
			ContentType: "image/png",
			Data:        tinyPNGData(),
		}}), "/hops/view")

		images, err := service.ListHopImages(ctx, db, hop.ID)
		if err != nil {
			t.Fatalf("list images: %v", err)
		}
		if len(images) != 1 {
			t.Fatalf("expected 1 image, got %d", len(images))
		}

		requireStatus(t, member.PostForm("/hops/images/delete", formKV(
			"org_id", strconv.FormatInt(org.ID, 10),
			"hop_id", strconv.FormatInt(hop.ID, 10),
			"image_id", strconv.FormatInt(images[0].ID, 10),
		)), 403)

		requireRedirectPath(t, owner.PostForm("/hops/images/delete", formKV(
			"org_id", strconv.FormatInt(org.ID, 10),
			"hop_id", strconv.FormatInt(hop.ID, 10),
			"image_id", strconv.FormatInt(images[0].ID, 10),
		)), "/hops/view")
	})

	t.Run("HOP-30 and HOP-31 hop image fetch auth members vs non-members", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner", "member")
		nonMember := createSeededMember(t, ctx, db, "image_non_member", suffix)
		hop, err := service.CreateHop(ctx, db, service.CreateHopParams{
			OrganizationID: org.ID,
			MemberID:       members["owner"].Member.ID,
			Title:          "Image fetch hop " + suffix,
			Details:        "Fetch image test.",
			EstimatedHours: 1,
			NeededByKind:   types.HopNeededByAnytime,
			IsPrivate:      true,
		})
		if err != nil {
			t.Fatalf("create hop: %v", err)
		}
		if err := service.AddHopImage(ctx, db, hop.ID, members["owner"].Member.ID, "image/png", tinyPNGData()); err != nil {
			t.Fatalf("add hop image: %v", err)
		}
		images, err := service.ListHopImages(ctx, db, hop.ID)
		if err != nil || len(images) == 0 {
			t.Fatalf("list hop images: %v len=%d", err, len(images))
		}
		imageID := images[0].ID

		server := newHTTPServer(t, db)
		member := newTestActor(t, "member", server.URL, members["member"].Member.Email, members["member"].Password)
		outsider := newTestActor(t, "nonMember", server.URL, nonMember.Member.Email, nonMember.Password)
		member.Login()
		outsider.Login()

		requireStatus(t, member.Get("/hops/image?image_id="+strconv.FormatInt(imageID, 10)), 200)
		requireStatus(t, outsider.Get("/hops/image?image_id="+strconv.FormatInt(imageID, 10)), 404)
	})

	t.Run("HOP-32 /my-hops views requested/helped/offered", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner", "helper", "requester")
		requestedHop, err := service.CreateHop(ctx, db, service.CreateHopParams{
			OrganizationID: org.ID,
			MemberID:       members["requester"].Member.ID,
			Title:          "Requested View Hop " + suffix,
			Details:        "Requested view test.",
			EstimatedHours: 1,
			NeededByKind:   types.HopNeededByAnytime,
			IsPrivate:      false,
		})
		if err != nil {
			t.Fatalf("create requested hop: %v", err)
		}
		helpedHop := createAcceptedHopViaOffer(t, ctx, db, org.ID, members["owner"].Member.ID, members["helper"].Member.ID, "Helped View Hop "+suffix)
		offeredHop, err := service.CreateHop(ctx, db, service.CreateHopParams{
			OrganizationID: org.ID,
			MemberID:       members["owner"].Member.ID,
			Title:          "Offered View Hop " + suffix,
			Details:        "Offered view test.",
			EstimatedHours: 1,
			NeededByKind:   types.HopNeededByAnytime,
			IsPrivate:      false,
		})
		if err != nil {
			t.Fatalf("create offered hop: %v", err)
		}
		if err := service.OfferHopHelp(ctx, db, service.OfferHopParams{
			OrganizationID: org.ID,
			HopID:          offeredHop.ID,
			OffererID:      members["helper"].Member.ID,
			OffererName:    "helper",
		}); err != nil {
			t.Fatalf("offer hop: %v", err)
		}

		server := newHTTPServer(t, db)
		helper := newTestActor(t, "helper", server.URL, members["helper"].Member.Email, members["helper"].Password)
		requester := newTestActor(t, "requester", server.URL, members["requester"].Member.Email, members["requester"].Password)
		helper.Login()
		requester.Login()

		requestedBody := requireStatus(t, requester.Get("/my-hops?org_id="+strconv.FormatInt(org.ID, 10)+"&view=requested"), 200)
		requireBodyContains(t, requestedBody, requestedHop.Title)

		helpedBody := requireStatus(t, helper.Get("/my-hops?org_id="+strconv.FormatInt(org.ID, 10)+"&view=helped"), 200)
		requireBodyContains(t, helpedBody, helpedHop.Title)

		offeredBody := requireStatus(t, helper.Get("/my-hops?org_id="+strconv.FormatInt(org.ID, 10)+"&view=offered"), 200)
		requireBodyContains(t, offeredBody, offeredHop.Title)
	})

	t.Run("HOP-33 /my-hopshare org switch updates current organization", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		member := createSeededMember(t, ctx, db, "org_switch_member", suffix)
		ownerA := createSeededMember(t, ctx, db, "org_switch_owner_a", suffix)
		ownerB := createSeededMember(t, ctx, db, "org_switch_owner_b", suffix)
		orgA, err := service.CreateOrganization(ctx, db, "Switch Org A "+suffix, "City", "ST", "Desc", ownerA.Member.ID)
		if err != nil {
			t.Fatalf("create orgA: %v", err)
		}
		orgB, err := service.CreateOrganization(ctx, db, "Switch Org B "+suffix, "City", "ST", "Desc", ownerB.Member.ID)
		if err != nil {
			t.Fatalf("create orgB: %v", err)
		}
		approveMemberForOrganization(t, ctx, db, orgA.ID, ownerA.Member.ID, member.Member.ID)
		approveMemberForOrganization(t, ctx, db, orgB.ID, ownerB.Member.ID, member.Member.ID)

		server := newHTTPServer(t, db)
		actor := newTestActor(t, "member", server.URL, member.Member.Email, member.Password)
		actor.Login()

		requireStatus(t, actor.Get("/my-hopshare?org_id="+strconv.FormatInt(orgB.ID, 10)), 200)
		updated, err := service.GetMemberByID(ctx, db, member.Member.ID)
		if err != nil {
			t.Fatalf("load member: %v", err)
		}
		if updated.CurrentOrganization == nil || *updated.CurrentOrganization != orgB.ID {
			t.Fatalf("expected current organization %d, got %v", orgB.ID, updated.CurrentOrganization)
		}
	})

	t.Run("HOP-34 invalid org query values fallback without error", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner", "member")
		_ = org
		server := newHTTPServer(t, db)
		actor := newTestActor(t, "member", server.URL, members["member"].Member.Email, members["member"].Password)
		actor.Login()
		requireStatus(t, actor.Get("/my-hopshare?org_id=not-a-number"), 200)
		requireStatus(t, actor.Get("/my-hops?org_id=not-a-number"), 200)
	})

	t.Run("HOP-35 /my-hopshare expires stale hops", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner")
		pastDate := time.Now().UTC().AddDate(0, 0, -7)
		hop, err := service.CreateHop(ctx, db, service.CreateHopParams{
			OrganizationID: org.ID,
			MemberID:       members["owner"].Member.ID,
			Title:          "Expire hop " + suffix,
			Details:        "Should expire via my-hopshare render.",
			EstimatedHours: 1,
			NeededByKind:   types.HopNeededByOn,
			NeededByDate:   &pastDate,
			IsPrivate:      false,
		})
		if err != nil {
			t.Fatalf("create expiring hop: %v", err)
		}
		server := newHTTPServer(t, db)
		owner := newTestActor(t, "owner", server.URL, members["owner"].Member.Email, members["owner"].Password)
		owner.Login()
		requireStatus(t, owner.Get("/my-hopshare?org_id="+strconv.FormatInt(org.ID, 10)), 200)
		updated, err := service.GetHopByID(ctx, db, org.ID, hop.ID)
		if err != nil {
			t.Fatalf("load hop: %v", err)
		}
		if updated.Status != types.HopStatusExpired {
			t.Fatalf("expected expired hop status, got %q", updated.Status)
		}
	})

	t.Run("HOP-36 /my-hopshare shows active accepted hop for requester and helper", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner", "requester", "helper")
		title := "Dashboard accepted hop " + suffix
		createAcceptedHopViaOffer(t, ctx, db, org.ID, members["requester"].Member.ID, members["helper"].Member.ID, title)

		server := newHTTPServer(t, db)
		requester := newTestActor(t, "requester", server.URL, members["requester"].Member.Email, members["requester"].Password)
		helper := newTestActor(t, "helper", server.URL, members["helper"].Member.Email, members["helper"].Password)
		requester.Login()
		helper.Login()

		requesterBody := requireStatus(t, requester.Get("/my-hopshare?org_id="+strconv.FormatInt(org.ID, 10)), 200)
		requireBodyContains(t, requesterBody, "Active accepted hop")
		requireBodyContains(t, requesterBody, title)

		helperBody := requireStatus(t, helper.Get("/my-hopshare?org_id="+strconv.FormatInt(org.ID, 10)), 200)
		requireBodyContains(t, helperBody, "Active accepted hop")
		requireBodyContains(t, helperBody, title)
	})

	t.Run("HOP-37 /my-hopshare hides active accepted hop when user has no accepted hop", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner", "member")
		if _, err := service.CreateHop(ctx, db, service.CreateHopParams{
			OrganizationID: org.ID,
			MemberID:       members["owner"].Member.ID,
			Title:          "Open only hop " + suffix,
			Details:        "No accepted hop yet.",
			EstimatedHours: 1,
			NeededByKind:   types.HopNeededByAnytime,
			IsPrivate:      false,
		}); err != nil {
			t.Fatalf("create open hop: %v", err)
		}

		server := newHTTPServer(t, db)
		member := newTestActor(t, "member", server.URL, members["member"].Member.Email, members["member"].Password)
		member.Login()

		body := requireStatus(t, member.Get("/my-hopshare?org_id="+strconv.FormatInt(org.ID, 10)), 200)
		requireBodyNotContains(t, body, "Active accepted hop")
	})

	t.Run("HOP-38 /my-hopshare/organizations shows switch form when member has multiple organizations", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		member := createSeededMember(t, ctx, db, "org_switcher_member", suffix)
		ownerA := createSeededMember(t, ctx, db, "org_switcher_owner_a", suffix)
		ownerB := createSeededMember(t, ctx, db, "org_switcher_owner_b", suffix)

		orgA, err := service.CreateOrganization(ctx, db, "Switcher Org A "+suffix, "City", "ST", "Desc", ownerA.Member.ID)
		if err != nil {
			t.Fatalf("create orgA: %v", err)
		}
		orgB, err := service.CreateOrganization(ctx, db, "Switcher Org B "+suffix, "City", "ST", "Desc", ownerB.Member.ID)
		if err != nil {
			t.Fatalf("create orgB: %v", err)
		}
		approveMemberForOrganization(t, ctx, db, orgA.ID, ownerA.Member.ID, member.Member.ID)
		approveMemberForOrganization(t, ctx, db, orgB.ID, ownerB.Member.ID, member.Member.ID)

		server := newHTTPServer(t, db)
		actor := newTestActor(t, "member", server.URL, member.Member.Email, member.Password)
		actor.Login()

		body := requireStatus(t, actor.Get("/my-hopshare/organizations"), 200)
		requireBodyContains(t, body, "Switch organizations")
		requireBodyContains(t, body, orgA.Name)
		requireBodyContains(t, body, orgB.Name)
		requireBodyContains(t, body, `name="org_id"`)
		requireBodyContains(t, body, "Find more Organizations...")
	})

	t.Run("HOP-39 /my-hopshare/organizations hides switch form when member has one organization", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner")

		server := newHTTPServer(t, db)
		owner := newTestActor(t, "owner", server.URL, members["owner"].Member.Email, members["owner"].Password)
		owner.Login()

		body := requireStatus(t, owner.Get("/my-hopshare/organizations"), 200)
		requireBodyContains(t, body, org.Name)
		requireBodyContains(t, body, "Find more Organizations...")
		requireBodyNotContains(t, body, `name="org_id"`)
		requireBodyNotContains(t, body, "Choose a different organization")
	})
}

func createAcceptedHopViaOffer(t *testing.T, ctx context.Context, db *sql.DB, orgID, requesterID, helperID int64, title string) types.Hop {
	t.Helper()
	hop, err := service.CreateHop(ctx, db, service.CreateHopParams{
		OrganizationID: orgID,
		MemberID:       requesterID,
		Title:          title,
		Details:        "Accepted hop setup.",
		EstimatedHours: 1,
		NeededByKind:   types.HopNeededByAnytime,
		IsPrivate:      false,
	})
	if err != nil {
		t.Fatalf("create hop: %v", err)
	}
	if err := service.OfferHopHelp(ctx, db, service.OfferHopParams{
		OrganizationID: orgID,
		HopID:          hop.ID,
		OffererID:      helperID,
		OffererName:    "helper",
	}); err != nil {
		t.Fatalf("offer hop: %v", err)
	}
	if err := service.AcceptPendingHopOffer(ctx, db, hop.ID, requesterID, helperID, "requester", "accepted"); err != nil {
		t.Fatalf("accept offer: %v", err)
	}
	acceptedHop, err := service.GetHopByID(ctx, db, orgID, hop.ID)
	if err != nil {
		t.Fatalf("get accepted hop: %v", err)
	}
	return acceptedHop
}
