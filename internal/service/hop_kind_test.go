package service

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"hopshare/internal/types"
)

func TestCreateHopOfferKindAndMaxBalanceLimit(t *testing.T) {
	db := require_db(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	suffix := fmt.Sprintf("offer_kind_%d", time.Now().UnixNano())
	owner := createHopKindTestMember(t, ctx, db, suffix, "Owner")
	offerer := createHopKindTestMember(t, ctx, db, suffix, "Offerer")
	org := createHopKindTestOrganization(t, ctx, db, suffix, owner)
	approveHopKindTestMember(t, ctx, db, org.ID, owner.ID, offerer.ID)

	hop, err := CreateHop(ctx, db, CreateHopParams{
		OrganizationID: org.ID,
		MemberID:       offerer.ID,
		Kind:           types.HopKindOffer,
		Title:          "Offer hop " + suffix,
		Details:        "Offering time to another member.",
		EstimatedHours: 2,
		NeededByKind:   types.HopNeededByAnytime,
	})
	if err != nil {
		t.Fatalf("create offer hop: %v", err)
	}
	if hop.Kind != types.HopKindOffer {
		t.Fatalf("expected hop kind %q, got %q", types.HopKindOffer, hop.Kind)
	}

	if err := AdjustMemberHourBalance(ctx, db, AdjustMemberHourBalanceParams{
		OrganizationID: org.ID,
		MemberID:       offerer.ID,
		AdminMemberID:  owner.ID,
		HoursDelta:     5,
		Reason:         "bring offerer to max balance",
	}); err != nil {
		t.Fatalf("adjust offerer balance: %v", err)
	}

	_, err = CreateHop(ctx, db, CreateHopParams{
		OrganizationID: org.ID,
		MemberID:       offerer.ID,
		Kind:           types.HopKindOffer,
		Title:          "Blocked offer hop " + suffix,
		Details:        "Should fail at max balance.",
		EstimatedHours: 1,
		NeededByKind:   types.HopNeededByAnytime,
	})
	if err != ErrHopOfferLimit {
		t.Fatalf("expected %v, got %v", ErrHopOfferLimit, err)
	}
}

func TestOfferHopHelpBlocksOfferInterestBelowMinimumBalance(t *testing.T) {
	db := require_db(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	suffix := fmt.Sprintf("offer_interest_%d", time.Now().UnixNano())
	owner := createHopKindTestMember(t, ctx, db, suffix, "Owner")
	offerer := createHopKindTestMember(t, ctx, db, suffix, "Offerer")
	recipient := createHopKindTestMember(t, ctx, db, suffix, "Recipient")
	org := createHopKindTestOrganization(t, ctx, db, suffix, owner)
	approveHopKindTestMember(t, ctx, db, org.ID, owner.ID, offerer.ID)
	approveHopKindTestMember(t, ctx, db, org.ID, owner.ID, recipient.ID)

	hop, err := CreateHop(ctx, db, CreateHopParams{
		OrganizationID: org.ID,
		MemberID:       offerer.ID,
		Kind:           types.HopKindOffer,
		Title:          "Offer interest limit " + suffix,
		Details:        "Recipient interest should be limited by min balance.",
		EstimatedHours: 2,
		NeededByKind:   types.HopNeededByAnytime,
	})
	if err != nil {
		t.Fatalf("create offer hop: %v", err)
	}

	if err := AdjustMemberHourBalance(ctx, db, AdjustMemberHourBalanceParams{
		OrganizationID: org.ID,
		MemberID:       recipient.ID,
		AdminMemberID:  owner.ID,
		HoursDelta:     -10,
		Reason:         "bring recipient to min balance",
	}); err != nil {
		t.Fatalf("adjust recipient balance: %v", err)
	}

	err = OfferHopHelp(ctx, db, OfferHopParams{
		OrganizationID: org.ID,
		HopID:          hop.ID,
		OffererID:      recipient.ID,
		OffererName:    "Recipient Integration",
	})
	if err != ErrHopInterestLimit {
		t.Fatalf("expected %v, got %v", ErrHopInterestLimit, err)
	}
}

func TestCompleteHopWithResultOfferKindMatchedMemberMayOverrideHours(t *testing.T) {
	db := require_db(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	suffix := fmt.Sprintf("offer_complete_override_%d", time.Now().UnixNano())
	owner := createHopKindTestMember(t, ctx, db, suffix, "Owner")
	offerer := createHopKindTestMember(t, ctx, db, suffix, "Offerer")
	recipient := createHopKindTestMember(t, ctx, db, suffix, "Recipient")
	org := createHopKindTestOrganization(t, ctx, db, suffix, owner)
	approveHopKindTestMember(t, ctx, db, org.ID, owner.ID, offerer.ID)
	approveHopKindTestMember(t, ctx, db, org.ID, owner.ID, recipient.ID)

	hop := createAcceptedOfferHopForServiceTest(t, ctx, db, org.ID, offerer.ID, recipient.ID, "Offer completion override "+suffix, 4)

	result, err := CompleteHopWithResult(ctx, db, CompleteHopParams{
		OrganizationID: org.ID,
		HopID:          hop.ID,
		CompletedBy:    recipient.ID,
		Comment:        "Recipient completed the offer hop.",
		CompletedHours: 2,
	})
	if err != nil {
		t.Fatalf("complete offer hop: %v", err)
	}
	if result.HopKind != types.HopKindOffer {
		t.Fatalf("expected hop kind %q, got %q", types.HopKindOffer, result.HopKind)
	}
	if result.RequestedHours != 2 {
		t.Fatalf("expected requested hours 2, got %d", result.RequestedHours)
	}
	if result.AwardedHours != 2 {
		t.Fatalf("expected awarded hours 2, got %d", result.AwardedHours)
	}

	updated, err := GetHopByID(ctx, db, org.ID, hop.ID)
	if err != nil {
		t.Fatalf("load completed hop: %v", err)
	}
	if updated.CompletedHours == nil || *updated.CompletedHours != 2 {
		t.Fatalf("expected completed hours 2, got %v", updated.CompletedHours)
	}

	var fromMemberID int64
	var toMemberID int64
	var hours int
	if err := db.QueryRowContext(ctx, `
		SELECT from_member_id, to_member_id, hours
		FROM hop_transactions
		WHERE organization_id = $1 AND hop_id = $2
	`, org.ID, hop.ID).Scan(&fromMemberID, &toMemberID, &hours); err != nil {
		t.Fatalf("load hop transaction: %v", err)
	}
	if fromMemberID != recipient.ID || toMemberID != offerer.ID || hours != 2 {
		t.Fatalf("unexpected transaction from=%d to=%d hours=%d", fromMemberID, toMemberID, hours)
	}
}

func TestCompleteHopWithResultOfferKindCreatorCannotLowerHours(t *testing.T) {
	db := require_db(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	suffix := fmt.Sprintf("offer_complete_creator_%d", time.Now().UnixNano())
	owner := createHopKindTestMember(t, ctx, db, suffix, "Owner")
	offerer := createHopKindTestMember(t, ctx, db, suffix, "Offerer")
	recipient := createHopKindTestMember(t, ctx, db, suffix, "Recipient")
	org := createHopKindTestOrganization(t, ctx, db, suffix, owner)
	approveHopKindTestMember(t, ctx, db, org.ID, owner.ID, offerer.ID)
	approveHopKindTestMember(t, ctx, db, org.ID, owner.ID, recipient.ID)

	hop := createAcceptedOfferHopForServiceTest(t, ctx, db, org.ID, offerer.ID, recipient.ID, "Offer completion creator "+suffix, 4)

	result, err := CompleteHopWithResult(ctx, db, CompleteHopParams{
		OrganizationID: org.ID,
		HopID:          hop.ID,
		CompletedBy:    offerer.ID,
		Comment:        "Offerer completed the offer hop.",
		CompletedHours: 1,
	})
	if err != nil {
		t.Fatalf("complete offer hop as creator: %v", err)
	}
	if result.RequestedHours != 4 {
		t.Fatalf("expected requested hours to stay at estimate 4, got %d", result.RequestedHours)
	}
	if result.AwardedHours != 4 {
		t.Fatalf("expected awarded hours 4, got %d", result.AwardedHours)
	}

	updated, err := GetHopByID(ctx, db, org.ID, hop.ID)
	if err != nil {
		t.Fatalf("load completed hop: %v", err)
	}
	if updated.CompletedHours == nil || *updated.CompletedHours != 4 {
		t.Fatalf("expected completed hours 4, got %v", updated.CompletedHours)
	}
}

func TestCompleteHopWithResultOfferKindUsesAdjustmentsForLimitCalculation(t *testing.T) {
	db := require_db(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	suffix := fmt.Sprintf("offer_complete_limits_%d", time.Now().UnixNano())
	owner := createHopKindTestMember(t, ctx, db, suffix, "Owner")
	offerer := createHopKindTestMember(t, ctx, db, suffix, "Offerer")
	recipient := createHopKindTestMember(t, ctx, db, suffix, "Recipient")
	org := createHopKindTestOrganization(t, ctx, db, suffix, owner)
	approveHopKindTestMember(t, ctx, db, org.ID, owner.ID, offerer.ID)
	approveHopKindTestMember(t, ctx, db, org.ID, owner.ID, recipient.ID)

	hop := createAcceptedOfferHopForServiceTest(t, ctx, db, org.ID, offerer.ID, recipient.ID, "Offer completion limits "+suffix, 5)

	if err := AdjustMemberHourBalance(ctx, db, AdjustMemberHourBalanceParams{
		OrganizationID: org.ID,
		MemberID:       recipient.ID,
		AdminMemberID:  owner.ID,
		HoursDelta:     -8,
		Reason:         "bring payer near minimum before completion",
	}); err != nil {
		t.Fatalf("adjust recipient balance: %v", err)
	}
	if err := AdjustMemberHourBalance(ctx, db, AdjustMemberHourBalanceParams{
		OrganizationID: org.ID,
		MemberID:       offerer.ID,
		AdminMemberID:  owner.ID,
		HoursDelta:     4,
		Reason:         "bring earner near maximum before completion",
	}); err != nil {
		t.Fatalf("adjust offerer balance: %v", err)
	}

	result, err := CompleteHopWithResult(ctx, db, CompleteHopParams{
		OrganizationID: org.ID,
		HopID:          hop.ID,
		CompletedBy:    recipient.ID,
		Comment:        "Recipient completed with balance limits.",
		CompletedHours: 5,
	})
	if err != nil {
		t.Fatalf("complete offer hop with adjustments: %v", err)
	}
	if result.RequestedHours != 5 {
		t.Fatalf("expected requested hours 5, got %d", result.RequestedHours)
	}
	if result.AwardedHours != 1 {
		t.Fatalf("expected awarded hours 1, got %d", result.AwardedHours)
	}
	if !result.PayerMinLimited || !result.EarnerMaxLimited {
		t.Fatalf("expected both min/max limit flags, got payer=%v earner=%v", result.PayerMinLimited, result.EarnerMaxLimited)
	}
}

func TestMemberStatsAndBalanceTransactionsIncludeMixedAskOfferAndAdjustments(t *testing.T) {
	db := require_db(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	suffix := fmt.Sprintf("mixed_hops_%d", time.Now().UnixNano())
	owner := createHopKindTestMember(t, ctx, db, suffix, "Owner")
	member := createHopKindTestMember(t, ctx, db, suffix, "Member")
	helper := createHopKindTestMember(t, ctx, db, suffix, "Helper")
	recipient := createHopKindTestMember(t, ctx, db, suffix, "Recipient")
	org := createHopKindTestOrganization(t, ctx, db, suffix, owner)
	approveHopKindTestMember(t, ctx, db, org.ID, owner.ID, member.ID)
	approveHopKindTestMember(t, ctx, db, org.ID, owner.ID, helper.ID)
	approveHopKindTestMember(t, ctx, db, org.ID, owner.ID, recipient.ID)

	askHop, err := CreateHop(ctx, db, CreateHopParams{
		OrganizationID: org.ID,
		MemberID:       member.ID,
		Kind:           types.HopKindAsk,
		Title:          "Ask hop " + suffix,
		Details:        "Member needs help.",
		EstimatedHours: 2,
		NeededByKind:   types.HopNeededByAnytime,
	})
	if err != nil {
		t.Fatalf("create ask hop: %v", err)
	}
	if err := AcceptHop(ctx, db, org.ID, askHop.ID, helper.ID); err != nil {
		t.Fatalf("accept ask hop: %v", err)
	}
	if _, err := CompleteHopWithResult(ctx, db, CompleteHopParams{
		OrganizationID: org.ID,
		HopID:          askHop.ID,
		CompletedBy:    member.ID,
		Comment:        "Ask hop completed.",
		CompletedHours: 2,
	}); err != nil {
		t.Fatalf("complete ask hop: %v", err)
	}

	offerHop := createAcceptedOfferHopForServiceTest(t, ctx, db, org.ID, member.ID, recipient.ID, "Offer hop "+suffix, 3)
	if _, err := CompleteHopWithResult(ctx, db, CompleteHopParams{
		OrganizationID: org.ID,
		HopID:          offerHop.ID,
		CompletedBy:    recipient.ID,
		Comment:        "Offer hop completed.",
		CompletedHours: 3,
	}); err != nil {
		t.Fatalf("complete offer hop: %v", err)
	}

	if err := AdjustMemberHourBalance(ctx, db, AdjustMemberHourBalanceParams{
		OrganizationID: org.ID,
		MemberID:       member.ID,
		AdminMemberID:  owner.ID,
		HoursDelta:     4,
		Reason:         "bonus override",
	}); err != nil {
		t.Fatalf("apply positive adjustment: %v", err)
	}
	if err := AdjustMemberHourBalance(ctx, db, AdjustMemberHourBalanceParams{
		OrganizationID: org.ID,
		MemberID:       member.ID,
		AdminMemberID:  owner.ID,
		HoursDelta:     -1,
		Reason:         "correction override",
	}); err != nil {
		t.Fatalf("apply negative adjustment: %v", err)
	}

	stats, err := MemberStats(ctx, db, org.ID, member.ID)
	if err != nil {
		t.Fatalf("member stats: %v", err)
	}
	if stats.BalanceHours != 9 {
		t.Fatalf("expected balance 9, got %d", stats.BalanceHours)
	}

	ledger, err := ListMemberBalanceTransactions(ctx, db, org.ID, member.ID)
	if err != nil {
		t.Fatalf("list balance transactions: %v", err)
	}
	if len(ledger) < 5 {
		t.Fatalf("expected at least 5 ledger rows, got %d", len(ledger))
	}

	foundAsk := false
	foundOffer := false
	foundPositiveAdjustment := false
	foundNegativeAdjustment := false
	for _, txn := range ledger {
		switch {
		case strings.Contains(txn.Description, "Helper Integration helped You") && txn.HoursExchanged == -2:
			foundAsk = true
		case strings.Contains(txn.Description, "You helped Recipient Integration") && txn.HoursExchanged == 3:
			foundOffer = true
		case txn.Description == "Adjustment: bonus override" && txn.HoursExchanged == 4:
			foundPositiveAdjustment = true
		case txn.Description == "Adjustment: correction override" && txn.HoursExchanged == -1:
			foundNegativeAdjustment = true
		}
	}

	if !foundAsk {
		t.Fatalf("expected ask-hop ledger entry, got %+v", ledger)
	}
	if !foundOffer {
		t.Fatalf("expected offer-hop ledger entry, got %+v", ledger)
	}
	if !foundPositiveAdjustment || !foundNegativeAdjustment {
		t.Fatalf("expected adjustment ledger entries, got %+v", ledger)
	}
}

func createHopKindTestMember(t *testing.T, ctx context.Context, db *sql.DB, suffix string, firstName string) types.Member {
	t.Helper()

	emailLocal := strings.ToLower(firstName) + "_" + suffix + "_" + fmt.Sprintf("%d", time.Now().UnixNano())
	member, err := CreateMember(ctx, db, types.Member{
		FirstName:        firstName,
		LastName:         "Integration",
		Email:            emailLocal + "@example.com",
		PasswordHash:     "hashed_password",
		PreferredContact: emailLocal + "@example.com",
		Enabled:          true,
		Verified:         true,
	})
	if err != nil {
		t.Fatalf("create member %s: %v", firstName, err)
	}
	return member
}

func createHopKindTestOrganization(t *testing.T, ctx context.Context, db *sql.DB, suffix string, owner types.Member) types.Organization {
	t.Helper()

	org, err := CreateOrganization(ctx, db, "Offer Kind Org "+suffix, "Test City", "TS", "Offer kind test organization.", owner.ID)
	if err != nil {
		t.Fatalf("create organization: %v", err)
	}
	return org
}

func approveHopKindTestMember(t *testing.T, ctx context.Context, db *sql.DB, orgID, ownerID, memberID int64) {
	t.Helper()

	if err := RequestMembership(ctx, db, memberID, orgID, nil); err != nil {
		t.Fatalf("request membership member=%d: %v", memberID, err)
	}
	requests, err := PendingMembershipRequests(ctx, db, orgID)
	if err != nil {
		t.Fatalf("pending membership requests: %v", err)
	}
	var requestID int64
	for _, req := range requests {
		if req.MemberID == memberID {
			requestID = req.ID
			break
		}
	}
	if requestID == 0 {
		t.Fatalf("missing membership request for member=%d org=%d", memberID, orgID)
	}
	if err := ApproveMembershipRequest(ctx, db, requestID, ownerID); err != nil {
		t.Fatalf("approve membership request member=%d: %v", memberID, err)
	}
}

func createAcceptedOfferHopForServiceTest(t *testing.T, ctx context.Context, db *sql.DB, orgID, offererID, recipientID int64, title string, estimatedHours int) types.Hop {
	t.Helper()

	hop, err := CreateHop(ctx, db, CreateHopParams{
		OrganizationID: orgID,
		MemberID:       offererID,
		Kind:           types.HopKindOffer,
		Title:          title,
		Details:        "Offer hop setup.",
		EstimatedHours: estimatedHours,
		NeededByKind:   types.HopNeededByAnytime,
	})
	if err != nil {
		t.Fatalf("create offer hop: %v", err)
	}
	if err := OfferHopHelp(ctx, db, OfferHopParams{
		OrganizationID: orgID,
		HopID:          hop.ID,
		OffererID:      recipientID,
		OffererName:    "Recipient Integration",
	}); err != nil {
		t.Fatalf("register interest in offer hop: %v", err)
	}
	if err := AcceptPendingHopOffer(ctx, db, hop.ID, offererID, recipientID, "Offerer Integration", "accepted"); err != nil {
		t.Fatalf("accept offer-hop interest: %v", err)
	}
	accepted, err := GetHopByID(ctx, db, orgID, hop.ID)
	if err != nil {
		t.Fatalf("load accepted offer hop: %v", err)
	}
	return accepted
}
