package http_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"hopshare/internal/service"
	"hopshare/internal/types"
)

const adminOverviewLeaderboardLimit = 5

type adminLeaderboardExpectation struct {
	OrganizationID      int64
	OrganizationEnabled bool
	Value               int
	EnabledUsers        int
	DisabledUsers       int
}

type adminOverviewExpectation struct {
	OrganizationEnabledCount  int
	OrganizationDisabledCount int
	UserEnabledCount          int
	UserDisabledCount         int
	UserVerifiedCount         int
	UserNotVerifiedCount      int
	OverrideCount             int
	OverrideHoursGiven        int
	OverrideHoursRemoved      int
	HopStatusCounts           map[string]int
	TotalHoursExchanged       int
	TopByHopsCreated          []adminLeaderboardExpectation
	TopByHoursExchanged       []adminLeaderboardExpectation
	TopByUsers                []adminLeaderboardExpectation
}

func TestAdminOverviewHTTP(t *testing.T) {
	db := requireHTTPTestDB(t)

	t.Run("ADMIN-01 GET /admin app overview renders global metrics and leaderboards", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()

		suffix := uniqueTestSuffix()
		admin := createSeededMember(t, ctx, db, "admin_overview_admin", suffix)
		ownerEnabled := createSeededMember(t, ctx, db, "admin_overview_owner_enabled", suffix)
		ownerDisabled := createSeededMember(t, ctx, db, "admin_overview_owner_disabled", suffix)
		helperEnabled := createSeededMember(t, ctx, db, "admin_overview_helper_enabled", suffix)
		helperDisabled := createSeededMember(t, ctx, db, "admin_overview_helper_disabled", suffix)

		orgEnabled, err := service.CreateOrganization(ctx, db, "Admin Overview Enabled Org "+suffix, "City", "ST", "Enabled org for admin integration coverage.", ownerEnabled.Member.ID)
		if err != nil {
			t.Fatalf("create enabled org: %v", err)
		}
		orgDisabled, err := service.CreateOrganization(ctx, db, "Admin Overview Disabled Org "+suffix, "City", "ST", "Disabled org for admin integration coverage.", ownerDisabled.Member.ID)
		if err != nil {
			t.Fatalf("create disabled org: %v", err)
		}

		approveMemberForOrganization(t, ctx, db, orgEnabled.ID, ownerEnabled.Member.ID, helperEnabled.Member.ID)
		approveMemberForOrganization(t, ctx, db, orgDisabled.ID, ownerDisabled.Member.ID, helperEnabled.Member.ID)
		approveMemberForOrganization(t, ctx, db, orgDisabled.ID, ownerDisabled.Member.ID, helperDisabled.Member.ID)

		for i := 0; i < 5; i++ {
			role := fmt.Sprintf("admin_overview_extra_%d", i)
			extra := createSeededMember(t, ctx, db, role, suffix)
			approveMemberForOrganization(t, ctx, db, orgDisabled.ID, ownerDisabled.Member.ID, extra.Member.ID)
			if i%2 == 0 {
				if _, err := db.ExecContext(ctx, `UPDATE members SET enabled = FALSE WHERE id = $1`, extra.Member.ID); err != nil {
					t.Fatalf("disable extra member %d: %v", extra.Member.ID, err)
				}
			}
		}

		if _, err := db.ExecContext(ctx, `UPDATE organizations SET enabled = FALSE WHERE id = $1`, orgDisabled.ID); err != nil {
			t.Fatalf("disable org %d: %v", orgDisabled.ID, err)
		}
		if _, err := db.ExecContext(ctx, `UPDATE members SET enabled = FALSE WHERE id = $1`, ownerDisabled.Member.ID); err != nil {
			t.Fatalf("disable owner member %d: %v", ownerDisabled.Member.ID, err)
		}
		if _, err := db.ExecContext(ctx, `UPDATE members SET enabled = FALSE WHERE id = $1`, helperDisabled.Member.ID); err != nil {
			t.Fatalf("disable helper member %d: %v", helperDisabled.Member.ID, err)
		}

		openHop := createAdminOverviewHop(t, ctx, db, orgEnabled.ID, ownerEnabled.Member.ID, "Admin Overview Open Hop "+suffix, types.HopNeededByAnytime, nil)
		_ = openHop

		acceptedHop := createAdminOverviewHop(t, ctx, db, orgEnabled.ID, ownerEnabled.Member.ID, "Admin Overview Accepted Hop "+suffix, types.HopNeededByAnytime, nil)
		if err := service.AcceptHop(ctx, db, orgEnabled.ID, acceptedHop.ID, helperEnabled.Member.ID); err != nil {
			t.Fatalf("accept hop %d: %v", acceptedHop.ID, err)
		}

		canceledHop := createAdminOverviewHop(t, ctx, db, orgEnabled.ID, ownerEnabled.Member.ID, "Admin Overview Canceled Hop "+suffix, types.HopNeededByAnytime, nil)
		if err := service.CancelHop(ctx, db, orgEnabled.ID, canceledHop.ID, ownerEnabled.Member.ID); err != nil {
			t.Fatalf("cancel hop %d: %v", canceledHop.ID, err)
		}

		expiredDate := time.Now().UTC().AddDate(0, 0, -3)
		_ = createAdminOverviewHop(t, ctx, db, orgEnabled.ID, ownerEnabled.Member.ID, "Admin Overview Expired Hop "+suffix, types.HopNeededByOn, &expiredDate)
		if _, err := service.ExpireHops(ctx, db, orgEnabled.ID, time.Now().UTC()); err != nil {
			t.Fatalf("expire hops for org %d: %v", orgEnabled.ID, err)
		}

		completedHop := createAdminOverviewHop(t, ctx, db, orgEnabled.ID, ownerEnabled.Member.ID, "Admin Overview Completed Hop "+suffix, types.HopNeededByAnytime, nil)
		if err := service.AcceptHop(ctx, db, orgEnabled.ID, completedHop.ID, helperEnabled.Member.ID); err != nil {
			t.Fatalf("accept hop %d before completion: %v", completedHop.ID, err)
		}
		if err := service.CompleteHop(ctx, db, service.CompleteHopParams{
			OrganizationID: orgEnabled.ID,
			HopID:          completedHop.ID,
			CompletedBy:    ownerEnabled.Member.ID,
			Comment:        "completed for admin overview integration test",
			CompletedHours: 6,
		}); err != nil {
			t.Fatalf("complete hop %d: %v", completedHop.ID, err)
		}

		if _, err := db.ExecContext(ctx, `
			WITH inserted AS (
				INSERT INTO hops (
					organization_id,
					created_by,
					title,
					details,
					estimated_hours,
					is_private,
					needed_by_kind,
					needed_by_date,
					expires_at,
					status,
					accepted_by,
					accepted_at,
					completed_by,
					completed_at,
					completed_hours,
					completion_comment
				)
				SELECT
					$1,
					$2,
					'Admin Overview Bulk Completed Hop ' || seq,
					NULL,
					2,
					FALSE,
					$3,
					NULL,
					NULL,
					$4,
					$5,
					NOW(),
					$2,
					NOW(),
					8,
					'bulk completed hop for admin overview integration test'
				FROM generate_series(1, 20) AS seq
				RETURNING id
			)
			INSERT INTO hop_transactions (organization_id, hop_id, from_member_id, to_member_id, hours)
			SELECT $1, id, $2, $5, 8
			FROM inserted
		`, orgDisabled.ID, ownerDisabled.Member.ID, types.HopNeededByAnytime, types.HopStatusCompleted, helperEnabled.Member.ID); err != nil {
			t.Fatalf("seed bulk completed hops for disabled org: %v", err)
		}

		expected := queryAdminOverviewExpectation(t, ctx, db)

		server := newHTTPServerWithAdmins(t, db, []string{" " + admin.Member.Username + " "})
		actor := newTestActor(t, "admin", server.URL, admin.Member.Username, admin.Password)
		actor.Login()

		body := requireStatus(t, actor.Get("/admin"), http.StatusOK)
		requireBodyContains(t, body, "App-Wide Metrics")
		requireBodyContains(t, body, "Leaderboards")
		requireBodyContains(t, body, `href="/admin?tab=app"`)

		requireBodyContains(t, body, fmt.Sprintf(`data-testid="admin-org-enabled-count">%d</span>`, expected.OrganizationEnabledCount))
		requireBodyContains(t, body, fmt.Sprintf(`data-testid="admin-org-disabled-count">%d</span>`, expected.OrganizationDisabledCount))
		requireBodyContains(t, body, fmt.Sprintf(`data-testid="admin-user-enabled-count">%d</span>`, expected.UserEnabledCount))
		requireBodyContains(t, body, fmt.Sprintf(`data-testid="admin-user-disabled-count">%d</span>`, expected.UserDisabledCount))
		requireBodyContains(t, body, fmt.Sprintf(`data-testid="admin-user-verified-count">%d</span>`, expected.UserVerifiedCount))
		requireBodyContains(t, body, fmt.Sprintf(`data-testid="admin-user-not-verified-count">%d</span>`, expected.UserNotVerifiedCount))
		requireBodyContains(t, body, fmt.Sprintf(`data-testid="admin-total-hours">%d</p>`, expected.TotalHoursExchanged))
		requireBodyContains(t, body, fmt.Sprintf(`data-testid="admin-overrides-count">%d</span>`, expected.OverrideCount))
		requireBodyContains(t, body, fmt.Sprintf(`data-testid="admin-overrides-hours-given">%d</span>`, expected.OverrideHoursGiven))
		requireBodyContains(t, body, fmt.Sprintf(`data-testid="admin-overrides-hours-removed">%d</span>`, expected.OverrideHoursRemoved))

		hopStatuses := []string{
			types.HopStatusOpen,
			types.HopStatusAccepted,
			types.HopStatusCompleted,
			types.HopStatusCanceled,
			types.HopStatusExpired,
		}
		for _, status := range hopStatuses {
			requireBodyContains(t, body, fmt.Sprintf(`data-testid="admin-hop-status-count-%s">%d</span>`, status, expected.HopStatusCounts[status]))
		}

		assertAdminLeaderboardByValue(t, body, "hops", expected.TopByHopsCreated)
		assertAdminLeaderboardByValue(t, body, "hours", expected.TopByHoursExchanged)
		assertAdminLeaderboardByUsers(t, body, expected.TopByUsers)
	})
}

