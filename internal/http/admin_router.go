package http

import (
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"hopshare/internal/service"
	"hopshare/internal/types"
	"hopshare/web/templates"
)

const (
	adminTabApp                    = "app"
	adminTabOrganizations          = "organizations"
	adminTabModeration             = "moderation"
	adminTabUsers                  = "users"
	adminTabMessages               = "messages"
	adminTabAudit                  = "audit"
	adminOverviewLeaderboardCap    = 5
	adminOrganizationsSearchLimit  = 25
	adminOrganizationHopListLimit  = 100
	adminModerationReportListLimit = 200
	adminUsersSearchLimit          = 25
	adminMessagesSearchLimit       = 25
	adminMessagesConversationLimit = 200
	adminAuditEventListLimit       = 250
	adminAuditExportLimit          = 2000

	adminOrgActionDisable = "disable_org"
	adminOrgActionEnable  = "enable_org"
	adminOrgActionExpire  = "expire_hop"
	adminOrgActionDelete  = "delete_hop"

	adminModerationActionDismiss       = "dismiss_report"
	adminModerationActionDeleteComment = "delete_comment"
	adminModerationActionDeleteImage   = "delete_image"

	adminModerationStatusAll = "all"
	adminModerationTypeAll   = "all"

	adminUserActionDisable            = "disable_user"
	adminUserActionEnable             = "enable_user"
	adminUserActionDelete             = "delete_user"
	adminUserActionForcePasswordReset = "force_password_reset"
	adminUserActionSendVerification   = "send_verification_email"
	adminUserActionRevokeSessions     = "revoke_sessions"
	adminUserActionAdjustHours        = "adjust_hours"

	adminMessageSubjectPrefix = "ADMIN Message:"

	adminAuditExportFormatCSV  = "csv"
	adminAuditExportFormatJSON = "json"
)

func (s *Server) handleAdmin(w http.ResponseWriter, r *http.Request) {
	user := s.currentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	activeTab := normalizeAdminTab(r.URL.Query().Get("tab"))

	var overview types.AdminAppOverview
	var orgTab types.AdminOrganizationTabData
	var moderationTab types.AdminModerationTabData
	var usersTab types.AdminUsersTabData
	var messagesTab types.AdminMessagesTabData
	var auditTab types.AdminAuditTabData
	var err error

	switch activeTab {
	case adminTabOrganizations:
		orgTab, err = s.loadAdminOrganizationTabData(r)
		if err != nil {
			log.Printf("load admin organization tab data: %v", err)
			http.Error(w, "could not load admin organizations", http.StatusInternalServerError)
			return
		}
	case adminTabModeration:
		moderationTab, err = s.loadAdminModerationTabData(r)
		if err != nil {
			log.Printf("load admin moderation tab data: %v", err)
			http.Error(w, "could not load admin moderation queue", http.StatusInternalServerError)
			return
		}
	case adminTabUsers:
		usersTab, err = s.loadAdminUsersTabData(r)
		if err != nil {
			log.Printf("load admin users tab data: %v", err)
			http.Error(w, "could not load admin users", http.StatusInternalServerError)
			return
		}
	case adminTabMessages:
		messagesTab, err = s.loadAdminMessagesTabData(r, user.ID)
		if err != nil {
			log.Printf("load admin messages tab data: %v", err)
			http.Error(w, "could not load admin messages", http.StatusInternalServerError)
			return
		}
	case adminTabAudit:
		auditTab, err = s.loadAdminAuditTabData(r)
		if err != nil {
			log.Printf("load admin audit tab data: %v", err)
			http.Error(w, "could not load admin audit events", http.StatusInternalServerError)
			return
		}
	default:
		overview, err = service.AdminAppOverview(r.Context(), s.db, adminOverviewLeaderboardCap)
		if err != nil {
			log.Printf("load admin app overview: %v", err)
			http.Error(w, "could not load admin overview", http.StatusInternalServerError)
			return
		}
	}

	render(w, r, templates.Admin(s.currentUserEmailPtr(r), activeTab, overview, orgTab, moderationTab, usersTab, messagesTab, auditTab))
}

