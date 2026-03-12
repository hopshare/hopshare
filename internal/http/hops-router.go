package http

import (
	"errors"
	"fmt"
	"io"
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

	hasCreatedOrganization, err := service.MemberCreatedOrganization(r.Context(), s.db, user.ID)
	if err != nil {
		log.Printf("check member created organization for member %d: %v", user.ID, err)
		http.Error(w, "could not load organizations", http.StatusInternalServerError)
		return
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

	view := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("view")))
	viewTitle := "Hops I Have Requested"
	viewDescription := "All of the hops you've requested in this organization."
	emptyMessage := "You haven't requested any hops yet."
	switch view {
	case "helped", "help":
		view = "helped"
		viewTitle = "Hops I Have Helped With"
		viewDescription = "Hops you've been accepted to help with, including those still pending."
		emptyMessage = "You haven't helped with any hops yet."
	case "offered", "offers", "offering":
		view = "offered"
		viewTitle = "Pending Hops I Have Offered Help With"
		viewDescription = "Offers you've made that are still awaiting a response."
		emptyMessage = "You don't have any pending hop offers."
	default:
		view = "requested"
	}

	var myHops []types.Hop
	if currentOrgID != 0 {
		switch view {
		case "helped":
			myHops, err = service.ListHelpedHops(r.Context(), s.db, currentOrgID, user.ID)
		case "offered":
			myHops, err = service.ListPendingOfferedHops(r.Context(), s.db, currentOrgID, user.ID)
		default:
			myHops, err = service.ListRequestedHops(r.Context(), s.db, currentOrgID, user.ID)
		}
		if err != nil {
			log.Printf("load my hops org=%d member=%d view=%s: %v", currentOrgID, user.ID, view, err)
			http.Error(w, "could not load hops", http.StatusInternalServerError)
			return
		}

		if view == "offered" {
			for i := range myHops {
				myHops[i].HasPendingOffer = true
			}
		}
	}

	render(w, r, templates.MyHops(
		user.Email,
		orgs,
		currentOrgID,
		myHops,
		hasCreatedOrganization,
		view,
		viewTitle,
		viewDescription,
		emptyMessage,
		user.ID,
		successMsg,
		errorMsg,
	))
}