func TestAdminOrganizationsHTTP(t *testing.T) {
	db := requireHTTPTestDB(t)

	t.Run("ADMIN-02 organizations tab supports disable, re-enable, expire, and delete actions", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()

		suffix := uniqueTestSuffix()
		admin := createSeededMember(t, ctx, db, "admin_org_admin", suffix)
		owner := createSeededMember(t, ctx, db, "admin_org_owner", suffix)
		helper := createSeededMember(t, ctx, db, "admin_org_helper", suffix)

		org, err := service.CreateOrganization(ctx, db, "Admin Org Actions "+suffix, "City", "ST", "Org for admin organization actions integration coverage.", owner.Member.ID)
		if err != nil {
			t.Fatalf("create organization: %v", err)
		}
		approveMemberForOrganization(t, ctx, db, org.ID, owner.Member.ID, helper.Member.ID)

		openHop := createAdminOverviewHop(t, ctx, db, org.ID, owner.Member.ID, "Admin Org Open Hop "+suffix, types.HopNeededByAnytime, nil)
		acceptedHop := createAdminOverviewHop(t, ctx, db, org.ID, owner.Member.ID, "Admin Org Accepted Hop "+suffix, types.HopNeededByAnytime, nil)
		if err := service.AcceptHop(ctx, db, org.ID, acceptedHop.ID, helper.Member.ID); err != nil {
			t.Fatalf("accept hop %d: %v", acceptedHop.ID, err)
		}
		completedHop := createAdminOverviewHop(t, ctx, db, org.ID, owner.Member.ID, "Admin Org Completed Hop "+suffix, types.HopNeededByAnytime, nil)
		if err := service.AcceptHop(ctx, db, org.ID, completedHop.ID, helper.Member.ID); err != nil {
			t.Fatalf("accept completed hop %d: %v", completedHop.ID, err)
		}
		if err := service.CompleteHop(ctx, db, service.CompleteHopParams{
			OrganizationID: org.ID,
			HopID:          completedHop.ID,
			CompletedBy:    owner.Member.ID,
			Comment:        "mark completed for delete rejection coverage",
			CompletedHours: 2,
		}); err != nil {
			t.Fatalf("complete hop %d: %v", completedHop.ID, err)
		}
		canceledHop := createAdminOverviewHop(t, ctx, db, org.ID, owner.Member.ID, "Admin Org Canceled Hop "+suffix, types.HopNeededByAnytime, nil)
		if err := service.CancelHop(ctx, db, org.ID, canceledHop.ID, owner.Member.ID); err != nil {
			t.Fatalf("cancel hop %d: %v", canceledHop.ID, err)
		}
		if err := service.AdjustMemberHourBalance(ctx, db, service.AdjustMemberHourBalanceParams{
			OrganizationID: org.ID,
			MemberID:       owner.Member.ID,
			AdminMemberID:  admin.Member.ID,
			HoursDelta:     3,
			Reason:         "seeded positive override for org tab metrics",
		}); err != nil {
			t.Fatalf("seed positive org override: %v", err)
		}
		if err := service.AdjustMemberHourBalance(ctx, db, service.AdjustMemberHourBalanceParams{
			OrganizationID: org.ID,
			MemberID:       helper.Member.ID,
			AdminMemberID:  admin.Member.ID,
			HoursDelta:     -2,
			Reason:         "seeded negative override for org tab metrics",
		}); err != nil {
			t.Fatalf("seed negative org override: %v", err)
		}

		server := newHTTPServerWithAdmins(t, db, []string{admin.Member.Username})
		adminActor := newTestActor(t, "admin", server.URL, admin.Member.Username, admin.Password)
		adminActor.Login()
		ownerActor := newTestActor(t, "owner", server.URL, owner.Member.Username, owner.Password)
		ownerActor.Login()

		adminBody := requireStatus(t, adminActor.Get("/admin?tab=organizations&org_id="+strconv.FormatInt(org.ID, 10)+"&q="+url.QueryEscape("Admin Org")), http.StatusOK)
		requireBodyContains(t, adminBody, "Organizations")
		requireBodyContains(t, adminBody, org.Name)
		requireBodyContains(t, adminBody, "aria-label=\"Confirm admin action\"")
		requireBodyContains(t, adminBody, "data-testid=\"admin-hop-expire-"+strconv.FormatInt(acceptedHop.ID, 10)+"\"")
		requireBodyContains(t, adminBody, "data-testid=\"admin-hop-delete-"+strconv.FormatInt(openHop.ID, 10)+"\"")
		requireBodyNotContains(t, adminBody, ">Expire hop</button>")
		requireBodyNotContains(t, adminBody, ">Delete hop</button>")
		requireBodyContains(t, adminBody, "data-testid=\"admin-org-overrides-count-"+strconv.FormatInt(org.ID, 10)+"\">2</span>")
		requireBodyContains(t, adminBody, "data-testid=\"admin-org-overrides-given-"+strconv.FormatInt(org.ID, 10)+"\">3</span>")
		requireBodyContains(t, adminBody, "data-testid=\"admin-org-overrides-removed-"+strconv.FormatInt(org.ID, 10)+"\">2</span>")

		requireStatus(t, ownerActor.Get("/organization/"+org.URLName), http.StatusOK)

		loc := requireRedirectPath(t, adminActor.PostForm("/admin/organizations/action", formKV(
			"action", "disable_org",
			"org_id", strconv.FormatInt(org.ID, 10),
			"q", "Admin Org",
			"reason", "disable for maintenance",
		)), "/admin")
		requireQueryValue(t, loc, "tab", "organizations")
		requireQueryValue(t, loc, "success", "Organization disabled.")

		requireStatus(t, ownerActor.Get("/organization/"+org.URLName), http.StatusNotFound)
		orgsBody := requireStatus(t, ownerActor.Get("/organizations"), http.StatusOK)
		requireBodyNotContains(t, orgsBody, org.Name)

		myHopshareBody := requireStatus(t, ownerActor.Get("/my-hopshare"), http.StatusOK)
		requireBodyContains(t, myHopshareBody, "Join an organization to get started!")

		createLoc := requireRedirectPath(t, ownerActor.PostForm("/hops/create", formKV(
			"org_id", strconv.FormatInt(org.ID, 10),
			"title", "Disabled Org Hop Attempt",
			"details", "This should fail because the org is disabled.",
			"estimated_hours", "2",
			"needed_by_kind", "anytime",
		)), "/my-hopshare")
		requireQueryValue(t, createLoc, "error", "Could not create hop.")

		loc = requireRedirectPath(t, adminActor.PostForm("/admin/organizations/action", formKV(
			"action", "enable_org",
			"org_id", strconv.FormatInt(org.ID, 10),
			"q", "Admin Org",
			"reason", "restore access after maintenance",
		)), "/admin")
		requireQueryValue(t, loc, "tab", "organizations")
		requireQueryValue(t, loc, "success", "Organization re-enabled.")

		requireStatus(t, ownerActor.Get("/organization/"+org.URLName), http.StatusOK)

		deleteRejectedLoc := requireRedirectPath(t, adminActor.PostForm("/admin/organizations/action", formKV(
			"action", "delete_hop",
			"org_id", strconv.FormatInt(org.ID, 10),
			"hop_id", strconv.FormatInt(completedHop.ID, 10),
			"q", "Admin Org",
			"reason", "attempt delete completed hop",
		)), "/admin")
		requireQueryValue(t, deleteRejectedLoc, "error", "Hop action not allowed for this status.")

		deleteOpenLoc := requireRedirectPath(t, adminActor.PostForm("/admin/organizations/action", formKV(
			"action", "delete_hop",
			"org_id", strconv.FormatInt(org.ID, 10),
			"hop_id", strconv.FormatInt(openHop.ID, 10),
			"q", "Admin Org",
			"reason", "remove stale open hop",
		)), "/admin")
		requireQueryValue(t, deleteOpenLoc, "success", "Hop deleted.")

		if _, err := service.GetHopByID(ctx, db, org.ID, openHop.ID); !errors.Is(err, service.ErrHopNotFound) {
			t.Fatalf("expected deleted hop to be missing, got %v", err)
		}

		expireLoc := requireRedirectPath(t, adminActor.PostForm("/admin/organizations/action", formKV(
			"action", "expire_hop",
			"org_id", strconv.FormatInt(org.ID, 10),
			"hop_id", strconv.FormatInt(acceptedHop.ID, 10),
			"q", "Admin Org",
			"reason", "close lingering accepted hop",
		)), "/admin")
		requireQueryValue(t, expireLoc, "success", "Hop expired.")

		expiredHop, err := service.GetHopByID(ctx, db, org.ID, acceptedHop.ID)
		if err != nil {
			t.Fatalf("load expired hop: %v", err)
		}
		if expiredHop.Status != types.HopStatusExpired {
			t.Fatalf("expected hop status %q after expire action, got %q", types.HopStatusExpired, expiredHop.Status)
		}

		reasonRequiredLoc := requireRedirectPath(t, adminActor.PostForm("/admin/organizations/action", formKV(
			"action", "delete_hop",
			"org_id", strconv.FormatInt(org.ID, 10),
			"hop_id", strconv.FormatInt(canceledHop.ID, 10),
			"q", "Admin Org",
			"reason", "",
		)), "/admin")
		requireQueryValue(t, reasonRequiredLoc, "error", "A reason is required for this action.")
	})
}