func (s *Server) handleAdminOrganizationAction(w http.ResponseWriter, r *http.Request) {
	user := s.currentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	action := strings.TrimSpace(r.FormValue("action"))
	orgID, err := parseAdminOrgID(r.FormValue("org_id"))
	if err != nil {
		http.Redirect(w, r, "/admin?tab="+adminTabOrganizations+"&error="+url.QueryEscape("Invalid organization."), http.StatusSeeOther)
		return
	}

	searchQuery := strings.TrimSpace(r.FormValue("q"))
	reason := strings.TrimSpace(r.FormValue("reason"))
	redirectBase := adminOrganizationsRedirect(orgID, searchQuery)
	if reason == "" {
		http.Redirect(w, r, redirectWithMessage(redirectBase, "error", "A reason is required for this action."), http.StatusSeeOther)
		return
	}

	switch action {
	case adminOrgActionDisable:
		if err := service.SetOrganizationEnabled(r.Context(), s.db, orgID, false); err != nil {
			handleAdminOrganizationActionError(w, r, redirectBase, err, "Could not disable organization.")
			return
		}
		if err := s.writeAdminOrganizationAudit(r, user.ID, orgID, service.AdminAuditActionOrganizationDisable, nil, reason); err != nil {
			http.Redirect(w, r, redirectWithMessage(redirectBase, "error", err.Error()), http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, redirectWithMessage(redirectBase, "success", "Organization disabled."), http.StatusSeeOther)
	case adminOrgActionEnable:
		if err := service.SetOrganizationEnabled(r.Context(), s.db, orgID, true); err != nil {
			handleAdminOrganizationActionError(w, r, redirectBase, err, "Could not re-enable organization.")
			return
		}
		if err := s.writeAdminOrganizationAudit(r, user.ID, orgID, service.AdminAuditActionOrganizationEnable, nil, reason); err != nil {
			http.Redirect(w, r, redirectWithMessage(redirectBase, "error", err.Error()), http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, redirectWithMessage(redirectBase, "success", "Organization re-enabled."), http.StatusSeeOther)
	case adminOrgActionExpire:
		hopID, err := parseAdminHopID(r.FormValue("hop_id"))
		if err != nil {
			http.Redirect(w, r, redirectWithMessage(redirectBase, "error", "Invalid hop."), http.StatusSeeOther)
			return
		}
		if err := service.AdminExpireHop(r.Context(), s.db, orgID, hopID); err != nil {
			handleAdminOrganizationActionError(w, r, redirectBase, err, "Could not expire hop.")
			return
		}
		if err := s.writeAdminOrganizationAudit(r, user.ID, orgID, service.AdminAuditActionHopExpire, &hopID, reason); err != nil {
			http.Redirect(w, r, redirectWithMessage(redirectBase, "error", err.Error()), http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, redirectWithMessage(redirectBase, "success", "Hop expired."), http.StatusSeeOther)
	case adminOrgActionDelete:
		hopID, err := parseAdminHopID(r.FormValue("hop_id"))
		if err != nil {
			http.Redirect(w, r, redirectWithMessage(redirectBase, "error", "Invalid hop."), http.StatusSeeOther)
			return
		}
		if err := service.AdminDeleteHop(r.Context(), s.db, orgID, hopID); err != nil {
			handleAdminOrganizationActionError(w, r, redirectBase, err, "Could not delete hop.")
			return
		}
		if err := s.writeAdminOrganizationAudit(r, user.ID, orgID, service.AdminAuditActionHopDelete, &hopID, reason); err != nil {
			http.Redirect(w, r, redirectWithMessage(redirectBase, "error", err.Error()), http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, redirectWithMessage(redirectBase, "success", "Hop deleted."), http.StatusSeeOther)
	default:
		http.Redirect(w, r, redirectWithMessage(redirectBase, "error", "Unknown admin action."), http.StatusSeeOther)
	}
}

func (s *Server) handleAdminModerationAction(w http.ResponseWriter, r *http.Request) {
	user := s.currentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	action := strings.TrimSpace(r.FormValue("action"))
	reportID, err := parseAdminReportID(r.FormValue("report_id"))
	if err != nil {
		http.Redirect(w, r, "/admin?tab="+adminTabModeration+"&error="+url.QueryEscape("Invalid report."), http.StatusSeeOther)
		return
	}

	statusFilter := normalizeModerationStatusFilter(r.FormValue("status"))
	typeFilter := normalizeModerationTypeFilter(r.FormValue("type"))
	searchQuery := strings.TrimSpace(r.FormValue("q"))
	redirectBase := adminModerationRedirect(statusFilter, typeFilter, searchQuery)

	reason := strings.TrimSpace(r.FormValue("reason"))
	if reason == "" {
		http.Redirect(w, r, redirectWithMessage(redirectBase, "error", "A reason is required for this action."), http.StatusSeeOther)
		return
	}

	var result types.ModerationReport
	var auditAction string
	var successMessage string

	switch action {
	case adminModerationActionDismiss:
		result, err = service.DismissModerationReport(r.Context(), s.db, reportID, user.ID)
		auditAction = service.AdminAuditActionModerationDismiss
		successMessage = "Report dismissed."
	case adminModerationActionDeleteComment:
		result, err = service.DeleteReportedHopComment(r.Context(), s.db, reportID, user.ID)
		auditAction = service.AdminAuditActionModerationCommentDelete
		successMessage = "Comment deleted."
	case adminModerationActionDeleteImage:
		result, err = service.DeleteReportedHopImage(r.Context(), s.db, reportID, user.ID)
		auditAction = service.AdminAuditActionModerationImageDelete
		successMessage = "Image deleted."
	default:
		http.Redirect(w, r, redirectWithMessage(redirectBase, "error", "Unknown moderation action."), http.StatusSeeOther)
		return
	}
	if err != nil {
		switch {
		case errors.Is(err, service.ErrModerationReportNotFound):
			http.Redirect(w, r, redirectWithMessage(redirectBase, "error", "Invalid report."), http.StatusSeeOther)
		case errors.Is(err, service.ErrModerationReportResolved):
			http.Redirect(w, r, redirectWithMessage(redirectBase, "error", "Report has already been resolved."), http.StatusSeeOther)
		case errors.Is(err, service.ErrModerationTargetMismatch):
			http.Redirect(w, r, redirectWithMessage(redirectBase, "error", "Action does not match report type."), http.StatusSeeOther)
		case errors.Is(err, service.ErrModerationTargetNotFound):
			http.Redirect(w, r, redirectWithMessage(redirectBase, "error", "Reported content was already removed."), http.StatusSeeOther)
		default:
			log.Printf("resolve moderation report action=%q report=%d: %v", action, reportID, err)
			http.Redirect(w, r, redirectWithMessage(redirectBase, "error", "Could not process moderation action."), http.StatusSeeOther)
		}
		return
	}

	if err := s.writeAdminModerationAudit(r, user.ID, result, auditAction, reason); err != nil {
		http.Redirect(w, r, redirectWithMessage(redirectBase, "error", err.Error()), http.StatusSeeOther)
		return
	}

	http.Redirect(w, r, redirectWithMessage(redirectBase, "success", successMessage), http.StatusSeeOther)
}

func (s *Server) handleAdminUserAction(w http.ResponseWriter, r *http.Request) {
	user := s.currentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	action := strings.TrimSpace(r.FormValue("action"))
	memberID, err := parseAdminMemberID(r.FormValue("member_id"))
	if err != nil {
		http.Redirect(w, r, "/admin?tab="+adminTabUsers+"&error="+url.QueryEscape("Invalid user."), http.StatusSeeOther)
		return
	}

	searchQuery := strings.TrimSpace(r.FormValue("q"))
	redirectBase := adminUsersRedirect(memberID, searchQuery)

	reason := strings.TrimSpace(r.FormValue("reason"))
	if reason == "" {
		http.Redirect(w, r, redirectWithMessage(redirectBase, "error", "A reason is required for this action."), http.StatusSeeOther)
		return
	}

	targetMember, targetMemberErr := service.GetMemberByID(r.Context(), s.db, memberID)
	if targetMemberErr != nil {
		handleAdminUserActionError(w, r, redirectBase, targetMemberErr, "Could not load user.")
		return
	}

	actionMetadata := map[string]any{}
	var auditAction string
	var successMessage string

	switch action {
	case adminUserActionDisable:
		reopenedCount, err := service.AdminDisableMember(r.Context(), s.db, memberID, user.ID, memberDisplayName(user))
		if err != nil {
			handleAdminUserActionError(w, r, redirectBase, err, "Could not disable user.")
			return
		}
		auditAction = service.AdminAuditActionUserDisable
		actionMetadata["reopened_accepted_hop_count"] = reopenedCount
		successMessage = "User disabled."
		if reopenedCount > 0 {
			successMessage = fmt.Sprintf("User disabled. %d accepted hops were reopened.", reopenedCount)
		}
	case adminUserActionEnable:
		if err := service.SetMemberEnabled(r.Context(), s.db, memberID, true); err != nil {
			handleAdminUserActionError(w, r, redirectBase, err, "Could not re-enable user.")
			return
		}
		auditAction = service.AdminAuditActionUserEnable
		successMessage = "User re-enabled."
	case adminUserActionDelete:
		if memberID == user.ID {
			http.Redirect(w, r, redirectWithMessage(redirectBase, "error", "You cannot permanently delete your own admin account."), http.StatusSeeOther)
			return
		}
		if s.admins.IsAdmin(targetMember.Email) {
			http.Redirect(w, r, redirectWithMessage(redirectBase, "error", "Cannot delete an account listed in HOPSHARE_ADMIN_EMAILS. Remove the email from admin config first."), http.StatusSeeOther)
			return
		}
		deleteResult, err := service.AdminDeleteMember(r.Context(), s.db, memberID, user.ID, memberDisplayName(user))
		if err != nil {
			handleAdminUserActionError(w, r, redirectBase, err, "Could not permanently delete user.")
			return
		}
		s.sessions.RevokeAllForMember(memberID)
		actionMetadata["deleted_user_id"] = targetMember.ID
		actionMetadata["reopened_accepted_hop_count"] = deleteResult.ReopenedAcceptedHopCount
		actionMetadata["canceled_open_hop_count"] = deleteResult.CanceledOpenHopCount
		actionMetadata["withdrawn_offer_count"] = deleteResult.WithdrawnOfferCount
		actionMetadata["ended_membership_count"] = deleteResult.EndedMembershipCount
		actionMetadata["permanent_delete"] = true
		auditAction = service.AdminAuditActionUserDelete
		successMessage = "User permanently deleted. Their email can now be reused."
	case adminUserActionForcePasswordReset:
		if err := service.AdminForcePasswordReset(r.Context(), s.db, memberID); err != nil {
			handleAdminUserActionError(w, r, redirectBase, err, "Could not force password reset.")
			return
		}
		auditAction = service.AdminAuditActionUserForcePasswordReset
		successMessage = "Password reset forced. User must use Forgot Password."
	case adminUserActionSendVerification:
		auditAction = service.AdminAuditActionUserVerificationEmail
		if targetMember.Verified {
			actionMetadata["already_verified"] = true
			successMessage = "User email is already verified."
			break
		}

		token, tokenErr := service.IssueMemberToken(r.Context(), s.db, service.IssueMemberTokenParams{
			MemberID:    targetMember.ID,
			Purpose:     service.MemberTokenPurposeEmailVerification,
			TTL:         service.DefaultMemberTokenTTL,
			RequestedIP: requestIPFromRequest(r),
		})
		if tokenErr != nil {
			log.Printf("admin send verification email token issue failed: member_id=%d: %v", targetMember.ID, tokenErr)
			http.Redirect(w, r, redirectWithMessage(redirectBase, "error", "Could not send verification email."), http.StatusSeeOther)
			return
		}
		verifyURL := s.verifyEmailURL(token, targetMember.Email)
		if sendErr := s.passwordResetEmailSender.SendEmailVerification(r.Context(), targetMember.Email, verifyURL); sendErr != nil {
			log.Printf("admin send verification email failed: member_id=%d: %v", targetMember.ID, sendErr)
			http.Redirect(w, r, redirectWithMessage(redirectBase, "error", "Could not send verification email."), http.StatusSeeOther)
			return
		}
		actionMetadata["verification_email_sent"] = true
		successMessage = "Verification email sent."
	case adminUserActionRevokeSessions:
		revokedCount := s.sessions.RevokeAllForMember(memberID)
		actionMetadata["revoked_session_count"] = revokedCount
		auditAction = service.AdminAuditActionUserRevokeSessions
		successMessage = fmt.Sprintf("Revoked %d active session(s).", revokedCount)
	case adminUserActionAdjustHours:
		orgID, err := parseAdminOrgID(r.FormValue("org_id"))
		if err != nil {
			http.Redirect(w, r, redirectWithMessage(redirectBase, "error", "Invalid organization."), http.StatusSeeOther)
			return
		}
		hoursDelta, err := parseAdminHoursDelta(r.FormValue("hours_delta"))
		if err != nil {
			http.Redirect(w, r, redirectWithMessage(redirectBase, "error", "Invalid hours adjustment."), http.StatusSeeOther)
			return
		}

		if err := service.AdjustMemberHourBalance(r.Context(), s.db, service.AdjustMemberHourBalanceParams{
			OrganizationID: orgID,
			MemberID:       memberID,
			AdminMemberID:  user.ID,
			HoursDelta:     hoursDelta,
			Reason:         reason,
		}); err != nil {
			handleAdminUserActionError(w, r, redirectBase, err, "Could not adjust hour balance.")
			return
		}
		actionMetadata["org_id"] = orgID
		actionMetadata["hours_delta"] = hoursDelta
		auditAction = service.AdminAuditActionUserBalanceAdjust
		successMessage = "Hour balance adjusted."
	default:
		http.Redirect(w, r, redirectWithMessage(redirectBase, "error", "Unknown user action."), http.StatusSeeOther)
		return
	}

	if err := s.writeAdminUserAudit(r, user.ID, memberID, auditAction, reason, actionMetadata); err != nil {
		http.Redirect(w, r, redirectWithMessage(redirectBase, "error", err.Error()), http.StatusSeeOther)
		return
	}

	http.Redirect(w, r, redirectWithMessage(redirectBase, "success", successMessage), http.StatusSeeOther)
}

func (s *Server) handleAdminMessageSend(w http.ResponseWriter, r *http.Request) {
	user := s.currentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	recipientID, err := parseAdminMemberID(r.FormValue("recipient_id"))
	if err != nil {
		http.Redirect(w, r, "/admin?tab="+adminTabMessages+"&error="+url.QueryEscape("Invalid recipient."), http.StatusSeeOther)
		return
	}
	searchQuery := strings.TrimSpace(r.FormValue("q"))
	redirectBase := adminMessagesRedirect(recipientID, searchQuery)

	if recipientID == user.ID {
		http.Redirect(w, r, redirectWithMessage(redirectBase, "error", "Choose another user as recipient."), http.StatusSeeOther)
		return
	}

	if _, err := service.GetMemberByID(r.Context(), s.db, recipientID); err != nil {
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, service.ErrMissingMemberID) {
			http.Redirect(w, r, redirectWithMessage(redirectBase, "error", "Invalid recipient."), http.StatusSeeOther)
			return
		}
		log.Printf("load admin message recipient=%d: %v", recipientID, err)
		http.Redirect(w, r, redirectWithMessage(redirectBase, "error", "Could not load recipient."), http.StatusSeeOther)
		return
	}

	subjectCore := adminMessageSubjectCore(r.FormValue("subject"))
	if subjectCore == "" {
		http.Redirect(w, r, redirectWithMessage(redirectBase, "error", "Subject is required."), http.StatusSeeOther)
		return
	}
	body := strings.TrimSpace(r.FormValue("body"))
	if body == "" {
		http.Redirect(w, r, redirectWithMessage(redirectBase, "error", "Message body is required."), http.StatusSeeOther)
		return
	}
	subject := adminMessageSubject(subjectCore)

	senderID := user.ID
	if err := service.SendMessage(r.Context(), s.db, service.SendMessageParams{
		SenderID:    &senderID,
		SenderName:  memberDisplayName(user),
		RecipientID: recipientID,
		Subject:     subject,
		Body:        body,
	}); err != nil {
		log.Printf("send admin message recipient=%d: %v", recipientID, err)
		http.Redirect(w, r, redirectWithMessage(redirectBase, "error", "Could not send message."), http.StatusSeeOther)
		return
	}

	if err := s.writeAdminMessageAudit(r, user.ID, recipientID, subject); err != nil {
		http.Redirect(w, r, redirectWithMessage(redirectBase, "error", err.Error()), http.StatusSeeOther)
		return
	}

	http.Redirect(w, r, redirectWithMessage(redirectBase, "success", "Message sent."), http.StatusSeeOther)
}

func (s *Server) handleAdminAuditExport(w http.ResponseWriter, r *http.Request) {
	user := s.currentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	format := normalizeAdminAuditExportFormat(r.URL.Query().Get("format"))
	filter, params, err := parseAdminAuditFilter(r.URL.Query())
	if err != nil {
		http.Redirect(w, r, redirectWithMessage(adminAuditRedirect(filter), "error", err.Error()), http.StatusSeeOther)
		return
	}
	if format == "" {
		http.Redirect(w, r, redirectWithMessage(adminAuditRedirect(filter), "error", "Invalid export format."), http.StatusSeeOther)
		return
	}

	params.Limit = adminAuditExportLimit
	events, err := service.ListAdminAuditEvents(r.Context(), s.db, params)
	if err != nil {
		log.Printf("list admin audit events for export: %v", err)
		http.Error(w, "could not export admin audit events", http.StatusInternalServerError)
		return
	}
	if err := s.writeAdminAuditExportAudit(r, user.ID, format, filter, len(events)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	filenameTime := time.Now().UTC().Format("20060102-150405")
	switch format {
	case adminAuditExportFormatCSV:
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="admin-audit-%s.csv"`, filenameTime))

		csvWriter := csv.NewWriter(w)
		if err := csvWriter.Write([]string{
			"id",
			"created_at",
			"actor_member_id",
			"actor_email",
			"actor_name",
			"action",
			"target",
			"reason",
			"organization_id",
			"organization_name",
			"user_member_id",
			"user_email",
			"user_name",
			"metadata",
		}); err != nil {
			http.Error(w, "could not write csv export", http.StatusInternalServerError)
			return
		}
		for _, event := range events {
			record := []string{
				strconv.FormatInt(event.ID, 10),
				event.CreatedAt.UTC().Format(time.RFC3339),
				strconv.FormatInt(event.ActorMemberID, 10),
				event.ActorEmail,
				event.ActorName,
				event.Action,
				event.Target,
				adminAuditOptionalString(event.Reason),
				adminAuditOptionalInt64(event.OrganizationID),
				adminAuditOptionalString(event.OrganizationName),
				adminAuditOptionalInt64(event.UserMemberID),
				adminAuditOptionalString(event.UserEmail),
				adminAuditOptionalString(event.UserName),
				string(event.Metadata),
			}
			if err := csvWriter.Write(record); err != nil {
				http.Error(w, "could not write csv export", http.StatusInternalServerError)
				return
			}
		}
		csvWriter.Flush()
		if err := csvWriter.Error(); err != nil {
			http.Error(w, "could not write csv export", http.StatusInternalServerError)
			return
		}
	case adminAuditExportFormatJSON:
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="admin-audit-%s.json"`, filenameTime))
		encoder := json.NewEncoder(w)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(events); err != nil {
			http.Error(w, "could not write json export", http.StatusInternalServerError)
			return
		}
	default:
		http.Error(w, "invalid export format", http.StatusBadRequest)
	}
}

func (s *Server) loadAdminOrganizationTabData(r *http.Request) (types.AdminOrganizationTabData, error) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	orgID, _ := parseOptionalPositiveInt64(r.URL.Query().Get("org_id"))

	results, err := service.SearchOrganizationsForAdmin(r.Context(), s.db, q, adminOrganizationsSearchLimit)
	if err != nil {
		return types.AdminOrganizationTabData{}, err
	}

	var selected *types.AdminOrganizationDetail
	if orgID > 0 {
		detail, err := service.AdminOrganizationDetail(r.Context(), s.db, orgID, adminOrganizationHopListLimit)
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				return types.AdminOrganizationTabData{}, err
			}
		} else {
			selected = &detail
		}
	}

	return types.AdminOrganizationTabData{
		Query:         q,
		Results:       results,
		SelectedOrgID: orgID,
		Selected:      selected,
		SuccessMsg:    r.URL.Query().Get("success"),
		ErrorMsg:      r.URL.Query().Get("error"),
	}, nil
}

