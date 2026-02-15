package http

import (
	"database/sql"
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

func (s *Server) handleCreateOrganization(w http.ResponseWriter, r *http.Request) {
	user := s.currentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	if _, err := service.PrimaryOwnedOrganization(r.Context(), s.db, user.ID); err == nil {
		http.Redirect(w, r, "/organizations/manage?error="+url.QueryEscape("You already manage an organization."), http.StatusSeeOther)
		return
	} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
		log.Printf("check primary organization: %v", err)
		http.Error(w, "could not load organization", http.StatusInternalServerError)
		return
	}

	successMsg := r.URL.Query().Get("success")
	errorMsg := r.URL.Query().Get("error")

	switch r.Method {
	case http.MethodGet:
		render(w, r, templates.CreateOrganization(s.currentUserEmailPtr(r), "", "", "", "", successMsg, errorMsg))
	case http.MethodPost:
		const maxLogoUploadBytes = 20 << 20
		const maxBodyBytes = maxLogoUploadBytes + (1 << 20)
		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		if err := r.ParseMultipartForm(maxBodyBytes); err != nil {
			http.Error(w, "invalid form", http.StatusBadRequest)
			return
		}

		name := strings.TrimSpace(r.FormValue("name"))
		city := strings.TrimSpace(r.FormValue("city"))
		state := strings.TrimSpace(r.FormValue("state"))
		description := strings.TrimSpace(r.FormValue("description"))
		logoData, logoContentType, hasLogo, err := readLogoUpload(r, "logo_file", maxLogoUploadBytes)
		if err != nil {
			render(w, r, templates.CreateOrganization(s.currentUserEmailPtr(r), name, city, state, description, "", err.Error()))
			return
		}

		org, err := service.CreateOrganization(r.Context(), s.db, name, city, state, description, user.ID)
		if err != nil {
			log.Printf("create organization failed: %v", err)
			msg := "Could not create organization right now."
			switch {
			case errors.Is(err, service.ErrMissingOrgName):
				msg = "Organization name is required."
			case errors.Is(err, service.ErrMissingField):
				msg = "Organization description is required."
			case errors.Is(err, service.ErrMissingMemberID):
				msg = "A member is required to create an organization."
			case errors.Is(err, service.ErrAlreadyPrimaryOwner):
				msg = "You already manage an organization."
			}
			render(w, r, templates.CreateOrganization(s.currentUserEmailPtr(r), name, city, state, description, "", msg))
			return
		}

		if hasLogo {
			if err := service.SetOrganizationLogo(r.Context(), s.db, org.ID, logoContentType, logoData); err != nil {
				log.Printf("set org logo org=%d: %v", org.ID, err)
				http.Redirect(w, r, "/organizations/manage?error="+url.QueryEscape("Organization created, but logo upload failed."), http.StatusSeeOther)
				return
			}
		}

		success := fmt.Sprintf("Created %s and set you as primary owner.", org.Name)
		http.Redirect(w, r, "/my-hopshare?success="+url.QueryEscape(success), http.StatusSeeOther)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleOrganizations(w http.ResponseWriter, r *http.Request) {
	orgs, err := service.ListOrganizations(r.Context(), s.db)
	if err != nil {
		log.Printf("list organizations: %v", err)
		http.Error(w, "could not load organizations", http.StatusInternalServerError)
		return
	}

	successMsg := r.URL.Query().Get("success")
	errorMsg := r.URL.Query().Get("error")

	render(w, r, templates.Organizations(s.currentUserEmailPtr(r), orgs, successMsg, errorMsg))
}

func (s *Server) handleOrganization(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/organization" {
		orgIDStr := strings.TrimSpace(r.URL.Query().Get("org_id"))
		if orgIDStr == "" {
			http.NotFound(w, r)
			return
		}
		orgID, err := strconv.ParseInt(orgIDStr, 10, 64)
		if err != nil || orgID <= 0 {
			http.NotFound(w, r)
			return
		}

		org, err := service.GetOrganizationByID(r.Context(), s.db, orgID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				http.NotFound(w, r)
				return
			}
			log.Printf("load organization %d for redirect: %v", orgID, err)
			http.Error(w, "could not load organization", http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, "/organization/"+org.URLName, http.StatusMovedPermanently)
		return
	}

	orgURLName, ok := organizationURLNameFromPath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}

	org, err := service.GetOrganizationByURLName(r.Context(), s.db, orgURLName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		log.Printf("load organization by url name %q: %v", orgURLName, err)
		http.Error(w, "could not load organization", http.StatusInternalServerError)
		return
	}

	showJoinPanel := true
	showPendingPanel := false
	var pendingHops []types.Hop
	if user := s.currentUser(r); user != nil {
		hasMembership, err := service.MemberHasActiveMembership(r.Context(), s.db, user.ID, org.ID)
		if err != nil {
			log.Printf("check member organization membership member=%d org=%d: %v", user.ID, org.ID, err)
			http.Error(w, "could not load organization", http.StatusInternalServerError)
			return
		}
		showJoinPanel = !hasMembership
		showPendingPanel = hasMembership
	}

	metrics, err := service.OrgMetrics(r.Context(), s.db, org.ID)
	if err != nil {
		log.Printf("load organization metrics org=%d: %v", org.ID, err)
		http.Error(w, "could not load organization", http.StatusInternalServerError)
		return
	}

	recentCompleted, err := service.RecentPublicCompletedHops(r.Context(), s.db, org.ID, 25)
	if err != nil {
		log.Printf("load organization recent completed hops org=%d: %v", org.ID, err)
		http.Error(w, "could not load organization", http.StatusInternalServerError)
		return
	}
	if showPendingPanel {
		pendingHops, err = service.RecentPublicPendingHops(r.Context(), s.db, org.ID, 100)
		if err != nil {
			log.Printf("load organization pending hops org=%d: %v", org.ID, err)
			http.Error(w, "could not load organization", http.StatusInternalServerError)
			return
		}
	}

	successMsg := r.URL.Query().Get("success")
	errorMsg := r.URL.Query().Get("error")
	render(w, r, templates.Organization(s.currentUserEmailPtr(r), org, metrics, recentCompleted, pendingHops, showJoinPanel, showPendingPanel, successMsg, errorMsg))
}

