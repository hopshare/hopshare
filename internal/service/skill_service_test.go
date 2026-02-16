package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"hopshare/internal/types"
)

func TestReplaceOrganizationSkillsRemovesMemberSelections(t *testing.T) {
	db := require_db(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	owner := mustCreateMemberForSkillsTest(t, ctx, db, "owner")
	org, err := CreateOrganization(ctx, db, fmt.Sprintf("Skills Org %d", time.Now().UnixNano()), "Test City", "TS", "Skill test org", owner.ID)
	if err != nil {
		t.Fatalf("CreateOrganization returned error: %v", err)
	}

	member := mustCreateMemberForSkillsTest(t, ctx, db, "member")
	if _, err := db.ExecContext(ctx, `
		INSERT INTO organization_memberships (organization_id, member_id, role, is_primary_owner)
		VALUES ($1, $2, 'member', FALSE)
	`, org.ID, member.ID); err != nil {
		t.Fatalf("insert organization membership: %v", err)
	}

	if err := ReplaceOrganizationSkills(ctx, db, org.ID, owner.ID, []string{"Fishing", "Boating"}); err != nil {
		t.Fatalf("ReplaceOrganizationSkills returned error: %v", err)
	}

	orgSkills, err := ListOrganizationSkills(ctx, db, org.ID)
	if err != nil {
		t.Fatalf("ListOrganizationSkills returned error: %v", err)
	}
	if len(orgSkills) != 2 {
		t.Fatalf("expected 2 organization skills, got %d", len(orgSkills))
	}

	var fishingID int64
	for _, s := range orgSkills {
		if s.Name == "Fishing" {
			fishingID = s.ID
		}
	}
	if fishingID == 0 {
		t.Fatalf("expected fishing skill id")
	}

	defaultSkillID := lookupDefaultSkillID(t, ctx, db, "Cooking")
	if err := ReplaceMemberSkills(ctx, db, member.ID, []int64{fishingID, defaultSkillID}); err != nil {
		t.Fatalf("ReplaceMemberSkills returned error: %v", err)
	}

	if err := ReplaceOrganizationSkills(ctx, db, org.ID, owner.ID, []string{"Boating"}); err != nil {
		t.Fatalf("ReplaceOrganizationSkills remove skill returned error: %v", err)
	}

	selectedIDs, err := ListSelectedSkillIDsForMember(ctx, db, member.ID)
	if err != nil {
		t.Fatalf("ListSelectedSkillIDsForMember returned error: %v", err)
	}
	if len(selectedIDs) != 1 || selectedIDs[0] != defaultSkillID {
		t.Fatalf("expected only default skill selection after org skill removal, got %v", selectedIDs)
	}
}

func TestReplaceMemberSkillsRejectsUnauthorizedSkill(t *testing.T) {
	db := require_db(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	owner := mustCreateMemberForSkillsTest(t, ctx, db, "owner")
	org, err := CreateOrganization(ctx, db, fmt.Sprintf("Skills Auth Org %d", time.Now().UnixNano()), "Test City", "TS", "Skill auth org", owner.ID)
	if err != nil {
		t.Fatalf("CreateOrganization returned error: %v", err)
	}
	if err := ReplaceOrganizationSkills(ctx, db, org.ID, owner.ID, []string{"Fishing"}); err != nil {
		t.Fatalf("ReplaceOrganizationSkills returned error: %v", err)
	}

	other := mustCreateMemberForSkillsTest(t, ctx, db, "other")
	orgSkills, err := ListOrganizationSkills(ctx, db, org.ID)
	if err != nil {
		t.Fatalf("ListOrganizationSkills returned error: %v", err)
	}
	if len(orgSkills) != 1 {
		t.Fatalf("expected 1 organization skill, got %d", len(orgSkills))
	}

	err = ReplaceMemberSkills(ctx, db, other.ID, []int64{orgSkills[0].ID})
	if !errors.Is(err, ErrSkillForbidden) {
		t.Fatalf("expected ErrSkillForbidden, got %v", err)
	}

	defaultSkillID := lookupDefaultSkillID(t, ctx, db, "Driving")
	if err := ReplaceMemberSkills(ctx, db, other.ID, []int64{defaultSkillID}); err != nil {
		t.Fatalf("ReplaceMemberSkills with default skill returned error: %v", err)
	}
}

func mustCreateMemberForSkillsTest(t *testing.T, ctx context.Context, db *sql.DB, prefix string) types.Member {
	t.Helper()

	base := fmt.Sprintf("skill_%s_%d", prefix, time.Now().UnixNano())
	email := base + "@example.com"
	m, err := CreateMember(ctx, db, types.Member{
		FirstName:              "Skill",
		LastName:               "Tester",
		Username:               base,
		Email:                  email,
		PasswordHash:           "hashed_password",
		PreferredContactMethod: types.ContactMethodEmail,
		PreferredContact:       email,
		Enabled:                true,
		Verified:               true,
	})
	if err != nil {
		t.Fatalf("CreateMember returned error: %v", err)
	}
	return m
}

func lookupDefaultSkillID(t *testing.T, ctx context.Context, db *sql.DB, name string) int64 {
	t.Helper()

	var id int64
	if err := db.QueryRowContext(ctx, `
		SELECT id
		FROM skills
		WHERE organization_id IS NULL AND name = $1
	`, name).Scan(&id); err != nil {
		t.Fatalf("lookup default skill %q: %v", name, err)
	}
	return id
}