func (s *Server) loadAdminModerationTabData(r *http.Request) (types.AdminModerationTabData, error) {
	statusFilter := normalizeModerationStatusFilter(r.URL.Query().Get("status"))
	typeFilter := normalizeModerationTypeFilter(r.URL.Query().Get("type"))
	query := strings.TrimSpace(r.URL.Query().Get("q"))

	reports, err := service.ListModerationReports(r.Context(), s.db, types.ListModerationReportsParams{
		Status:     statusFilter,
		ReportType: typeFilter,
		Query:      query,
		Limit:      adminModerationReportListLimit,
	})
	if err != nil {
		return types.AdminModerationTabData{}, err
	}

	return types.AdminModerationTabData{
		StatusFilter: statusFilter,
		TypeFilter:   typeFilter,
		Query:        query,
		Reports:      reports,
		SuccessMsg:   r.URL.Query().Get("success"),
		ErrorMsg:     r.URL.Query().Get("error"),
	}, nil
}

func (s *Server) loadAdminUsersTabData(r *http.Request) (types.AdminUsersTabData, error) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	memberID, _ := parseOptionalPositiveInt64(r.URL.Query().Get("member_id"))

	results, err := service.SearchMembersForAdmin(r.Context(), s.db, q, adminUsersSearchLimit)
	if err != nil {
		return types.AdminUsersTabData{}, err
	}

	var selected *types.AdminUserDetail
	if memberID > 0 {
		detail, err := service.AdminUserDetail(r.Context(), s.db, memberID)
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				return types.AdminUsersTabData{}, err
			}
		} else {
			selected = &detail
		}
	}

	return types.AdminUsersTabData{
		Query:            q,
		Results:          results,
		SelectedMemberID: memberID,
		Selected:         selected,
		SuccessMsg:       r.URL.Query().Get("success"),
		ErrorMsg:         r.URL.Query().Get("error"),
	}, nil
}

