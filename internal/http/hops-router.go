package http

import (
	"database/sql"
	"errors"
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

func (s *Server) handleMyHops(w http.ResponseWriter, r *http.Request) {
	user := s.currentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	successMsg := r.URL.Query().Get("success")
	errorMsg := r.URL.Query().Get("error")

	hasPrimary := false
	if _, err := service.PrimaryOwnedOrganization(r.Context(), s.db, user.ID); err == nil {
		hasPrimary = true
	} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
		log.Printf("load primary organization for member %d: %v", user.ID, err)
	}

	orgs, err := service.ActiveOrganizationsForMember(r.Context(), s.db, user.ID)
	if err != nil {
		log.Printf("load organizations for member %d: %v", user.ID, err)
		http.Error(w, "could not load organizations", http.StatusInternalServerError)
		return
	}

	var currentOrgID int64
	var selectedFromQuery bool
	if orgIDStr := strings.TrimSpace(r.URL.Query().Get("org_id")); orgIDStr != "" {
		if parsed, err := strconv.ParseInt(orgIDStr, 10, 64); err == nil && parsed > 0 {
			currentOrgID = parsed
			selectedFromQuery = true
		}
	}
	if currentOrgID == 0 && user.CurrentOrganization != nil {
		currentOrgID = *user.CurrentOrganization
	}
	if len(orgs) > 0 && currentOrgID == 0 {
		currentOrgID = orgs[0].ID
	}
	if currentOrgID != 0 && !orgIDInList(orgs, currentOrgID) && len(orgs) > 0 {
		currentOrgID = orgs[0].ID
	}

	if currentOrgID != 0 && (selectedFromQuery || user.CurrentOrganization == nil || *user.CurrentOrganization != currentOrgID) {
		if err := service.UpdateMemberCurrentOrganization(r.Context(), s.db, user.ID, currentOrgID); err != nil {
			log.Printf("update current organization member=%d org=%d: %v", user.ID, currentOrgID, err)
		}
	}

	var myHops []types.Hop
	if currentOrgID != 0 {
		if _, err := service.ExpireHops(r.Context(), s.db, currentOrgID, time.Now().UTC()); err != nil {
			log.Printf("expire hops org=%d: %v", currentOrgID, err)
		}

		myHops, err = service.ListMemberHops(r.Context(), s.db, currentOrgID, user.ID)
		if err != nil {
			log.Printf("load my hops org=%d member=%d: %v", currentOrgID, user.ID, err)
			http.Error(w, "could not load hops", http.StatusInternalServerError)
			return
		}
		pendingOffers, err := service.PendingHopOfferIDs(r.Context(), s.db, user.ID)
		if err != nil {
			log.Printf("load pending hop offers member=%d: %v", user.ID, err)
		} else if len(pendingOffers) > 0 {
			for i := range myHops {
				if _, ok := pendingOffers[myHops[i].ID]; ok {
					myHops[i].HasPendingOffer = true
				}
			}
		}
	}

	render(w, r, templates.MyHops(
		user.Email,
		orgs,
		currentOrgID,
		myHops,
		hasPrimary,
		successMsg,
		errorMsg,
	))
}

func (s *Server) handleCreateHop(w http.ResponseWriter, r *http.Request) {
	user := s.currentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	orgID, err := strconv.ParseInt(r.FormValue("org_id"), 10, 64)
	if err != nil || orgID <= 0 {
		http.Redirect(w, r, "/my-hopshare?error="+url.QueryEscape("Invalid organization."), http.StatusSeeOther)
		return
	}

	estimatedHours, err := strconv.Atoi(strings.TrimSpace(r.FormValue("estimated_hours")))
	if err != nil {
		http.Redirect(w, r, "/my-hopshare?org_id="+strconv.FormatInt(orgID, 10)+"&error="+url.QueryEscape("Invalid hours."), http.StatusSeeOther)
		return
	}

	var neededByDate *time.Time
	dateStr := strings.TrimSpace(r.FormValue("needed_by_date"))
	if dateStr != "" {
		t, err := time.Parse("2006-01-02", dateStr)
		if err != nil {
			http.Redirect(w, r, "/my-hopshare?org_id="+strconv.FormatInt(orgID, 10)+"&error="+url.QueryEscape("Invalid date."), http.StatusSeeOther)
			return
		}
		neededByDate = &t
	}

	_, err = service.CreateHop(r.Context(), s.db, service.CreateHopParams{
		OrganizationID: orgID,
		MemberID:       user.ID,
		Title:          r.FormValue("title"),
		Details:        r.FormValue("details"),
		EstimatedHours: estimatedHours,
		NeededByKind:   r.FormValue("needed_by_kind"),
		NeededByDate:   neededByDate,
	})
	if err != nil {
		log.Printf("create hop failed: %v", err)
		http.Redirect(w, r, "/my-hopshare?org_id="+strconv.FormatInt(orgID, 10)+"&error="+url.QueryEscape("Could not create hop."), http.StatusSeeOther)
		return
	}

	http.Redirect(w, r, "/my-hopshare?org_id="+strconv.FormatInt(orgID, 10)+"&success="+url.QueryEscape("Hop created."), http.StatusSeeOther)
}

