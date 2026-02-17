package http

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"hopshare/internal/service"
	"hopshare/internal/types"
	"hopshare/web/templates"
)

const (
	adminTabApp                   = "app"
	adminTabOrganizations         = "organizations"
	adminOverviewLeaderboardCap   = 5
	adminOrganizationsSearchLimit = 25
	adminOrganizationHopListLimit = 100

	adminOrgActionDisable = "disable_org"
	adminOrgActionEnable  = "enable_org"
	adminOrgActionExpire  = "expire_hop"
	adminOrgActionDelete  = "delete_hop"
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
	var err error

	switch activeTab {
	case adminTabOrganizations:
		orgTab, err = s.loadAdminOrganizationTabData(r)
		if err != nil {
			log.Printf("load admin organization tab data: %v", err)
			http.Error(w, "could not load admin organizations", http.StatusInternalServerError)
			return
		}
	default:
		overview, err = service.AdminAppOverview(r.Context(), s.db, adminOverviewLeaderboardCap)
		if err != nil {
			log.Printf("load admin app overview: %v", err)
			http.Error(w, "could not load admin overview", http.StatusInternalServerError)
			return
		}

		auditMetadata, err := json.Marshal(map[string]string{
			"tab": activeTab,
		})
		if err != nil {
			log.Printf("marshal admin audit metadata: %v", err)
			http.Error(w, "could not log admin action", http.StatusInternalServerError)
			return
		}
		if _, err := service.WriteAdminAuditEvent(r.Context(), s.db, service.WriteAdminAuditEventParams{
			ActorMemberID: user.ID,
			Action:        service.AdminAuditActionAppOverviewViewed,
			Target:        service.AdminAuditTargetApplication,
			Metadata:      auditMetadata,
		}); err != nil {
			log.Printf("write admin audit event actor=%d action=%q: %v", user.ID, service.AdminAuditActionAppOverviewViewed, err)
			http.Error(w, "could not log admin action", http.StatusInternalServerError)
			return
		}
	}

	render(w, r, templates.Admin(s.currentUserEmailPtr(r), activeTab, overview, orgTab))
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

func normalizeAdminTab(raw string) string {
	tab := strings.TrimSpace(strings.ToLower(raw))
	switch tab {
	case "", adminTabApp:
		return adminTabApp
	case adminTabOrganizations:
		return adminTabOrganizations
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
