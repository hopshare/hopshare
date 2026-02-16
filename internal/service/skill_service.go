package service

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/lib/pq"

	"hopshare/internal/types"
)

// ListAvailableSkillsForMember returns default skills plus organization skills for active memberships.
func ListAvailableSkillsForMember(ctx context.Context, db *sql.DB, memberID int64) ([]types.Skill, error) {
	if db == nil {
		return nil, ErrNilDB
	}
	if memberID == 0 {
		return nil, ErrMissingMemberID
	}

	rows, err := db.QueryContext(ctx, `
		SELECT s.id, s.organization_id, s.name, o.name
		FROM skills s
		LEFT JOIN organizations o ON o.id = s.organization_id
		WHERE s.organization_id IS NULL
			OR EXISTS (
				SELECT 1
				FROM organization_memberships om
				WHERE om.organization_id = s.organization_id
					AND om.member_id = $1
					AND om.left_at IS NULL
			)
		ORDER BY
			CASE WHEN s.organization_id IS NULL THEN 0 ELSE 1 END,
			LOWER(s.name),
			COALESCE(o.name, ''),
			s.id
	`, memberID)
	if err != nil {
		return nil, fmt.Errorf("list available skills: %w", err)
	}
	defer rows.Close()

	var skills []types.Skill
	for rows.Next() {
		var skill types.Skill
		var orgID sql.NullInt64
		var sourceName sql.NullString
		if err := rows.Scan(&skill.ID, &orgID, &skill.Name, &sourceName); err != nil {
			return nil, fmt.Errorf("scan available skill: %w", err)
		}
		if orgID.Valid {
			skill.OrganizationID = &orgID.Int64
		}
		if sourceName.Valid && strings.TrimSpace(sourceName.String) != "" {
			skill.SourceLabel = sourceName.String
		} else {
			skill.SourceLabel = "Default"
		}
		skills = append(skills, skill)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list available skills: %w", err)
	}

	return skills, nil
}