func (s *Server) loadAdminMessagesTabData(r *http.Request, actorMemberID int64) (types.AdminMessagesTabData, error) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	recipientID, _ := parseOptionalPositiveInt64(r.URL.Query().Get("recipient_id"))

	results, err := service.SearchMembersForAdmin(r.Context(), s.db, q, adminMessagesSearchLimit)
	if err != nil {
		return types.AdminMessagesTabData{}, err
	}

	filteredResults := make([]types.AdminUserSearchResult, 0, len(results))
	for _, row := range results {
		if row.MemberID == actorMemberID {
			continue
		}
		filteredResults = append(filteredResults, row)
	}

	var selected *types.AdminUserSearchResult
	if recipientID > 0 && recipientID != actorMemberID {
		for i := range filteredResults {
			if filteredResults[i].MemberID == recipientID {
				selected = &filteredResults[i]
				break
			}
		}
		if selected == nil {
			member, err := service.GetMemberByID(r.Context(), s.db, recipientID)
			if err != nil {
				if !errors.Is(err, sql.ErrNoRows) {
					return types.AdminMessagesTabData{}, err
				}
			} else if member.ID != actorMemberID {
				fallback := types.AdminUserSearchResult{
					MemberID:    member.ID,
					FirstName:   member.FirstName,
					LastName:    member.LastName,
					Email:       member.Email,
					Enabled:     member.Enabled,
					LastLoginAt: member.LastLoginAt,
				}
				selected = &fallback
			}
		}
	}

	conversation := make([]types.Message, 0)
	selectedRecipientID := int64(0)
	if selected != nil {
		selectedRecipientID = selected.MemberID
		conversation, err = service.ListMessagesBetweenMembers(r.Context(), s.db, actorMemberID, selected.MemberID, adminMessagesConversationLimit)
		if err != nil {
			return types.AdminMessagesTabData{}, err
		}
	}

	return types.AdminMessagesTabData{
		Query:               q,
		Results:             filteredResults,
		SelectedRecipientID: selectedRecipientID,
		SelectedRecipient:   selected,
		Conversation:        conversation,
		SuccessMsg:          r.URL.Query().Get("success"),
		ErrorMsg:            r.URL.Query().Get("error"),
	}, nil
}