func TestAdminModerationHTTP(t *testing.T) {
	db := requireHTTPTestDB(t)

	t.Run("ADMIN-03 moderation queue supports dismiss and delete actions for reported hop content", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()

		suffix := uniqueTestSuffix()
		admin := createSeededMember(t, ctx, db, "admin_mod_admin", suffix)
		owner := createSeededMember(t, ctx, db, "admin_mod_owner", suffix)
		reporter := createSeededMember(t, ctx, db, "admin_mod_reporter", suffix)

		org, err := service.CreateOrganization(ctx, db, "Admin Moderation Org "+suffix, "City", "ST", "Org for moderation integration coverage.", owner.Member.ID)
		if err != nil {
			t.Fatalf("create organization: %v", err)
		}
		approveMemberForOrganization(t, ctx, db, org.ID, owner.Member.ID, reporter.Member.ID)

		hop := createAdminOverviewHop(t, ctx, db, org.ID, owner.Member.ID, "Admin Moderation Hop "+suffix, types.HopNeededByAnytime, nil)
		if err := service.AddHopComment(ctx, db, hop.ID, owner.Member.ID, "Comment to delete "+suffix); err != nil {
			t.Fatalf("add hop comment to delete: %v", err)
		}
		if err := service.AddHopComment(ctx, db, hop.ID, owner.Member.ID, "Comment to dismiss "+suffix); err != nil {
			t.Fatalf("add hop comment to dismiss: %v", err)
		}
		if err := service.AddHopImage(ctx, db, hop.ID, owner.Member.ID, "image/png", tinyPNGData()); err != nil {
			t.Fatalf("add hop image to delete: %v", err)
		}

		comments, err := service.ListHopComments(ctx, db, hop.ID)
		if err != nil || len(comments) < 2 {
			t.Fatalf("list hop comments: err=%v len=%d", err, len(comments))
		}
		images, err := service.ListHopImages(ctx, db, hop.ID)
		if err != nil || len(images) == 0 {
			t.Fatalf("list hop images: err=%v len=%d", err, len(images))
		}

		var deleteCommentID int64
		var dismissCommentID int64
		for _, comment := range comments {
			switch comment.Body {
			case "Comment to delete " + suffix:
				deleteCommentID = comment.ID
			case "Comment to dismiss " + suffix:
				dismissCommentID = comment.ID
			}
		}
		if deleteCommentID == 0 || dismissCommentID == 0 {
			t.Fatalf("failed to locate seeded comments for moderation: delete=%d dismiss=%d", deleteCommentID, dismissCommentID)
		}
		deleteImageID := images[0].ID

		server := newHTTPServerWithAdmins(t, db, []string{admin.Member.Username})
		reporterActor := newTestActor(t, "reporter", server.URL, reporter.Member.Username, reporter.Password)
		reporterActor.Login()
		adminActor := newTestActor(t, "admin", server.URL, admin.Member.Username, admin.Password)
		adminActor.Login()

		requireRedirectPath(t, reporterActor.PostForm("/hops/comments/report", formKV(
			"org_id", strconv.FormatInt(org.ID, 10),
			"hop_id", strconv.FormatInt(hop.ID, 10),
			"comment_id", strconv.FormatInt(deleteCommentID, 10),
		)), "/hops/view")
		requireRedirectPath(t, reporterActor.PostForm("/hops/images/report", formKV(
			"org_id", strconv.FormatInt(org.ID, 10),
			"hop_id", strconv.FormatInt(hop.ID, 10),
			"image_id", strconv.FormatInt(deleteImageID, 10),
		)), "/hops/view")
		requireRedirectPath(t, reporterActor.PostForm("/hops/comments/report", formKV(
			"org_id", strconv.FormatInt(org.ID, 10),
			"hop_id", strconv.FormatInt(hop.ID, 10),
			"comment_id", strconv.FormatInt(dismissCommentID, 10),
		)), "/hops/view")

		deleteCommentReportID := moderationReportIDForComment(t, ctx, db, deleteCommentID)
		deleteImageReportID := moderationReportIDForImage(t, ctx, db, deleteImageID)
		dismissReportID := moderationReportIDForComment(t, ctx, db, dismissCommentID)

		queueBody := requireStatus(t, adminActor.Get("/admin?tab=moderation&status=open&type=all&q="+url.QueryEscape("Admin Moderation Org")), http.StatusOK)
		requireBodyContains(t, queueBody, "Moderation Queue")
		requireBodyContains(t, queueBody, fmt.Sprintf(`data-testid="admin-moderation-report-%d"`, deleteCommentReportID))
		requireBodyContains(t, queueBody, fmt.Sprintf(`data-testid="admin-moderation-report-%d"`, deleteImageReportID))
		requireBodyContains(t, queueBody, fmt.Sprintf(`data-testid="admin-moderation-report-%d"`, dismissReportID))

		beforeDismissCount := countAdminAuditEventsForActorAction(t, ctx, db, admin.Member.ID, service.AdminAuditActionModerationDismiss)
		beforeCommentDeleteCount := countAdminAuditEventsForActorAction(t, ctx, db, admin.Member.ID, service.AdminAuditActionModerationCommentDelete)
		beforeImageDeleteCount := countAdminAuditEventsForActorAction(t, ctx, db, admin.Member.ID, service.AdminAuditActionModerationImageDelete)

		deleteCommentLoc := requireRedirectPath(t, adminActor.PostForm("/admin/moderation/action", formKV(
			"action", types.ModerationResolutionDeleteComment,
			"report_id", strconv.FormatInt(deleteCommentReportID, 10),
			"status", "open",
			"type", "all",
			"q", "Admin Moderation Org",
			"reason", "remove abusive comment",
		)), "/admin")
		requireQueryValue(t, deleteCommentLoc, "tab", "moderation")
		requireQueryValue(t, deleteCommentLoc, "success", "Comment deleted.")

		deleteImageLoc := requireRedirectPath(t, adminActor.PostForm("/admin/moderation/action", formKV(
			"action", types.ModerationResolutionDeleteImage,
			"report_id", strconv.FormatInt(deleteImageReportID, 10),
			"status", "open",
			"type", "all",
			"q", "Admin Moderation Org",
			"reason", "remove unsafe image",
		)), "/admin")
		requireQueryValue(t, deleteImageLoc, "tab", "moderation")
		requireQueryValue(t, deleteImageLoc, "success", "Image deleted.")

		dismissLoc := requireRedirectPath(t, adminActor.PostForm("/admin/moderation/action", formKV(
			"action", types.ModerationResolutionDismiss,
			"report_id", strconv.FormatInt(dismissReportID, 10),
			"status", "open",
			"type", "all",
			"q", "Admin Moderation Org",
			"reason", "report not actionable",
		)), "/admin")
		requireQueryValue(t, dismissLoc, "tab", "moderation")
		requireQueryValue(t, dismissLoc, "success", "Report dismissed.")

		afterDismissCount := countAdminAuditEventsForActorAction(t, ctx, db, admin.Member.ID, service.AdminAuditActionModerationDismiss)
		afterCommentDeleteCount := countAdminAuditEventsForActorAction(t, ctx, db, admin.Member.ID, service.AdminAuditActionModerationCommentDelete)
		afterImageDeleteCount := countAdminAuditEventsForActorAction(t, ctx, db, admin.Member.ID, service.AdminAuditActionModerationImageDelete)
		if afterDismissCount != beforeDismissCount+1 {
			t.Fatalf("expected dismiss moderation audit count increment by 1, before=%d after=%d", beforeDismissCount, afterDismissCount)
		}
		if afterCommentDeleteCount != beforeCommentDeleteCount+1 {
			t.Fatalf("expected comment-delete moderation audit count increment by 1, before=%d after=%d", beforeCommentDeleteCount, afterCommentDeleteCount)
		}
		if afterImageDeleteCount != beforeImageDeleteCount+1 {
			t.Fatalf("expected image-delete moderation audit count increment by 1, before=%d after=%d", beforeImageDeleteCount, afterImageDeleteCount)
		}

		remainingComments, err := service.ListHopComments(ctx, db, hop.ID)
		if err != nil {
			t.Fatalf("list remaining hop comments: %v", err)
		}
		if moderationContainsCommentID(remainingComments, deleteCommentID) {
			t.Fatalf("expected deleted comment %d to be removed", deleteCommentID)
		}
		if !moderationContainsCommentID(remainingComments, dismissCommentID) {
			t.Fatalf("expected dismissed report comment %d to remain", dismissCommentID)
		}

		remainingImages, err := service.ListHopImages(ctx, db, hop.ID)
		if err != nil {
			t.Fatalf("list remaining hop images: %v", err)
		}
		if moderationContainsImageID(remainingImages, deleteImageID) {
			t.Fatalf("expected deleted image %d to be removed", deleteImageID)
		}

		assertModerationReportResolution(t, ctx, db, deleteCommentReportID, types.ModerationReportStatusActioned, types.ModerationResolutionDeleteComment)
		assertModerationReportResolution(t, ctx, db, deleteImageReportID, types.ModerationReportStatusActioned, types.ModerationResolutionDeleteImage)
		assertModerationReportResolution(t, ctx, db, dismissReportID, types.ModerationReportStatusDismissed, types.ModerationResolutionDismiss)
	})
}

