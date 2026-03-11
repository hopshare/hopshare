package http

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"sort"
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

	alreadyCreated, err := service.MemberCreatedOrganization(r.Context(), s.db, user.ID)
	if err != nil {
		log.Printf("check member created organization: %v", err)
		http.Error(w, "could not load organization", http.StatusInternalServerError)
		return
	}
	if alreadyCreated {
		http.Redirect(w, r, "/organizations/manage?error="+url.QueryEscape("You have already created an organization."), http.StatusSeeOther)
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
			case errors.Is(err, service.ErrOrganizationAlreadyCreated):
				msg = "You have already created an organization."
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

		success := fmt.Sprintf("Created %s.", org.Name)
		query := url.Values{}
		query.Set("org_id", strconv.FormatInt(org.ID, 10))
		query.Set("success", success)
		if s.featureEmail {
			query.Set("invite_prompt", "1")
		}
		http.Redirect(w, r, "/my-hopshare?"+query.Encode(), http.StatusSeeOther)
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

		org, err := service.GetEnabledOrganizationByID(r.Context(), s.db, orgID)
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

	org, err := service.GetEnabledOrganizationByURLName(r.Context(), s.db, orgURLName)
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
	showAllHops := false
	var pendingHops []types.Hop
	currentUser := s.currentUser(r)
	if currentUser != nil {
		hasMembership, err := service.MemberHasActiveMembership(r.Context(), s.db, currentUser.ID, org.ID)
		if err != nil {
			log.Printf("check member organization membership member=%d org=%d: %v", currentUser.ID, org.ID, err)
			http.Error(w, "could not load organization", http.StatusInternalServerError)
			return
		}
		showJoinPanel = !hasMembership
		showPendingPanel = hasMembership
		showAllHops = hasMembership
	}

	metrics, err := service.OrgMetrics(r.Context(), s.db, org.ID)
	if err != nil {
		log.Printf("load organization metrics org=%d: %v", org.ID, err)
		http.Error(w, "could not load organization", http.StatusInternalServerError)
		return
	}

	var recentCompleted []types.Hop
	var recentAccepted []types.Hop
	if showAllHops {
		recentCompleted, err = service.RecentCompletedHops(r.Context(), s.db, org.ID, 25)
		if err == nil {
			recentAccepted, err = service.RecentAcceptedHops(r.Context(), s.db, org.ID, 25)
		}
	} else {
		recentCompleted, err = service.RecentPublicCompletedHops(r.Context(), s.db, org.ID, 25)
		if err == nil {
			recentAccepted, err = service.RecentPublicAcceptedHops(r.Context(), s.db, org.ID, 25)
		}
	}
	if err != nil {
		log.Printf("load organization recent activity hops org=%d: %v", org.ID, err)
		http.Error(w, "could not load organization", http.StatusInternalServerError)
		return
	}
	recentCompleted = mergeRecentOrganizationHops(recentCompleted, recentAccepted, 25)
	recentCompleted, err = service.MarkLeftOrganizationParticipants(r.Context(), s.db, org.ID, recentCompleted)
	if err != nil {
		log.Printf("annotate left organization hop participants org=%d: %v", org.ID, err)
		http.Error(w, "could not load organization", http.StatusInternalServerError)
		return
	}
	if showPendingPanel {
		pendingHops, err = service.RecentPendingHops(r.Context(), s.db, org.ID, 100)
		if err != nil {
			log.Printf("load organization pending hops org=%d: %v", org.ID, err)
			http.Error(w, "could not load organization", http.StatusInternalServerError)
			return
		}
		if currentUser != nil && len(pendingHops) > 1 {
			sortedHops, sortErr := service.SortHopsByMemberSkillMatch(r.Context(), s.db, org.ID, currentUser.ID, pendingHops)
			if sortErr != nil {
				log.Printf("sort organization pending hops by skill match org=%d member=%d: %v", org.ID, currentUser.ID, sortErr)
			} else {
				pendingHops = sortedHops
			}
		}
		pendingHops, err = service.MarkLeftOrganizationParticipants(r.Context(), s.db, org.ID, pendingHops)
		if err != nil {
			log.Printf("annotate left organization pending hop participants org=%d: %v", org.ID, err)
			http.Error(w, "could not load organization", http.StatusInternalServerError)
			return
		}
	}

	successMsg := r.URL.Query().Get("success")
	errorMsg := r.URL.Query().Get("error")
	render(w, r, templates.Organization(s.currentUserEmailPtr(r), org, metrics, recentCompleted, pendingHops, showJoinPanel, showPendingPanel, successMsg, errorMsg))
}

