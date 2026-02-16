package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"hopshare/internal/types"
)

func TestMatchHopTextToSkillCandidatesMoveCouch(t *testing.T) {
	candidates := []SkillCandidate{
		{
			Skill: typesSkill("Moving"),
			Aliases: []string{
				"moving",
				"move",
				"couch",
				"furniture",
			},
		},
		{
			Skill: typesSkill("Taxes"),
			Aliases: []string{
				"tax",
				"tax prep",
			},
		},
	}

	matches := MatchHopTextToSkillCandidates("Need someone to help move a couch", "", candidates)
	if len(matches) == 0 {
		t.Fatalf("expected matches, got none")
	}
	if matches[0].Skill.Name != "Moving" {
		t.Fatalf("expected top skill match Moving, got %q", matches[0].Skill.Name)
	}
	if matches[0].Score <= 0 {
		t.Fatalf("expected positive score, got %d", matches[0].Score)
	}
}

func TestMatchHopToMemberSelectedSkillsWithAliases(t *testing.T) {
	db := require_db(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	owner := mustCreateMemberForSkillsTest(t, ctx, db, "owner")
	org, err := CreateOrganization(ctx, db, fmt.Sprintf("Match Org %d", time.Now().UnixNano()), "Test City", "TS", "Matcher org", owner.ID)
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

	if err := ReplaceOrganizationSkills(ctx, db, org.ID, owner.ID, []string{"Moving"}); err != nil {
		t.Fatalf("ReplaceOrganizationSkills returned error: %v", err)
	}
	orgSkills, err := ListOrganizationSkills(ctx, db, org.ID)
	if err != nil {
		t.Fatalf("ListOrganizationSkills returned error: %v", err)
	}
	if len(orgSkills) != 1 {
		t.Fatalf("expected one organization skill, got %d", len(orgSkills))
	}

	if err := ReplaceSkillAliases(ctx, db, orgSkills[0].ID, owner.ID, []string{"move", "couch", "furniture"}); err != nil {
		t.Fatalf("ReplaceSkillAliases returned error: %v", err)
	}
	if err := ReplaceMemberSkills(ctx, db, member.ID, []int64{orgSkills[0].ID}); err != nil {
		t.Fatalf("ReplaceMemberSkills returned error: %v", err)
	}

	matches, err := MatchHopToMemberSelectedSkills(ctx, db, org.ID, member.ID, "Need someone to help move a couch", "")
	if err != nil {
		t.Fatalf("MatchHopToMemberSelectedSkills returned error: %v", err)
	}
	if len(matches) == 0 {
		t.Fatalf("expected at least one match")
	}
	if matches[0].Skill.Name != "Moving" {
		t.Fatalf("expected top match to be Moving, got %q", matches[0].Skill.Name)
	}
	if matches[0].MatchedAlias == "" {
		t.Fatalf("expected matched alias to be populated")
	}
}

func TestSortHopsByMemberSkillMatch(t *testing.T) {
	db := require_db(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	owner := mustCreateMemberForSkillsTest(t, ctx, db, "owner")
	org, err := CreateOrganization(ctx, db, fmt.Sprintf("Sort Match Org %d", time.Now().UnixNano()), "Test City", "TS", "Sort matcher org", owner.ID)
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

	if err := ReplaceOrganizationSkills(ctx, db, org.ID, owner.ID, []string{"Moving"}); err != nil {
		t.Fatalf("ReplaceOrganizationSkills returned error: %v", err)
	}
	orgSkills, err := ListOrganizationSkills(ctx, db, org.ID)
	if err != nil {
		t.Fatalf("ListOrganizationSkills returned error: %v", err)
	}
	if len(orgSkills) != 1 {
		t.Fatalf("expected one organization skill, got %d", len(orgSkills))
	}
	if err := ReplaceSkillAliases(ctx, db, orgSkills[0].ID, owner.ID, []string{"move", "couch"}); err != nil {
		t.Fatalf("ReplaceSkillAliases returned error: %v", err)
	}
	if err := ReplaceMemberSkills(ctx, db, member.ID, []int64{orgSkills[0].ID}); err != nil {
		t.Fatalf("ReplaceMemberSkills returned error: %v", err)
	}

	hops := []types.Hop{
		{ID: 1, Title: "Need help with tax filing"},
		{ID: 2, Title: "Need someone to help move a couch"},
	}
	sorted, err := SortHopsByMemberSkillMatch(ctx, db, org.ID, member.ID, hops)
	if err != nil {
		t.Fatalf("SortHopsByMemberSkillMatch returned error: %v", err)
	}
	if len(sorted) != 2 {
		t.Fatalf("expected 2 hops, got %d", len(sorted))
	}
	if sorted[0].ID != 2 {
		t.Fatalf("expected moving hop first after sorting, got hop ID %d", sorted[0].ID)
	}
}

func typesSkill(name string) types.Skill {
	return types.Skill{Name: name}
}
