package http_test

import (
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"

	"hopshare/internal/service"
	"hopshare/internal/types"
)

func TestHTTPConcurrencyMatrix(t *testing.T) {
	db := requireHTTPTestDB(t)

	t.Run("RACE-01 same helper concurrent duplicate offers yields one pending offer", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner", "helper")
		hop, err := service.CreateHop(ctx, db, service.CreateHopParams{
			OrganizationID: org.ID,
			MemberID:       members["owner"].Member.ID,
			Title:          "Race duplicate offers " + suffix,
			Details:        "Concurrent duplicate offers.",
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

		responses := runConcurrently(2, func() *http.Response {
			return helper.PostForm("/hops/offer", formKV(
				"org_id", strconv.FormatInt(org.ID, 10),
				"hop_id", strconv.FormatInt(hop.ID, 10),
			))
		})
		successCount := 0
		for _, resp := range responses {
			loc := requireRedirectPathOneOf(t, resp, "/my-hopshare")
			if loc.Query().Get("success") == "Interest registered." {
				successCount++
			}
		}
		if successCount != 1 {
			t.Fatalf("expected exactly one successful offer, got %d", successCount)
		}

		pending, err := service.HasPendingHopOffer(ctx, db, hop.ID, members["helper"].Member.ID)
		if err != nil {
			t.Fatalf("check pending offer: %v", err)
		}
		if !pending {
			t.Fatalf("expected one pending offer")
		}
	})

	t.Run("RACE-02 two helpers offer concurrently create two pending offers", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner", "helper1", "helper2")
		hop, err := service.CreateHop(ctx, db, service.CreateHopParams{
			OrganizationID: org.ID,
			MemberID:       members["owner"].Member.ID,
			Title:          "Race two helpers offer " + suffix,
			Details:        "Concurrent different offers.",
			EstimatedHours: 1,
			NeededByKind:   types.HopNeededByAnytime,
			IsPrivate:      false,
		})
		if err != nil {
			t.Fatalf("create hop: %v", err)
		}
		server := newHTTPServer(t, db)
		helper1 := newTestActor(t, "helper1", server.URL, members["helper1"].Member.Email, members["helper1"].Password)
		helper2 := newTestActor(t, "helper2", server.URL, members["helper2"].Member.Email, members["helper2"].Password)
		helper1.Login()
		helper2.Login()

		var wg sync.WaitGroup
		wg.Add(2)
		var resp1, resp2 *http.Response
		go func() {
			defer wg.Done()
			resp1 = helper1.PostForm("/hops/offer", formKV("org_id", strconv.FormatInt(org.ID, 10), "hop_id", strconv.FormatInt(hop.ID, 10)))
		}()
		go func() {
			defer wg.Done()
			resp2 = helper2.PostForm("/hops/offer", formKV("org_id", strconv.FormatInt(org.ID, 10), "hop_id", strconv.FormatInt(hop.ID, 10)))
		}()
		wg.Wait()
		requireRedirectPath(t, resp1, "/my-hopshare")
		requireRedirectPath(t, resp2, "/my-hopshare")

		offerNotifications := 0
		for _, notification := range listMemberNotificationsForTest(t, ctx, db, members["owner"].Member.ID) {
			if strings.Contains(notification.Text, hop.Title) {
				if notification.Href != nil && *notification.Href == "/hops/view?org_id="+strconv.FormatInt(org.ID, 10)+"&hop_id="+strconv.FormatInt(hop.ID, 10) {
					offerNotifications++
				}
			}
		}
		if offerNotifications != 2 {
			t.Fatalf("expected 2 offer notifications, got %d", offerNotifications)
		}
	})

	t.Run("RACE-03 concurrent accepts on two hop offers yields one accepted helper", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner", "helper1", "helper2")
		hop, err := service.CreateHop(ctx, db, service.CreateHopParams{
			OrganizationID: org.ID,
			MemberID:       members["owner"].Member.ID,
			Title:          "Race concurrent accepts " + suffix,
			Details:        "Two helpers offered.",
			EstimatedHours: 1,
			NeededByKind:   types.HopNeededByAnytime,
			IsPrivate:      false,
		})
		if err != nil {
			t.Fatalf("create hop: %v", err)
		}
		if err := service.OfferHopHelp(ctx, db, service.OfferHopParams{OrganizationID: org.ID, HopID: hop.ID, OffererID: members["helper1"].Member.ID, OffererName: "helper1"}); err != nil {
			t.Fatalf("offer helper1: %v", err)
		}
		if err := service.OfferHopHelp(ctx, db, service.OfferHopParams{OrganizationID: org.ID, HopID: hop.ID, OffererID: members["helper2"].Member.ID, OffererName: "helper2"}); err != nil {
			t.Fatalf("offer helper2: %v", err)
		}
		server := newHTTPServer(t, db)
		owner := newTestActor(t, "owner", server.URL, members["owner"].Member.Email, members["owner"].Password)
		owner.Login()

		var wg sync.WaitGroup
		wg.Add(2)
		var resp1, resp2 *http.Response
		go func() {
			defer wg.Done()
			resp1 = owner.PostForm("/hops/offers/accept", formKV(
				"org_id", strconv.FormatInt(org.ID, 10),
				"hop_id", strconv.FormatInt(hop.ID, 10),
				"offer_member_id", strconv.FormatInt(members["helper1"].Member.ID, 10),
				"redirect_to", "/hops/view?org_id="+strconv.FormatInt(org.ID, 10)+"&hop_id="+strconv.FormatInt(hop.ID, 10),
			))
		}()
		go func() {
			defer wg.Done()
			resp2 = owner.PostForm("/hops/offers/accept", formKV(
				"org_id", strconv.FormatInt(org.ID, 10),
				"hop_id", strconv.FormatInt(hop.ID, 10),
				"offer_member_id", strconv.FormatInt(members["helper2"].Member.ID, 10),
				"redirect_to", "/hops/view?org_id="+strconv.FormatInt(org.ID, 10)+"&hop_id="+strconv.FormatInt(hop.ID, 10),
			))
		}()
		wg.Wait()
		requireRedirectPath(t, resp1, "/hops/view")
		requireRedirectPath(t, resp2, "/hops/view")

		reloaded, err := service.GetHopByID(ctx, db, org.ID, hop.ID)
		if err != nil {
			t.Fatalf("reload hop: %v", err)
		}
		if reloaded.Status != types.HopStatusAccepted || reloaded.AcceptedBy == nil {
			t.Fatalf("expected accepted hop with accepted_by set, got status=%q accepted_by=%v", reloaded.Status, reloaded.AcceptedBy)
		}
		if *reloaded.AcceptedBy != members["helper1"].Member.ID && *reloaded.AcceptedBy != members["helper2"].Member.ID {
			t.Fatalf("accepted_by not one of offered helpers: %d", *reloaded.AcceptedBy)
		}
	})

	t.Run("RACE-04 duplicate accept on same hop offer yields one success", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner", "helper")
		hop, err := service.CreateHop(ctx, db, service.CreateHopParams{
			OrganizationID: org.ID,
			MemberID:       members["owner"].Member.ID,
			Title:          "Race duplicate accept " + suffix,
			Details:        "Duplicate accept message.",
			EstimatedHours: 1,
			NeededByKind:   types.HopNeededByAnytime,
			IsPrivate:      false,
		})
		if err != nil {
			t.Fatalf("create hop: %v", err)
		}
		if err := service.OfferHopHelp(ctx, db, service.OfferHopParams{OrganizationID: org.ID, HopID: hop.ID, OffererID: members["helper"].Member.ID, OffererName: "helper"}); err != nil {
			t.Fatalf("offer helper: %v", err)
		}
		server := newHTTPServer(t, db)
		owner := newTestActor(t, "owner", server.URL, members["owner"].Member.Email, members["owner"].Password)
		owner.Login()

		responses := runConcurrently(2, func() *http.Response {
			return owner.PostForm("/hops/offers/accept", formKV(
				"org_id", strconv.FormatInt(org.ID, 10),
				"hop_id", strconv.FormatInt(hop.ID, 10),
				"offer_member_id", strconv.FormatInt(members["helper"].Member.ID, 10),
				"redirect_to", "/hops/view?org_id="+strconv.FormatInt(org.ID, 10)+"&hop_id="+strconv.FormatInt(hop.ID, 10),
			))
		})
		success := 0
		for _, resp := range responses {
			loc := requireRedirectPath(t, resp, "/hops/view")
			if loc.Query().Get("success") == "Offer accepted." {
				success++
			}
		}
		if success != 1 {
			t.Fatalf("expected one accept success, got %d", success)
		}
	})

	t.Run("RACE-05 cancel vs accept concurrent results in valid terminal/open transition", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner", "helper")
		hop, err := service.CreateHop(ctx, db, service.CreateHopParams{
			OrganizationID: org.ID,
			MemberID:       members["owner"].Member.ID,
			Title:          "Race cancel vs accept " + suffix,
			Details:        "Cancel accept race.",
			EstimatedHours: 1,
			NeededByKind:   types.HopNeededByAnytime,
			IsPrivate:      false,
		})
		if err != nil {
			t.Fatalf("create hop: %v", err)
		}
		if err := service.OfferHopHelp(ctx, db, service.OfferHopParams{OrganizationID: org.ID, HopID: hop.ID, OffererID: members["helper"].Member.ID, OffererName: "helper"}); err != nil {
			t.Fatalf("offer helper: %v", err)
		}
		server := newHTTPServer(t, db)
		owner := newTestActor(t, "owner", server.URL, members["owner"].Member.Email, members["owner"].Password)
		owner.Login()

		var wg sync.WaitGroup
		wg.Add(2)
		var cancelResp, acceptResp *http.Response
		go func() {
			defer wg.Done()
			cancelResp = owner.PostForm("/hops/cancel", formKV("org_id", strconv.FormatInt(org.ID, 10), "hop_id", strconv.FormatInt(hop.ID, 10)))
		}()
		go func() {
			defer wg.Done()
			acceptResp = owner.PostForm("/hops/offers/accept", formKV(
				"org_id", strconv.FormatInt(org.ID, 10),
				"hop_id", strconv.FormatInt(hop.ID, 10),
				"offer_member_id", strconv.FormatInt(members["helper"].Member.ID, 10),
				"redirect_to", "/hops/view?org_id="+strconv.FormatInt(org.ID, 10)+"&hop_id="+strconv.FormatInt(hop.ID, 10),
			))
		}()
		wg.Wait()
		_ = requireRedirectPathOneOf(t, cancelResp, "/my-hopshare")
		_ = requireRedirectPath(t, acceptResp, "/hops/view")

		reloaded, err := service.GetHopByID(ctx, db, org.ID, hop.ID)
		if err != nil {
			t.Fatalf("reload hop: %v", err)
		}
		if reloaded.Status != types.HopStatusCanceled && reloaded.Status != types.HopStatusAccepted {
			t.Fatalf("unexpected final status: %q", reloaded.Status)
		}
	})

	t.Run("RACE-06 complete vs cancel concurrent on accepted hop keeps valid state", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner", "helper")
		hop := createAcceptedHopViaOffer(t, ctx, db, org.ID, members["owner"].Member.ID, members["helper"].Member.ID, "Race complete cancel "+suffix)
		server := newHTTPServer(t, db)
		owner := newTestActor(t, "owner", server.URL, members["owner"].Member.Email, members["owner"].Password)
		helper := newTestActor(t, "helper", server.URL, members["helper"].Member.Email, members["helper"].Password)
		owner.Login()
		helper.Login()

		var wg sync.WaitGroup
		wg.Add(2)
		var cancelResp, completeResp *http.Response
		go func() {
			defer wg.Done()
			cancelResp = owner.PostForm("/hops/cancel", formKV("org_id", strconv.FormatInt(org.ID, 10), "hop_id", strconv.FormatInt(hop.ID, 10)))
		}()
		go func() {
			defer wg.Done()
			completeResp = helper.PostForm("/hops/complete", formKV(
				"org_id", strconv.FormatInt(org.ID, 10),
				"hop_id", strconv.FormatInt(hop.ID, 10),
				"completion_comment", "Concurrent complete",
				"completed_hours", "1",
			))
		}()
		wg.Wait()
		_ = requireRedirectPathOneOf(t, cancelResp, "/my-hopshare")
		_ = requireRedirectPathOneOf(t, completeResp, "/my-hopshare")

		reloaded, err := service.GetHopByID(ctx, db, org.ID, hop.ID)
		if err != nil {
			t.Fatalf("reload hop: %v", err)
		}
		if reloaded.Status != types.HopStatusCompleted && reloaded.Status != types.HopStatusCanceled {
			t.Fatalf("unexpected final status %q", reloaded.Status)
		}
	})

	t.Run("RACE-07 two associated users complete concurrently yields one transaction", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner", "helper")
		hop := createAcceptedHopViaOffer(t, ctx, db, org.ID, members["owner"].Member.ID, members["helper"].Member.ID, "Race dual complete "+suffix)
		server := newHTTPServer(t, db)
		owner := newTestActor(t, "owner", server.URL, members["owner"].Member.Email, members["owner"].Password)
		helper := newTestActor(t, "helper", server.URL, members["helper"].Member.Email, members["helper"].Password)
		owner.Login()
		helper.Login()

		var wg sync.WaitGroup
		wg.Add(2)
		var ownerResp, helperResp *http.Response
		go func() {
			defer wg.Done()
			ownerResp = owner.PostForm("/hops/complete", formKV("org_id", strconv.FormatInt(org.ID, 10), "hop_id", strconv.FormatInt(hop.ID, 10), "completion_comment", "owner complete", "completed_hours", "1"))
		}()
		go func() {
			defer wg.Done()
			helperResp = helper.PostForm("/hops/complete", formKV("org_id", strconv.FormatInt(org.ID, 10), "hop_id", strconv.FormatInt(hop.ID, 10), "completion_comment", "helper complete", "completed_hours", "1"))
		}()
		wg.Wait()
		_ = requireRedirectPathOneOf(t, ownerResp, "/my-hopshare")
		_ = requireRedirectPathOneOf(t, helperResp, "/my-hopshare")

		var txCount int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM hop_transactions WHERE organization_id = $1 AND hop_id = $2`, org.ID, hop.ID).Scan(&txCount); err != nil {
			t.Fatalf("count transactions: %v", err)
		}
		if txCount != 1 {
			t.Fatalf("expected exactly one transaction, got %d", txCount)
		}
	})

	t.Run("RACE-08 two owners process same membership request concurrently", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner", "owner2")
		if err := service.UpdateOrganizationMemberRole(ctx, db, org.ID, members["owner2"].Member.ID, true); err != nil {
			t.Fatalf("promote owner2: %v", err)
		}
		requester := createSeededMember(t, ctx, db, "race_owner_requester", suffix)
		if err := service.RequestMembership(ctx, db, requester.Member.ID, org.ID, nil); err != nil {
			t.Fatalf("request membership: %v", err)
		}
		reqID := requirePendingRequestID(t, ctx, db, org.ID, requester.Member.ID)

		server := newHTTPServer(t, db)
		owner1 := newTestActor(t, "owner", server.URL, members["owner"].Member.Email, members["owner"].Password)
		owner2 := newTestActor(t, "owner2", server.URL, members["owner2"].Member.Email, members["owner2"].Password)
		owner1.Login()
		owner2.Login()

		var wg sync.WaitGroup
		wg.Add(2)
		var owner1Resp, owner2Resp *http.Response
		go func() {
			defer wg.Done()
			owner1Resp = owner1.PostForm("/organizations/manage/request", formKV("request_id", strconv.FormatInt(reqID, 10), "action", "accept"))
		}()
		go func() {
			defer wg.Done()
			owner2Resp = owner2.PostForm("/organizations/manage/request", formKV("request_id", strconv.FormatInt(reqID, 10), "action", "accept"))
		}()
		wg.Wait()
		_ = requireRedirectPath(t, owner1Resp, "/organizations/manage")
		_ = requireRedirectPath(t, owner2Resp, "/organizations/manage")

		hasMembership, err := service.MemberHasActiveMembership(ctx, db, requester.Member.ID, org.ID)
		if err != nil {
			t.Fatalf("check membership: %v", err)
		}
		if !hasMembership {
			t.Fatalf("expected membership to be approved")
		}
	})

	t.Run("RACE-09 duplicate membership request concurrent creates one pending row", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner")
		requester := createSeededMember(t, ctx, db, "race_dup_requester", suffix)
		server := newHTTPServer(t, db)
		actor := newTestActor(t, "requester", server.URL, requester.Member.Email, requester.Password)
		actor.Login()

		responses := runConcurrently(2, func() *http.Response {
			return actor.PostForm("/organizations/request", formKV("org_id", strconv.FormatInt(org.ID, 10)))
		})
		for _, resp := range responses {
			requireRedirectPathOneOf(t, resp, "/organization/"+org.URLName, "/organizations")
		}
		reqs, err := service.PendingMembershipRequests(ctx, db, org.ID)
		if err != nil {
			t.Fatalf("pending requests: %v", err)
		}
		count := 0
		for _, req := range reqs {
			if req.MemberID == requester.Member.ID {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("expected 1 pending request row, got %d", count)
		}
		_ = members
	})

	t.Run("RACE-10 concurrent image delete on same image yields one success and one 404", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner", "helper")
		hop := createAcceptedHopViaOffer(t, ctx, db, org.ID, members["owner"].Member.ID, members["helper"].Member.ID, "Race image delete "+suffix)
		if err := service.AddHopImage(ctx, db, hop.ID, members["owner"].Member.ID, "image/png", tinyPNGData()); err != nil {
			t.Fatalf("add hop image: %v", err)
		}
		images, err := service.ListHopImages(ctx, db, hop.ID)
		if err != nil || len(images) == 0 {
			t.Fatalf("list hop images: %v len=%d", err, len(images))
		}
		imageID := images[0].ID

		server := newHTTPServer(t, db)
		owner := newTestActor(t, "owner", server.URL, members["owner"].Member.Email, members["owner"].Password)
		helper := newTestActor(t, "helper", server.URL, members["helper"].Member.Email, members["helper"].Password)
		owner.Login()
		helper.Login()

		var wg sync.WaitGroup
		wg.Add(2)
		var resp1, resp2 *http.Response
		go func() {
			defer wg.Done()
			resp1 = owner.PostForm("/hops/images/delete", formKV("org_id", strconv.FormatInt(org.ID, 10), "hop_id", strconv.FormatInt(hop.ID, 10), "image_id", strconv.FormatInt(imageID, 10)))
		}()
		go func() {
			defer wg.Done()
			resp2 = helper.PostForm("/hops/images/delete", formKV("org_id", strconv.FormatInt(org.ID, 10), "hop_id", strconv.FormatInt(hop.ID, 10), "image_id", strconv.FormatInt(imageID, 10)))
		}()
		wg.Wait()

		s1 := resp1.StatusCode
		s2 := resp2.StatusCode
		if !((s1 == 303 && s2 == 404) || (s1 == 404 && s2 == 303)) {
			body1 := requireStatus(t, resp1, s1)
			body2 := requireStatus(t, resp2, s2)
			t.Fatalf("expected one 303 and one 404, got %d/%d bodies=%q/%q", s1, s2, body1, body2)
		}
		if s1 == 303 {
			_ = requireRedirectPath(t, resp1, "/hops/view")
			_ = requireStatus(t, resp2, 404)
		} else {
			_ = requireRedirectPath(t, resp2, "/hops/view")
			_ = requireStatus(t, resp1, 404)
		}
	})
}

func runConcurrently(n int, fn func() *http.Response) []*http.Response {
	out := make([]*http.Response, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			out[i] = fn()
		}()
	}
	wg.Wait()
	return out
}