// ListSelectedSkillIDsForMember returns selected skill IDs for the given member.
func ListSelectedSkillIDsForMember(ctx context.Context, db *sql.DB, memberID int64) ([]int64, error) {
	if db == nil {
		return nil, ErrNilDB
	}
	if memberID == 0 {
		return nil, ErrMissingMemberID
	}

	rows, err := db.QueryContext(ctx, `
		SELECT skill_id
		FROM member_skills
		WHERE member_id = $1
		ORDER BY skill_id
	`, memberID)
	if err != nil {
		return nil, fmt.Errorf("list member selected skills: %w", err)
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var skillID int64
		if err := rows.Scan(&skillID); err != nil {
			return nil, fmt.Errorf("scan member selected skill: %w", err)
		}
		ids = append(ids, skillID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list member selected skills: %w", err)
	}

	return ids, nil
}

// ReplaceMemberSkills replaces all skill selections for a member with the supplied set.
func ReplaceMemberSkills(ctx context.Context, db *sql.DB, memberID int64, skillIDs []int64) error {
	if db == nil {
		return ErrNilDB
	}
	if memberID == 0 {
		return ErrMissingMemberID
	}

	uniqueSkillIDs := dedupePositiveSkillIDs(skillIDs)
	if len(uniqueSkillIDs) > 0 {
		var allowedCount int
		if err := db.QueryRowContext(ctx, `
			SELECT COUNT(*)
			FROM skills s
			WHERE s.id = ANY($2)
				AND (
					s.organization_id IS NULL
					OR EXISTS (
						SELECT 1
						FROM organization_memberships om
						WHERE om.organization_id = s.organization_id
							AND om.member_id = $1
							AND om.left_at IS NULL
					)
				)
		`, memberID, pq.Array(uniqueSkillIDs)).Scan(&allowedCount); err != nil {
			return fmt.Errorf("validate member skill selection: %w", err)
		}
		if allowedCount != len(uniqueSkillIDs) {
			return ErrSkillForbidden
		}
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin replace member skills: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	if _, err = tx.ExecContext(ctx, `
		DELETE FROM member_skills
		WHERE member_id = $1
	`, memberID); err != nil {
		return fmt.Errorf("delete member skills: %w", err)
	}

	for _, skillID := range uniqueSkillIDs {
		if _, err = tx.ExecContext(ctx, `
			INSERT INTO member_skills (member_id, skill_id)
			VALUES ($1, $2)
		`, memberID, skillID); err != nil {
			return fmt.Errorf("insert member skill: %w", err)
		}
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit replace member skills: %w", err)
	}

	return nil
}

// ListOrganizationSkills returns organization-scoped skills for the given organization.
func ListOrganizationSkills(ctx context.Context, db *sql.DB, orgID int64) ([]types.Skill, error) {
	if db == nil {
		return nil, ErrNilDB
	}
	if orgID == 0 {
		return nil, ErrMissingOrgID
	}

	rows, err := db.QueryContext(ctx, `
		SELECT s.id, s.organization_id, s.name, o.name
		FROM skills s
		JOIN organizations o ON o.id = s.organization_id
		WHERE s.organization_id = $1
		ORDER BY LOWER(s.name), s.id
	`, orgID)
	if err != nil {
		return nil, fmt.Errorf("list organization skills: %w", err)
	}
	defer rows.Close()

	var skills []types.Skill
	for rows.Next() {
		var skill types.Skill
		var orgIDVal int64
		if err := rows.Scan(&skill.ID, &orgIDVal, &skill.Name, &skill.SourceLabel); err != nil {
			return nil, fmt.Errorf("scan organization skill: %w", err)
		}
		skill.OrganizationID = &orgIDVal
		skills = append(skills, skill)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list organization skills: %w", err)
	}

	return skills, nil
}

// ReplaceOrganizationSkills rewrites all organization-specific skills for an organization.
func ReplaceOrganizationSkills(ctx context.Context, db *sql.DB, orgID, actorMemberID int64, names []string) error {
	if db == nil {
		return ErrNilDB
	}
	if orgID == 0 {
		return ErrMissingOrgID
	}
	if actorMemberID == 0 {
		return ErrMissingMemberID
	}

	desired := normalizeSkillNames(names)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin replace organization skills: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	rows, err := tx.QueryContext(ctx, `
		SELECT id, name
		FROM skills
		WHERE organization_id = $1
	`, orgID)
	if err != nil {
		return fmt.Errorf("load existing organization skills: %w", err)
	}

	type existingSkill struct {
		id   int64
		name string
	}
	existingByKey := make(map[string]existingSkill)
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			rows.Close()
			return fmt.Errorf("scan existing organization skill: %w", err)
		}
		existingByKey[normalizeSkillName(name)] = existingSkill{id: id, name: name}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("load existing organization skills: %w", err)
	}
	rows.Close()

	keepIDs := make(map[int64]struct{}, len(desired))
	for _, name := range desired {
		key := normalizeSkillName(name)
		existing, ok := existingByKey[key]
		if ok {
			keepIDs[existing.id] = struct{}{}
			if existing.name != name {
				if _, err = tx.ExecContext(ctx, `
					UPDATE skills
					SET name = $1, updated_at = NOW()
					WHERE id = $2
				`, name, existing.id); err != nil {
					return fmt.Errorf("update organization skill name: %w", err)
				}
			}
			continue
		}

		var insertedID int64
		if err = tx.QueryRowContext(ctx, `
			INSERT INTO skills (organization_id, name, created_by)
			VALUES ($1, $2, $3)
			RETURNING id
		`, orgID, name, actorMemberID).Scan(&insertedID); err != nil {
			return fmt.Errorf("insert organization skill: %w", err)
		}
		keepIDs[insertedID] = struct{}{}
	}

	var removeIDs []int64
	for _, existing := range existingByKey {
		if _, ok := keepIDs[existing.id]; !ok {
			removeIDs = append(removeIDs, existing.id)
		}
	}
	if len(removeIDs) > 0 {
		if _, err = tx.ExecContext(ctx, `
			DELETE FROM skills
			WHERE id = ANY($1)
		`, pq.Array(removeIDs)); err != nil {
			return fmt.Errorf("delete organization skills: %w", err)
		}
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit replace organization skills: %w", err)
	}
	return nil
}

func dedupePositiveSkillIDs(ids []int64) []int64 {
	seen := make(map[int64]struct{}, len(ids))
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func normalizeSkillNames(names []string) []string {
	seen := make(map[string]struct{}, len(names))
	out := make([]string, 0, len(names))
	for _, name := range names {
		n := normalizeSkillDisplayName(name)
		if n == "" {
			continue
		}
		key := normalizeSkillName(n)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, n)
	}
	return out
}

func normalizeSkillDisplayName(name string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(name)), " ")
}

func normalizeSkillName(name string) string {
	return strings.ToLower(normalizeSkillDisplayName(name))
}
