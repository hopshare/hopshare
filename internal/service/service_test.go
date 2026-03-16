package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sync"
	"testing"
	"time"

	_ "github.com/lib/pq"

	"hopshare/internal/database"
	"hopshare/internal/database/migrate"
	"hopshare/internal/types"
)

var (
	dbOnce        sync.Once
	sharedDB      *sql.DB
	dbSetupErr    error
	errMissingURL = errors.New("HOPSHARE_DB_URL or DATABASE_URL not set")
)

// require_db returns a live database connection and ensures migrations run once.
func require_db(t *testing.T) *sql.DB {
	t.Helper()

	dbOnce.Do(func() {
		dbURL := os.Getenv("HOPSHARE_DB_URL")
		if dbURL == "" {
			dbURL = os.Getenv("DATABASE_URL")
		}
		if dbURL == "" {
			dbSetupErr = errMissingURL
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		sharedDB, dbSetupErr = database.New(ctx, dbURL)
		if dbSetupErr != nil {
			return
		}

		dbSetupErr = migrate.Run(ctx, sharedDB)
	})

	if errors.Is(dbSetupErr, errMissingURL) {
		t.Skip(errMissingURL.Error())
	}
	if dbSetupErr != nil {
		t.Fatalf("database setup failed: %v", dbSetupErr)
	}
	return sharedDB
}

func TestCreateMember(t *testing.T) {
	db := require_db(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	username := fmt.Sprintf("inttest_%d", time.Now().UnixNano())
	email := fmt.Sprintf("%s@example.com", username)

	input := types.Member{
		FirstName:        "Test",
		LastName:         "Member",
		Email:            email,
		PasswordHash:     "hashed_password",
		PreferredContact: email,
		Enabled:          true,
		Verified:         true,
	}

	member, err := CreateMember(ctx, db, input)
	if err != nil {
		t.Fatalf("CreateMember returned error: %v", err)
	}

	if member.ID == 0 {
		t.Fatalf("expected member ID to be set")
	}
	if member.FirstName != input.FirstName || member.LastName != input.LastName || member.Email != input.Email || member.PasswordHash != input.PasswordHash {
		t.Fatalf("returned member does not match input")
	}
	if member.CreatedAt.IsZero() || member.UpdatedAt.IsZero() {
		t.Fatalf("expected timestamps to be set, got created_at=%v updated_at=%v", member.CreatedAt, member.UpdatedAt)
	}
}

func TestCreateOrganization(t *testing.T) {
	db := require_db(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	base := fmt.Sprintf("inttest_org_%d", time.Now().UnixNano())
	memberEmail := fmt.Sprintf("%s@example.com", base)

	memberInput := types.Member{
		FirstName:        "Owner",
		LastName:         "Member",
		Email:            memberEmail,
		PasswordHash:     "hashed_password",
		PreferredContact: memberEmail,
		Enabled:          true,
		Verified:         true,
	}

	member, err := CreateMember(ctx, db, memberInput)
	if err != nil {
		t.Fatalf("CreateMember returned error: %v", err)
	}

	org, err := CreateOrganization(ctx, db, base+" Org", "Test City", "TS", "Test description", member.ID)
	if err != nil {
		t.Fatalf("CreateOrganization returned error: %v", err)
	}
	if org.ID == 0 {
		t.Fatalf("expected organization ID to be set")
	}
	if org.Name == "" {
		t.Fatalf("expected organization name to be set")
	}
	if org.URLName == "" {
		t.Fatalf("expected organization URL name to be set")
	}
	if !regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`).MatchString(org.URLName) {
		t.Fatalf("expected organization URL name to be DNS-compatible, got %q", org.URLName)
	}

	var membershipCount int
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM organization_memberships
		WHERE organization_id = $1 AND member_id = $2 AND left_at IS NULL AND role = 'owner'
	`, org.ID, member.ID).Scan(&membershipCount); err != nil {
		t.Fatalf("query membership: %v", err)
	}
	if membershipCount != 1 {
		t.Fatalf("expected owner membership, got %d", membershipCount)
	}

	orgs, err := ActiveOrganizationsForMember(ctx, db, member.ID)
	if err != nil {
		t.Fatalf("ActiveOrganizationsForMember returned error: %v", err)
	}
	if len(orgs) != 1 || orgs[0].ID != org.ID {
		t.Fatalf("expected one organization for member; got %d", len(orgs))
	}
	if org.TimebankMinBalance != DefaultTimebankMinBalance || org.TimebankMaxBalance != DefaultTimebankMaxBalance || org.TimebankStartingBalance != DefaultTimebankStartingBalance {
		t.Fatalf(
			"unexpected default timebank policy: min=%d max=%d start=%d",
			org.TimebankMinBalance,
			org.TimebankMaxBalance,
			org.TimebankStartingBalance,
		)
	}
	if org.Theme != types.OrganizationThemeDefault {
		t.Fatalf("expected default organization theme, got %q", org.Theme)
	}

	stats, err := MemberStats(ctx, db, org.ID, member.ID)
	if err != nil {
		t.Fatalf("MemberStats returned error: %v", err)
	}
	if stats.BalanceHours != DefaultTimebankStartingBalance {
		t.Fatalf("expected owner starting balance of %d, got %d", DefaultTimebankStartingBalance, stats.BalanceHours)
	}
}

func TestUpdateOrganizationThemeAndBanner(t *testing.T) {
	db := require_db(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	base := fmt.Sprintf("inttest_org_theme_%d", time.Now().UnixNano())
	member, err := CreateMember(ctx, db, types.Member{
		FirstName:        "Owner",
		LastName:         "Theme",
		Email:            base + "@example.com",
		PasswordHash:     "hashed_password",
		PreferredContact: base + "@example.com",
		Enabled:          true,
		Verified:         true,
	})
	if err != nil {
		t.Fatalf("create member: %v", err)
	}

	org, err := CreateOrganization(ctx, db, base+" Org", "City", "TS", "Desc", member.ID)
	if err != nil {
		t.Fatalf("create organization: %v", err)
	}

	org.Theme = types.OrganizationThemeFun
	org.Description = "Updated description"
	if err := UpdateOrganization(ctx, db, org); err != nil {
		t.Fatalf("update organization: %v", err)
	}
	if err := SetOrganizationBanner(ctx, db, org.ID, "image/png", []byte{1, 2, 3, 4}); err != nil {
		t.Fatalf("set organization banner: %v", err)
	}

	updated, err := GetOrganizationByID(ctx, db, org.ID)
	if err != nil {
		t.Fatalf("get organization by id: %v", err)
	}
	if updated.Theme != types.OrganizationThemeFun {
		t.Fatalf("expected theme %q, got %q", types.OrganizationThemeFun, updated.Theme)
	}
	if !updated.HasBanner {
		t.Fatalf("expected uploaded banner to be present")
	}
	if updated.BannerContentType == nil || *updated.BannerContentType != "image/png" {
		t.Fatalf("unexpected banner content type: %v", updated.BannerContentType)
	}

	if err := ClearOrganizationBanner(ctx, db, org.ID); err != nil {
		t.Fatalf("clear organization banner: %v", err)
	}

	cleared, err := GetOrganizationByID(ctx, db, org.ID)
	if err != nil {
		t.Fatalf("get cleared organization by id: %v", err)
	}
	if cleared.HasBanner {
		t.Fatalf("expected banner to be cleared")
	}
	if cleared.BannerContentType != nil {
		t.Fatalf("expected banner content type to be nil, got %v", cleared.BannerContentType)
	}
}

func TestMembershipStartingBalanceGrantedOnlyOnce(t *testing.T) {
	db := require_db(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	base := fmt.Sprintf("inttest_org_start_%d", time.Now().UnixNano())
	ownerInput := types.Member{
		FirstName:        "Owner",
		LastName:         "User",
		Email:            base + "_owner@example.com",
		PasswordHash:     "hashed_password",
		PreferredContact: base + "_owner@example.com",
		Enabled:          true,
		Verified:         true,
	}
	memberInput := types.Member{
		FirstName:        "Member",
		LastName:         "User",
		Email:            base + "_member@example.com",
		PasswordHash:     "hashed_password",
		PreferredContact: base + "_member@example.com",
		Enabled:          true,
		Verified:         true,
	}

	owner, err := CreateMember(ctx, db, ownerInput)
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	member, err := CreateMember(ctx, db, memberInput)
	if err != nil {
		t.Fatalf("create member: %v", err)
	}

	org, err := CreateOrganization(ctx, db, base+" Org", "City", "TS", "Desc", owner.ID)
	if err != nil {
		t.Fatalf("create organization: %v", err)
	}

	approve := func() {
		if err := RequestMembership(ctx, db, member.ID, org.ID, nil); err != nil {
			t.Fatalf("request membership: %v", err)
		}
		requests, err := PendingMembershipRequests(ctx, db, org.ID)
		if err != nil {
			t.Fatalf("pending requests: %v", err)
		}
		var requestID int64
		for _, req := range requests {
			if req.MemberID == member.ID {
				requestID = req.ID
				break
			}
		}
		if requestID == 0 {
			t.Fatalf("expected pending request for member=%d", member.ID)
		}
		if err := ApproveMembershipRequest(ctx, db, requestID, owner.ID); err != nil {
			t.Fatalf("approve membership request: %v", err)
		}
	}

	approve()

	if err := RemoveOrganizationMember(ctx, db, org.ID, member.ID, owner.ID); err != nil {
		t.Fatalf("remove member after first approval: %v", err)
	}

	approve()

	stats, err := MemberStats(ctx, db, org.ID, member.ID)
	if err != nil {
		t.Fatalf("member stats after rejoin: %v", err)
	}
	if stats.BalanceHours != DefaultTimebankStartingBalance {
		t.Fatalf("expected rejoined member balance to remain %d, got %d", DefaultTimebankStartingBalance, stats.BalanceHours)
	}

	var startingRows int
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM hour_balance_adjustments
		WHERE organization_id = $1
			AND member_id = $2
			AND is_starting_balance = TRUE
	`, org.ID, member.ID).Scan(&startingRows); err != nil {
		t.Fatalf("count starting balance rows: %v", err)
	}
	if startingRows != 1 {
		t.Fatalf("expected exactly one starting balance row, got %d", startingRows)
	}
}
