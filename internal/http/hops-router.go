package http

import (
	"database/sql"
	"errors"
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
	if hop.IsPrivate && !isAssociated {
		s.renderUnauthorized(w, r)
		return
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

	images, err := service.ListHopImages(r.Context(), s.db, hopID)
	if err != nil {
		log.Printf("load hop images %d: %v", hopID, err)
		http.Error(w, "could not load hop", http.StatusInternalServerError)
		return
	}

	showBack := strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("from")), "my-hops")
	canToggle := isAssociated
	canComment := isAssociated || !hop.IsPrivate
	canUpload := isAssociated
	render(w, r, templates.HopDetails(s.currentUserEmailPtr(r), org, hop, showBack, canToggle, canComment, canUpload, comments, images))
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

	isPrivate := true
	if strings.EqualFold(strings.TrimSpace(r.FormValue("is_public")), "true") {
		isPrivate = false
	}

	_, err = service.CreateHop(r.Context(), s.db, service.CreateHopParams{
		OrganizationID: orgID,
		MemberID:       user.ID,
		Title:          r.FormValue("title"),
		Details:        r.FormValue("details"),
		EstimatedHours: estimatedHours,
		NeededByKind:   r.FormValue("needed_by_kind"),
		NeededByDate:   neededByDate,
		IsPrivate:      isPrivate,
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

	http.Redirect(w, r, hopDetailsRedirect(orgID, hopID, r.FormValue("from")), http.StatusSeeOther)
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

	http.Redirect(w, r, hopDetailsRedirect(orgID, hopID, r.FormValue("from")), http.StatusSeeOther)
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

	http.Redirect(w, r, hopDetailsRedirect(orgID, hopID, r.FormValue("from")), http.StatusSeeOther)
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

	http.Redirect(w, r, hopDetailsRedirect(orgID, hopID, r.FormValue("from")), http.StatusSeeOther)
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

	isAssociated := img.CreatedBy == user.ID
	if !isAssociated && img.AcceptedBy != nil && *img.AcceptedBy == user.ID {
		isAssociated = true
	}
	if img.IsPrivate && !isAssociated {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", img.ContentType)
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(img.Data); err != nil {
		log.Printf("write hop image %d: %v", imageID, err)
	}
}

func hopDetailsRedirect(orgID, hopID int64, from string) string {
	base := "/hops/view?hop_id=" + strconv.FormatInt(hopID, 10)
	if orgID > 0 {
		base += "&org_id=" + strconv.FormatInt(orgID, 10)
	}
	if strings.EqualFold(strings.TrimSpace(from), "my-hops") {
		base += "&from=my-hops"
	}
	return base
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
