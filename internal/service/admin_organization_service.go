package service

import (
	"context"
	"database/sql"
	"fmt"

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

	return detail, nil
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
