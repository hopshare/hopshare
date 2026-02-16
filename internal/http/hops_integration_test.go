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
		requester := newTestActor(t, "requester", server.URL, members["requester"].Member.Username, members["requester"].Password)
		requester.Login()
		loc := requireRedirectPath(t, requester.PostForm("/hops/create", formKV(
			"org_id", strconv.FormatInt(org.ID, 10),
			"title", "Create Hop Success "+suffix,
			"details", "Need help.",
			"estimated_hours", "2",
			"needed_by_kind", types.HopNeededByAnytime,
		)), "/my-hopshare")
		requireQueryValue(t, loc, "success", "Hop created.")
	})

	t.Run("HOP-02 create hop invalid inputs are rejected", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner", "requester")
		server := newHTTPServer(t, db)
		requester := newTestActor(t, "requester", server.URL, members["requester"].Member.Username, members["requester"].Password)
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
	})

	t.Run("HOP-03 create hop by non-member fails", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, _ := createOrganizationWithMembers(t, ctx, db, suffix, "owner")
		outsider := createSeededMember(t, ctx, db, "hop_non_member", suffix)
		server := newHTTPServer(t, db)
		actor := newTestActor(t, "outsider", server.URL, outsider.Member.Username, outsider.Password)
		actor.Login()
		loc := requireRedirectPath(t, actor.PostForm("/hops/create", formKV(
			"org_id", strconv.FormatInt(org.ID, 10),
			"title", "Should Not Create "+suffix,
			"estimated_hours", "1",
			"needed_by_kind", types.HopNeededByAnytime,
		)), "/my-hopshare")
		requireQueryValue(t, loc, "error", "Could not create hop.")
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
		member := newTestActor(t, "member", server.URL, members["member"].Member.Username, members["member"].Password)
		member.Login()
		body := requireStatus(t, member.Get("/hops/view?org_id="+strconv.FormatInt(org.ID, 10)+"&hop_id="+strconv.FormatInt(hop.ID, 10)), 200)
		requireBodyContains(t, body, "Viewable Hop")
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
		actor := newTestActor(t, "outsider", server.URL, outsider.Member.Username, outsider.Password)
		actor.Login()
		requireStatus(t, actor.Get("/hops/view?org_id="+strconv.FormatInt(org.ID, 10)+"&hop_id="+strconv.FormatInt(hop.ID, 10)), 403)
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
		helper := newTestActor(t, "helper", server.URL, members["helper"].Member.Username, members["helper"].Password)
		owner := newTestActor(t, "owner", server.URL, members["owner"].Member.Username, members["owner"].Password)
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
		owner := newTestActor(t, "owner", server.URL, members["owner"].Member.Username, members["owner"].Password)
		owner.Login()
		loc := requireRedirectPath(t, owner.PostForm("/hops/cancel", formKV(
			"org_id", strconv.FormatInt(org.ID, 10),
			"hop_id", strconv.FormatInt(hop.ID, 10),
		)), "/my-hopshare")
		requireQueryValue(t, loc, "success", "Hop canceled.")
	})

	t.Run("HOP-16 cancel accepted hop by creator succeeds", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner", "helper")
		hop := createAcceptedHopViaOffer(t, ctx, db, org.ID, members["owner"].Member.ID, members["helper"].Member.ID, "Cancel Accepted Hop "+suffix)
		server := newHTTPServer(t, db)
		owner := newTestActor(t, "owner", server.URL, members["owner"].Member.Username, members["owner"].Password)
		owner.Login()
		loc := requireRedirectPath(t, owner.PostForm("/hops/cancel", formKV(
			"org_id", strconv.FormatInt(org.ID, 10),
			"hop_id", strconv.FormatInt(hop.ID, 10),
		)), "/my-hopshare")
		requireQueryValue(t, loc, "success", "Hop canceled.")
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
		helper := newTestActor(t, "helper", server.URL, members["helper"].Member.Username, members["helper"].Password)
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
		owner := newTestActor(t, "owner", server.URL, members["owner"].Member.Username, members["owner"].Password)
		owner.Login()
		loc := requireRedirectPath(t, owner.PostForm("/hops/complete", formKV(
			"org_id", strconv.FormatInt(org.ID, 10),
			"hop_id", strconv.FormatInt(hop.ID, 10),
			"completed_hours", "1",
			"completion_comment", "Requester completed.",
		)), "/my-hopshare")
		requireQueryValue(t, loc, "success", "Hop completed.")
	})

	t.Run("HOP-13 complete hop missing comment is rejected", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner", "helper")
		hop := createAcceptedHopViaOffer(t, ctx, db, org.ID, members["owner"].Member.ID, members["helper"].Member.ID, "Complete missing comment "+suffix)
		server := newHTTPServer(t, db)
		helper := newTestActor(t, "helper", server.URL, members["helper"].Member.Username, members["helper"].Password)
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
		owner := newTestActor(t, "owner", server.URL, members["owner"].Member.Username, members["owner"].Password)
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
		owner := newTestActor(t, "owner", server.URL, members["owner"].Member.Username, members["owner"].Password)
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
		owner := newTestActor(t, "owner", server.URL, members["owner"].Member.Username, members["owner"].Password)
		owner.Login()
		resp := owner.Request("POST", "/hops/privacy", strings.NewReader(formKV(
			"org_id", strconv.FormatInt(org.ID, 10),
			"hop_id", strconv.FormatInt(hop.ID, 10),
			"is_private", "false",
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
		member := newTestActor(t, "member", server.URL, members["member"].Member.Username, members["member"].Password)
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
		owner := newTestActor(t, "owner", server.URL, members["owner"].Member.Username, members["owner"].Password)
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
		member := newTestActor(t, "member", server.URL, members["member"].Member.Username, members["member"].Password)
		owner := newTestActor(t, "owner", server.URL, members["owner"].Member.Username, members["owner"].Password)
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
		owner := newTestActor(t, "owner", server.URL, members["owner"].Member.Username, members["owner"].Password)
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
		owner := newTestActor(t, "owner", server.URL, members["owner"].Member.Username, members["owner"].Password)
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
		owner := newTestActor(t, "owner", server.URL, members["owner"].Member.Username, members["owner"].Password)
		member := newTestActor(t, "member", server.URL, members["member"].Member.Username, members["member"].Password)
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
		member := newTestActor(t, "member", server.URL, members["member"].Member.Username, members["member"].Password)
		outsider := newTestActor(t, "nonMember", server.URL, nonMember.Member.Username, nonMember.Password)
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
		helper := newTestActor(t, "helper", server.URL, members["helper"].Member.Username, members["helper"].Password)
		requester := newTestActor(t, "requester", server.URL, members["requester"].Member.Username, members["requester"].Password)
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
		actor := newTestActor(t, "member", server.URL, member.Member.Username, member.Password)
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
		actor := newTestActor(t, "member", server.URL, members["member"].Member.Username, members["member"].Password)
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
		owner := newTestActor(t, "owner", server.URL, members["owner"].Member.Username, members["owner"].Password)
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
	msg := findPendingActionMessageForHop(t, ctx, db, requesterID, hop.ID)
	if err := service.AcceptHopOfferMessage(ctx, db, msg.ID, requesterID, "requester", "accepted"); err != nil {
		t.Fatalf("accept offer: %v", err)
	}
	acceptedHop, err := service.GetHopByID(ctx, db, orgID, hop.ID)
	if err != nil {
		t.Fatalf("get accepted hop: %v", err)
	}
	return acceptedHop
}