func (s *Server) loadAdminAuditTabData(r *http.Request) (types.AdminAuditTabData, error) {
	filter, params, err := parseAdminAuditFilter(r.URL.Query())
	if err != nil {
		return types.AdminAuditTabData{
			Filter:     filter,
			Events:     []types.AdminAuditEventView{},
			SuccessMsg: r.URL.Query().Get("success"),
			ErrorMsg:   err.Error(),
		}, nil
	}
	params.Limit = adminAuditEventListLimit

	events, err := service.ListAdminAuditEvents(r.Context(), s.db, params)
	if err != nil {
		return types.AdminAuditTabData{}, err
	}

	return types.AdminAuditTabData{
		Filter:     filter,
		Events:     events,
		SuccessMsg: r.URL.Query().Get("success"),
		ErrorMsg:   r.URL.Query().Get("error"),
	}, nil
}

func (s *Server) writeAdminOrganizationAudit(r *http.Request, actorID, orgID int64, action string, hopID *int64, reason string) error {
	metadata := map[string]any{
		"tab":    adminTabOrganizations,
		"org_id": orgID,
	}
	target := fmt.Sprintf("organization:%d", orgID)
	if hopID != nil {
		metadata["hop_id"] = *hopID
		target = fmt.Sprintf("hop:%d", *hopID)
	}
	rawMetadata, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("marshal admin action metadata: %w", err)
	}

	if _, err := service.WriteAdminAuditEvent(r.Context(), s.db, service.WriteAdminAuditEventParams{
		ActorMemberID: actorID,
		Action:        action,
		Target:        target,
		Reason:        reason,
		Metadata:      rawMetadata,
		Sensitive:     true,
	}); err != nil {
		if errors.Is(err, service.ErrAuditReasonRequired) {
			return fmt.Errorf("A reason is required for this action.")
		}
		return fmt.Errorf("Could not log admin action.")
	}
	return nil
}

