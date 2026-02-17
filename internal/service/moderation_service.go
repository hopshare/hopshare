package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/lib/pq"

	"hopshare/internal/types"
)

const defaultModerationReportLimit = 100

type CreateHopCommentReportParams struct {
	OrganizationID   int64
	HopID            int64
	HopCommentID     int64
	ReportedMemberID int64
	ReporterDetails  string
}

type CreateHopImageReportParams struct {
	OrganizationID   int64
	HopID            int64
	HopImageID       int64
	ReportedMemberID int64
	ReporterDetails  string
}

func CreateHopCommentReport(ctx context.Context, db *sql.DB, p CreateHopCommentReportParams) (int64, error) {
	if db == nil {
		return 0, ErrNilDB
	}
	if p.OrganizationID == 0 {
		return 0, ErrMissingOrgID
	}
	if p.HopID == 0 || p.HopCommentID == 0 {
		return 0, ErrModerationTargetNotFound
	}
	if p.ReportedMemberID == 0 {
		return 0, ErrMissingMemberID
	}

	details := strings.TrimSpace(p.ReporterDetails)
	var reportID int64
	if err := db.QueryRowContext(ctx, `
		INSERT INTO moderation_reports (
			organization_id,
			hop_id,
			report_type,
			hop_comment_id,
			hop_image_id,
			reported_member_id,
			content_member_id,
			content_summary,
			reporter_details
		)
		SELECT
			h.organization_id,
			h.id,
			$1,
			c.id,
			NULL,
			$2,
			c.member_id,
			LEFT(c.body, 400),
			NULLIF(BTRIM($3), '')
		FROM hop_comments c
		JOIN hops h ON h.id = c.hop_id
		WHERE c.id = $4
			AND c.hop_id = $5
			AND h.organization_id = $6
		RETURNING id
	`, types.ModerationReportTypeHopComment, p.ReportedMemberID, details, p.HopCommentID, p.HopID, p.OrganizationID).Scan(&reportID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrModerationTargetNotFound
		}
		if moderationIsUniqueViolation(err) {
			return 0, ErrModerationAlreadyReported
		}
		return 0, fmt.Errorf("create hop comment report: %w", err)
	}
	return reportID, nil
}

func CreateHopImageReport(ctx context.Context, db *sql.DB, p CreateHopImageReportParams) (int64, error) {
	if db == nil {
		return 0, ErrNilDB
	}
	if p.OrganizationID == 0 {
		return 0, ErrMissingOrgID
	}
	if p.HopID == 0 || p.HopImageID == 0 {
		return 0, ErrModerationTargetNotFound
	}
	if p.ReportedMemberID == 0 {
		return 0, ErrMissingMemberID
	}

	details := strings.TrimSpace(p.ReporterDetails)
	var reportID int64
	if err := db.QueryRowContext(ctx, `
		INSERT INTO moderation_reports (
			organization_id,
			hop_id,
			report_type,
			hop_comment_id,
			hop_image_id,
			reported_member_id,
			content_member_id,
			content_summary,
			reporter_details
		)
		SELECT
			h.organization_id,
			h.id,
			$1,
			NULL,
			hi.id,
			$2,
			hi.member_id,
			LEFT(COALESCE(hi.content_type, 'image') || ' uploaded on ' || TO_CHAR(hi.created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS') || ' UTC', 400),
			NULLIF(BTRIM($3), '')
		FROM hop_images hi
		JOIN hops h ON h.id = hi.hop_id
		WHERE hi.id = $4
			AND hi.hop_id = $5
			AND h.organization_id = $6
		RETURNING id
	`, types.ModerationReportTypeHopImage, p.ReportedMemberID, details, p.HopImageID, p.HopID, p.OrganizationID).Scan(&reportID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrModerationTargetNotFound
		}
		if moderationIsUniqueViolation(err) {
			return 0, ErrModerationAlreadyReported
		}
		return 0, fmt.Errorf("create hop image report: %w", err)
	}
	return reportID, nil
}