func mergeRecentOrganizationHops(completed []types.Hop, accepted []types.Hop, limit int) []types.Hop {
	out := make([]types.Hop, 0, len(completed)+len(accepted))
	out = append(out, completed...)
	out = append(out, accepted...)
	sort.Slice(out, func(i, j int) bool {
		return organizationHopActivityAt(out[i]).After(organizationHopActivityAt(out[j]))
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func organizationHopActivityAt(hop types.Hop) time.Time {
	if hop.Status == types.HopStatusCompleted && hop.CompletedAt != nil {
		return hop.CompletedAt.UTC()
	}
	if hop.Status == types.HopStatusAccepted && hop.AcceptedAt != nil {
		return hop.AcceptedAt.UTC()
	}
	return hop.UpdatedAt.UTC()
}

func (s *Server) handleOrganizationLogo(w http.ResponseWriter, r *http.Request) {
	orgID, err := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("org_id")), 10, 64)
	if err != nil || orgID <= 0 {
		http.Error(w, "invalid organization", http.StatusBadRequest)
		return
	}

	if _, err := service.GetEnabledOrganizationByID(r.Context(), s.db, orgID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		log.Printf("load organization for logo org=%d: %v", orgID, err)
		http.Error(w, "could not load logo", http.StatusInternalServerError)
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
	if org, err := service.GetEnabledOrganizationByID(r.Context(), s.db, orgID); err == nil {
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
		case errors.Is(err, service.ErrOrganizationDisabled):
			msg = "This organization is currently disabled."
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
	activeTab := normalizeManageOrganizationTab(r.URL.Query().Get("tab"))
	invitePostRedirect := normalizeManageOrganizationInvitePostRedirect(r.URL.Query().Get("post_invite_redirect"))

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
					createdOrg, createdErr := service.MemberCreatedOrganization(r.Context(), s.db, user.ID)
					if createdErr != nil {
						log.Printf("check member created organization: %v", createdErr)
						http.Error(w, "could not load organization", http.StatusInternalServerError)
						return
					}
					if createdOrg {
						http.Redirect(w, r, "/my-hopshare?error="+url.QueryEscape("You are not an owner of any organization."), http.StatusSeeOther)
						return
					}
					http.Redirect(w, r, "/organizations/create?error="+url.QueryEscape("You need to create an organization first."), http.StatusSeeOther)
					return
				}
				log.Printf("load default owned organization: %v", err)
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

		orgSkills, err := service.ListOrganizationSkills(r.Context(), s.db, org.ID)
		if err != nil {
			log.Printf("load organization skills org=%d: %v", org.ID, err)
			http.Error(w, "could not load organization skills", http.StatusInternalServerError)
			return
		}
		invitations, err := service.ListOrganizationInvitations(r.Context(), s.db, org.ID, 100)
		if err != nil {
			log.Printf("load organization invitations org=%d: %v", org.ID, err)
			http.Error(w, "could not load organization invitations", http.StatusInternalServerError)
			return
		}
		inviteRemaining, err := service.RemainingOrganizationInviteSlotsToday(r.Context(), s.db, org.ID, time.Now().UTC(), s.appLocation)
		if err != nil {
			log.Printf("remaining organization invites org=%d: %v", org.ID, err)
			http.Error(w, "could not load invitation limits", http.StatusInternalServerError)
			return
		}

		render(w, r, templates.ManageOrganizationWithMembers(s.currentUserEmailPtr(r), org, requests, members, orgSkills, invitations, inviteRemaining, s.featureEmail, user.ID, successMsg, errorMsg, activeTab, invitePostRedirect))
	case http.MethodPost:
		const maxLogoUploadBytes = 20 << 20
		const maxBodyBytes = maxLogoUploadBytes + (1 << 20)
		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		if err := r.ParseMultipartForm(maxBodyBytes); err != nil && !errors.Is(err, http.ErrNotMultipart) {
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
					createdOrg, createdErr := service.MemberCreatedOrganization(r.Context(), s.db, user.ID)
					if createdErr != nil {
						log.Printf("check member created organization: %v", createdErr)
						http.Error(w, "could not load organization", http.StatusInternalServerError)
						return
					}
					if createdOrg {
						http.Redirect(w, r, "/my-hopshare?error="+url.QueryEscape("You are not an owner of any organization."), http.StatusSeeOther)
						return
					}
					http.Redirect(w, r, "/organizations/create?error="+url.QueryEscape("You need to create an organization first."), http.StatusSeeOther)
					return
				}
				log.Printf("load default owned organization: %v", err)
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

		orgSkills, err := service.ListOrganizationSkills(r.Context(), s.db, org.ID)
		if err != nil {
			log.Printf("load organization skills org=%d: %v", org.ID, err)
			http.Error(w, "could not load organization skills", http.StatusInternalServerError)
			return
		}
		invitations, err := service.ListOrganizationInvitations(r.Context(), s.db, org.ID, 100)
		if err != nil {
			log.Printf("load organization invitations org=%d: %v", org.ID, err)
			http.Error(w, "could not load organization invitations", http.StatusInternalServerError)
			return
		}
		inviteRemaining, err := service.RemainingOrganizationInviteSlotsToday(r.Context(), s.db, org.ID, time.Now().UTC(), s.appLocation)
		if err != nil {
			log.Printf("remaining organization invites org=%d: %v", org.ID, err)
			http.Error(w, "could not load invitation limits", http.StatusInternalServerError)
			return
		}

		action := strings.TrimSpace(r.FormValue("action"))
		if action == "" {
			action = "details"
		}
		activeTab = normalizeManageOrganizationTab(r.FormValue("tab"))
		if activeTab == "details" && action != "details" && strings.TrimSpace(r.FormValue("tab")) == "" {
			activeTab = normalizeManageOrganizationTab(action)
		}
		invitePostRedirect = normalizeManageOrganizationInvitePostRedirect(r.FormValue("post_invite_redirect"))

		switch action {
		case "details":
			name := strings.TrimSpace(r.FormValue("name"))
			city := strings.TrimSpace(r.FormValue("city"))
			state := strings.TrimSpace(r.FormValue("state"))
			description := strings.TrimSpace(r.FormValue("description"))
			logoData, logoContentType, hasLogo, err := readLogoUpload(r, "logo_file", maxLogoUploadBytes)
			if err != nil {
				render(w, r, templates.ManageOrganizationWithMembers(s.currentUserEmailPtr(r), org, requests, members, orgSkills, invitations, inviteRemaining, s.featureEmail, user.ID, "", err.Error(), activeTab, invitePostRedirect))
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
				render(w, r, templates.ManageOrganizationWithMembers(s.currentUserEmailPtr(r), org, requests, members, orgSkills, invitations, inviteRemaining, s.featureEmail, user.ID, "", "Could not update organization.", activeTab, invitePostRedirect))
				return
			}

			if hasLogo {
				if err := service.SetOrganizationLogo(r.Context(), s.db, org.ID, logoContentType, logoData); err != nil {
					log.Printf("set org logo org=%d: %v", org.ID, err)
					render(w, r, templates.ManageOrganizationWithMembers(s.currentUserEmailPtr(r), org, requests, members, orgSkills, invitations, inviteRemaining, s.featureEmail, user.ID, "", "Could not upload logo.", activeTab, invitePostRedirect))
					return
				}
			}

			redirectURL := manageOrganizationRedirectURL(org.ID, "Organization updated.", activeTab, invitePostRedirect)
			http.Redirect(w, r, redirectURL, http.StatusSeeOther)
		case "skills":
			skillNames := parseSkillLines(r.FormValue("skills_text"))
			if err := service.ReplaceOrganizationSkills(r.Context(), s.db, org.ID, user.ID, skillNames); err != nil {
				log.Printf("replace organization skills org=%d actor=%d: %v", org.ID, user.ID, err)
				render(w, r, templates.ManageOrganizationWithMembers(s.currentUserEmailPtr(r), org, requests, members, orgSkills, invitations, inviteRemaining, s.featureEmail, user.ID, "", "Could not update skills.", activeTab, invitePostRedirect))
				return
			}
			redirectURL := manageOrganizationRedirectURL(org.ID, "Organization skills updated.", activeTab, invitePostRedirect)
			http.Redirect(w, r, redirectURL, http.StatusSeeOther)
		case "timebank":
			minBalance, err := parseRequiredInt(strings.TrimSpace(r.FormValue("timebank_min_balance")))
			if err != nil {
				render(w, r, templates.ManageOrganizationWithMembers(s.currentUserEmailPtr(r), org, requests, members, orgSkills, invitations, inviteRemaining, s.featureEmail, user.ID, "", "Minimum balance must be a whole number.", activeTab, invitePostRedirect))
				return
			}
			maxBalance, err := parseRequiredInt(strings.TrimSpace(r.FormValue("timebank_max_balance")))
			if err != nil {
				render(w, r, templates.ManageOrganizationWithMembers(s.currentUserEmailPtr(r), org, requests, members, orgSkills, invitations, inviteRemaining, s.featureEmail, user.ID, "", "Maximum balance must be a whole number.", activeTab, invitePostRedirect))
				return
			}
			startingBalance, err := parseRequiredInt(strings.TrimSpace(r.FormValue("timebank_starting_balance")))
			if err != nil {
				render(w, r, templates.ManageOrganizationWithMembers(s.currentUserEmailPtr(r), org, requests, members, orgSkills, invitations, inviteRemaining, s.featureEmail, user.ID, "", "Starting balance must be a whole number.", activeTab, invitePostRedirect))
				return
			}

			if err := service.UpdateOrganizationTimebankPolicy(r.Context(), s.db, org.ID, minBalance, maxBalance, startingBalance); err != nil {
				msg := "Could not update time bank settings."
				switch {
				case errors.Is(err, service.ErrInvalidTimebankMinBalance):
					msg = "Minimum balance must be below zero and greater than -10."
				case errors.Is(err, service.ErrInvalidTimebankMaxBalance):
					msg = "Maximum balance must be above zero and less than 20."
				case errors.Is(err, service.ErrInvalidTimebankStart):
					msg = "Starting balance must be above zero and less than 10."
				case errors.Is(err, service.ErrInvalidTimebank):
					msg = "Starting balance must be less than or equal to the maximum balance."
				}
				render(w, r, templates.ManageOrganizationWithMembers(s.currentUserEmailPtr(r), org, requests, members, orgSkills, invitations, inviteRemaining, s.featureEmail, user.ID, "", msg, activeTab, invitePostRedirect))
				return
			}

			redirectURL := manageOrganizationRedirectURL(org.ID, "Time bank settings updated.", activeTab, invitePostRedirect)
			http.Redirect(w, r, redirectURL, http.StatusSeeOther)
		case "delete_organization":
			confirmation := r.FormValue("delete_organization_confirmation")
			required := "I want to remove " + org.Name
			if confirmation != required {
				msg := fmt.Sprintf("Please type \"%s\" exactly to confirm organization deletion.", required)
				render(
					w,
					r,
					templates.ManageOrganizationWithMembers(
						s.currentUserEmailPtr(r),
						org,
						requests,
						members,
						orgSkills,
						invitations,
						inviteRemaining,
						s.featureEmail,
						user.ID,
						"",
						msg,
						"details",
						invitePostRedirect,
					),
				)
				return
			}

			if err := service.DeleteOrganization(r.Context(), s.db, org.ID, user.ID); err != nil {
				log.Printf("delete organization org=%d actor=%d: %v", org.ID, user.ID, err)
				msg := "Could not delete organization."
				if errors.Is(err, sql.ErrNoRows) || errors.Is(err, service.ErrMissingOrgID) {
					msg = "Invalid organization."
				}
				render(
					w,
					r,
					templates.ManageOrganizationWithMembers(
						s.currentUserEmailPtr(r),
						org,
						requests,
						members,
						orgSkills,
						invitations,
						inviteRemaining,
						s.featureEmail,
						user.ID,
						"",
						msg,
						"details",
						invitePostRedirect,
					),
				)
				return
			}

			http.Redirect(w, r, "/my-hopshare?success="+url.QueryEscape("Organization permanently deleted."), http.StatusSeeOther)
			return
		case "invites":
			if !s.featureEmail {
				render(w, r, templates.ManageOrganizationWithMembers(s.currentUserEmailPtr(r), org, requests, members, orgSkills, invitations, inviteRemaining, s.featureEmail, user.ID, "", "Invitations are currently unavailable.", activeTab, invitePostRedirect))
				return
			}

			result := service.OrganizationInviteBlastResult{}
			rawInviteEmails := strings.TrimSpace(r.FormValue("invite_emails"))
			normalized, invalidEmails, duplicateEmails := service.ParseAndNormalizeInviteEmails(rawInviteEmails)
			result.InvalidEmails = invalidEmails
			result.DuplicateEmails = duplicateEmails

			if len(normalized) == 0 {
				msg := "No valid emails were provided."
				if len(result.InvalidEmails) == 0 && len(result.DuplicateEmails) == 0 {
					msg = "Please enter one or more comma-separated email addresses."
				}
				render(w, r, templates.ManageOrganizationWithMembers(s.currentUserEmailPtr(r), org, requests, members, orgSkills, invitations, inviteRemaining, s.featureEmail, user.ID, "", msg, activeTab, invitePostRedirect))
				return
			}

			remaining := inviteRemaining
			nowUTC := time.Now().UTC()
			for _, email := range normalized {
				if remaining <= 0 {
					result.QuotaSkippedEmails = append(result.QuotaSkippedEmails, email)
					continue
				}

				member, lookupErr := service.GetMemberByEmail(r.Context(), s.db, email)
				if lookupErr == nil && !member.Enabled {
					result.DisabledEmails = append(result.DisabledEmails, email)
					continue
				}
				if lookupErr != nil && !errors.Is(lookupErr, sql.ErrNoRows) {
					log.Printf("lookup invite email member org=%d email=%q: %v", org.ID, email, lookupErr)
					result.SendFailedEmails = append(result.SendFailedEmails, email)
					continue
				}

				alreadyMember, err := service.MemberHasActiveMembershipByEmail(r.Context(), s.db, org.ID, email)
				if err != nil {
					log.Printf("membership by email org=%d email=%q: %v", org.ID, email, err)
					result.SendFailedEmails = append(result.SendFailedEmails, email)
					continue
				}
				if alreadyMember {
					result.AlreadyMemberEmails = append(result.AlreadyMemberEmails, email)
					continue
				}

				expiredCount, err := service.ExpirePendingOrganizationInvitesByEmail(r.Context(), s.db, org.ID, email, nowUTC)
				if err != nil {
					log.Printf("expire pending invite org=%d email=%q: %v", org.ID, email, err)
					result.SendFailedEmails = append(result.SendFailedEmails, email)
					continue
				}
				result.ExpiredPreviousPendingCount += expiredCount

				issued, err := service.CreateOrganizationInvitation(r.Context(), s.db, org.ID, user.ID, email, nowUTC)
				if err != nil {
					log.Printf("create organization invitation org=%d email=%q: %v", org.ID, email, err)
					result.SendFailedEmails = append(result.SendFailedEmails, email)
					continue
				}

				inviteURL := s.organizationInviteURL(issued.RawToken)
				if sendErr := s.passwordResetEmailSender.SendOrganizationInvite(r.Context(), email, inviteURL, org.Name, memberDisplayName(user), issued.ExpiresAt); sendErr != nil {
					log.Printf("send organization invite org=%d email=%q: %v", org.ID, email, sendErr)
					result.SendFailedEmails = append(result.SendFailedEmails, email)
					if expireErr := service.ExpireOrganizationInvitation(r.Context(), s.db, issued.InvitationID, nowUTC); expireErr != nil {
						log.Printf("expire failed invite org=%d invite=%d email=%q: %v", org.ID, issued.InvitationID, email, expireErr)
					}
					continue
				}

				if err := service.MarkOrganizationInvitationSent(r.Context(), s.db, issued.InvitationID, nowUTC); err != nil {
					log.Printf("mark invite sent org=%d invite=%d email=%q: %v", org.ID, issued.InvitationID, email, err)
					result.SendFailedEmails = append(result.SendFailedEmails, email)
					if expireErr := service.ExpireOrganizationInvitation(r.Context(), s.db, issued.InvitationID, nowUTC); expireErr != nil {
						log.Printf("expire unsent invite org=%d invite=%d email=%q: %v", org.ID, issued.InvitationID, email, expireErr)
					}
					continue
				}

				result.SentCount++
				remaining--
			}

			remainingNow, err := service.RemainingOrganizationInviteSlotsToday(r.Context(), s.db, org.ID, time.Now().UTC(), s.appLocation)
			if err != nil {
				log.Printf("remaining organization invites org=%d: %v", org.ID, err)
				remainingNow = remaining
			}
			result.RemainingToday = remainingNow
			if summaryErr := service.SendOwnerInviteBlastSummaryMessage(r.Context(), s.db, user.ID, org.Name, result); summaryErr != nil {
				log.Printf("send invite blast summary org=%d owner=%d: %v", org.ID, user.ID, summaryErr)
			}

			success := fmt.Sprintf("Invite blast complete: %d sent, %d remaining today.", result.SentCount, result.RemainingToday)
			if invitePostRedirect == "my-hopshare" {
				redirectURL := "/my-hopshare?org_id=" + strconv.FormatInt(org.ID, 10) + "&success=" + url.QueryEscape(success)
				http.Redirect(w, r, redirectURL, http.StatusSeeOther)
				return
			}
			redirectURL := manageOrganizationRedirectURL(org.ID, success, activeTab, invitePostRedirect)
			http.Redirect(w, r, redirectURL, http.StatusSeeOther)
		default:
			http.Error(w, "invalid form", http.StatusBadRequest)
		}
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func normalizeManageOrganizationTab(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "details", "members", "skills", "timebank", "invite":
		return strings.ToLower(strings.TrimSpace(raw))
	case "invites":
		return "invite"
	default:
		return "details"
	}
}

func normalizeManageOrganizationInvitePostRedirect(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "my-hopshare":
		return "my-hopshare"
	default:
		return ""
	}
}

func manageOrganizationRedirectURL(orgID int64, successMsg, activeTab string, invitePostRedirect string) string {
	query := url.Values{}
	if orgID != 0 {
		query.Set("org_id", strconv.FormatInt(orgID, 10))
	}
	if successMsg != "" {
		query.Set("success", successMsg)
	}
	tab := normalizeManageOrganizationTab(activeTab)
	if tab != "details" {
		query.Set("tab", tab)
	}
	invitePostRedirect = normalizeManageOrganizationInvitePostRedirect(invitePostRedirect)
	if invitePostRedirect != "" {
		query.Set("post_invite_redirect", invitePostRedirect)
	}
	return "/organizations/manage?" + query.Encode()
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

func parseSkillLines(raw string) []string {
	lines := strings.Split(raw, "\n")
	skills := make([]string, 0, len(lines))
	for _, line := range lines {
		v := strings.TrimSpace(line)
		if v == "" {
			continue
		}
		skills = append(skills, v)
	}
	return skills
}

func parseRequiredInt(raw string) (int, error) {
	if raw == "" {
		return 0, errors.New("missing integer")
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil {
		return 0, err
	}
	return parsed, nil
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

	manageURLWith := func(key, value string) string {
		query := url.Values{}
		if reqOrgID > 0 {
			query.Set("org_id", strconv.FormatInt(reqOrgID, 10))
		}
		query.Set("tab", "members")
		query.Set(key, value)
		return "/organizations/manage?" + query.Encode()
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
				createdOrg, createdErr := service.MemberCreatedOrganization(r.Context(), s.db, user.ID)
				if createdErr != nil {
					log.Printf("check member created organization: %v", createdErr)
					http.Error(w, "could not load organization", http.StatusInternalServerError)
					return
				}
				if createdOrg {
					http.Redirect(w, r, "/my-hopshare?error="+url.QueryEscape("You are not an owner of any organization."), http.StatusSeeOther)
					return
				}
				http.Redirect(w, r, "/organizations/create?error="+url.QueryEscape("You need to create an organization first."), http.StatusSeeOther)
				return
			}
			log.Printf("load default owned organization: %v", err)
			http.Error(w, "could not load organization", http.StatusInternalServerError)
			return
		}
	}

	memberID, err := strconv.ParseInt(r.FormValue("member_id"), 10, 64)
	if err != nil || memberID <= 0 {
		query := url.Values{}
		if org.ID != 0 {
			query.Set("org_id", strconv.FormatInt(org.ID, 10))
		}
		query.Set("tab", "members")
		query.Set("error", "Invalid member.")
		http.Redirect(w, r, "/organizations/manage?"+query.Encode(), http.StatusSeeOther)
		return
	}

	if err := service.RemoveOrganizationMember(r.Context(), s.db, org.ID, memberID, user.ID); err != nil {
		log.Printf("remove member %d from org %d: %v", memberID, org.ID, err)
		msg := "Could not remove member."
		if errors.Is(err, service.ErrMembershipNotFound) {
			msg = "Membership not found."
		} else if errors.Is(err, service.ErrInvalidRoleChange) {
			msg = "Cannot remove the last owner from an organization."
		}
		query := url.Values{}
		if org.ID != 0 {
			query.Set("org_id", strconv.FormatInt(org.ID, 10))
		}
		query.Set("tab", "members")
		query.Set("error", msg)
		http.Redirect(w, r, "/organizations/manage?"+query.Encode(), http.StatusSeeOther)
		return
	}

	query := url.Values{}
	if org.ID != 0 {
		query.Set("org_id", strconv.FormatInt(org.ID, 10))
	}
	query.Set("tab", "members")
	query.Set("success", "Member removed.")
	http.Redirect(w, r, "/organizations/manage?"+query.Encode(), http.StatusSeeOther)
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
				createdOrg, createdErr := service.MemberCreatedOrganization(r.Context(), s.db, user.ID)
				if createdErr != nil {
					log.Printf("check member created organization: %v", createdErr)
					http.Error(w, "could not load organization", http.StatusInternalServerError)
					return
				}
				if createdOrg {
					http.Redirect(w, r, "/my-hopshare?error="+url.QueryEscape("You are not an owner of any organization."), http.StatusSeeOther)
					return
				}
				http.Redirect(w, r, "/organizations/create?error="+url.QueryEscape("You need to create an organization first."), http.StatusSeeOther)
				return
			}
			log.Printf("load default owned organization: %v", err)
			http.Error(w, "could not load organization", http.StatusInternalServerError)
			return
		}
	}

	memberID, err := strconv.ParseInt(r.FormValue("member_id"), 10, 64)
	if err != nil || memberID <= 0 {
		query := url.Values{}
		if org.ID != 0 {
			query.Set("org_id", strconv.FormatInt(org.ID, 10))
		}
		query.Set("tab", "members")
		query.Set("error", "Invalid member.")
		http.Redirect(w, r, "/organizations/manage?"+query.Encode(), http.StatusSeeOther)
		return
	}
	action := strings.ToLower(strings.TrimSpace(r.FormValue("action")))

	switch action {
	case "make_owner":
		if err := service.UpdateOrganizationMemberRole(r.Context(), s.db, org.ID, memberID, true); err != nil {
			log.Printf("make owner member=%d org=%d: %v", memberID, org.ID, err)
			query := url.Values{}
			if org.ID != 0 {
				query.Set("org_id", strconv.FormatInt(org.ID, 10))
			}
			query.Set("tab", "members")
			query.Set("error", "Could not update member role.")
			http.Redirect(w, r, "/organizations/manage?"+query.Encode(), http.StatusSeeOther)
			return
		}
		query := url.Values{}
		if org.ID != 0 {
			query.Set("org_id", strconv.FormatInt(org.ID, 10))
		}
		query.Set("tab", "members")
		query.Set("success", "Member promoted to owner.")
		http.Redirect(w, r, "/organizations/manage?"+query.Encode(), http.StatusSeeOther)
	case "revoke_owner":
		if err := service.UpdateOrganizationMemberRole(r.Context(), s.db, org.ID, memberID, false); err != nil {
			log.Printf("revoke owner member=%d org=%d: %v", memberID, org.ID, err)
			query := url.Values{}
			if org.ID != 0 {
				query.Set("org_id", strconv.FormatInt(org.ID, 10))
			}
			query.Set("tab", "members")
			query.Set("error", "Could not update member role.")
			http.Redirect(w, r, "/organizations/manage?"+query.Encode(), http.StatusSeeOther)
			return
		}
		query := url.Values{}
		if org.ID != 0 {
			query.Set("org_id", strconv.FormatInt(org.ID, 10))
		}
		query.Set("tab", "members")
		query.Set("success", "Owner role revoked.")
		http.Redirect(w, r, "/organizations/manage?"+query.Encode(), http.StatusSeeOther)
	default:
		query := url.Values{}
		if org.ID != 0 {
			query.Set("org_id", strconv.FormatInt(org.ID, 10))
		}
		query.Set("tab", "members")
		query.Set("error", "Unknown action.")
		http.Redirect(w, r, "/organizations/manage?"+query.Encode(), http.StatusSeeOther)
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