func (s *Server) writeAdminModerationAudit(r *http.Request, actorID int64, result types.ModerationReport, action string, reason string) error {
	metadata := map[string]any{
		"tab":         adminTabModeration,
		"report_id":   result.ID,
		"org_id":      result.OrganizationID,
		"hop_id":      result.HopID,
		"report_type": result.ReportType,
	}
	target := fmt.Sprintf("moderation_report:%d", result.ID)
	if result.HopCommentID != nil {
		metadata["hop_comment_id"] = *result.HopCommentID
	}
	if result.HopImageID != nil {
		metadata["hop_image_id"] = *result.HopImageID
	}
	rawMetadata, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("marshal moderation action metadata: %w", err)
	}

	if _, err := service.WriteAdminAuditEvent(r.Context(), s.db, service.WriteAdminAuditEventParams{
		ActorMemberID: actorID,
		Action:        action,
		Target:        target,
		Reason:        reason,
		Metadata:      rawMetadata,
		Sensitive:     true,
	}); err != nil {
		if errors.Is(err, service.ErrAuditReasonRequired) {
			return fmt.Errorf("A reason is required for this action.")
		}
		return fmt.Errorf("Could not log admin action.")
	}
	return nil
}

func (s *Server) writeAdminUserAudit(r *http.Request, actorID, memberID int64, action string, reason string, actionMetadata map[string]any) error {
	metadata := map[string]any{
		"tab":       adminTabUsers,
		"member_id": memberID,
	}
	for k, v := range actionMetadata {
		metadata[k] = v
	}
	rawMetadata, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("marshal user action metadata: %w", err)
	}

	if _, err := service.WriteAdminAuditEvent(r.Context(), s.db, service.WriteAdminAuditEventParams{
		ActorMemberID: actorID,
		Action:        action,
		Target:        fmt.Sprintf("member:%d", memberID),
		Reason:        reason,
		Metadata:      rawMetadata,
		Sensitive:     true,
	}); err != nil {
		if errors.Is(err, service.ErrAuditReasonRequired) {
			return fmt.Errorf("A reason is required for this action.")
		}
		return fmt.Errorf("Could not log admin action.")
	}
	return nil
}

func (s *Server) writeAdminMessageAudit(r *http.Request, actorID, recipientID int64, subject string) error {
	rawMetadata, err := json.Marshal(map[string]any{
		"tab":                 adminTabMessages,
		"recipient_member_id": recipientID,
		"subject":             subject,
	})
	if err != nil {
		return fmt.Errorf("marshal message action metadata: %w", err)
	}

	if _, err := service.WriteAdminAuditEvent(r.Context(), s.db, service.WriteAdminAuditEventParams{
		ActorMemberID: actorID,
		Action:        service.AdminAuditActionMessageSend,
		Target:        fmt.Sprintf("member:%d", recipientID),
		Metadata:      rawMetadata,
	}); err != nil {
		return fmt.Errorf("Could not log admin action.")
	}
	return nil
}