func ListModerationReports(ctx context.Context, db *sql.DB, p types.ListModerationReportsParams) ([]types.ModerationReport, error) {
	if db == nil {
		return nil, ErrNilDB
	}

	statusFilter := strings.TrimSpace(strings.ToLower(p.Status))
	if statusFilter == "" {
		statusFilter = types.ModerationReportStatusOpen
	}
	typeFilter := strings.TrimSpace(strings.ToLower(p.ReportType))
	if typeFilter == "" {
		typeFilter = "all"
	}
	query := strings.TrimSpace(p.Query)
	limit := p.Limit
	if limit <= 0 {
		limit = defaultModerationReportLimit
	}

	rows, err := db.QueryContext(ctx, `
		SELECT
			mr.id,
			mr.organization_id,
			o.name,
			o.url_name,
			mr.hop_id,
			mr.report_type,
			mr.hop_comment_id,
			mr.hop_image_id,
			mr.reported_member_id,
			COALESCE(NULLIF(TRIM(CONCAT_WS(' ', reporter.first_name, reporter.last_name)), ''), reporter.username),
			mr.content_member_id,
			COALESCE(NULLIF(TRIM(CONCAT_WS(' ', content_member.first_name, content_member.last_name)), ''), content_member.username),
			mr.content_summary,
			mr.reporter_details,
			mr.status,
			mr.resolution_action,
			mr.resolved_by_member_id,
			mr.resolved_at,
			mr.created_at
		FROM moderation_reports mr
		JOIN organizations o ON o.id = mr.organization_id
		JOIN members reporter ON reporter.id = mr.reported_member_id
		JOIN members content_member ON content_member.id = mr.content_member_id
		WHERE ($1 = 'all' OR mr.status = $1)
			AND ($2 = 'all' OR mr.report_type = $2)
			AND ($3 = '' OR LOWER(o.name) LIKE '%' || LOWER($3) || '%')
		ORDER BY
			CASE WHEN mr.status = $4 THEN 0 ELSE 1 END,
			mr.created_at DESC,
			mr.id DESC
		LIMIT $5
	`, statusFilter, typeFilter, query, types.ModerationReportStatusOpen, limit)
	if err != nil {
		return nil, fmt.Errorf("list moderation reports: %w", err)
	}
	defer rows.Close()

	out := make([]types.ModerationReport, 0)
	for rows.Next() {
		var report types.ModerationReport
		var hopCommentID sql.NullInt64
		var hopImageID sql.NullInt64
		var reporterDetails sql.NullString
		var resolutionAction sql.NullString
		var resolvedByMemberID sql.NullInt64
		var resolvedAt sql.NullTime
		if err := rows.Scan(
			&report.ID,
			&report.OrganizationID,
			&report.OrganizationName,
			&report.OrganizationURLName,
			&report.HopID,
			&report.ReportType,
			&hopCommentID,
			&hopImageID,
			&report.ReportedMemberID,
			&report.ReportedMemberName,
			&report.ContentMemberID,
			&report.ContentMemberName,
			&report.ContentSummary,
			&reporterDetails,
			&report.Status,
			&resolutionAction,
			&resolvedByMemberID,
			&resolvedAt,
			&report.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan moderation report: %w", err)
		}
		if hopCommentID.Valid {
			v := hopCommentID.Int64
			report.HopCommentID = &v
		}
		if hopImageID.Valid {
			v := hopImageID.Int64
			report.HopImageID = &v
		}
		if reporterDetails.Valid {
			v := reporterDetails.String
			report.ReporterDetails = &v
		}
		if resolutionAction.Valid {
			v := resolutionAction.String
			report.ResolutionAction = &v
		}
		if resolvedByMemberID.Valid {
			v := resolvedByMemberID.Int64
			report.ResolvedByMemberID = &v
		}
		if resolvedAt.Valid {
			v := resolvedAt.Time
			report.ResolvedAt = &v
		}
		out = append(out, report)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list moderation reports: %w", err)
	}
	return out, nil
}

func DismissModerationReport(ctx context.Context, db *sql.DB, reportID, actorMemberID int64) (types.ModerationReport, error) {
	return resolveModerationReport(ctx, db, reportID, actorMemberID, types.ModerationResolutionDismiss, false)
}

func DeleteReportedHopComment(ctx context.Context, db *sql.DB, reportID, actorMemberID int64) (types.ModerationReport, error) {
	return resolveModerationReport(ctx, db, reportID, actorMemberID, types.ModerationResolutionDeleteComment, true)
}

func DeleteReportedHopImage(ctx context.Context, db *sql.DB, reportID, actorMemberID int64) (types.ModerationReport, error) {
	return resolveModerationReport(ctx, db, reportID, actorMemberID, types.ModerationResolutionDeleteImage, true)
}

type moderationReportResolutionRow struct {
	ID             int64
	OrganizationID int64
	HopID          int64
	ReportType     string
	HopCommentID   sql.NullInt64
	HopImageID     sql.NullInt64
	Status         string
}