func (s *Server) handleOfferHop(w http.ResponseWriter, r *http.Request) {
	user := s.currentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	orgID, _ := strconv.ParseInt(r.FormValue("org_id"), 10, 64)
	hopID, _ := strconv.ParseInt(r.FormValue("hop_id"), 10, 64)
	if orgID <= 0 || hopID <= 0 {
		http.Redirect(w, r, "/my-hopshare?error="+url.QueryEscape("Invalid hop."), http.StatusSeeOther)
		return
	}

	offererName := memberDisplayName(user)

	if err := service.OfferHopHelp(r.Context(), s.db, service.OfferHopParams{
		OrganizationID: orgID,
		HopID:          hopID,
		OffererID:      user.ID,
		OffererName:    offererName,
	}); err != nil {
		log.Printf("offer hop help failed: %v", err)
		http.Redirect(w, r, "/my-hopshare?org_id="+strconv.FormatInt(orgID, 10)+"&error="+url.QueryEscape("Could not send offer."), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/my-hopshare?org_id="+strconv.FormatInt(orgID, 10)+"&success="+url.QueryEscape("Offer sent."), http.StatusSeeOther)
}

func (s *Server) handleCancelHop(w http.ResponseWriter, r *http.Request) {
	user := s.currentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	orgID, _ := strconv.ParseInt(r.FormValue("org_id"), 10, 64)
	hopID, _ := strconv.ParseInt(r.FormValue("hop_id"), 10, 64)
	defaultRedirect := "/my-hopshare"
	if orgID > 0 {
		defaultRedirect = "/my-hopshare?org_id=" + strconv.FormatInt(orgID, 10)
	}
	redirectBase := safeRedirectPath(r.FormValue("redirect_to"), defaultRedirect)
	if orgID <= 0 || hopID <= 0 {
		http.Redirect(w, r, redirectWithMessage(redirectBase, "error", "Invalid hop."), http.StatusSeeOther)
		return
	}

	if err := service.CancelHop(r.Context(), s.db, orgID, hopID, user.ID); err != nil {
		log.Printf("cancel hop failed: %v", err)
		http.Redirect(w, r, redirectWithMessage(redirectBase, "error", "Could not cancel hop."), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, redirectWithMessage(redirectBase, "success", "Hop canceled."), http.StatusSeeOther)
}

func (s *Server) handleCompleteHop(w http.ResponseWriter, r *http.Request) {
	user := s.currentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	orgID, _ := strconv.ParseInt(r.FormValue("org_id"), 10, 64)
	hopID, _ := strconv.ParseInt(r.FormValue("hop_id"), 10, 64)
	defaultRedirect := "/my-hopshare"
	if orgID > 0 {
		defaultRedirect = "/my-hopshare?org_id=" + strconv.FormatInt(orgID, 10)
	}
	redirectBase := safeRedirectPath(r.FormValue("redirect_to"), defaultRedirect)
	if orgID <= 0 || hopID <= 0 {
		http.Redirect(w, r, redirectWithMessage(redirectBase, "error", "Invalid hop."), http.StatusSeeOther)
		return
	}

	completedHours, _ := strconv.Atoi(strings.TrimSpace(r.FormValue("completed_hours")))
	if err := service.CompleteHop(r.Context(), s.db, service.CompleteHopParams{
		OrganizationID: orgID,
		HopID:          hopID,
		CompletedBy:    user.ID,
		Comment:        r.FormValue("completion_comment"),
		CompletedHours: completedHours,
	}); err != nil {
		log.Printf("complete hop failed: %v", err)
		http.Redirect(w, r, redirectWithMessage(redirectBase, "error", "Could not complete hop."), http.StatusSeeOther)
		return
	}

	http.Redirect(w, r, redirectWithMessage(redirectBase, "success", "Hop completed."), http.StatusSeeOther)
}

func safeRedirectPath(input string, fallback string) string {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return fallback
	}
	if !strings.HasPrefix(trimmed, "/") || strings.HasPrefix(trimmed, "//") {
		return fallback
	}
	return trimmed
}

func redirectWithMessage(base string, key string, msg string) string {
	sep := "?"
	if strings.Contains(base, "?") {
		sep = "&"
	}
	return base + sep + key + "=" + url.QueryEscape(msg)
}