func (s *Server) writeAdminAuditExportAudit(r *http.Request, actorID int64, format string, filter types.AdminAuditFilter, exportedEventCount int) error {
	action := ""
	switch format {
	case adminAuditExportFormatCSV:
		action = service.AdminAuditActionExportCSV
	case adminAuditExportFormatJSON:
		action = service.AdminAuditActionExportJSON
	default:
		return fmt.Errorf("Could not log admin action.")
	}

	filterMetadata := map[string]string{}
	if v := strings.TrimSpace(filter.Actor); v != "" {
		filterMetadata["actor"] = v
	}
	if v := strings.TrimSpace(filter.Action); v != "" {
		filterMetadata["action"] = v
	}
	if v := strings.TrimSpace(filter.Organization); v != "" {
		filterMetadata["organization"] = v
	}
	if v := strings.TrimSpace(filter.User); v != "" {
		filterMetadata["user"] = v
	}
	if v := strings.TrimSpace(filter.Target); v != "" {
		filterMetadata["target"] = v
	}
	if v := strings.TrimSpace(filter.StartDate); v != "" {
		filterMetadata["start_date"] = v
	}
	if v := strings.TrimSpace(filter.EndDate); v != "" {
		filterMetadata["end_date"] = v
	}

	metadata := map[string]any{
		"tab":                  adminTabAudit,
		"format":               format,
		"exported_event_count": exportedEventCount,
	}
	if len(filterMetadata) > 0 {
		metadata["filter"] = filterMetadata
	}

	rawMetadata, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("Could not log admin action.")
	}

	if _, err := service.WriteAdminAuditEvent(r.Context(), s.db, service.WriteAdminAuditEventParams{
		ActorMemberID: actorID,
		Action:        action,
		Target:        service.AdminAuditTargetApplication,
		Metadata:      rawMetadata,
	}); err != nil {
		return fmt.Errorf("Could not log admin action.")
	}
	return nil
}

func handleAdminOrganizationActionError(w http.ResponseWriter, r *http.Request, redirectBase string, err error, fallbackMsg string) {
	msg := fallbackMsg
	switch {
	case errors.Is(err, sql.ErrNoRows):
		msg = "Invalid organization."
	case errors.Is(err, service.ErrMissingOrgID):
		msg = "Invalid organization."
	case errors.Is(err, service.ErrHopNotFound):
		msg = "Invalid hop."
	case errors.Is(err, service.ErrHopInvalidState):
		msg = "Hop action not allowed for this status."
	}
	http.Redirect(w, r, redirectWithMessage(redirectBase, "error", msg), http.StatusSeeOther)
}

func handleAdminUserActionError(w http.ResponseWriter, r *http.Request, redirectBase string, err error, fallbackMsg string) {
	msg := fallbackMsg
	switch {
	case errors.Is(err, sql.ErrNoRows):
		msg = "Invalid user."
	case errors.Is(err, service.ErrMissingMemberID):
		msg = "Invalid user."
	case errors.Is(err, service.ErrMissingOrgID):
		msg = "Invalid organization."
	case errors.Is(err, service.ErrMembershipNotFound):
		msg = "User is not an active member of that organization."
	case errors.Is(err, service.ErrInvalidHoursDelta):
		msg = "Invalid hours adjustment."
	case errors.Is(err, service.ErrMemberAlreadyDeleted):
		msg = "User has already been permanently deleted."
	case errors.Is(err, service.ErrMemberDeleteBlocked):
		msg = "Cannot delete a user who is the sole active owner of an organization."
	}
	http.Redirect(w, r, redirectWithMessage(redirectBase, "error", msg), http.StatusSeeOther)
}

func normalizeAdminTab(raw string) string {
	tab := strings.TrimSpace(strings.ToLower(raw))
	switch tab {
	case "", adminTabApp:
		return adminTabApp
	case adminTabOrganizations:
		return adminTabOrganizations
	case adminTabModeration:
		return adminTabModeration
	case adminTabUsers:
		return adminTabUsers
	case adminTabMessages:
		return adminTabMessages
	case adminTabAudit:
		return adminTabAudit
	default:
		return adminTabApp
	}
}

func parseAdminOrgID(raw string) (int64, error) {
	orgID, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || orgID <= 0 {
		return 0, fmt.Errorf("invalid org id")
	}
	return orgID, nil
}

func parseAdminHopID(raw string) (int64, error) {
	hopID, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || hopID <= 0 {
		return 0, fmt.Errorf("invalid hop id")
	}
	return hopID, nil
}

func parseAdminReportID(raw string) (int64, error) {
	reportID, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || reportID <= 0 {
		return 0, fmt.Errorf("invalid report id")
	}
	return reportID, nil
}

func parseAdminMemberID(raw string) (int64, error) {
	memberID, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || memberID <= 0 {
		return 0, fmt.Errorf("invalid member id")
	}
	return memberID, nil
}

func parseAdminHoursDelta(raw string) (int, error) {
	hoursDelta, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || hoursDelta == 0 {
		return 0, fmt.Errorf("invalid hours delta")
	}
	return hoursDelta, nil
}

func parseOptionalPositiveInt64(raw string) (int64, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, nil
	}
	value, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("invalid id")
	}
	return value, nil
}

func adminOrganizationsRedirect(orgID int64, query string) string {
	base := "/admin?tab=" + adminTabOrganizations
	if orgID > 0 {
		base += "&org_id=" + strconv.FormatInt(orgID, 10)
	}
	if strings.TrimSpace(query) != "" {
		base += "&q=" + url.QueryEscape(query)
	}
	return base
}

func adminModerationRedirect(statusFilter, typeFilter, query string) string {
	base := "/admin?tab=" + adminTabModeration
	statusFilter = normalizeModerationStatusFilter(statusFilter)
	typeFilter = normalizeModerationTypeFilter(typeFilter)
	if statusFilter != types.ModerationReportStatusOpen {
		base += "&status=" + url.QueryEscape(statusFilter)
	}
	if typeFilter != adminModerationTypeAll {
		base += "&type=" + url.QueryEscape(typeFilter)
	}
	if strings.TrimSpace(query) != "" {
		base += "&q=" + url.QueryEscape(query)
	}
	return base
}

func adminUsersRedirect(memberID int64, query string) string {
	base := "/admin?tab=" + adminTabUsers
	if memberID > 0 {
		base += "&member_id=" + strconv.FormatInt(memberID, 10)
	}
	if strings.TrimSpace(query) != "" {
		base += "&q=" + url.QueryEscape(query)
	}
	return base
}

