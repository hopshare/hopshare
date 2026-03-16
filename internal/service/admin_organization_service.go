package service

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"hopshare/internal/types"
)

const defaultAdminOrgHopListLimit = 50

func AdminOrganizationDetail(ctx context.Context, db *sql.DB, orgID int64, hopLimit int) (types.AdminOrganizationDetail, error) {
	if db == nil {
		return types.AdminOrganizationDetail{}, ErrNilDB
	}
	if orgID == 0 {
		return types.AdminOrganizationDetail{}, ErrMissingOrgID
	}
	if hopLimit <= 0 {
		hopLimit = defaultAdminOrgHopListLimit
	}

	org, err := GetOrganizationByID(ctx, db, orgID)
	if err != nil {
		return types.AdminOrganizationDetail{}, err
	}

	var detail types.AdminOrganizationDetail
	detail.Organization = org

	if err := db.QueryRowContext(ctx, `
		SELECT
			COALESCE(COUNT(CASE WHEN om.member_id IS NOT NULL AND om.left_at IS NULL THEN 1 END), 0) AS total_members,
			COALESCE(COUNT(CASE WHEN om.member_id IS NOT NULL AND om.left_at IS NULL AND m.enabled THEN 1 END), 0) AS enabled_members,
			COALESCE(COUNT(CASE WHEN om.member_id IS NOT NULL AND om.left_at IS NULL AND NOT m.enabled THEN 1 END), 0) AS disabled_members
		FROM organization_memberships om
		LEFT JOIN members m ON m.id = om.member_id
		WHERE om.organization_id = $1
	`, orgID).Scan(&detail.MemberCount, &detail.EnabledMemberCount, &detail.DisabledMemberCount); err != nil {
		return types.AdminOrganizationDetail{}, fmt.Errorf("load organization member counts: %w", err)
	}

	if err := db.QueryRowContext(ctx, `
		SELECT
			COUNT(*),
			COALESCE(SUM(CASE WHEN hours_delta > 0 THEN hours_delta ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN hours_delta < 0 THEN -hours_delta ELSE 0 END), 0)
		FROM hour_balance_adjustments
		WHERE organization_id = $1
			AND is_starting_balance = FALSE
	`, orgID).Scan(&detail.HourOverrideCounts.Count, &detail.HourOverrideCounts.HoursGiven, &detail.HourOverrideCounts.HoursRemoved); err != nil {
		return types.AdminOrganizationDetail{}, fmt.Errorf("load organization hour override counts: %w", err)
	}

	statusCounts, err := adminOrganizationHopCountsByStatus(ctx, db, orgID)
	if err != nil {
		return types.AdminOrganizationDetail{}, err
	}
	detail.HopCounts = statusCounts

	kindCounts, err := organizationHopCountsByKind(ctx, db, orgID)
	if err != nil {
		return types.AdminOrganizationDetail{}, err
	}
	detail.HopKinds = kindCounts

	hops, err := adminOrganizationRecentHops(ctx, db, orgID, hopLimit)
	if err != nil {
		return types.AdminOrganizationDetail{}, err
	}
	detail.Hops = hops

	return detail, nil
}

func adminOrganizationHopCountsByStatus(ctx context.Context, db *sql.DB, orgID int64) ([]types.AdminHopStatusCount, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT status, COUNT(*)
		FROM hops
		WHERE organization_id = $1
		GROUP BY status
	`, orgID)
	if err != nil {
		return nil, fmt.Errorf("count organization hops by status: %w", err)
	}
	defer rows.Close()

	return scanAdminHopStatusCounts(rows)
}

func organizationHopCountsByKind(ctx context.Context, db *sql.DB, orgID int64) ([]types.AdminHopKindCount, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT hop_kind, COUNT(*)
		FROM hops
		WHERE organization_id = $1
		GROUP BY hop_kind
	`, orgID)
	if err != nil {
		return nil, fmt.Errorf("count organization hops by kind: %w", err)
	}
	defer rows.Close()

	return scanAdminHopKindCounts(rows)
}