func TestAdminUsersHTTP(t *testing.T) {
	db := requireHTTPTestDB(t)

	t.Run("ADMIN-04 users tab supports recovery actions, accepted-hop reopen policy, and audit logs", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()

		suffix := uniqueTestSuffix()
		admin := createSeededMember(t, ctx, db, "admin_users_admin", suffix)
		requester := createSeededMember(t, ctx, db, "admin_users_requester", suffix)
		target := createSeededMember(t, ctx, db, "admin_users_target", suffix)
		helper := createSeededMember(t, ctx, db, "admin_users_helper", suffix)

		org, err := service.CreateOrganization(ctx, db, "Admin Users Org "+suffix, "City", "ST", "Org for admin user action integration coverage.", requester.Member.ID)
		if err != nil {
			t.Fatalf("create organization: %v", err)
		}
		approveMemberForOrganization(t, ctx, db, org.ID, requester.Member.ID, target.Member.ID)
		approveMemberForOrganization(t, ctx, db, org.ID, requester.Member.ID, helper.Member.ID)

		targetAsHelperHop := createAdminOverviewHop(t, ctx, db, org.ID, requester.Member.ID, "Target as accepted helper "+suffix, types.HopNeededByAnytime, nil)
		if err := service.AcceptHop(ctx, db, org.ID, targetAsHelperHop.ID, target.Member.ID); err != nil {
			t.Fatalf("accept target-as-helper hop: %v", err)
		}

		targetAsRequesterHop := createAdminOverviewHop(t, ctx, db, org.ID, target.Member.ID, "Target as requester "+suffix, types.HopNeededByAnytime, nil)
		if err := service.AcceptHop(ctx, db, org.ID, targetAsRequesterHop.ID, helper.Member.ID); err != nil {
			t.Fatalf("accept target-as-requester hop: %v", err)
		}

		resetSender := &recordingPasswordResetEmailSender{}
		server := newHTTPServerWithAdminsAndPasswordResetEmailSender(t, db, []string{admin.Member.Username}, resetSender)
		adminActor := newTestActor(t, "admin", server.URL, admin.Member.Username, admin.Password)
		adminActor.Login()
		targetActor := newTestActor(t, "target", server.URL, target.Member.Username, target.Password)
		targetActor.Login()

		userTabBody := requireStatus(t, adminActor.Get("/admin?tab=users&q="+url.QueryEscape(target.Member.Username)+"&member_id="+strconv.FormatInt(target.Member.ID, 10)), http.StatusOK)
		requireBodyContains(t, userTabBody, "Users")
		requireBodyContains(t, userTabBody, fmt.Sprintf(`data-testid="admin-user-result-%d"`, target.Member.ID))
		requireBodyContains(t, userTabBody, fmt.Sprintf(`data-testid="admin-user-detail-%d"`, target.Member.ID))
		requireBodyContains(t, userTabBody, fmt.Sprintf(`data-testid="admin-user-send-verification-%d"`, target.Member.ID))
		requireBodyContains(t, userTabBody, fmt.Sprintf(`data-testid="admin-user-verified-%d">Verified</span>`, target.Member.ID))

		beforeDisableAuditCount := countAdminAuditEventsForActorAction(t, ctx, db, admin.Member.ID, service.AdminAuditActionUserDisable)
		disableLoc := requireRedirectPath(t, adminActor.PostForm("/admin/users/action", formKV(
			"action", "disable_user",
			"member_id", strconv.FormatInt(target.Member.ID, 10),
			"q", target.Member.Username,
			"reason", "disable for account recovery policy",
		)), "/admin")
		requireQueryValue(t, disableLoc, "tab", "users")
		requireQueryValue(t, disableLoc, "member_id", strconv.FormatInt(target.Member.ID, 10))

		afterDisableAuditCount := countAdminAuditEventsForActorAction(t, ctx, db, admin.Member.ID, service.AdminAuditActionUserDisable)
		if afterDisableAuditCount != beforeDisableAuditCount+1 {
			t.Fatalf("expected one user-disable audit event, before=%d after=%d", beforeDisableAuditCount, afterDisableAuditCount)
		}

		targetAfterDisable, err := service.GetMemberByID(ctx, db, target.Member.ID)
		if err != nil {
			t.Fatalf("load target member after disable: %v", err)
		}
		if targetAfterDisable.Enabled {
			t.Fatalf("expected target member to be disabled")
		}

		targetAsHelperHopAfterDisable, err := service.GetHopByID(ctx, db, org.ID, targetAsHelperHop.ID)
		if err != nil {
			t.Fatalf("load target-as-helper hop after disable: %v", err)
		}
		if targetAsHelperHopAfterDisable.Status != types.HopStatusOpen || targetAsHelperHopAfterDisable.AcceptedBy != nil {
			t.Fatalf("expected target-as-helper hop to reopen, got status=%q accepted_by=%v", targetAsHelperHopAfterDisable.Status, targetAsHelperHopAfterDisable.AcceptedBy)
		}

		targetAsRequesterHopAfterDisable, err := service.GetHopByID(ctx, db, org.ID, targetAsRequesterHop.ID)
		if err != nil {
			t.Fatalf("load target-as-requester hop after disable: %v", err)
		}
		if targetAsRequesterHopAfterDisable.Status != types.HopStatusOpen || targetAsRequesterHopAfterDisable.AcceptedBy != nil {
			t.Fatalf("expected target-as-requester hop to reopen, got status=%q accepted_by=%v", targetAsRequesterHopAfterDisable.Status, targetAsRequesterHopAfterDisable.AcceptedBy)
		}

		if countReopenedHopNotificationMessages(t, ctx, db, requester.Member.ID) < 1 {
			t.Fatalf("expected requester to receive reopen notification")
		}
		if countReopenedHopNotificationMessages(t, ctx, db, helper.Member.ID) < 1 {
			t.Fatalf("expected helper to receive reopen notification")
		}

		disabledLoginActor := newTestActor(t, "disabled-target-login", server.URL, target.Member.Username, target.Password)
		disabledLoginBody := requireStatus(t, disabledLoginActor.PostForm("/login", formKV(
			"username", target.Member.Username,
			"password", target.Password,
		)), http.StatusOK)
		requireBodyContains(t, disabledLoginBody, "Invalid username or password.")

		enableLoc := requireRedirectPath(t, adminActor.PostForm("/admin/users/action", formKV(
			"action", "enable_user",
			"member_id", strconv.FormatInt(target.Member.ID, 10),
			"q", target.Member.Username,
			"reason", "restore account access",
		)), "/admin")
		requireQueryValue(t, enableLoc, "success", "User re-enabled.")

		targetAfterEnable, err := service.GetMemberByID(ctx, db, target.Member.ID)
		if err != nil {
			t.Fatalf("load target member after enable: %v", err)
		}
		if !targetAfterEnable.Enabled {
			t.Fatalf("expected target member to be enabled")
		}

		forceResetLoc := requireRedirectPath(t, adminActor.PostForm("/admin/users/action", formKV(
			"action", "force_password_reset",
			"member_id", strconv.FormatInt(target.Member.ID, 10),
			"q", target.Member.Username,
			"reason", "credential recovery",
		)), "/admin")
		requireQueryValue(t, forceResetLoc, "success", "Password reset forced. User must use Forgot Password.")

		oldPasswordLoginBody := requireStatus(t, disabledLoginActor.PostForm("/login", formKV(
			"username", target.Member.Username,
			"password", target.Password,
		)), http.StatusOK)
		requireBodyContains(t, oldPasswordLoginBody, "Invalid username or password.")

		anon := newTestActor(t, "anon", server.URL, "", "")
		forgotBody := requireStatus(t, anon.PostForm("/forgot-password", formKV(
			"email", target.Member.Email,
		)), http.StatusOK)
		requireBodyContains(t, forgotBody, "If an account exists for that email")
		requireBodyNotContains(t, forgotBody, "/reset-password?token=")
		resetEmail, ok := resetSender.Last()
		if !ok {
			t.Fatalf("expected password reset email for forced-reset user")
		}
		token := extractResetTokenFromURL(t, resetEmail.ResetURL)

		requireRedirectPath(t, anon.PostForm("/reset-password", formKV(
			"token", token,
			"new_password", "TargetNewPassword123!",
			"confirm_password", "TargetNewPassword123!",
		)), "/login")

		targetWithNewPassword := newTestActor(t, "target-new-password", server.URL, target.Member.Username, "TargetNewPassword123!")
		targetWithNewPassword.Login()
		requireStatus(t, targetWithNewPassword.Get("/my-hopshare"), http.StatusOK)

		revokeSessionsLoc := requireRedirectPath(t, adminActor.PostForm("/admin/users/action", formKV(
			"action", "revoke_sessions",
			"member_id", strconv.FormatInt(target.Member.ID, 10),
			"q", target.Member.Username,
			"reason", "security session invalidation",
		)), "/admin")
		requireQueryValue(t, revokeSessionsLoc, "tab", "users")
		requireRedirectPath(t, targetWithNewPassword.Get("/my-hopshare"), "/login")

		adjustHoursLoc := requireRedirectPath(t, adminActor.PostForm("/admin/users/action", formKV(
			"action", "adjust_hours",
			"member_id", strconv.FormatInt(target.Member.ID, 10),
			"org_id", strconv.FormatInt(org.ID, 10),
			"hours_delta", "3",
			"q", target.Member.Username,
			"reason", "manual correction",
		)), "/admin")
		requireQueryValue(t, adjustHoursLoc, "success", "Hour balance adjusted.")

		stats, err := service.MemberStats(ctx, db, org.ID, target.Member.ID)
		if err != nil {
			t.Fatalf("load member stats after hour adjustment: %v", err)
		}
		expectedBalance := service.DefaultTimebankStartingBalance + 3
		if stats.BalanceHours != expectedBalance {
			t.Fatalf("expected adjusted balance of %d, got %d", expectedBalance, stats.BalanceHours)
		}

		var adjustmentCount int
		if err := db.QueryRowContext(ctx, `
			SELECT COUNT(*)
			FROM hour_balance_adjustments
			WHERE organization_id = $1
			  AND member_id = $2
			  AND admin_member_id = $3
			  AND hours_delta = 3
		`, org.ID, target.Member.ID, admin.Member.ID).Scan(&adjustmentCount); err != nil {
			t.Fatalf("count hour balance adjustments: %v", err)
		}
		if adjustmentCount == 0 {
			t.Fatalf("expected at least one hour adjustment ledger row")
		}
	})

	t.Run("ADMIN-04A users tab send verification email action is available for unverified users", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()

		suffix := uniqueTestSuffix()
		admin := createSeededMember(t, ctx, db, "admin_users_verify_admin", suffix)
		target := createSeededMember(t, ctx, db, "admin_users_verify_target", suffix)
		if err := service.UpdateMemberVerified(ctx, db, target.Member.ID, false); err != nil {
			t.Fatalf("mark target unverified: %v", err)
		}

		resetSender := &recordingPasswordResetEmailSender{}
		server := newHTTPServerWithAdminsAndPasswordResetEmailSender(t, db, []string{admin.Member.Username}, resetSender)
		adminActor := newTestActor(t, "admin", server.URL, admin.Member.Username, admin.Password)
		adminActor.Login()

		userTabBody := requireStatus(t, adminActor.Get("/admin?tab=users&q="+url.QueryEscape(target.Member.Username)+"&member_id="+strconv.FormatInt(target.Member.ID, 10)), http.StatusOK)
		requireBodyContains(t, userTabBody, fmt.Sprintf(`data-testid="admin-user-send-verification-%d"`, target.Member.ID))
		requireBodyContains(t, userTabBody, fmt.Sprintf(`data-testid="admin-user-verified-%d">Not Verified</span>`, target.Member.ID))

		beforeAuditCount := countAdminAuditEventsForActorAction(t, ctx, db, admin.Member.ID, service.AdminAuditActionUserVerificationEmail)
		beforeEmailCount := resetSender.VerificationCount()
		sendLoc := requireRedirectPath(t, adminActor.PostForm("/admin/users/action", formKV(
			"action", "send_verification_email",
			"member_id", strconv.FormatInt(target.Member.ID, 10),
			"q", target.Member.Username,
			"reason", "requested by user support",
		)), "/admin")
		requireQueryValue(t, sendLoc, "success", "Verification email sent.")

		afterAuditCount := countAdminAuditEventsForActorAction(t, ctx, db, admin.Member.ID, service.AdminAuditActionUserVerificationEmail)
		if afterAuditCount != beforeAuditCount+1 {
			t.Fatalf("expected one verification-email audit event, before=%d after=%d", beforeAuditCount, afterAuditCount)
		}
		if resetSender.VerificationCount() != beforeEmailCount+1 {
			t.Fatalf("expected one verification email, before=%d after=%d", beforeEmailCount, resetSender.VerificationCount())
		}

		verifyEmail, ok := resetSender.LastVerification()
		if !ok {
			t.Fatalf("expected verification email")
		}
		if verifyEmail.ToEmail != target.Member.Email {
			t.Fatalf("verification email recipient mismatch: got=%q want=%q", verifyEmail.ToEmail, target.Member.Email)
		}
		if verifyEmail.Username != target.Member.Username {
			t.Fatalf("verification email username mismatch: got=%q want=%q", verifyEmail.Username, target.Member.Username)
		}
		verifyToken := extractVerifyTokenFromURL(t, verifyEmail.VerifyURL)
		if verifyToken == "" {
			t.Fatalf("expected non-empty verification token")
		}
	})
}

