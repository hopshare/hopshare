package service

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"hopshare/internal/types"
)

type SkillCandidate struct {
	Skill   types.Skill
	Aliases []string
}

type SkillMatch struct {
	Skill        types.Skill
	Score        int
	MatchedAlias string
	Reasons      []string
}

// ListSelectedSkillCandidatesForMember returns selected skills for a member in the
// target organization scope (organization-specific + defaults), including aliases.
func ListSelectedSkillCandidatesForMember(ctx context.Context, db *sql.DB, orgID, memberID int64) ([]SkillCandidate, error) {
	if db == nil {
		return nil, ErrNilDB
	}
	if orgID == 0 {
		return nil, ErrMissingOrgID
	}
	if memberID == 0 {
		return nil, ErrMissingMemberID
	}

	rows, err := db.QueryContext(ctx, `
		SELECT
			s.id,
			s.organization_id,
			s.name,
			COALESCE(NULLIF(o.name, ''), 'Default') AS source_label,
			sa.alias
		FROM member_skills ms
		JOIN skills s ON s.id = ms.skill_id
		LEFT JOIN organizations o ON o.id = s.organization_id
		LEFT JOIN skill_aliases sa ON sa.skill_id = s.id
		WHERE ms.member_id = $1
			AND (
				s.organization_id IS NULL
				OR s.organization_id = $2
			)
		ORDER BY s.id, LOWER(sa.alias), sa.alias
	`, memberID, orgID)
	if err != nil {
		return nil, fmt.Errorf("list selected skill candidates: %w", err)
	}
	defer rows.Close()

	bySkillID := make(map[int64]*bucket)
	order := make([]int64, 0, 16)

	for rows.Next() {
		var skillID int64
		var orgIDValue sql.NullInt64
		var skillName string
		var sourceLabel string
		var alias sql.NullString
		if err := rows.Scan(&skillID, &orgIDValue, &skillName, &sourceLabel, &alias); err != nil {
			return nil, fmt.Errorf("scan selected skill candidate: %w", err)
		}

		group, ok := bySkillID[skillID]
		if !ok {
			skill := types.Skill{
				ID:          skillID,
				Name:        skillName,
				SourceLabel: sourceLabel,
			}
			if orgIDValue.Valid {
				skill.OrganizationID = &orgIDValue.Int64
			}
			group = &bucket{
				candidate: SkillCandidate{
					Skill:   skill,
					Aliases: make([]string, 0, 4),
				},
				seenAlias: make(map[string]struct{}, 4),
			}
			bySkillID[skillID] = group
			order = append(order, skillID)
			addAliasToBucket(group, skillName)
		}
		if alias.Valid {
			addAliasToBucket(group, alias.String)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list selected skill candidates: %w", err)
	}

	out := make([]SkillCandidate, 0, len(order))
	for _, skillID := range order {
		group := bySkillID[skillID]
		out = append(out, group.candidate)
	}
	return out, nil
}

// MatchHopToMemberSelectedSkills scores selected skills against hop text.
func MatchHopToMemberSelectedSkills(ctx context.Context, db *sql.DB, orgID, memberID int64, title, details string) ([]SkillMatch, error) {
	candidates, err := ListSelectedSkillCandidatesForMember(ctx, db, orgID, memberID)
	if err != nil {
		return nil, err
	}
	return MatchHopTextToSkillCandidates(title, details, candidates), nil
}

// SortHopsByMemberSkillMatch returns hops sorted by descending skill-match score.
func SortHopsByMemberSkillMatch(ctx context.Context, db *sql.DB, orgID, memberID int64, hops []types.Hop) ([]types.Hop, error) {
	if len(hops) <= 1 {
		out := make([]types.Hop, len(hops))
		copy(out, hops)
		return out, nil
	}

	candidates, err := ListSelectedSkillCandidatesForMember(ctx, db, orgID, memberID)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		out := make([]types.Hop, len(hops))
		copy(out, hops)
		return out, nil
	}

	type scoredHop struct {
		hop   types.Hop
		score int
	}
	scored := make([]scoredHop, 0, len(hops))
	for _, hop := range hops {
		details := ""
		if hop.Details != nil {
			details = *hop.Details
		}
		matches := MatchHopTextToSkillCandidates(hop.Title, details, candidates)
		score := 0
		if len(matches) > 0 {
			score = matches[0].Score
		}
		scored = append(scored, scoredHop{hop: hop, score: score})
	}

	sort.SliceStable(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	out := make([]types.Hop, 0, len(scored))
	for _, item := range scored {
		out = append(out, item.hop)
	}
	return out, nil
}

// MatchHopTextToSkillCandidates returns scored matches for skill candidates.
func MatchHopTextToSkillCandidates(title, details string, candidates []SkillCandidate) []SkillMatch {
	titleNorm := normalizeMatchText(title)
	detailsNorm := normalizeMatchText(details)
	titleTokens := countTokens(tokenizeMatchText(title))
	detailsTokens := countTokens(tokenizeMatchText(details))

	matches := make([]SkillMatch, 0, len(candidates))
	for _, candidate := range candidates {
		best := SkillMatch{Skill: candidate.Skill}
		for _, alias := range candidate.Aliases {
			score, reasons := scoreAliasMatch(alias, titleNorm, detailsNorm, titleTokens, detailsTokens)
			if score <= 0 {
				continue
			}
			if score > best.Score {
				best.Score = score
				best.MatchedAlias = alias
				best.Reasons = reasons
			}
		}
		if best.Score > 0 {
			matches = append(matches, best)
		}
	}

	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Score == matches[j].Score {
			return strings.ToLower(matches[i].Skill.Name) < strings.ToLower(matches[j].Skill.Name)
		}
		return matches[i].Score > matches[j].Score
	})
	return matches
}

