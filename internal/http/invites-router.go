package http

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"hopshare/internal/service"
	"hopshare/web/templates"
)

func (s *Server) handleInvite(w http.ResponseWriter, r *http.Request) {
	token := sanitizeInviteToken(r.URL.Query().Get("token"))
	if token == "" {
		render(w, r, templates.InviteStatus(
			s.currentUserEmailPtr(r),
			"Invitation not found",
			"This invitation link is invalid. Please ask an organization owner for a new invitation.",
			"/organizations",
			"Find organizations",
			"/",
			"Return home",
		))
		return
	}

	if !s.featureEmail {
		render(w, r, templates.InviteStatus(
			s.currentUserEmailPtr(r),
			"Invitations are currently unavailable",
			"Email-based invitations are currently disabled.",
			"/",
			"Return home",
			"",
			"",
		))
		return
	}

	resolution, err := service.ResolveOrganizationInvite(r.Context(), s.db, token, time.Now().UTC())
	if err != nil {
		switch {
		case errors.Is(err, service.ErrInviteExpired):
			primaryHref := "/organizations"
			if resolution.Organization.URLName != "" {
				primaryHref = "/organization/" + resolution.Organization.URLName
			}
			render(w, r, templates.InviteStatus(
				s.currentUserEmailPtr(r),
				"Invitation expired",
				"This invitation has expired. Contact an organization owner for a new invitation.",
				primaryHref,
				"Open organization page",
				"/",
				"Return home",
			))
			return
		case errors.Is(err, service.ErrOrganizationDisabled):
			render(w, r, templates.InviteStatus(
				s.currentUserEmailPtr(r),
				"Organization unavailable",
				"Sorry, this Organization no longer exists. Feel free to look around for another Organization that you might recognize.",
				"/organizations",
				"Find organizations",
				"/",
				"Return home",
			))
			return
		default:
			render(w, r, templates.InviteStatus(
				s.currentUserEmailPtr(r),
				"Invitation not found",
				"This invitation link is invalid. Please ask an organization owner for a new invitation.",
				"/organizations",
				"Find organizations",
				"/",
				"Return home",
			))
			return
		}
	}

	user := s.currentUser(r)
	if user == nil {
		inviteNext := "/invite?token=" + url.QueryEscape(token)
		loginHref := "/login?email=" + url.QueryEscape(resolution.InvitedEmail) + "&next=" + url.QueryEscape(inviteNext)
		signupHref := "/signup?invite_token=" + url.QueryEscape(token)
		render(w, r, templates.InviteLanding(
			s.currentUserEmailPtr(r),
			resolution.Organization,
			resolution.InvitedEmail,
			signupHref,
			loginHref,
		))
		return
	}

	if !strings.EqualFold(strings.TrimSpace(user.Email), strings.TrimSpace(resolution.InvitedEmail)) {
		render(w, r, templates.InviteStatus(
			s.currentUserEmailPtr(r),
			"Wrong account for this invitation",
			"This invite was sent to "+resolution.InvitedEmail+". Please log out and continue with that email address.",
			"/my-hopshare",
			"Go to My hopShare",
			"/organizations",
			"Find organizations",
		))
		return
	}

	accepted, acceptErr := service.AcceptOrganizationInvite(r.Context(), s.db, token, user.ID, user.Email, time.Now().UTC())
	if acceptErr != nil {
		switch {
		case errors.Is(acceptErr, service.ErrInviteExpired):
			render(w, r, templates.InviteStatus(
				s.currentUserEmailPtr(r),
				"Invitation expired",
				"This invitation has expired. Contact an organization owner for a new invitation.",
				"/organization/"+resolution.Organization.URLName,
				"Open organization page",
				"/",
				"Return home",
			))
			return
		case errors.Is(acceptErr, service.ErrOrganizationDisabled):
			render(w, r, templates.InviteStatus(
				s.currentUserEmailPtr(r),
				"Organization unavailable",
				"Sorry, this Organization no longer exists. Feel free to look around for another Organization that you might recognize.",
				"/organizations",
				"Find organizations",
				"/",
				"Return home",
			))
			return
		case errors.Is(acceptErr, service.ErrInviteEmailMismatch):
			render(w, r, templates.InviteStatus(
				s.currentUserEmailPtr(r),
				"Wrong account for this invitation",
				"This invite was sent to "+resolution.InvitedEmail+". Please log out and continue with that email address.",
				"/my-hopshare",
				"Go to My hopShare",
				"/organizations",
				"Find organizations",
			))
			return
		default:
			render(w, r, templates.InviteStatus(
				s.currentUserEmailPtr(r),
				"Could not accept invitation",
				"Please try again in a moment.",
				"/organization/"+resolution.Organization.URLName,
				"Open organization page",
				"/",
				"Return home",
			))
			return
		}
	}

	success := "Invitation accepted."
	if accepted.AlreadyMember {
		success = "You're already in this organization."
	}
	http.Redirect(w, r, "/organization/"+accepted.Organization.URLName+"?success="+url.QueryEscape(success), http.StatusSeeOther)
}