func TestAdminMessagesHTTP(t *testing.T) {
	db := requireHTTPTestDB(t)

	t.Run("ADMIN-05 messages tab allows admin send, user reply, and audits each send", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()

		suffix := uniqueTestSuffix()
		admin := createSeededMember(t, ctx, db, "admin_messages_admin", suffix)
		recipient := createSeededMember(t, ctx, db, "admin_messages_recipient", suffix)

		server := newHTTPServerWithAdmins(t, db, []string{admin.Member.Username})
		adminActor := newTestActor(t, "admin", server.URL, admin.Member.Username, admin.Password)
		adminActor.Login()
		recipientActor := newTestActor(t, "recipient", server.URL, recipient.Member.Username, recipient.Password)
		recipientActor.Login()

		searchBody := requireStatus(t, adminActor.Get("/admin?tab=messages&q="+url.QueryEscape(recipient.Member.Username)), http.StatusOK)
		requireBodyContains(t, searchBody, "Admin Messages")
		requireBodyContains(t, searchBody, fmt.Sprintf(`data-testid="admin-message-result-%d"`, recipient.Member.ID))

		beforeAuditCount := countAdminAuditEventsForActorAction(t, ctx, db, admin.Member.ID, service.AdminAuditActionMessageSend)
		firstLoc := requireRedirectPath(t, adminActor.PostForm("/admin/messages/send", formKV(
			"recipient_id", strconv.FormatInt(recipient.Member.ID, 10),
			"q", recipient.Member.Username,
			"subject", "Account update "+suffix,
			"body", "Message body "+suffix,
		)), "/admin")
		requireQueryValue(t, firstLoc, "tab", "messages")
		requireQueryValue(t, firstLoc, "recipient_id", strconv.FormatInt(recipient.Member.ID, 10))
		requireQueryValue(t, firstLoc, "success", "Message sent.")

		afterAuditCount := countAdminAuditEventsForActorAction(t, ctx, db, admin.Member.ID, service.AdminAuditActionMessageSend)
		if afterAuditCount != beforeAuditCount+1 {
			t.Fatalf("expected one admin message-send audit event, before=%d after=%d", beforeAuditCount, afterAuditCount)
		}

		firstSubject := "ADMIN Message: Account update " + suffix
		firstMessage := findMessageBySubjectForRecipient(t, ctx, db, recipient.Member.ID, firstSubject)
		if firstMessage.SenderID == nil || *firstMessage.SenderID != admin.Member.ID {
			t.Fatalf("expected admin sender id %d on sent message, got %+v", admin.Member.ID, firstMessage.SenderID)
		}
		if firstMessage.MessageType != types.MessageTypeInformation {
			t.Fatalf("expected information message type, got %q", firstMessage.MessageType)
		}

		recipientInboxBody := requireStatus(t, recipientActor.Get("/messages"), http.StatusOK)
		requireBodyContains(t, recipientInboxBody, firstSubject)

		replyText := "Reply from recipient " + suffix
		replyLoc := requireRedirectPath(t, recipientActor.PostForm("/messages/reply", formKV(
			"message_id", strconv.FormatInt(firstMessage.ID, 10),
			"body", replyText,
		)), "/messages")
		requireQueryValue(t, replyLoc, "success", "Reply sent.")

		adminInboxBody := requireStatus(t, adminActor.Get("/messages"), http.StatusOK)
		requireBodyContains(t, adminInboxBody, "Re: "+firstSubject)

		conversationBody := requireStatus(t, adminActor.Get("/admin?tab=messages&recipient_id="+strconv.FormatInt(recipient.Member.ID, 10)+"&q="+url.QueryEscape(recipient.Member.Username)), http.StatusOK)
		requireBodyContains(t, conversationBody, replyText)
		requireBodyContains(t, conversationBody, firstSubject)

		secondLoc := requireRedirectPath(t, adminActor.PostForm("/admin/messages/send", formKV(
			"recipient_id", strconv.FormatInt(recipient.Member.ID, 10),
			"q", recipient.Member.Username,
			"subject", "ADMIN Message: Follow-up "+suffix,
			"body", "Second admin message "+suffix,
		)), "/admin")
		requireQueryValue(t, secondLoc, "success", "Message sent.")

		secondSubject := "ADMIN Message: Follow-up " + suffix
		secondMessage := findMessageBySubjectForRecipient(t, ctx, db, recipient.Member.ID, secondSubject)
		if strings.Count(secondMessage.Subject, adminMessageSubjectPrefixInTests) != 1 {
			t.Fatalf("expected exactly one admin subject prefix, got %q", secondMessage.Subject)
		}
	})
}