func OrganizationHopMetricsDetail(ctx context.Context, db *sql.DB, orgID int64) (types.OrganizationHopMetricsDetail, error) {
	if db == nil {
		return types.OrganizationHopMetricsDetail{}, ErrNilDB
	}
	if orgID == 0 {
		return types.OrganizationHopMetricsDetail{}, ErrMissingOrgID
	}

	var detail types.OrganizationHopMetricsDetail
	statusCounts, err := adminOrganizationHopCountsByStatus(ctx, db, orgID)
	if err != nil {
		return types.OrganizationHopMetricsDetail{}, err
	}
	detail.HopsByStatus = statusCounts

	kindCounts, err := organizationHopCountsByKind(ctx, db, orgID)
	if err != nil {
		return types.OrganizationHopMetricsDetail{}, err
	}
	detail.HopsByKind = kindCounts

	if err := db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(hours), 0)
		FROM hop_transactions
		WHERE organization_id = $1
	`, orgID).Scan(&detail.TotalHoursExchanged); err != nil {
		return types.OrganizationHopMetricsDetail{}, fmt.Errorf("sum organization exchanged hours: %w", err)
	}

	if err := db.QueryRowContext(ctx, `
		SELECT
			COALESCE(COUNT(*) FILTER (WHERE joined_at >= NOW() - INTERVAL '30 days'), 0),
			COALESCE(COUNT(*) FILTER (WHERE left_at >= NOW() - INTERVAL '30 days'), 0)
		FROM organization_memberships
		WHERE organization_id = $1
	`, orgID).Scan(&detail.JoinersPast30Days, &detail.LeaversPast30Days); err != nil {
		return types.OrganizationHopMetricsDetail{}, fmt.Errorf("count organization joiners and leavers: %w", err)
	}

	topParticipants, err := organizationTopParticipants(ctx, db, orgID, 5)
	if err != nil {
		return types.OrganizationHopMetricsDetail{}, err
	}
	detail.TopParticipants = topParticipants

	if err := db.QueryRowContext(ctx, `
		WITH active_members AS (
			SELECT DISTINCT om.member_id
			FROM organization_memberships om
			WHERE om.organization_id = $1
				AND om.left_at IS NULL
		),
		participant_ids AS (
			SELECT DISTINCT member_id
			FROM (
				SELECT h.created_user AS member_id
				FROM hops h
				WHERE h.organization_id = $1
				UNION
				SELECT h.matched_user AS member_id
				FROM hops h
				WHERE h.organization_id = $1
					AND h.matched_user IS NOT NULL
			) participants
		)
		SELECT
			COALESCE(COUNT(*) FILTER (WHERE p.member_id IS NOT NULL), 0),
			COALESCE(COUNT(*) FILTER (WHERE p.member_id IS NULL), 0)
		FROM active_members am
		LEFT JOIN participant_ids p ON p.member_id = am.member_id
	`, orgID).Scan(&detail.ParticipatedUsers, &detail.NeverParticipatedUsers); err != nil {
		return types.OrganizationHopMetricsDetail{}, fmt.Errorf("count organization participant coverage: %w", err)
	}

	detail.AvgOffersAccepted, err = averageOffersPerHopStatus(ctx, db, orgID, types.HopStatusAccepted)
	if err != nil {
		return types.OrganizationHopMetricsDetail{}, err
	}

	detail.AvgOffersCompleted, err = averageOffersPerHopStatus(ctx, db, orgID, types.HopStatusCompleted)
	if err != nil {
		return types.OrganizationHopMetricsDetail{}, err
	}

	return detail, nil
}

func organizationTopParticipants(ctx context.Context, db *sql.DB, orgID int64, limit int) ([]types.OrganizationParticipantMetric, error) {
	if limit <= 0 {
		limit = 5
	}

	rows, err := db.QueryContext(ctx, `
		WITH active_members AS (
			SELECT DISTINCT om.member_id
			FROM organization_memberships om
			WHERE om.organization_id = $1
				AND om.left_at IS NULL
		),
		created_counts AS (
			SELECT h.created_user AS member_id, COUNT(*) AS created_count
			FROM hops h
			WHERE h.organization_id = $1
			GROUP BY h.created_user
		),
		matched_counts AS (
			SELECT h.matched_user AS member_id, COUNT(*) AS matched_count
			FROM hops h
			WHERE h.organization_id = $1
				AND h.matched_user IS NOT NULL
			GROUP BY h.matched_user
		)
		SELECT
			m.id,
			COALESCE(NULLIF(TRIM(CONCAT_WS(' ', m.first_name, m.last_name)), ''), m.email) AS member_name,
			COALESCE(cc.created_count, 0) AS created_count,
			COALESCE(mc.matched_count, 0) AS matched_count
		FROM active_members am
		JOIN members m ON m.id = am.member_id
		LEFT JOIN created_counts cc ON cc.member_id = am.member_id
		LEFT JOIN matched_counts mc ON mc.member_id = am.member_id
		WHERE COALESCE(cc.created_count, 0) + COALESCE(mc.matched_count, 0) > 0
		ORDER BY COALESCE(cc.created_count, 0) + COALESCE(mc.matched_count, 0) DESC,
			COALESCE(cc.created_count, 0) DESC,
			member_name ASC
		LIMIT $2
	`, orgID, limit)
	if err != nil {
		return nil, fmt.Errorf("list organization top participants: %w", err)
	}
	defer rows.Close()

	out := make([]types.OrganizationParticipantMetric, 0, limit)
	for rows.Next() {
		var row types.OrganizationParticipantMetric
		if err := rows.Scan(&row.MemberID, &row.MemberName, &row.CreatedCount, &row.MatchedCount); err != nil {
			return nil, fmt.Errorf("scan organization top participant: %w", err)
		}
		row.ParticipationCount = row.CreatedCount + row.MatchedCount
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list organization top participants: %w", err)
	}
	return out, nil
}

func averageOffersPerHopStatus(ctx context.Context, db *sql.DB, orgID int64, status string) (float64, error) {
	var avg sql.NullFloat64
	if err := db.QueryRowContext(ctx, `
		SELECT AVG(offer_count)
		FROM (
			SELECT COUNT(hho.member_id)::float8 AS offer_count
			FROM hops h
			LEFT JOIN hop_help_offers hho ON hho.hop_id = h.id
			WHERE h.organization_id = $1
				AND h.status = $2
			GROUP BY h.id
		) offers
	`, orgID, status).Scan(&avg); err != nil {
		return 0, fmt.Errorf("average offers per %s hop: %w", strings.TrimSpace(status), err)
	}
	if !avg.Valid {
		return 0, nil
	}
	return avg.Float64, nil
}

func adminOrganizationRecentHops(ctx context.Context, db *sql.DB, orgID int64, limit int) ([]types.AdminOrganizationHop, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT
			h.id,
			h.organization_id,
			h.title,
			h.status,
			COALESCE(NULLIF(TRIM(CONCAT_WS(' ', m.first_name, m.last_name)), ''), m.email),
			h.created_at
		FROM hops h
		JOIN members m ON m.id = h.created_user
		WHERE h.organization_id = $1
		ORDER BY h.created_at DESC
		LIMIT $2
	`, orgID, limit)
	if err != nil {
		return nil, fmt.Errorf("list organization hops for admin: %w", err)
	}
	defer rows.Close()

	var hops []types.AdminOrganizationHop
	for rows.Next() {
		var hop types.AdminOrganizationHop
		if err := rows.Scan(&hop.ID, &hop.OrganizationID, &hop.Title, &hop.Status, &hop.CreatedByName, &hop.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan organization hop for admin: %w", err)
		}
		hops = append(hops, hop)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list organization hops for admin: %w", err)
	}
	return hops, nil
}