func adminMessagesRedirect(recipientID int64, query string) string {
	base := "/admin?tab=" + adminTabMessages
	if recipientID > 0 {
		base += "&recipient_id=" + strconv.FormatInt(recipientID, 10)
	}
	if strings.TrimSpace(query) != "" {
		base += "&q=" + url.QueryEscape(query)
	}
	return base
}

func adminAuditRedirect(filter types.AdminAuditFilter) string {
	values := url.Values{}
	values.Set("tab", adminTabAudit)
	if strings.TrimSpace(filter.Actor) != "" {
		values.Set("actor", strings.TrimSpace(filter.Actor))
	}
	if strings.TrimSpace(filter.StartDate) != "" {
		values.Set("start_date", strings.TrimSpace(filter.StartDate))
	}
	if strings.TrimSpace(filter.EndDate) != "" {
		values.Set("end_date", strings.TrimSpace(filter.EndDate))
	}
	if strings.TrimSpace(filter.Action) != "" {
		values.Set("action", strings.TrimSpace(filter.Action))
	}
	if strings.TrimSpace(filter.Organization) != "" {
		values.Set("organization", strings.TrimSpace(filter.Organization))
	}
	if strings.TrimSpace(filter.User) != "" {
		values.Set("user", strings.TrimSpace(filter.User))
	}
	if strings.TrimSpace(filter.Target) != "" {
		values.Set("target", strings.TrimSpace(filter.Target))
	}
	return "/admin?" + values.Encode()
}

func parseAdminAuditFilter(values url.Values) (types.AdminAuditFilter, service.ListAdminAuditEventsParams, error) {
	filter := types.AdminAuditFilter{
		Actor:        strings.TrimSpace(values.Get("actor")),
		StartDate:    strings.TrimSpace(values.Get("start_date")),
		EndDate:      strings.TrimSpace(values.Get("end_date")),
		Action:       strings.TrimSpace(values.Get("action")),
		Organization: strings.TrimSpace(values.Get("organization")),
		User:         strings.TrimSpace(values.Get("user")),
		Target:       strings.TrimSpace(values.Get("target")),
	}

	startAt, endBefore, err := parseAdminAuditDateRange(filter.StartDate, filter.EndDate)
	if err != nil {
		return filter, service.ListAdminAuditEventsParams{}, err
	}

	return filter, service.ListAdminAuditEventsParams{
		ActorQuery:        filter.Actor,
		ActionQuery:       filter.Action,
		OrganizationQuery: filter.Organization,
		UserQuery:         filter.User,
		TargetQuery:       filter.Target,
		StartAt:           startAt,
		EndBefore:         endBefore,
	}, nil
}

func parseAdminAuditDateRange(startDate, endDate string) (*time.Time, *time.Time, error) {
	startAt, err := parseAdminAuditDate(startDate)
	if err != nil {
		return nil, nil, fmt.Errorf("Invalid start date. Use YYYY-MM-DD.")
	}
	endAt, err := parseAdminAuditDate(endDate)
	if err != nil {
		return nil, nil, fmt.Errorf("Invalid end date. Use YYYY-MM-DD.")
	}

	if startAt != nil && endAt != nil && startAt.After(*endAt) {
		return nil, nil, fmt.Errorf("Start date must be on or before end date.")
	}
	if endAt == nil {
		return startAt, nil, nil
	}
	endBefore := endAt.Add(24 * time.Hour)
	return startAt, &endBefore, nil
}

func parseAdminAuditDate(raw string) (*time.Time, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}
	parsed, err := time.Parse("2006-01-02", trimmed)
	if err != nil {
		return nil, err
	}
	parsed = parsed.UTC()
	return &parsed, nil
}

func normalizeAdminAuditExportFormat(raw string) string {
	format := strings.TrimSpace(strings.ToLower(raw))
	switch format {
	case adminAuditExportFormatCSV:
		return adminAuditExportFormatCSV
	case adminAuditExportFormatJSON:
		return adminAuditExportFormatJSON
	default:
		return ""
	}
}

func adminAuditOptionalString(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func adminAuditOptionalInt64(v *int64) string {
	if v == nil {
		return ""
	}
	return strconv.FormatInt(*v, 10)
}

func adminMessageSubjectCore(raw string) string {
	subject := strings.TrimSpace(raw)
	if len(subject) >= len(adminMessageSubjectPrefix) && strings.EqualFold(subject[:len(adminMessageSubjectPrefix)], adminMessageSubjectPrefix) {
		subject = strings.TrimSpace(subject[len(adminMessageSubjectPrefix):])
	}
	return subject
}

func adminMessageSubject(core string) string {
	core = strings.TrimSpace(core)
	if core == "" {
		return adminMessageSubjectPrefix
	}
	return adminMessageSubjectPrefix + " " + core
}

func normalizeModerationStatusFilter(raw string) string {
	status := strings.TrimSpace(strings.ToLower(raw))
	switch status {
	case "", types.ModerationReportStatusOpen:
		return types.ModerationReportStatusOpen
	case types.ModerationReportStatusDismissed:
		return types.ModerationReportStatusDismissed
	case types.ModerationReportStatusActioned:
		return types.ModerationReportStatusActioned
	case adminModerationStatusAll:
		return adminModerationStatusAll
	default:
		return types.ModerationReportStatusOpen
	}
}

func normalizeModerationTypeFilter(raw string) string {
	reportType := strings.TrimSpace(strings.ToLower(raw))
	switch reportType {
	case "", adminModerationTypeAll:
		return adminModerationTypeAll
	case types.ModerationReportTypeHopComment:
		return types.ModerationReportTypeHopComment
	case types.ModerationReportTypeHopImage:
		return types.ModerationReportTypeHopImage
	default:
		return adminModerationTypeAll
	}
}