func (s *Server) handleHopDetails(w http.ResponseWriter, r *http.Request) {
	user := s.currentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	hopIDStr := strings.TrimSpace(r.URL.Query().Get("hop_id"))
	if hopIDStr == "" {
		http.Error(w, "missing hop id", http.StatusBadRequest)
		return
	}
	hopID, err := strconv.ParseInt(hopIDStr, 10, 64)
	if err != nil || hopID <= 0 {
		http.Error(w, "invalid hop id", http.StatusBadRequest)
		return
	}

	var orgID int64
	if orgIDStr := strings.TrimSpace(r.URL.Query().Get("org_id")); orgIDStr != "" {
		orgID, err = strconv.ParseInt(orgIDStr, 10, 64)
		if err != nil || orgID <= 0 {
			http.Error(w, "invalid organization", http.StatusBadRequest)
			return
		}
	} else {
		orgID, err = service.HopOrganizationID(r.Context(), s.db, hopID)
		if err != nil {
			if errors.Is(err, service.ErrHopNotFound) {
				http.NotFound(w, r)
				return
			}
			log.Printf("load hop organization %d: %v", hopID, err)
			http.Error(w, "could not load hop", http.StatusInternalServerError)
			return
		}
	}

	memberOK, err := service.MemberHasActiveMembership(r.Context(), s.db, user.ID, orgID)
	if err != nil {
		log.Printf("check hop membership member=%d org=%d: %v", user.ID, orgID, err)
		http.Error(w, "could not load hop", http.StatusInternalServerError)
		return
	}
	if !memberOK {
		s.renderUnauthorized(w, r)
		return
	}

	hop, err := service.GetHopByID(r.Context(), s.db, orgID, hopID)
	if err != nil {
		if errors.Is(err, service.ErrHopNotFound) {
			http.NotFound(w, r)
			return
		}
		log.Printf("load hop %d: %v", hopID, err)
		http.Error(w, "could not load hop", http.StatusInternalServerError)
		return
	}

	isAssociated := hop.CreatedBy == user.ID
	if !isAssociated && hop.AcceptedBy != nil && *hop.AcceptedBy == user.ID {
		isAssociated = true
	}

	org, err := service.GetOrganizationByID(r.Context(), s.db, orgID)
	if err != nil {
		log.Printf("load organization %d: %v", orgID, err)
		http.Error(w, "could not load organization", http.StatusInternalServerError)
		return
	}

	comments, err := service.ListHopComments(r.Context(), s.db, hopID)
	if err != nil {
		log.Printf("load hop comments %d: %v", hopID, err)
		http.Error(w, "could not load hop", http.StatusInternalServerError)
		return
	}

	var images []types.HopImage
	if s.featureHopPictures {
		images, err = service.ListHopImages(r.Context(), s.db, hopID)
		if err != nil {
			log.Printf("load hop images %d: %v", hopID, err)
			http.Error(w, "could not load hop", http.StatusInternalServerError)
			return
		}
	}

	from := normalizeHopOrigin(r.URL.Query().Get("from"))
	backView := strings.TrimSpace(r.URL.Query().Get("view"))
	successMsg := strings.TrimSpace(r.URL.Query().Get("success"))
	errorMsg := strings.TrimSpace(r.URL.Query().Get("error"))
	canToggle := isAssociated
	canComment := isAssociated || !hop.IsPrivate
	canUpload := isAssociated
	canCancel := hop.CreatedBy == user.ID && (hop.Status == types.HopStatusOpen || hop.Status == types.HopStatusAccepted)
	canComplete := hop.Status == types.HopStatusAccepted && isAssociated
	canSetCompletionHours := hop.CreatedBy == user.ID
	canOfferHelp := hop.Status == types.HopStatusOpen && hop.CreatedBy != user.ID
	canManageOffers := hop.Status == types.HopStatusOpen && hop.CreatedBy == user.ID
	hasOfferedToHelp := false
	if canOfferHelp {
		hasPendingOffer, err := service.HasPendingHopOffer(r.Context(), s.db, hop.ID, user.ID)
		if err != nil {
			log.Printf("check pending hop offer hop=%d member=%d: %v", hop.ID, user.ID, err)
			http.Error(w, "could not load hop", http.StatusInternalServerError)
			return
		}
		hasOfferedToHelp = hasPendingOffer
		canOfferHelp = !hasPendingOffer
	}
	var pendingOffers []types.PendingHopOffer
	if canManageOffers {
		pendingOffers, err = service.ListPendingHopOffers(r.Context(), s.db, hop.ID, user.ID)
		if err != nil {
			log.Printf("list pending hop offers hop=%d requester=%d: %v", hop.ID, user.ID, err)
			http.Error(w, "could not load hop", http.StatusInternalServerError)
			return
		}
	}
	render(w, r, templates.HopDetails(s.currentUserEmailPtr(r), org, hop, from, backView, successMsg, errorMsg, canToggle, canComment, canUpload, canOfferHelp, hasOfferedToHelp, canManageOffers, pendingOffers, canCancel, canComplete, canSetCompletionHours, s.featureHopPictures, comments, images))
}

func (s *Server) handleRequestHopPage(w http.ResponseWriter, r *http.Request) {
	user := s.currentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
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

	if currentOrgID == 0 {
		http.Redirect(w, r, "/my-hopshare?error="+url.QueryEscape("Join an organization before requesting a hop."), http.StatusSeeOther)
		return
	}

	errorMsg := strings.TrimSpace(r.URL.Query().Get("error"))
	render(w, r, templates.RequestHopPage(user.Email, orgs, currentOrgID, errorMsg))
}