func TestAdminAuditHTTP(t *testing.T) {
	db := requireHTTPTestDB(t)

	t.Run("ADMIN-06 audit tab filters events and exports csv/json", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()

		suffix := uniqueTestSuffix()
		admin := createSeededMember(t, ctx, db, "admin_audit_admin", suffix)
		owner := createSeededMember(t, ctx, db, "admin_audit_owner", suffix)
		target := createSeededMember(t, ctx, db, "admin_audit_target", suffix)
		nonAdmin := createSeededMember(t, ctx, db, "admin_audit_non_admin", suffix)

		org, err := service.CreateOrganization(ctx, db, "Admin Audit Org "+suffix, "City", "ST", "Org for admin audit integration coverage.", owner.Member.ID)
		if err != nil {
			t.Fatalf("create organization: %v", err)
		}

		now := time.Now().UTC().Add(-2 * time.Minute)
		disableMetadata, err := json.Marshal(map[string]any{
			"tab":    "organizations",
			"org_id": org.ID,
		})
		if err != nil {
			t.Fatalf("marshal disable metadata: %v", err)
		}
		disableEvent, err := service.WriteAdminAuditEvent(ctx, db, service.WriteAdminAuditEventParams{
			ActorMemberID: admin.Member.ID,
			Action:        service.AdminAuditActionOrganizationDisable,
			Target:        fmt.Sprintf("organization:%d", org.ID),
			Reason:        "maintenance window",
			Metadata:      disableMetadata,
			OccurredAt:    now.Add(-time.Minute),
		})
		if err != nil {
			t.Fatalf("write disable audit event: %v", err)
		}

		messageMetadata, err := json.Marshal(map[string]any{
			"tab":                 "messages",
			"org_id":              org.ID,
			"recipient_member_id": target.Member.ID,
		})
		if err != nil {
			t.Fatalf("marshal message metadata: %v", err)
		}
		messageEvent, err := service.WriteAdminAuditEvent(ctx, db, service.WriteAdminAuditEventParams{
			ActorMemberID: admin.Member.ID,
			Action:        service.AdminAuditActionMessageSend,
			Target:        fmt.Sprintf("member:%d", target.Member.ID),
			Metadata:      messageMetadata,
			OccurredAt:    now,
		})
		if err != nil {
			t.Fatalf("write message audit event: %v", err)
		}

		userMetadata, err := json.Marshal(map[string]any{
			"tab":       "users",
			"member_id": target.Member.ID,
		})
		if err != nil {
			t.Fatalf("marshal user metadata: %v", err)
		}
		userEvent, err := service.WriteAdminAuditEvent(ctx, db, service.WriteAdminAuditEventParams{
			ActorMemberID: admin.Member.ID,
			Action:        service.AdminAuditActionUserEnable,
			Target:        fmt.Sprintf("member:%d", target.Member.ID),
			Metadata:      userMetadata,
			OccurredAt:    now.Add(time.Minute),
		})
		if err != nil {
			t.Fatalf("write user audit event: %v", err)
		}

		server := newHTTPServerWithAdmins(t, db, []string{admin.Member.Username})
		adminActor := newTestActor(t, "admin", server.URL, admin.Member.Username, admin.Password)
		adminActor.Login()
		nonAdminActor := newTestActor(t, "non-admin", server.URL, nonAdmin.Member.Username, nonAdmin.Password)
		nonAdminActor.Login()

		auditBody := requireStatus(t, adminActor.Get("/admin?tab=audit"), http.StatusOK)
		requireBodyContains(t, auditBody, "Admin Audit Log")
		requireBodyContains(t, auditBody, fmt.Sprintf(`data-testid="admin-audit-event-%d"`, disableEvent.ID))
		requireBodyContains(t, auditBody, fmt.Sprintf(`data-testid="admin-audit-event-%d"`, messageEvent.ID))
		requireBodyContains(t, auditBody, fmt.Sprintf(`data-testid="admin-audit-event-%d"`, userEvent.ID))

		filterDay := now.Format("2006-01-02")
		filteredURL := "/admin?tab=audit" +
			"&actor=" + url.QueryEscape(admin.Member.Username) +
			"&action=" + url.QueryEscape(service.AdminAuditActionMessageSend) +
			"&organization=" + url.QueryEscape(org.Name) +
			"&user=" + url.QueryEscape(target.Member.Username) +
			"&target=" + url.QueryEscape("member:") +
			"&start_date=" + url.QueryEscape(filterDay) +
			"&end_date=" + url.QueryEscape(filterDay)
		filteredBody := requireStatus(t, adminActor.Get(filteredURL), http.StatusOK)
		requireBodyContains(t, filteredBody, fmt.Sprintf(`data-testid="admin-audit-event-%d"`, messageEvent.ID))
		requireBodyNotContains(t, filteredBody, fmt.Sprintf(`data-testid="admin-audit-event-%d"`, disableEvent.ID))
		requireBodyNotContains(t, filteredBody, fmt.Sprintf(`data-testid="admin-audit-event-%d"`, userEvent.ID))

		exportQuery := "actor=" + url.QueryEscape(admin.Member.Username) +
			"&action=" + url.QueryEscape(service.AdminAuditActionMessageSend) +
			"&organization=" + url.QueryEscape(org.Name) +
			"&user=" + url.QueryEscape(target.Member.Username) +
			"&target=" + url.QueryEscape("member:") +
			"&start_date=" + url.QueryEscape(filterDay) +
			"&end_date=" + url.QueryEscape(filterDay)

		beforeCSVExportAuditCount := countAdminAuditEventsForActorAction(t, ctx, db, admin.Member.ID, service.AdminAuditActionExportCSV)
		beforeJSONExportAuditCount := countAdminAuditEventsForActorAction(t, ctx, db, admin.Member.ID, service.AdminAuditActionExportJSON)

		csvResp := adminActor.Get("/admin/audit/export?format=csv&" + exportQuery)
		csvBodyBytes, _ := io.ReadAll(csvResp.Body)
		_ = csvResp.Body.Close()
		if csvResp.StatusCode != http.StatusOK {
			t.Fatalf("expected csv export status %d, got %d body=%q", http.StatusOK, csvResp.StatusCode, string(csvBodyBytes))
		}
		if !strings.Contains(csvResp.Header.Get("Content-Type"), "text/csv") {
			t.Fatalf("expected csv export content type, got %q", csvResp.Header.Get("Content-Type"))
		}
		csvBody := string(csvBodyBytes)
		requireBodyContains(t, csvBody, "admin.message.send")
		requireBodyContains(t, csvBody, strconv.FormatInt(messageEvent.ID, 10))
		afterCSVExportAuditCount := countAdminAuditEventsForActorAction(t, ctx, db, admin.Member.ID, service.AdminAuditActionExportCSV)
		if afterCSVExportAuditCount != beforeCSVExportAuditCount+1 {
			t.Fatalf("expected one admin csv-export audit event, before=%d after=%d", beforeCSVExportAuditCount, afterCSVExportAuditCount)
		}

		jsonResp := adminActor.Get("/admin/audit/export?format=json&" + exportQuery)
		jsonBodyBytes, _ := io.ReadAll(jsonResp.Body)
		_ = jsonResp.Body.Close()
		if jsonResp.StatusCode != http.StatusOK {
			t.Fatalf("expected json export status %d, got %d body=%q", http.StatusOK, jsonResp.StatusCode, string(jsonBodyBytes))
		}
		if !strings.Contains(jsonResp.Header.Get("Content-Type"), "application/json") {
			t.Fatalf("expected json export content type, got %q", jsonResp.Header.Get("Content-Type"))
		}
		var exported []types.AdminAuditEventView
		if err := json.Unmarshal(jsonBodyBytes, &exported); err != nil {
			t.Fatalf("unmarshal json export: %v body=%q", err, string(jsonBodyBytes))
		}
		if len(exported) == 0 {
			t.Fatalf("expected json export to include at least one event")
		}
		afterJSONExportAuditCount := countAdminAuditEventsForActorAction(t, ctx, db, admin.Member.ID, service.AdminAuditActionExportJSON)
		if afterJSONExportAuditCount != beforeJSONExportAuditCount+1 {
			t.Fatalf("expected one admin json-export audit event, before=%d after=%d", beforeJSONExportAuditCount, afterJSONExportAuditCount)
		}

		unauthorizedBody := requireStatus(t, nonAdminActor.Get("/admin/audit/export?format=csv"), http.StatusForbidden)
		requireBodyContains(t, unauthorizedBody, "Unauthorized")
	})
}