func resolveModerationReport(ctx context.Context, db *sql.DB, reportID, actorMemberID int64, resolutionAction string, applyDeletion bool) (types.ModerationReport, error) {
	if db == nil {
		return types.ModerationReport{}, ErrNilDB
	}
	if reportID == 0 {
		return types.ModerationReport{}, ErrModerationReportNotFound
	}
	if actorMemberID == 0 {
		return types.ModerationReport{}, ErrMissingMemberID
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return types.ModerationReport{}, fmt.Errorf("begin moderation report resolution tx: %w", err)
	}
	defer tx.Rollback()

	row, err := loadModerationReportResolutionRow(ctx, tx, reportID)
	if err != nil {
		return types.ModerationReport{}, err
	}
	if row.Status != types.ModerationReportStatusOpen {
		return types.ModerationReport{}, ErrModerationReportResolved
	}

	if applyDeletion {
		switch resolutionAction {
		case types.ModerationResolutionDeleteComment:
			if row.ReportType != types.ModerationReportTypeHopComment || !row.HopCommentID.Valid {
				return types.ModerationReport{}, ErrModerationTargetMismatch
			}
			if err := moderationDeleteHopComment(ctx, tx, row.HopCommentID.Int64, row.HopID); err != nil {
				return types.ModerationReport{}, err
			}
		case types.ModerationResolutionDeleteImage:
			if row.ReportType != types.ModerationReportTypeHopImage || !row.HopImageID.Valid {
				return types.ModerationReport{}, ErrModerationTargetMismatch
			}
			if err := moderationDeleteHopImage(ctx, tx, row.HopImageID.Int64, row.HopID); err != nil {
				return types.ModerationReport{}, err
			}
		default:
			return types.ModerationReport{}, ErrModerationTargetMismatch
		}
	}

	nextStatus := types.ModerationReportStatusDismissed
	if resolutionAction != types.ModerationResolutionDismiss {
		nextStatus = types.ModerationReportStatusActioned
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE moderation_reports
		SET status = $1,
			resolution_action = $2,
			resolved_by_member_id = $3,
			resolved_at = NOW()
		WHERE id = $4
	`, nextStatus, resolutionAction, actorMemberID, reportID); err != nil {
		return types.ModerationReport{}, fmt.Errorf("resolve moderation report: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return types.ModerationReport{}, fmt.Errorf("commit moderation report resolution tx: %w", err)
	}

	result := types.ModerationReport{
		ID:             row.ID,
		OrganizationID: row.OrganizationID,
		HopID:          row.HopID,
		ReportType:     row.ReportType,
		Status:         nextStatus,
	}
	if row.HopCommentID.Valid {
		v := row.HopCommentID.Int64
		result.HopCommentID = &v
	}
	if row.HopImageID.Valid {
		v := row.HopImageID.Int64
		result.HopImageID = &v
	}
	return result, nil
}

func loadModerationReportResolutionRow(ctx context.Context, tx *sql.Tx, reportID int64) (moderationReportResolutionRow, error) {
	var row moderationReportResolutionRow
	if err := tx.QueryRowContext(ctx, `
		SELECT id, organization_id, hop_id, report_type, hop_comment_id, hop_image_id, status
		FROM moderation_reports
		WHERE id = $1
		FOR UPDATE
	`, reportID).Scan(
		&row.ID,
		&row.OrganizationID,
		&row.HopID,
		&row.ReportType,
		&row.HopCommentID,
		&row.HopImageID,
		&row.Status,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return moderationReportResolutionRow{}, ErrModerationReportNotFound
		}
		return moderationReportResolutionRow{}, fmt.Errorf("load moderation report: %w", err)
	}
	return row, nil
}

func moderationDeleteHopComment(ctx context.Context, tx *sql.Tx, commentID, hopID int64) error {
	res, err := tx.ExecContext(ctx, `
		DELETE FROM hop_comments
		WHERE id = $1 AND hop_id = $2
	`, commentID, hopID)
	if err != nil {
		return fmt.Errorf("delete reported hop comment: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete reported hop comment rows affected: %w", err)
	}
	if affected == 0 {
		return ErrModerationTargetNotFound
	}
	return nil
}

func moderationDeleteHopImage(ctx context.Context, tx *sql.Tx, imageID, hopID int64) error {
	res, err := tx.ExecContext(ctx, `
		DELETE FROM hop_images
		WHERE id = $1 AND hop_id = $2
	`, imageID, hopID)
	if err != nil {
		return fmt.Errorf("delete reported hop image: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete reported hop image rows affected: %w", err)
	}
	if affected == 0 {
		return ErrModerationTargetNotFound
	}
	return nil
}

func moderationIsUniqueViolation(err error) bool {
	var pqErr *pq.Error
	if !errors.As(err, &pqErr) {
		return false
	}
	return pqErr.Code == "23505"
}
