package service

import (
	"context"
	"database/sql"
	"fmt"
	"sort"

	"hopshare/internal/types"
)

const defaultAdminLeaderboardLimit = 5

var adminHopStatusOrder = []string{
	types.HopStatusOpen,
	types.HopStatusAccepted,
	types.HopStatusCompleted,
	types.HopStatusCanceled,
	types.HopStatusExpired,
}

func AdminAppOverview(ctx context.Context, db *sql.DB, leaderboardLimit int) (types.AdminAppOverview, error) {
	if db == nil {
		return types.AdminAppOverview{}, ErrNilDB
	}
	if leaderboardLimit <= 0 {
		leaderboardLimit = defaultAdminLeaderboardLimit
	}

	var out types.AdminAppOverview
	out.LeaderboardLimit = leaderboardLimit

	if err := db.QueryRowContext(ctx, `
		SELECT
			COALESCE(SUM(CASE WHEN enabled THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN enabled THEN 0 ELSE 1 END), 0)
		FROM organizations
	`).Scan(&out.OrganizationCounts.Enabled, &out.OrganizationCounts.Disabled); err != nil {
		return types.AdminAppOverview{}, fmt.Errorf("count organizations by enabled state: %w", err)
	}

	if err := db.QueryRowContext(ctx, `
		SELECT
			COALESCE(SUM(CASE WHEN enabled THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN enabled THEN 0 ELSE 1 END), 0)
		FROM members
	`).Scan(&out.UserCounts.Enabled, &out.UserCounts.Disabled); err != nil {
		return types.AdminAppOverview{}, fmt.Errorf("count users by enabled state: %w", err)
	}

	if err := db.QueryRowContext(ctx, `
		SELECT
			COALESCE(SUM(CASE WHEN verified THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN verified THEN 0 ELSE 1 END), 0)
		FROM members
	`).Scan(&out.UserVerificationCounts.Verified, &out.UserVerificationCounts.NotVerified); err != nil {
		return types.AdminAppOverview{}, fmt.Errorf("count users by verification state: %w", err)
	}

	if err := db.QueryRowContext(ctx, `
		SELECT
			COUNT(*),
			COALESCE(SUM(CASE WHEN hours_delta > 0 THEN hours_delta ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN hours_delta < 0 THEN -hours_delta ELSE 0 END), 0)
		FROM hour_balance_adjustments
		WHERE is_starting_balance = FALSE
	`).Scan(&out.HourOverrideCounts.Count, &out.HourOverrideCounts.HoursGiven, &out.HourOverrideCounts.HoursRemoved); err != nil {
		return types.AdminAppOverview{}, fmt.Errorf("count admin hour overrides: %w", err)
	}

	hopCountsByStatus, err := adminHopCountsByStatus(ctx, db)
	if err != nil {
		return types.AdminAppOverview{}, err
	}
	out.HopsByStatus = hopCountsByStatus

	if err := db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(hours), 0)
		FROM hop_transactions
	`).Scan(&out.TotalHoursExchanged); err != nil {
		return types.AdminAppOverview{}, fmt.Errorf("sum exchanged hours: %w", err)
	}

	topByHopsCreated, err := topOrganizationsByHopsCreated(ctx, db, leaderboardLimit)
	if err != nil {
		return types.AdminAppOverview{}, err
	}
	out.TopOrgsByHopsCreated = topByHopsCreated

	topByHours, err := topOrganizationsByHoursExchanged(ctx, db, leaderboardLimit)
	if err != nil {
		return types.AdminAppOverview{}, err
	}
	out.TopOrgsByHoursExchanged = topByHours

	topByUsers, err := topOrganizationsByUsers(ctx, db, leaderboardLimit)
	if err != nil {
		return types.AdminAppOverview{}, err
	}
	out.TopOrgsByUsers = topByUsers

	return out, nil
}

func adminHopCountsByStatus(ctx context.Context, db *sql.DB) ([]types.AdminHopStatusCount, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT status, COUNT(*)
		FROM hops
		GROUP BY status
	`)
	if err != nil {
		return nil, fmt.Errorf("count hops by status: %w", err)
	}
	defer rows.Close()

	countsByStatus := make(map[string]int)
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, fmt.Errorf("scan hop status count: %w", err)
		}
		countsByStatus[status] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("count hops by status: %w", err)
	}

	out := make([]types.AdminHopStatusCount, 0, len(adminHopStatusOrder)+len(countsByStatus))
	for _, status := range adminHopStatusOrder {
		out = append(out, types.AdminHopStatusCount{
			Status: status,
			Count:  countsByStatus[status],
		})
		delete(countsByStatus, status)
	}

	if len(countsByStatus) > 0 {
		extraStatuses := make([]string, 0, len(countsByStatus))
		for status := range countsByStatus {
			extraStatuses = append(extraStatuses, status)
		}
		sort.Strings(extraStatuses)
		for _, status := range extraStatuses {
			out = append(out, types.AdminHopStatusCount{
				Status: status,
				Count:  countsByStatus[status],
			})
		}
	}

	return out, nil
}