func TestAdminAuthorizationBoundariesHTTP(t *testing.T) {
	db := requireHTTPTestDB(t)

	t.Run("ADMIN-07 admin endpoints require authentication and admin role", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()

		suffix := uniqueTestSuffix()
		admin := createSeededMember(t, ctx, db, "admin_authz_admin", suffix)
		nonAdmin := createSeededMember(t, ctx, db, "admin_authz_member", suffix)

		server := newHTTPServerWithAdmins(t, db, []string{admin.Member.Username})
		anonActor := newTestActor(t, "anon", server.URL, "", "")
		nonAdminActor := newTestActor(t, "non-admin", server.URL, nonAdmin.Member.Username, nonAdmin.Password)
		nonAdminActor.Login()

		requireRedirectPath(t, anonActor.Get("/admin"), "/login")
		requireRedirectPath(t, anonActor.PostForm("/admin/organizations/action", formKV(
			"action", "disable_org",
			"org_id", "1",
			"reason", "authz test",
		)), "/login")
		requireRedirectPath(t, anonActor.PostForm("/admin/moderation/action", formKV(
			"action", "dismiss_report",
			"report_id", "1",
			"reason", "authz test",
		)), "/login")
		requireRedirectPath(t, anonActor.PostForm("/admin/users/action", formKV(
			"action", "disable_user",
			"member_id", "1",
			"reason", "authz test",
		)), "/login")
		requireRedirectPath(t, anonActor.PostForm("/admin/messages/send", formKV(
			"recipient_id", "1",
			"subject", "authz test",
			"body", "authz test",
		)), "/login")
		requireRedirectPath(t, anonActor.Get("/admin/audit/export?format=csv"), "/login")

		requireStatus(t, nonAdminActor.Get("/admin"), http.StatusForbidden)
		requireStatus(t, nonAdminActor.PostForm("/admin/organizations/action", formKV(
			"action", "disable_org",
			"org_id", "1",
			"reason", "authz test",
		)), http.StatusForbidden)
		requireStatus(t, nonAdminActor.PostForm("/admin/moderation/action", formKV(
			"action", "dismiss_report",
			"report_id", "1",
			"reason", "authz test",
		)), http.StatusForbidden)
		requireStatus(t, nonAdminActor.PostForm("/admin/users/action", formKV(
			"action", "disable_user",
			"member_id", "1",
			"reason", "authz test",
		)), http.StatusForbidden)
		requireStatus(t, nonAdminActor.PostForm("/admin/messages/send", formKV(
			"recipient_id", "1",
			"subject", "authz test",
			"body", "authz test",
		)), http.StatusForbidden)
		requireStatus(t, nonAdminActor.Get("/admin/audit/export?format=csv"), http.StatusForbidden)
	})
}

const adminMessageSubjectPrefixInTests = "ADMIN Message:"

func createAdminOverviewHop(t *testing.T, ctx context.Context, db *sql.DB, orgID, memberID int64, title, neededByKind string, neededByDate *time.Time) types.Hop {
	t.Helper()

	hop, err := service.CreateHop(ctx, db, service.CreateHopParams{
		OrganizationID: orgID,
		MemberID:       memberID,
		Title:          title,
		Details:        "admin overview integration test hop",
		EstimatedHours: 2,
		NeededByKind:   neededByKind,
		NeededByDate:   neededByDate,
		IsPrivate:      false,
	})
	if err != nil {
		t.Fatalf("create hop %q: %v", title, err)
	}
	return hop
}