func (s *Server) handleCompleteHopPage(w http.ResponseWriter, r *http.Request) {
	user := s.currentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	hopIDStr := strings.TrimSpace(r.URL.Query().Get("hop_id"))
	if hopIDStr == "" {
		http.Error(w, "missing hop id", http.StatusBadRequest)
		return
	}
	hopID, err := strconv.ParseInt(hopIDStr, 10, 64)
	if err != nil || hopID <= 0 {
		http.Error(w, "invalid hop id", http.StatusBadRequest)
		return
	}

	var orgID int64
	if orgIDStr := strings.TrimSpace(r.URL.Query().Get("org_id")); orgIDStr != "" {
		orgID, err = strconv.ParseInt(orgIDStr, 10, 64)
		if err != nil || orgID <= 0 {
			http.Error(w, "invalid organization", http.StatusBadRequest)
			return
		}
	} else {
		orgID, err = service.HopOrganizationID(r.Context(), s.db, hopID)
		if err != nil {
			if errors.Is(err, service.ErrHopNotFound) {
				http.NotFound(w, r)
				return
			}
			log.Printf("load hop organization %d: %v", hopID, err)
			http.Error(w, "could not load hop", http.StatusInternalServerError)
			return
		}
	}

	defaultRedirect := "/my-hopshare"
	if orgID > 0 {
		defaultRedirect = "/my-hopshare?org_id=" + strconv.FormatInt(orgID, 10)
	}
	redirectTo := safeRedirectPath(r.URL.Query().Get("redirect_to"), defaultRedirect)

	memberOK, err := service.MemberHasActiveMembership(r.Context(), s.db, user.ID, orgID)
	if err != nil {
		log.Printf("check hop completion page membership member=%d org=%d: %v", user.ID, orgID, err)
		http.Error(w, "could not load hop", http.StatusInternalServerError)
		return
	}
	if !memberOK {
		s.renderUnauthorized(w, r)
		return
	}

	hop, err := service.GetHopByID(r.Context(), s.db, orgID, hopID)
	if err != nil {
		if errors.Is(err, service.ErrHopNotFound) {
			http.NotFound(w, r)
			return
		}
		log.Printf("load hop %d for completion page: %v", hopID, err)
		http.Error(w, "could not load hop", http.StatusInternalServerError)
		return
	}

	isAssociated := hop.CreatedBy == user.ID
	if !isAssociated && hop.AcceptedBy != nil && *hop.AcceptedBy == user.ID {
		isAssociated = true
	}
	if !isAssociated {
		s.renderUnauthorized(w, r)
		return
	}
	if hop.Status != types.HopStatusAccepted {
		http.Redirect(w, r, redirectWithMessage(redirectTo, "error", "This hop can no longer be completed."), http.StatusSeeOther)
		return
	}

	org, err := service.GetOrganizationByID(r.Context(), s.db, orgID)
	if err != nil {
		log.Printf("load organization %d for completion page: %v", orgID, err)
		http.Error(w, "could not load organization", http.StatusInternalServerError)
		return
	}

	canSetCompletionHours := hop.CreatedBy == user.ID
	errorMsg := strings.TrimSpace(r.URL.Query().Get("error"))
	render(w, r, templates.CompleteHopPage(user.Email, org, hop, canSetCompletionHours, redirectTo, errorMsg))
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
	neededByKind := strings.TrimSpace(r.FormValue("needed_by_kind"))

	estimatedHours, err := strconv.Atoi(strings.TrimSpace(r.FormValue("estimated_hours")))
	if err != nil {
		http.Redirect(w, r, "/my-hopshare?org_id="+strconv.FormatInt(orgID, 10)+"&error="+url.QueryEscape("Invalid hours."), http.StatusSeeOther)
		return
	}

	var neededByDate *time.Time
	if neededByKind != types.HopNeededByAnytime {
		dateStr := strings.TrimSpace(r.FormValue("needed_by_date"))
		if dateStr != "" {
			t, err := time.Parse("2006-01-02", dateStr)
			if err != nil {
				http.Redirect(w, r, "/my-hopshare?org_id="+strconv.FormatInt(orgID, 10)+"&error="+url.QueryEscape("Invalid date."), http.StatusSeeOther)
				return
			}
			neededByDate = &t
		}
	}

	isPrivate := false

	_, err = service.CreateHop(r.Context(), s.db, service.CreateHopParams{
		OrganizationID: orgID,
		MemberID:       user.ID,
		Title:          r.FormValue("title"),
		Details:        r.FormValue("details"),
		EstimatedHours: estimatedHours,
		NeededByKind:   neededByKind,
		NeededByDate:   neededByDate,
		IsPrivate:      isPrivate,
	})
	if err != nil {
		log.Printf("create hop failed: %v", err)
		if errors.Is(err, service.ErrHopRequestLimit) {
			minBalance := service.DefaultTimebankMinBalance
			if org, orgErr := service.GetOrganizationByID(r.Context(), s.db, orgID); orgErr == nil {
				minBalance = org.TimebankMinBalance
			}
			msg := fmt.Sprintf("You're at this organization's minimum balance (%d). Complete a hop first to earn hours before requesting another.", minBalance)
			http.Redirect(w, r, "/my-hopshare?org_id="+strconv.FormatInt(orgID, 10)+"&error="+url.QueryEscape(msg), http.StatusSeeOther)
			return
		}
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
		if errors.Is(err, service.ErrHopOfferExists) {
			http.Redirect(w, r, "/my-hopshare?org_id="+strconv.FormatInt(orgID, 10)+"&error="+url.QueryEscape("You've already offered to help with this hop."), http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, "/my-hopshare?org_id="+strconv.FormatInt(orgID, 10)+"&error="+url.QueryEscape("Could not send offer."), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/my-hopshare?org_id="+strconv.FormatInt(orgID, 10)+"&success="+url.QueryEscape("Offer sent."), http.StatusSeeOther)
}

func (s *Server) handleAcceptHopOffer(w http.ResponseWriter, r *http.Request) {
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
	offerMemberID, _ := strconv.ParseInt(r.FormValue("offer_member_id"), 10, 64)
	defaultRedirect := "/my-hopshare"
	if orgID > 0 {
		defaultRedirect = "/my-hopshare?org_id=" + strconv.FormatInt(orgID, 10)
	}
	redirectBase := safeRedirectPath(r.FormValue("redirect_to"), defaultRedirect)
	if orgID <= 0 || hopID <= 0 || offerMemberID <= 0 {
		http.Redirect(w, r, redirectWithMessage(redirectBase, "error", "Invalid offer."), http.StatusSeeOther)
		return
	}

	if err := service.AcceptPendingHopOffer(r.Context(), s.db, hopID, user.ID, offerMemberID, memberDisplayName(user), r.FormValue("body")); err != nil {
		log.Printf("accept hop offer failed hop=%d requester=%d offerer=%d: %v", hopID, user.ID, offerMemberID, err)
		http.Redirect(w, r, redirectWithMessage(redirectBase, "error", "Could not update offer."), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, redirectWithMessage(redirectBase, "success", "Offer accepted."), http.StatusSeeOther)
}

func (s *Server) handleDeclineHopOffer(w http.ResponseWriter, r *http.Request) {
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
	offerMemberID, _ := strconv.ParseInt(r.FormValue("offer_member_id"), 10, 64)
	defaultRedirect := "/my-hopshare"
	if orgID > 0 {
		defaultRedirect = "/my-hopshare?org_id=" + strconv.FormatInt(orgID, 10)
	}
	redirectBase := safeRedirectPath(r.FormValue("redirect_to"), defaultRedirect)
	if orgID <= 0 || hopID <= 0 || offerMemberID <= 0 {
		http.Redirect(w, r, redirectWithMessage(redirectBase, "error", "Invalid offer."), http.StatusSeeOther)
		return
	}

	if err := service.DeclinePendingHopOffer(r.Context(), s.db, hopID, user.ID, offerMemberID, memberDisplayName(user), r.FormValue("body")); err != nil {
		log.Printf("decline hop offer failed hop=%d requester=%d offerer=%d: %v", hopID, user.ID, offerMemberID, err)
		http.Redirect(w, r, redirectWithMessage(redirectBase, "error", "Could not update offer."), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, redirectWithMessage(redirectBase, "success", "Offer declined."), http.StatusSeeOther)
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
	result, err := service.CompleteHopWithResult(r.Context(), s.db, service.CompleteHopParams{
		OrganizationID: orgID,
		HopID:          hopID,
		CompletedBy:    user.ID,
		Comment:        r.FormValue("completion_comment"),
		CompletedHours: completedHours,
	})
	if err != nil {
		log.Printf("complete hop failed: %v", err)
		http.Redirect(w, r, redirectWithMessage(redirectBase, "error", "Could not complete hop."), http.StatusSeeOther)
		return
	}

	http.Redirect(w, r, redirectWithMessage(redirectBase, "success", completeHopSuccessMessage(result)), http.StatusSeeOther)
}

func (s *Server) handleHopPrivacy(w http.ResponseWriter, r *http.Request) {
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
		http.Error(w, "invalid hop", http.StatusBadRequest)
		return
	}

	privacyValue := strings.ToLower(strings.TrimSpace(r.FormValue("is_private")))
	isPrivate := false
	switch privacyValue {
	case "":
		isPrivate = false
	case "true", "on":
		isPrivate = true
	case "false":
		isPrivate = false
	default:
		http.Error(w, "invalid privacy selection", http.StatusBadRequest)
		return
	}
	if err := service.SetHopPrivacy(r.Context(), s.db, orgID, hopID, user.ID, isPrivate); err != nil {
		if errors.Is(err, service.ErrHopNotFound) {
			http.NotFound(w, r)
			return
		}
		if errors.Is(err, service.ErrHopForbidden) {
			s.renderUnauthorized(w, r)
			return
		}
		log.Printf("update hop privacy failed: %v", err)
		http.Error(w, "could not update hop", http.StatusInternalServerError)
		return
	}

	if strings.EqualFold(r.Header.Get("HX-Request"), "true") {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	http.Redirect(w, r, hopDetailsRedirect(orgID, hopID, r.FormValue("from"), r.FormValue("view")), http.StatusSeeOther)
}

func (s *Server) handleCreateHopComment(w http.ResponseWriter, r *http.Request) {
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
		http.Error(w, "invalid hop", http.StatusBadRequest)
		return
	}

	memberOK, err := service.MemberHasActiveMembership(r.Context(), s.db, user.ID, orgID)
	if err != nil {
		log.Printf("check hop membership member=%d org=%d: %v", user.ID, orgID, err)
		http.Error(w, "could not load hop", http.StatusInternalServerError)
		return
	}
	if !memberOK {
		s.renderUnauthorized(w, r)
		return
	}

	hop, err := service.GetHopByID(r.Context(), s.db, orgID, hopID)
	if err != nil {
		if errors.Is(err, service.ErrHopNotFound) {
			http.NotFound(w, r)
			return
		}
		log.Printf("load hop %d: %v", hopID, err)
		http.Error(w, "could not load hop", http.StatusInternalServerError)
		return
	}

	isAssociated := hop.CreatedBy == user.ID
	if !isAssociated && hop.AcceptedBy != nil && *hop.AcceptedBy == user.ID {
		isAssociated = true
	}
	if hop.IsPrivate && !isAssociated {
		s.renderUnauthorized(w, r)
		return
	}

	if err := service.AddHopComment(r.Context(), s.db, hopID, user.ID, r.FormValue("body")); err != nil {
		log.Printf("add hop comment failed: %v", err)
		http.Error(w, "could not add comment", http.StatusInternalServerError)
		return
	}

	notifyMemberIDs := map[int64]struct{}{}
	if hop.CreatedBy != user.ID {
		notifyMemberIDs[hop.CreatedBy] = struct{}{}
	}
	if hop.AcceptedBy != nil && *hop.AcceptedBy != user.ID {
		notifyMemberIDs[*hop.AcceptedBy] = struct{}{}
	}
	commenterName := memberDisplayName(user)
	notificationText := fmt.Sprintf("%s commented on your hop: %s.", commenterName, hop.Title)
	notificationHref := fmt.Sprintf("/hops/view?org_id=%d&hop_id=%d", orgID, hopID)
	for recipientID := range notifyMemberIDs {
		if err := service.CreateMemberNotification(r.Context(), s.db, recipientID, notificationText, notificationHref); err != nil {
			log.Printf("create hop comment notification failed recipient=%d hop=%d: %v", recipientID, hopID, err)
		}
	}

	http.Redirect(w, r, hopDetailsRedirect(orgID, hopID, r.FormValue("from"), r.FormValue("view")), http.StatusSeeOther)
}

func (s *Server) handleReportHopComment(w http.ResponseWriter, r *http.Request) {
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
	commentID, _ := strconv.ParseInt(r.FormValue("comment_id"), 10, 64)
	if orgID <= 0 || hopID <= 0 || commentID <= 0 {
		http.Error(w, "invalid report target", http.StatusBadRequest)
		return
	}

	memberOK, err := service.MemberHasActiveMembership(r.Context(), s.db, user.ID, orgID)
	if err != nil {
		log.Printf("check moderation report membership member=%d org=%d: %v", user.ID, orgID, err)
		http.Error(w, "could not submit report", http.StatusInternalServerError)
		return
	}
	if !memberOK {
		s.renderUnauthorized(w, r)
		return
	}

	hop, err := service.GetHopByID(r.Context(), s.db, orgID, hopID)
	if err != nil {
		if errors.Is(err, service.ErrHopNotFound) {
			http.NotFound(w, r)
			return
		}
		log.Printf("load hop for report hop=%d org=%d: %v", hopID, orgID, err)
		http.Error(w, "could not submit report", http.StatusInternalServerError)
		return
	}
	isAssociated := hop.CreatedBy == user.ID
	if !isAssociated && hop.AcceptedBy != nil && *hop.AcceptedBy == user.ID {
		isAssociated = true
	}
	if hop.IsPrivate && !isAssociated {
		s.renderUnauthorized(w, r)
		return
	}

	_, err = service.CreateHopCommentReport(r.Context(), s.db, service.CreateHopCommentReportParams{
		OrganizationID:   orgID,
		HopID:            hopID,
		HopCommentID:     commentID,
		ReportedMemberID: user.ID,
		ReporterDetails:  r.FormValue("details"),
	})
	if err != nil {
		switch {
		case errors.Is(err, service.ErrModerationAlreadyReported):
			// Re-reporting the same target is safe and idempotent from the UI perspective.
		case errors.Is(err, service.ErrModerationTargetNotFound):
			http.NotFound(w, r)
			return
		default:
			log.Printf("create hop comment report failed: %v", err)
			http.Error(w, "could not submit report", http.StatusInternalServerError)
			return
		}
	}

	http.Redirect(w, r, hopDetailsRedirect(orgID, hopID, r.FormValue("from"), r.FormValue("view")), http.StatusSeeOther)
}

func (s *Server) handleUploadHopImage(w http.ResponseWriter, r *http.Request) {
	user := s.currentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	const maxHopImageSize = 5 << 20
	r.Body = http.MaxBytesReader(w, r.Body, maxHopImageSize+1024)
	if err := r.ParseMultipartForm(maxHopImageSize); err != nil {
		http.Error(w, "invalid upload", http.StatusBadRequest)
		return
	}

	orgID, _ := strconv.ParseInt(r.FormValue("org_id"), 10, 64)
	hopID, _ := strconv.ParseInt(r.FormValue("hop_id"), 10, 64)
	if orgID <= 0 || hopID <= 0 {
		http.Error(w, "invalid hop", http.StatusBadRequest)
		return
	}

	memberOK, err := service.MemberHasActiveMembership(r.Context(), s.db, user.ID, orgID)
	if err != nil {
		log.Printf("check hop membership member=%d org=%d: %v", user.ID, orgID, err)
		http.Error(w, "could not load hop", http.StatusInternalServerError)
		return
	}
	if !memberOK {
		s.renderUnauthorized(w, r)
		return
	}

	hop, err := service.GetHopByID(r.Context(), s.db, orgID, hopID)
	if err != nil {
		if errors.Is(err, service.ErrHopNotFound) {
			http.NotFound(w, r)
			return
		}
		log.Printf("load hop %d: %v", hopID, err)
		http.Error(w, "could not load hop", http.StatusInternalServerError)
		return
	}

	isAssociated := hop.CreatedBy == user.ID
	if !isAssociated && hop.AcceptedBy != nil && *hop.AcceptedBy == user.ID {
		isAssociated = true
	}
	if !isAssociated {
		s.renderUnauthorized(w, r)
		return
	}

	file, header, err := r.FormFile("image")
	if err != nil {
		http.Error(w, "missing image", http.StatusBadRequest)
		return
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, maxHopImageSize+1))
	if err != nil {
		http.Error(w, "could not read image", http.StatusBadRequest)
		return
	}
	if len(data) > maxHopImageSize {
		http.Error(w, "image too large", http.StatusBadRequest)
		return
	}

	contentType := strings.TrimSpace(header.Header.Get("Content-Type"))
	if contentType == "" {
		contentType = http.DetectContentType(data)
	}
	if !strings.HasPrefix(contentType, "image/") {
		http.Error(w, "invalid image type", http.StatusBadRequest)
		return
	}

	if err := service.AddHopImage(r.Context(), s.db, hopID, user.ID, contentType, data); err != nil {
		log.Printf("add hop image failed: %v", err)
		http.Error(w, "could not upload image", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, hopDetailsRedirect(orgID, hopID, r.FormValue("from"), r.FormValue("view")), http.StatusSeeOther)
}

func (s *Server) handleReportHopImage(w http.ResponseWriter, r *http.Request) {
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
	imageID, _ := strconv.ParseInt(r.FormValue("image_id"), 10, 64)
	if orgID <= 0 || hopID <= 0 || imageID <= 0 {
		http.Error(w, "invalid report target", http.StatusBadRequest)
		return
	}

	memberOK, err := service.MemberHasActiveMembership(r.Context(), s.db, user.ID, orgID)
	if err != nil {
		log.Printf("check moderation report membership member=%d org=%d: %v", user.ID, orgID, err)
		http.Error(w, "could not submit report", http.StatusInternalServerError)
		return
	}
	if !memberOK {
		s.renderUnauthorized(w, r)
		return
	}

	hop, err := service.GetHopByID(r.Context(), s.db, orgID, hopID)
	if err != nil {
		if errors.Is(err, service.ErrHopNotFound) {
			http.NotFound(w, r)
			return
		}
		log.Printf("load hop for image report hop=%d org=%d: %v", hopID, orgID, err)
		http.Error(w, "could not submit report", http.StatusInternalServerError)
		return
	}
	isAssociated := hop.CreatedBy == user.ID
	if !isAssociated && hop.AcceptedBy != nil && *hop.AcceptedBy == user.ID {
		isAssociated = true
	}
	if hop.IsPrivate && !isAssociated {
		s.renderUnauthorized(w, r)
		return
	}

	_, err = service.CreateHopImageReport(r.Context(), s.db, service.CreateHopImageReportParams{
		OrganizationID:   orgID,
		HopID:            hopID,
		HopImageID:       imageID,
		ReportedMemberID: user.ID,
		ReporterDetails:  r.FormValue("details"),
	})
	if err != nil {
		switch {
		case errors.Is(err, service.ErrModerationAlreadyReported):
			// Re-reporting the same target is safe and idempotent from the UI perspective.
		case errors.Is(err, service.ErrModerationTargetNotFound):
			http.NotFound(w, r)
			return
		default:
			log.Printf("create hop image report failed: %v", err)
			http.Error(w, "could not submit report", http.StatusInternalServerError)
			return
		}
	}

	http.Redirect(w, r, hopDetailsRedirect(orgID, hopID, r.FormValue("from"), r.FormValue("view")), http.StatusSeeOther)
}

func (s *Server) handleDeleteHopImage(w http.ResponseWriter, r *http.Request) {
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
	imageID, _ := strconv.ParseInt(r.FormValue("image_id"), 10, 64)
	if orgID <= 0 || hopID <= 0 || imageID <= 0 {
		http.Error(w, "invalid image", http.StatusBadRequest)
		return
	}

	memberOK, err := service.MemberHasActiveMembership(r.Context(), s.db, user.ID, orgID)
	if err != nil {
		log.Printf("check hop membership member=%d org=%d: %v", user.ID, orgID, err)
		http.Error(w, "could not load hop", http.StatusInternalServerError)
		return
	}
	if !memberOK {
		s.renderUnauthorized(w, r)
		return
	}

	hop, err := service.GetHopByID(r.Context(), s.db, orgID, hopID)
	if err != nil {
		if errors.Is(err, service.ErrHopNotFound) {
			http.NotFound(w, r)
			return
		}
		log.Printf("load hop %d: %v", hopID, err)
		http.Error(w, "could not load hop", http.StatusInternalServerError)
		return
	}

	isAssociated := hop.CreatedBy == user.ID
	if !isAssociated && hop.AcceptedBy != nil && *hop.AcceptedBy == user.ID {
		isAssociated = true
	}
	if !isAssociated {
		s.renderUnauthorized(w, r)
		return
	}

	if err := service.DeleteHopImage(r.Context(), s.db, hopID, imageID); err != nil {
		if errors.Is(err, service.ErrHopImageNotFound) {
			http.NotFound(w, r)
			return
		}
		log.Printf("delete hop image failed: %v", err)
		http.Error(w, "could not delete image", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, hopDetailsRedirect(orgID, hopID, r.FormValue("from"), r.FormValue("view")), http.StatusSeeOther)
}

func (s *Server) handleHopImage(w http.ResponseWriter, r *http.Request) {
	user := s.currentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	imageID, err := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("image_id")), 10, 64)
	if err != nil || imageID <= 0 {
		http.NotFound(w, r)
		return
	}

	img, err := service.GetHopImageData(r.Context(), s.db, imageID)
	if err != nil {
		if errors.Is(err, service.ErrHopImageNotFound) {
			http.NotFound(w, r)
			return
		}
		log.Printf("load hop image %d: %v", imageID, err)
		http.Error(w, "could not load image", http.StatusInternalServerError)
		return
	}

	memberOK, err := service.MemberHasActiveMembership(r.Context(), s.db, user.ID, img.OrganizationID)
	if err != nil {
		log.Printf("check hop image membership member=%d org=%d: %v", user.ID, img.OrganizationID, err)
		http.Error(w, "could not load image", http.StatusInternalServerError)
		return
	}
	if !memberOK {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", img.ContentType)
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(img.Data); err != nil {
		log.Printf("write hop image %d: %v", imageID, err)
	}
}

func hopDetailsRedirect(orgID, hopID int64, from string, view string) string {
	base := "/hops/view?hop_id=" + strconv.FormatInt(hopID, 10)
	if orgID > 0 {
		base += "&org_id=" + strconv.FormatInt(orgID, 10)
	}
	if origin := normalizeHopOrigin(from); origin != "" {
		base += "&from=" + origin
		if view = strings.TrimSpace(view); view != "" {
			base += "&view=" + url.QueryEscape(view)
		}
	}
	return base
}

func normalizeHopOrigin(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "my-hops", "dashboard", "organization":
		return strings.ToLower(strings.TrimSpace(raw))
	default:
		return ""
	}
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

func completeHopSuccessMessage(result service.CompleteHopResult) string {
	if result.AwardedHours == result.RequestedHours {
		return "Hop completed."
	}
	if result.AwardedHours == 0 {
		return fmt.Sprintf(
			"Hop completed. No hours were transferred because this would exceed this organization's limits (minimum %d, maximum %d).",
			result.MinBalance,
			result.MaxBalance,
		)
	}
	if result.RequesterMinLimited && result.HelperMaxLimited {
		return fmt.Sprintf(
			"Hop completed. %d hour(s) were transferred instead of %d due to this organization's minimum (%d) and maximum (%d) balance limits.",
			result.AwardedHours,
			result.RequestedHours,
			result.MinBalance,
			result.MaxBalance,
		)
	}
	if result.RequesterMinLimited {
		return fmt.Sprintf(
			"Hop completed. %d hour(s) were transferred instead of %d to keep the requester above the organization's minimum balance (%d).",
			result.AwardedHours,
			result.RequestedHours,
			result.MinBalance,
		)
	}
	if result.HelperMaxLimited {
		return fmt.Sprintf(
			"Hop completed. %d hour(s) were transferred instead of %d to keep the helper below the organization's maximum balance (%d).",
			result.AwardedHours,
			result.RequestedHours,
			result.MaxBalance,
		)
	}
	return "Hop completed."
}