func topOrganizationsByHopsCreated(ctx context.Context, db *sql.DB, limit int) ([]types.AdminOrganizationLeaderboardEntry, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT
			o.id,
			o.name,
			o.url_name,
			o.enabled,
			COUNT(h.id) AS metric_value
		FROM organizations o
		LEFT JOIN hops h ON h.organization_id = o.id
		GROUP BY o.id, o.name, o.url_name, o.enabled
		ORDER BY metric_value DESC, o.name ASC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("list top organizations by hops created: %w", err)
	}
	defer rows.Close()

	return scanAdminOrganizationLeaderboardRows(rows, false)
}

func topOrganizationsByHoursExchanged(ctx context.Context, db *sql.DB, limit int) ([]types.AdminOrganizationLeaderboardEntry, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT
			o.id,
			o.name,
			o.url_name,
			o.enabled,
			COALESCE(SUM(ht.hours), 0) AS metric_value
		FROM organizations o
		LEFT JOIN hop_transactions ht ON ht.organization_id = o.id
		GROUP BY o.id, o.name, o.url_name, o.enabled
		ORDER BY metric_value DESC, o.name ASC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("list top organizations by exchanged hours: %w", err)
	}
	defer rows.Close()

	return scanAdminOrganizationLeaderboardRows(rows, false)
}

func topOrganizationsByUsers(ctx context.Context, db *sql.DB, limit int) ([]types.AdminOrganizationLeaderboardEntry, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT
			o.id,
			o.name,
			o.url_name,
			o.enabled,
			COALESCE(COUNT(CASE WHEN om.member_id IS NOT NULL AND om.left_at IS NULL THEN 1 END), 0) AS total_users,
			COALESCE(COUNT(CASE WHEN om.member_id IS NOT NULL AND om.left_at IS NULL AND m.enabled THEN 1 END), 0) AS enabled_users,
			COALESCE(COUNT(CASE WHEN om.member_id IS NOT NULL AND om.left_at IS NULL AND NOT m.enabled THEN 1 END), 0) AS disabled_users
		FROM organizations o
		LEFT JOIN organization_memberships om ON om.organization_id = o.id
		LEFT JOIN members m ON m.id = om.member_id
		GROUP BY o.id, o.name, o.url_name, o.enabled
		ORDER BY total_users DESC, o.name ASC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("list top organizations by users: %w", err)
	}
	defer rows.Close()

	return scanAdminOrganizationLeaderboardRows(rows, true)
}

func scanAdminOrganizationLeaderboardRows(rows *sql.Rows, includeUserBreakdown bool) ([]types.AdminOrganizationLeaderboardEntry, error) {
	out := make([]types.AdminOrganizationLeaderboardEntry, 0)
	for rows.Next() {
		var row types.AdminOrganizationLeaderboardEntry
		if includeUserBreakdown {
			if err := rows.Scan(
				&row.OrganizationID,
				&row.OrganizationName,
				&row.OrganizationURLName,
				&row.OrganizationEnabled,
				&row.Value,
				&row.EnabledUsers,
				&row.DisabledUsers,
			); err != nil {
				return nil, fmt.Errorf("scan top organization row: %w", err)
			}
		} else {
			if err := rows.Scan(
				&row.OrganizationID,
				&row.OrganizationName,
				&row.OrganizationURLName,
				&row.OrganizationEnabled,
				&row.Value,
			); err != nil {
				return nil, fmt.Errorf("scan top organization row: %w", err)
			}
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list top organization rows: %w", err)
	}

	return out, nil
}