func queryAdminOverviewExpectation(t *testing.T, ctx context.Context, db *sql.DB) adminOverviewExpectation {
	t.Helper()

	var expected adminOverviewExpectation
	expected.HopStatusCounts = make(map[string]int)

	if err := db.QueryRowContext(ctx, `
		SELECT
			COALESCE(SUM(CASE WHEN enabled THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN enabled THEN 0 ELSE 1 END), 0)
		FROM organizations
	`).Scan(&expected.OrganizationEnabledCount, &expected.OrganizationDisabledCount); err != nil {
		t.Fatalf("query organization enabled/disabled counts: %v", err)
	}

	if err := db.QueryRowContext(ctx, `
		SELECT
			COALESCE(SUM(CASE WHEN enabled THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN enabled THEN 0 ELSE 1 END), 0)
		FROM members
	`).Scan(&expected.UserEnabledCount, &expected.UserDisabledCount); err != nil {
		t.Fatalf("query user enabled/disabled counts: %v", err)
	}

	if err := db.QueryRowContext(ctx, `
		SELECT
			COALESCE(SUM(CASE WHEN verified THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN verified THEN 0 ELSE 1 END), 0)
		FROM members
	`).Scan(&expected.UserVerifiedCount, &expected.UserNotVerifiedCount); err != nil {
		t.Fatalf("query user verified/not-verified counts: %v", err)
	}

	if err := db.QueryRowContext(ctx, `
			SELECT
				COUNT(*),
				COALESCE(SUM(CASE WHEN hours_delta > 0 THEN hours_delta ELSE 0 END), 0),
				COALESCE(SUM(CASE WHEN hours_delta < 0 THEN -hours_delta ELSE 0 END), 0)
			FROM hour_balance_adjustments
			WHERE is_starting_balance = FALSE
		`).Scan(&expected.OverrideCount, &expected.OverrideHoursGiven, &expected.OverrideHoursRemoved); err != nil {
		t.Fatalf("query admin override counts: %v", err)
	}

	hopRows, err := db.QueryContext(ctx, `
		SELECT status, COUNT(*)
		FROM hops
		GROUP BY status
	`)
	if err != nil {
		t.Fatalf("query hop counts by status: %v", err)
	}
	for hopRows.Next() {
		var status string
		var count int
		if err := hopRows.Scan(&status, &count); err != nil {
			hopRows.Close()
			t.Fatalf("scan hop counts by status: %v", err)
		}
		expected.HopStatusCounts[status] = count
	}
	if err := hopRows.Err(); err != nil {
		hopRows.Close()
		t.Fatalf("iterate hop counts by status: %v", err)
	}
	if err := hopRows.Close(); err != nil {
		t.Fatalf("close hop counts rows: %v", err)
	}

	if err := db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(hours), 0)
		FROM hop_transactions
	`).Scan(&expected.TotalHoursExchanged); err != nil {
		t.Fatalf("query total exchanged hours: %v", err)
	}

	expected.TopByHopsCreated = queryAdminLeaderboardByValueExpectation(t, ctx, db, `
		SELECT
			o.id,
			o.enabled,
			COUNT(h.id) AS metric_value
		FROM organizations o
		LEFT JOIN hops h ON h.organization_id = o.id
		GROUP BY o.id, o.name, o.enabled
		ORDER BY metric_value DESC, o.name ASC
		LIMIT $1
	`)

	expected.TopByHoursExchanged = queryAdminLeaderboardByValueExpectation(t, ctx, db, `
		SELECT
			o.id,
			o.enabled,
			COALESCE(SUM(ht.hours), 0) AS metric_value
		FROM organizations o
		LEFT JOIN hop_transactions ht ON ht.organization_id = o.id
		GROUP BY o.id, o.name, o.enabled
		ORDER BY metric_value DESC, o.name ASC
		LIMIT $1
	`)

	userRows, err := db.QueryContext(ctx, `
		SELECT
			o.id,
			o.enabled,
			COALESCE(COUNT(CASE WHEN om.member_id IS NOT NULL AND om.left_at IS NULL THEN 1 END), 0) AS total_users,
			COALESCE(COUNT(CASE WHEN om.member_id IS NOT NULL AND om.left_at IS NULL AND m.enabled THEN 1 END), 0) AS enabled_users,
			COALESCE(COUNT(CASE WHEN om.member_id IS NOT NULL AND om.left_at IS NULL AND NOT m.enabled THEN 1 END), 0) AS disabled_users
		FROM organizations o
		LEFT JOIN organization_memberships om ON om.organization_id = o.id
		LEFT JOIN members m ON m.id = om.member_id
		GROUP BY o.id, o.name, o.enabled
		ORDER BY total_users DESC, o.name ASC
		LIMIT $1
	`, adminOverviewLeaderboardLimit)
	if err != nil {
		t.Fatalf("query leaderboard by users: %v", err)
	}
	for userRows.Next() {
		var row adminLeaderboardExpectation
		if err := userRows.Scan(&row.OrganizationID, &row.OrganizationEnabled, &row.Value, &row.EnabledUsers, &row.DisabledUsers); err != nil {
			userRows.Close()
			t.Fatalf("scan leaderboard by users: %v", err)
		}
		expected.TopByUsers = append(expected.TopByUsers, row)
	}
	if err := userRows.Err(); err != nil {
		userRows.Close()
		t.Fatalf("iterate leaderboard by users: %v", err)
	}
	if err := userRows.Close(); err != nil {
		t.Fatalf("close leaderboard by users rows: %v", err)
	}

	return expected
}

func queryAdminLeaderboardByValueExpectation(t *testing.T, ctx context.Context, db *sql.DB, query string) []adminLeaderboardExpectation {
	t.Helper()

	rows, err := db.QueryContext(ctx, query, adminOverviewLeaderboardLimit)
	if err != nil {
		t.Fatalf("query admin leaderboard by value: %v", err)
	}
	defer rows.Close()

	var out []adminLeaderboardExpectation
	for rows.Next() {
		var row adminLeaderboardExpectation
		if err := rows.Scan(&row.OrganizationID, &row.OrganizationEnabled, &row.Value); err != nil {
			t.Fatalf("scan admin leaderboard by value: %v", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate admin leaderboard by value: %v", err)
	}
	return out
}

func assertAdminLeaderboardByValue(t *testing.T, body string, keyPrefix string, rows []adminLeaderboardExpectation) {
	t.Helper()

	for _, row := range rows {
		requireBodyContains(t, body, fmt.Sprintf(`data-testid="admin-leaderboard-%s-org-%d"`, keyPrefix, row.OrganizationID))
		requireBodyContains(t, body, fmt.Sprintf(`data-testid="admin-leaderboard-%s-value-%d">%d</span>`, keyPrefix, row.OrganizationID, row.Value))
		requireBodyContains(t, body, fmt.Sprintf(`data-testid="admin-leaderboard-%s-org-%d" data-org-enabled="%s"`, keyPrefix, row.OrganizationID, boolString(row.OrganizationEnabled)))
	}
}

func assertAdminLeaderboardByUsers(t *testing.T, body string, rows []adminLeaderboardExpectation) {
	t.Helper()

	for _, row := range rows {
		requireBodyContains(t, body, fmt.Sprintf(`data-testid="admin-leaderboard-users-org-%d"`, row.OrganizationID))
		requireBodyContains(t, body, fmt.Sprintf(`data-testid="admin-leaderboard-users-org-%d" data-org-enabled="%s"`, row.OrganizationID, boolString(row.OrganizationEnabled)))
		requireBodyContains(t, body, fmt.Sprintf(`data-testid="admin-leaderboard-users-value-%d">%d</span>`, row.OrganizationID, row.Value))
		requireBodyContains(t, body, fmt.Sprintf(`data-testid="admin-leaderboard-users-enabled-%d">%d</span>`, row.OrganizationID, row.EnabledUsers))
		requireBodyContains(t, body, fmt.Sprintf(`data-testid="admin-leaderboard-users-disabled-%d">%d</span>`, row.OrganizationID, row.DisabledUsers))
	}
}

func boolString(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func countAdminAuditEventsForActorAction(t *testing.T, ctx context.Context, db *sql.DB, actorMemberID int64, action string) int {
	t.Helper()

	var count int
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM admin_audit_events
		WHERE actor_member_id = $1
		  AND action = $2
	`, actorMemberID, action).Scan(&count); err != nil {
		t.Fatalf("count admin audit events actor=%d action=%q: %v", actorMemberID, action, err)
	}
	return count
}

func countReopenedHopNotificationMessages(t *testing.T, ctx context.Context, db *sql.DB, recipientMemberID int64) int {
	t.Helper()

	var count int
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM messages
		WHERE recipient_member_id = $1
		  AND subject LIKE 'Accepted hop reopened:%'
	`, recipientMemberID).Scan(&count); err != nil {
		t.Fatalf("count reopened hop notification messages recipient=%d: %v", recipientMemberID, err)
	}
	return count
}

func moderationReportIDForComment(t *testing.T, ctx context.Context, db *sql.DB, commentID int64) int64 {
	t.Helper()

	var reportID int64
	if err := db.QueryRowContext(ctx, `
		SELECT id
		FROM moderation_reports
		WHERE report_type = $1
		  AND hop_comment_id = $2
		ORDER BY id DESC
		LIMIT 1
	`, types.ModerationReportTypeHopComment, commentID).Scan(&reportID); err != nil {
		t.Fatalf("query moderation report for comment %d: %v", commentID, err)
	}
	return reportID
}

func moderationReportIDForImage(t *testing.T, ctx context.Context, db *sql.DB, imageID int64) int64 {
	t.Helper()

	var reportID int64
	if err := db.QueryRowContext(ctx, `
		SELECT id
		FROM moderation_reports
		WHERE report_type = $1
		  AND hop_image_id = $2
		ORDER BY id DESC
		LIMIT 1
	`, types.ModerationReportTypeHopImage, imageID).Scan(&reportID); err != nil {
		t.Fatalf("query moderation report for image %d: %v", imageID, err)
	}
	return reportID
}

func moderationContainsCommentID(comments []types.HopComment, commentID int64) bool {
	for _, comment := range comments {
		if comment.ID == commentID {
			return true
		}
	}
	return false
}

func moderationContainsImageID(images []types.HopImage, imageID int64) bool {
	for _, img := range images {
		if img.ID == imageID {
			return true
		}
	}
	return false
}

func assertModerationReportResolution(t *testing.T, ctx context.Context, db *sql.DB, reportID int64, expectedStatus, expectedResolutionAction string) {
	t.Helper()

	var status string
	var resolutionAction sql.NullString
	var resolvedBy sql.NullInt64
	var resolvedAt sql.NullTime
	if err := db.QueryRowContext(ctx, `
		SELECT status, resolution_action, resolved_by_member_id, resolved_at
		FROM moderation_reports
		WHERE id = $1
	`, reportID).Scan(&status, &resolutionAction, &resolvedBy, &resolvedAt); err != nil {
		t.Fatalf("query moderation report resolution report_id=%d: %v", reportID, err)
	}
	if status != expectedStatus {
		t.Fatalf("unexpected moderation report status report_id=%d got=%q want=%q", reportID, status, expectedStatus)
	}
	if !resolutionAction.Valid || resolutionAction.String != expectedResolutionAction {
		t.Fatalf("unexpected moderation report resolution action report_id=%d got=%q want=%q", reportID, resolutionAction.String, expectedResolutionAction)
	}
	if !resolvedBy.Valid || resolvedBy.Int64 == 0 {
		t.Fatalf("expected moderation report %d to have resolved_by_member_id", reportID)
	}
	if !resolvedAt.Valid {
		t.Fatalf("expected moderation report %d to have resolved_at", reportID)
	}
}