func addAliasToBucket(group *bucket, alias string) {
	display := normalizeSkillDisplayName(alias)
	key := normalizeAliasText(display)
	if display == "" || key == "" {
		return
	}
	if _, ok := group.seenAlias[key]; ok {
		return
	}
	group.seenAlias[key] = struct{}{}
	group.candidate.Aliases = append(group.candidate.Aliases, display)
}

type bucket struct {
	candidate SkillCandidate
	seenAlias map[string]struct{}
}

func scoreAliasMatch(alias, titleNorm, detailsNorm string, titleTokens, detailsTokens map[string]int) (int, []string) {
	aliasNorm := normalizeAliasText(alias)
	if aliasNorm == "" {
		return 0, nil
	}
	aliasTokens := dedupeTokens(tokenizeMatchText(alias))
	if len(aliasTokens) == 0 {
		return 0, nil
	}

	score := 0
	reasons := make([]string, 0, 4)

	if containsWholePhrase(titleNorm, aliasNorm) {
		score += 16
		reasons = append(reasons, "title phrase match")
	} else if containsWholePhrase(detailsNorm, aliasNorm) {
		score += 10
		reasons = append(reasons, "details phrase match")
	}

	titleTokenHits := 0
	detailTokenHits := 0
	for _, tok := range aliasTokens {
		if titleTokens[tok] > 0 {
			titleTokenHits++
			score += 6
		}
		if detailsTokens[tok] > 0 {
			detailTokenHits++
			score += 3
		}
	}
	if titleTokenHits > 0 {
		reasons = append(reasons, "title token match")
	}
	if detailTokenHits > 0 {
		reasons = append(reasons, "details token match")
	}

	if len(aliasTokens) > 1 && (titleTokenHits+detailTokenHits) >= len(aliasTokens) {
		score += 4
		reasons = append(reasons, "full alias token coverage")
	}

	return score, reasons
}

func containsWholePhrase(body, phrase string) bool {
	if body == "" || phrase == "" {
		return false
	}
	return strings.Contains(" "+body+" ", " "+phrase+" ")
}

func countTokens(tokens []string) map[string]int {
	out := make(map[string]int, len(tokens))
	for _, tok := range tokens {
		if tok == "" {
			continue
		}
		out[tok]++
	}
	return out
}

func dedupeTokens(tokens []string) []string {
	seen := make(map[string]struct{}, len(tokens))
	out := make([]string, 0, len(tokens))
	for _, tok := range tokens {
		if tok == "" {
			continue
		}
		if _, ok := seen[tok]; ok {
			continue
		}
		seen[tok] = struct{}{}
		out = append(out, tok)
	}
	return out
}