func (s *Server) handleOrganizationLogo(w http.ResponseWriter, r *http.Request) {
	orgID, err := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("org_id")), 10, 64)
	if err != nil || orgID <= 0 {
		http.Error(w, "invalid organization", http.StatusBadRequest)
		return
	}

	// TODO: Add some filesystem caching here to alleviate load on the database.
	//       First time we render this logo, save it to the filesystem and serve from there on subsequent requests.
	//       This way we keep 'state' all in the database for backups, etc, but use filesystem for regular requests.
	//       Make sure we invalidate this cache if the logo ever gets updated.
	data, contentType, ok, err := service.OrganizationLogo(r.Context(), s.db, orgID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		log.Printf("load organization logo org=%d: %v", orgID, err)
		http.Error(w, "could not load logo", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Redirect(w, r, "/static/assets/image/logo_blue_transparent.png", http.StatusFound)
		return
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (s *Server) handleRequestMembership(w http.ResponseWriter, r *http.Request) {
	user := s.currentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	orgIDStr := r.FormValue("org_id")
	orgID, err := strconv.ParseInt(orgIDStr, 10, 64)
	if err != nil || orgID <= 0 {
		http.Redirect(w, r, "/organizations?error="+url.QueryEscape("Invalid organization."), http.StatusSeeOther)
		return
	}

	redirectBase := "/organizations"
	var orgName string
	if org, err := service.GetOrganizationByID(r.Context(), s.db, orgID); err == nil {
		orgName = org.Name
		redirectBase = "/organization/" + org.URLName
	} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
		log.Printf("load organization %d before membership request redirect: %v", orgID, err)
	}
	redirectWith := func(key, msg string) string {
		sep := "?"
		if strings.Contains(redirectBase, "?") {
			sep = "&"
		}
		return redirectBase + sep + key + "=" + url.QueryEscape(msg)
	}

	if err := service.RequestMembership(r.Context(), s.db, user.ID, orgID, nil); err != nil {
		log.Printf("request membership member=%d org=%d: %v", user.ID, orgID, err)
		msg := "Could not submit request."
		switch {
		case errors.Is(err, service.ErrMissingMemberID):
			msg = "You need a member record to request membership."
		case errors.Is(err, service.ErrMissingOrgID):
			msg = "Invalid organization."
		case errors.Is(err, service.ErrRequestAlreadyExists):
			msg = "You already have a pending request for this organization."
		}
		http.Redirect(w, r, redirectWith("error", msg), http.StatusSeeOther)
		return
	}

	success := "Membership request submitted."
	if orgName != "" {
		success = fmt.Sprintf("Requested membership in %s.", orgName)
	}
	http.Redirect(w, r, redirectWith("success", success), http.StatusSeeOther)
}

func (s *Server) handleManageOrganization(w http.ResponseWriter, r *http.Request) {
	user := s.currentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	successMsg := r.URL.Query().Get("success")
	errorMsg := r.URL.Query().Get("error")

	switch r.Method {
	case http.MethodGet:
		orgIDStr := strings.TrimSpace(r.URL.Query().Get("org_id"))
		var org types.Organization
		var err error
		if orgIDStr != "" {
			orgID, parseErr := strconv.ParseInt(orgIDStr, 10, 64)
			if parseErr != nil || orgID <= 0 {
				http.Error(w, "invalid organization", http.StatusBadRequest)
				return
			}
			org, err = service.OrganizationForOwner(r.Context(), s.db, user.ID, orgID)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					s.renderUnauthorized(w, r)
					return
				}
				log.Printf("load owned organization: %v", err)
				http.Error(w, "could not load organization", http.StatusInternalServerError)
				return
			}
		} else {
			org, err = service.PrimaryOwnedOrganization(r.Context(), s.db, user.ID)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					http.Redirect(w, r, "/organizations/create?error="+url.QueryEscape("You need to create an organization first."), http.StatusSeeOther)
					return
				}
				log.Printf("load primary organization: %v", err)
				http.Error(w, "could not load organization", http.StatusInternalServerError)
				return
			}
		}

		requests, err := service.PendingMembershipRequests(r.Context(), s.db, org.ID)
		if err != nil {
			log.Printf("load pending requests: %v", err)
			http.Error(w, "could not load requests", http.StatusInternalServerError)
			return
		}

		members, err := service.OrganizationMembers(r.Context(), s.db, org.ID)
		if err != nil {
			log.Printf("load organization members: %v", err)
			http.Error(w, "could not load members", http.StatusInternalServerError)
			return
		}

		render(w, r, templates.ManageOrganizationWithMembers(s.currentUserEmailPtr(r), org, requests, members, successMsg, errorMsg))
	case http.MethodPost:
		const maxLogoUploadBytes = 20 << 20
		const maxBodyBytes = maxLogoUploadBytes + (1 << 20)
		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		if err := r.ParseMultipartForm(maxBodyBytes); err != nil {
			http.Error(w, "invalid form", http.StatusBadRequest)
			return
		}

		orgIDStr := strings.TrimSpace(r.FormValue("org_id"))
		var org types.Organization
		var err error
		if orgIDStr != "" {
			orgID, parseErr := strconv.ParseInt(orgIDStr, 10, 64)
			if parseErr != nil || orgID <= 0 {
				http.Error(w, "invalid organization", http.StatusBadRequest)
				return
			}
			org, err = service.OrganizationForOwner(r.Context(), s.db, user.ID, orgID)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					s.renderUnauthorized(w, r)
					return
				}
				log.Printf("load owned organization: %v", err)
				http.Error(w, "could not load organization", http.StatusInternalServerError)
				return
			}
		} else {
			org, err = service.PrimaryOwnedOrganization(r.Context(), s.db, user.ID)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					http.Redirect(w, r, "/organizations/create?error="+url.QueryEscape("You need to create an organization first."), http.StatusSeeOther)
					return
				}
				log.Printf("load primary organization: %v", err)
				http.Error(w, "could not load organization", http.StatusInternalServerError)
				return
			}
		}

		requests, err := service.PendingMembershipRequests(r.Context(), s.db, org.ID)
		if err != nil {
			log.Printf("load pending requests: %v", err)
			http.Error(w, "could not load requests", http.StatusInternalServerError)
			return
		}

		members, err := service.OrganizationMembers(r.Context(), s.db, org.ID)
		if err != nil {
			log.Printf("load organization members: %v", err)
			http.Error(w, "could not load members", http.StatusInternalServerError)
			return
		}

		name := strings.TrimSpace(r.FormValue("name"))
		city := strings.TrimSpace(r.FormValue("city"))
		state := strings.TrimSpace(r.FormValue("state"))
		description := strings.TrimSpace(r.FormValue("description"))
		logoData, logoContentType, hasLogo, err := readLogoUpload(r, "logo_file", maxLogoUploadBytes)
		if err != nil {
			render(w, r, templates.ManageOrganizationWithMembers(s.currentUserEmailPtr(r), org, requests, members, "", err.Error()))
			return
		}

		updateOrg := types.Organization{
			ID:          org.ID,
			Name:        name,
			City:        city,
			State:       state,
			Description: description,
			Enabled:     org.Enabled,
		}
		if err := service.UpdateOrganization(r.Context(), s.db, updateOrg); err != nil {
			log.Printf("update organization %d: %v", org.ID, err)
			render(w, r, templates.ManageOrganizationWithMembers(s.currentUserEmailPtr(r), org, requests, members, "", "Could not update organization."))
			return
		}

		if hasLogo {
			if err := service.SetOrganizationLogo(r.Context(), s.db, org.ID, logoContentType, logoData); err != nil {
				log.Printf("set org logo org=%d: %v", org.ID, err)
				render(w, r, templates.ManageOrganizationWithMembers(s.currentUserEmailPtr(r), org, requests, members, "", "Could not upload logo."))
				return
			}
		}
		redirectURL := "/organizations/manage?success=" + url.QueryEscape("Organization updated.")
		if org.ID != 0 {
			redirectURL = "/organizations/manage?org_id=" + strconv.FormatInt(org.ID, 10) + "&success=" + url.QueryEscape("Organization updated.")
		}
		http.Redirect(w, r, redirectURL, http.StatusSeeOther)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func readLogoUpload(r *http.Request, field string, maxBytes int64) ([]byte, string, bool, error) {
	f, _, err := r.FormFile(field)
	if err != nil {
		if errors.Is(err, http.ErrMissingFile) {
			return nil, "", false, nil
		}
		return nil, "", false, fmt.Errorf("read logo file: %w", err)
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil {
		return nil, "", false, fmt.Errorf("read logo file: %w", err)
	}
	if int64(len(data)) > maxBytes {
		return nil, "", false, fmt.Errorf("logo file too large (max 20MB)")
	}
	if len(data) == 0 {
		return nil, "", false, fmt.Errorf("logo file is empty")
	}

	contentType := http.DetectContentType(data)
	switch contentType {
	case "image/png", "image/jpeg":
		return data, contentType, true, nil
	default:
		return nil, "", false, fmt.Errorf("logo must be a PNG or JPEG")
	}
}

func (s *Server) handleManageMembershipRequest(w http.ResponseWriter, r *http.Request) {
	user := s.currentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	reqID, err := strconv.ParseInt(r.FormValue("request_id"), 10, 64)
	if err != nil || reqID <= 0 {
		http.Redirect(w, r, "/organizations/manage?error="+url.QueryEscape("Invalid request."), http.StatusSeeOther)
		return
	}
	action := strings.ToLower(strings.TrimSpace(r.FormValue("action")))

	var reqOrgID int64
	if err := s.db.QueryRowContext(r.Context(), `
		SELECT organization_id FROM membership_requests WHERE id = $1
	`, reqID).Scan(&reqOrgID); err != nil {
		msg := "Request not found."
		if !errors.Is(err, sql.ErrNoRows) {
			log.Printf("lookup membership request %d: %v", reqID, err)
			msg = "Could not load request."
		}
		http.Redirect(w, r, "/organizations/manage?error="+url.QueryEscape(msg), http.StatusSeeOther)
		return
	}

	ownsOrg, err := service.MemberOwnsOrganization(r.Context(), s.db, user.ID, reqOrgID)
	if err != nil {
		log.Printf("check org ownership member=%d org=%d: %v", user.ID, reqOrgID, err)
		http.Error(w, "could not load organization", http.StatusInternalServerError)
		return
	}
	if !ownsOrg {
		http.Redirect(w, r, "/organizations/manage?error="+url.QueryEscape("You can only manage your organization's requests."), http.StatusSeeOther)
		return
	}

	manageURL := "/organizations/manage"
	if reqOrgID > 0 {
		manageURL = "/organizations/manage?org_id=" + strconv.FormatInt(reqOrgID, 10)
	}
	manageURLWith := func(key, value string) string {
		sep := "?"
		if strings.Contains(manageURL, "?") {
			sep = "&"
		}
		return manageURL + sep + key + "=" + url.QueryEscape(value)
	}

	switch action {
	case "accept":
		if err := service.ApproveMembershipRequest(r.Context(), s.db, reqID, user.ID); err != nil {
			log.Printf("approve request %d: %v", reqID, err)
			http.Redirect(w, r, manageURLWith("error", "Could not approve request."), http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, manageURLWith("success", "Membership approved."), http.StatusSeeOther)
	case "deny":
		if err := service.DenyMembershipRequest(r.Context(), s.db, reqID, user.ID); err != nil {
			log.Printf("deny request %d: %v", reqID, err)
			http.Redirect(w, r, manageURLWith("error", "Could not deny request."), http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, manageURLWith("success", "Membership denied."), http.StatusSeeOther)
	default:
		http.Redirect(w, r, manageURLWith("error", "Unknown action."), http.StatusSeeOther)
	}
}

func (s *Server) handleRemoveMember(w http.ResponseWriter, r *http.Request) {
	user := s.currentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	orgIDStr := strings.TrimSpace(r.FormValue("org_id"))
	var org types.Organization
	var err error
	if orgIDStr != "" {
		orgID, parseErr := strconv.ParseInt(orgIDStr, 10, 64)
		if parseErr != nil || orgID <= 0 {
			http.Redirect(w, r, "/organizations/manage?error="+url.QueryEscape("Invalid organization."), http.StatusSeeOther)
			return
		}
		org, err = service.OrganizationForOwner(r.Context(), s.db, user.ID, orgID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				s.renderUnauthorized(w, r)
				return
			}
			log.Printf("load owned organization: %v", err)
			http.Error(w, "could not load organization", http.StatusInternalServerError)
			return
		}
	} else {
		org, err = service.PrimaryOwnedOrganization(r.Context(), s.db, user.ID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				http.Redirect(w, r, "/organizations/create?error="+url.QueryEscape("You need to create an organization first."), http.StatusSeeOther)
				return
			}
			log.Printf("load primary organization: %v", err)
			http.Error(w, "could not load organization", http.StatusInternalServerError)
			return
		}
	}

	memberID, err := strconv.ParseInt(r.FormValue("member_id"), 10, 64)
	if err != nil || memberID <= 0 {
		redirectURL := "/organizations/manage?error=" + url.QueryEscape("Invalid member.")
		if org.ID != 0 {
			redirectURL = "/organizations/manage?org_id=" + strconv.FormatInt(org.ID, 10) + "&error=" + url.QueryEscape("Invalid member.")
		}
		http.Redirect(w, r, redirectURL, http.StatusSeeOther)
		return
	}

	if err := service.RemoveOrganizationMember(r.Context(), s.db, org.ID, memberID, user.ID); err != nil {
		log.Printf("remove member %d from org %d: %v", memberID, org.ID, err)
		msg := "Could not remove member."
		if errors.Is(err, service.ErrMembershipNotFound) {
			msg = "Membership not found."
		}
		redirectURL := "/organizations/manage?error=" + url.QueryEscape(msg)
		if org.ID != 0 {
			redirectURL = "/organizations/manage?org_id=" + strconv.FormatInt(org.ID, 10) + "&error=" + url.QueryEscape(msg)
		}
		http.Redirect(w, r, redirectURL, http.StatusSeeOther)
		return
	}

	redirectURL := "/organizations/manage?success=" + url.QueryEscape("Member removed.")
	if org.ID != 0 {
		redirectURL = "/organizations/manage?org_id=" + strconv.FormatInt(org.ID, 10) + "&success=" + url.QueryEscape("Member removed.")
	}
	http.Redirect(w, r, redirectURL, http.StatusSeeOther)
}

func (s *Server) handleChangeMemberRole(w http.ResponseWriter, r *http.Request) {
	user := s.currentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	orgIDStr := strings.TrimSpace(r.FormValue("org_id"))
	var org types.Organization
	var err error
	if orgIDStr != "" {
		orgID, parseErr := strconv.ParseInt(orgIDStr, 10, 64)
		if parseErr != nil || orgID <= 0 {
			http.Redirect(w, r, "/organizations/manage?error="+url.QueryEscape("Invalid organization."), http.StatusSeeOther)
			return
		}
		org, err = service.OrganizationForOwner(r.Context(), s.db, user.ID, orgID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				s.renderUnauthorized(w, r)
				return
			}
			log.Printf("load owned organization: %v", err)
			http.Error(w, "could not load organization", http.StatusInternalServerError)
			return
		}
	} else {
		org, err = service.PrimaryOwnedOrganization(r.Context(), s.db, user.ID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				http.Redirect(w, r, "/organizations/create?error="+url.QueryEscape("You need to create an organization first."), http.StatusSeeOther)
				return
			}
			log.Printf("load primary organization: %v", err)
			http.Error(w, "could not load organization", http.StatusInternalServerError)
			return
		}
	}

	memberID, err := strconv.ParseInt(r.FormValue("member_id"), 10, 64)
	if err != nil || memberID <= 0 {
		redirectURL := "/organizations/manage?error=" + url.QueryEscape("Invalid member.")
		if org.ID != 0 {
			redirectURL = "/organizations/manage?org_id=" + strconv.FormatInt(org.ID, 10) + "&error=" + url.QueryEscape("Invalid member.")
		}
		http.Redirect(w, r, redirectURL, http.StatusSeeOther)
		return
	}
	action := strings.ToLower(strings.TrimSpace(r.FormValue("action")))

	switch action {
	case "make_owner":
		if err := service.UpdateOrganizationMemberRole(r.Context(), s.db, org.ID, memberID, true); err != nil {
			log.Printf("make owner member=%d org=%d: %v", memberID, org.ID, err)
			redirectURL := "/organizations/manage?error=" + url.QueryEscape("Could not update member role.")
			if org.ID != 0 {
				redirectURL = "/organizations/manage?org_id=" + strconv.FormatInt(org.ID, 10) + "&error=" + url.QueryEscape("Could not update member role.")
			}
			http.Redirect(w, r, redirectURL, http.StatusSeeOther)
			return
		}
		redirectURL := "/organizations/manage?success=" + url.QueryEscape("Member promoted to owner.")
		if org.ID != 0 {
			redirectURL = "/organizations/manage?org_id=" + strconv.FormatInt(org.ID, 10) + "&success=" + url.QueryEscape("Member promoted to owner.")
		}
		http.Redirect(w, r, redirectURL, http.StatusSeeOther)
	case "revoke_owner":
		if err := service.UpdateOrganizationMemberRole(r.Context(), s.db, org.ID, memberID, false); err != nil {
			log.Printf("revoke owner member=%d org=%d: %v", memberID, org.ID, err)
			redirectURL := "/organizations/manage?error=" + url.QueryEscape("Could not update member role.")
			if org.ID != 0 {
				redirectURL = "/organizations/manage?org_id=" + strconv.FormatInt(org.ID, 10) + "&error=" + url.QueryEscape("Could not update member role.")
			}
			http.Redirect(w, r, redirectURL, http.StatusSeeOther)
			return
		}
		redirectURL := "/organizations/manage?success=" + url.QueryEscape("Owner role revoked.")
		if org.ID != 0 {
			redirectURL = "/organizations/manage?org_id=" + strconv.FormatInt(org.ID, 10) + "&success=" + url.QueryEscape("Owner role revoked.")
		}
		http.Redirect(w, r, redirectURL, http.StatusSeeOther)
	default:
		redirectURL := "/organizations/manage?error=" + url.QueryEscape("Unknown action.")
		if org.ID != 0 {
			redirectURL = "/organizations/manage?org_id=" + strconv.FormatInt(org.ID, 10) + "&error=" + url.QueryEscape("Unknown action.")
		}
		http.Redirect(w, r, redirectURL, http.StatusSeeOther)
	}
}

func humanOrgAge(createdAt time.Time) string {
	d := time.Since(createdAt)
	days := int(d.Hours() / 24)
	if days < 30 {
		if days <= 1 {
			return "1 day"
		}
		return fmt.Sprintf("%d days", days)
	}
	months := days / 30
	if months < 24 {
		if months == 1 {
			return "1 mo"
		}
		return fmt.Sprintf("%d mos", months)
	}
	years := months / 12
	if years == 1 {
		return "1 yr"
	}
	return fmt.Sprintf("%d yrs", years)
}

func organizationURLNameFromPath(path string) (string, bool) {
	const prefix = "/organization/"
	if !strings.HasPrefix(path, prefix) {
		return "", false
	}
	urlName := strings.TrimSpace(strings.TrimPrefix(path, prefix))
	if urlName == "" || strings.Contains(urlName, "/") {
		return "", false
	}
	return strings.ToLower(urlName), true
}
