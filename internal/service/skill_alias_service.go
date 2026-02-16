package service

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// ListSkillAliases returns aliases for a skill ordered alphabetically.
func ListSkillAliases(ctx context.Context, db *sql.DB, skillID int64) ([]string, error) {
	if db == nil {
		return nil, ErrNilDB
	}
	if skillID == 0 {
		return nil, ErrMissingField
	}

	rows, err := db.QueryContext(ctx, `
		SELECT alias
		FROM skill_aliases
		WHERE skill_id = $1
		ORDER BY LOWER(alias), alias
	`, skillID)
	if err != nil {
		return nil, fmt.Errorf("list skill aliases: %w", err)
	}
	defer rows.Close()

	var aliases []string
	for rows.Next() {
		var alias string
		if err := rows.Scan(&alias); err != nil {
			return nil, fmt.Errorf("scan skill alias: %w", err)
		}
		aliases = append(aliases, alias)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list skill aliases: %w", err)
	}
	return aliases, nil
}

// ReplaceSkillAliases rewrites aliases for a given skill.
func ReplaceSkillAliases(ctx context.Context, db *sql.DB, skillID, actorMemberID int64, aliases []string) error {
	if db == nil {
		return ErrNilDB
	}
	if skillID == 0 {
		return ErrMissingField
	}
	if actorMemberID == 0 {
		return ErrMissingMemberID
	}

	normalizedAliases := normalizeAliases(aliases)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin replace skill aliases: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	var skillName string
	if err = tx.QueryRowContext(ctx, `
		SELECT name
		FROM skills
		WHERE id = $1
	`, skillID).Scan(&skillName); err != nil {
		if err == sql.ErrNoRows {
			return sql.ErrNoRows
		}
		return fmt.Errorf("load skill: %w", err)
	}

	if _, err = tx.ExecContext(ctx, `
		DELETE FROM skill_aliases
		WHERE skill_id = $1
	`, skillID); err != nil {
		return fmt.Errorf("delete skill aliases: %w", err)
	}

	for _, alias := range normalizedAliases {
		if err = upsertSkillAlias(ctx, tx, skillID, alias, actorMemberID); err != nil {
			return err
		}
	}
	if err = upsertSkillAlias(ctx, tx, skillID, skillName, actorMemberID); err != nil {
		return err
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit replace skill aliases: %w", err)
	}
	return nil
}

func upsertSkillAlias(ctx context.Context, db execer, skillID int64, alias string, actorMemberID int64) error {
	normalizedAlias := normalizeAliasText(alias)
	alias = normalizeSkillDisplayName(alias)
	if normalizedAlias == "" || alias == "" {
		return nil
	}

	var createdBy any
	if actorMemberID > 0 {
		createdBy = actorMemberID
	}

	if _, err := db.ExecContext(ctx, `
		INSERT INTO skill_aliases (skill_id, alias, normalized_alias, created_by)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (skill_id, normalized_alias)
		DO UPDATE SET alias = EXCLUDED.alias
	`, skillID, alias, normalizedAlias, createdBy); err != nil {
		return fmt.Errorf("upsert skill alias: %w", err)
	}
	return nil
}

func normalizeAliases(aliases []string) []string {
	seen := make(map[string]struct{}, len(aliases))
	out := make([]string, 0, len(aliases))
	for _, alias := range aliases {
		display := normalizeSkillDisplayName(alias)
		if display == "" {
			continue
		}
		key := normalizeAliasText(display)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, display)
	}
	return out
}

func splitAliasLines(raw string) []string {
	lines := strings.Split(raw, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		out = append(out, line)
	}
	return out
}
