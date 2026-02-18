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
	`, orgID).Scan(&detail.HourOverrideCounts.Count, &detail.HourOverrideCounts.HoursGiven, &detail.HourOverrideCounts.HoursRemoved); err != nil {
		return types.AdminOrganizationDetail{}, fmt.Errorf("load organization hour override counts: %w", err)
	}

	statusCounts, err := adminOrganizationHopCountsByStatus(ctx, db, orgID)
	if err != nil {
		return types.AdminOrganizationDetail{}, err
	}
	detail.HopCounts = statusCounts

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

	countsByStatus := make(map[string]int)
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, fmt.Errorf("scan organization hop status count: %w", err)
		}
		countsByStatus[status] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("count organization hops by status: %w", err)
	}

	out := make([]types.AdminHopStatusCount, 0, len(adminHopStatusOrder)+len(countsByStatus))
	for _, status := range adminHopStatusOrder {
		out = append(out, types.AdminHopStatusCount{
			Status: status,
			Count:  countsByStatus[status],
		})
		delete(countsByStatus, status)
	}
	for status, count := range countsByStatus {
		out = append(out, types.AdminHopStatusCount{
			Status: status,
			Count:  count,
		})
	}
	return out, nil
}

func adminOrganizationRecentHops(ctx context.Context, db *sql.DB, orgID int64, limit int) ([]types.AdminOrganizationHop, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT
			h.id,
			h.organization_id,
			h.title,
			h.status,
			COALESCE(NULLIF(TRIM(CONCAT_WS(' ', m.first_name, m.last_name)), ''), m.username),
			h.created_at
		FROM hops h
		JOIN members m ON m.id = h.created_by
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
