package http

import (
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"hopshare/internal/service"
	"hopshare/web/templates"
)

func (s *Server) handleProfile(w http.ResponseWriter, r *http.Request) {
	user := s.currentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	successMsg := r.URL.Query().Get("success")
	errorMsg := r.URL.Query().Get("error")

	switch r.Method {
	case http.MethodGet:
		member, err := service.GetMemberByID(r.Context(), s.db, user.ID)
		if err != nil {
			log.Printf("load member profile %d: %v", user.ID, err)
			http.Error(w, "could not load profile", http.StatusInternalServerError)
			return
		}
		orgs, err := service.MemberOrganizations(r.Context(), s.db, user.ID)
		if err != nil {
			log.Printf("load member organizations %d: %v", user.ID, err)
			http.Error(w, "could not load profile", http.StatusInternalServerError)
			return
		}
		render(w, r, templates.MyProfile(user.Email, member, orgs, successMsg, errorMsg))
	case http.MethodPost:
		const maxAvatarUploadBytes = 20 << 20
		const maxBodyBytes = maxAvatarUploadBytes + (1 << 20)
		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		if err := r.ParseMultipartForm(maxBodyBytes); err != nil && !errors.Is(err, http.ErrNotMultipart) {
			http.Error(w, "invalid form", http.StatusBadRequest)
			return
		}

		action := strings.TrimSpace(r.FormValue("action"))
		switch action {
		case "profile":
			firstName := strings.TrimSpace(r.FormValue("first_name"))
			lastName := strings.TrimSpace(r.FormValue("last_name"))
			email := strings.TrimSpace(r.FormValue("email"))
			preferredContactMethod := strings.TrimSpace(r.FormValue("preferred_contact_method"))
			preferredContact := strings.TrimSpace(r.FormValue("preferred_contact"))
			city := strings.TrimSpace(r.FormValue("city"))
			state := strings.TrimSpace(r.FormValue("state"))

			avatarData, avatarContentType, hasAvatar, err := readAvatarUpload(r, "avatar_file", maxAvatarUploadBytes)
			if err != nil {
				http.Redirect(w, r, "/profile?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
				return
			}

			if err := service.UpdateMemberProfile(r.Context(), s.db, user.ID, firstName, lastName, email, preferredContactMethod, preferredContact, city, state); err != nil {
				msg := "Could not update profile."
				switch {
				case errors.Is(err, service.ErrMissingField):
					msg = "Name, email, contact method, and preferred contact are required."
				case errors.Is(err, service.ErrInvalidContactMethod):
					msg = "Please choose a preferred contact method."
				}
				log.Printf("update member profile %d: %v", user.ID, err)
				http.Redirect(w, r, "/profile?error="+url.QueryEscape(msg), http.StatusSeeOther)
				return
			}

			if hasAvatar {
				if err := service.SetMemberAvatar(r.Context(), s.db, user.ID, avatarContentType, avatarData); err != nil {
					log.Printf("set member avatar %d: %v", user.ID, err)
					http.Redirect(w, r, "/profile?error="+url.QueryEscape("Profile updated, but avatar upload failed."), http.StatusSeeOther)
					return
				}
			}

			http.Redirect(w, r, "/profile?success="+url.QueryEscape("Profile updated."), http.StatusSeeOther)
		case "password":
			currentPassword := r.FormValue("current_password")
			newPassword := r.FormValue("new_password")
			confirmPassword := r.FormValue("confirm_password")

			if currentPassword == "" || newPassword == "" || confirmPassword == "" {
				http.Redirect(w, r, "/profile?error="+url.QueryEscape("Please fill out all password fields."), http.StatusSeeOther)
				return
			}
			if newPassword != confirmPassword {
				http.Redirect(w, r, "/profile?error="+url.QueryEscape("New passwords do not match."), http.StatusSeeOther)
				return
			}
			if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(currentPassword)); err != nil {
				http.Redirect(w, r, "/profile?error="+url.QueryEscape("Current password is incorrect."), http.StatusSeeOther)
				return
			}

			passwordHash, err := service.HashPassword(newPassword)
			if err != nil {
				log.Printf("hash password failed: %v", err)
				http.Redirect(w, r, "/profile?error="+url.QueryEscape("Could not update password right now."), http.StatusSeeOther)
				return
			}
			if err := service.UpdateMemberPassword(r.Context(), s.db, user.ID, passwordHash); err != nil {
				log.Printf("update member password %d: %v", user.ID, err)
				http.Redirect(w, r, "/profile?error="+url.QueryEscape("Could not update password right now."), http.StatusSeeOther)
				return
			}

			http.Redirect(w, r, "/profile?success="+url.QueryEscape("Password updated."), http.StatusSeeOther)
		default:
			http.Error(w, "invalid form", http.StatusBadRequest)
		}
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleMemberAvatar(w http.ResponseWriter, r *http.Request) {
	user := s.currentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	memberID := user.ID
	target := user
	if memberIDStr := strings.TrimSpace(r.URL.Query().Get("member_id")); memberIDStr != "" {
		parsed, err := strconv.ParseInt(memberIDStr, 10, 64)
		if err != nil || parsed <= 0 {
			http.Error(w, "invalid member", http.StatusBadRequest)
			return
		}
		memberID = parsed
		if memberID != user.ID {
			shared, err := service.MembersShareOrganization(r.Context(), s.db, user.ID, memberID)
			if err != nil {
				log.Printf("check shared organization member=%d other=%d: %v", user.ID, memberID, err)
				http.Error(w, "could not load avatar", http.StatusInternalServerError)
				return
			}
			if !shared {
				http.NotFound(w, r)
				return
			}
			member, err := service.GetMemberByID(r.Context(), s.db, memberID)
			if err != nil {
				log.Printf("load member %d: %v", memberID, err)
				http.NotFound(w, r)
				return
			}
			target = &member
		}
	}

	data, contentType, ok, err := service.MemberAvatar(r.Context(), s.db, memberID)
	if err != nil {
		log.Printf("load member avatar %d: %v", memberID, err)
		http.Error(w, "could not load avatar", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	if ok {
		w.Header().Set("Content-Type", contentType)
		_, _ = w.Write(data)
		return
	}

	initial := avatarInitial(memberDisplayName(target))
	if initial == "" {
		initial = avatarInitial(target.Email)
	}
	w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
	_, _ = w.Write(avatarPlaceholderSVG(initial))
}

func (s *Server) handlePublicMemberAvatar(w http.ResponseWriter, r *http.Request) {
	memberID, err := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("member_id")), 10, 64)
	if err != nil || memberID <= 0 {
		http.Error(w, "invalid member", http.StatusBadRequest)
		return
	}
	orgID, err := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("org_id")), 10, 64)
	if err != nil || orgID <= 0 {
		http.Error(w, "invalid organization", http.StatusBadRequest)
		return
	}

	isMember, err := service.MemberHasActiveMembership(r.Context(), s.db, memberID, orgID)
	if err != nil {
		log.Printf("check public avatar membership member=%d org=%d: %v", memberID, orgID, err)
		http.Error(w, "could not load avatar", http.StatusInternalServerError)
		return
	}
	if !isMember {
		http.NotFound(w, r)
		return
	}

	target, err := service.GetMemberByID(r.Context(), s.db, memberID)
	if err != nil {
		log.Printf("load member for public avatar member=%d: %v", memberID, err)
		http.NotFound(w, r)
		return
	}

	data, contentType, ok, err := service.MemberAvatar(r.Context(), s.db, memberID)
	if err != nil {
		log.Printf("load public member avatar %d: %v", memberID, err)
		http.Error(w, "could not load avatar", http.StatusInternalServerError)
		return
	}

	// Public avatar URLs are stable and don't include a version token, so keep
	// caching short to balance freshness and browser/network efficiency.
	w.Header().Set("Cache-Control", "public, max-age=3600")
	if ok {
		w.Header().Set("Content-Type", contentType)
		_, _ = w.Write(data)
		return
	}

	initial := avatarInitial(memberDisplayName(&target))
	if initial == "" {
		initial = avatarInitial(target.Email)
	}
	w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
	_, _ = w.Write(avatarPlaceholderSVG(initial))
}

func readAvatarUpload(r *http.Request, field string, maxBytes int64) ([]byte, string, bool, error) {
	f, _, err := r.FormFile(field)
	if err != nil {
		if errors.Is(err, http.ErrMissingFile) {
			return nil, "", false, nil
		}
		return nil, "", false, fmt.Errorf("read avatar file: %w", err)
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil {
		return nil, "", false, fmt.Errorf("read avatar file: %w", err)
	}
	if int64(len(data)) > maxBytes {
		return nil, "", false, fmt.Errorf("avatar file too large (max 20MB)")
	}
	if len(data) == 0 {
		return nil, "", false, fmt.Errorf("avatar file is empty")
	}

	contentType := http.DetectContentType(data)
	switch contentType {
	case "image/png", "image/jpeg":
		return data, contentType, true, nil
	default:
		return nil, "", false, fmt.Errorf("avatar must be a PNG or JPEG")
	}
}

func avatarInitial(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	runes := []rune(name)
	if len(runes) == 0 {
		return ""
	}
	return strings.ToUpper(string(runes[0]))
}

func avatarPlaceholderSVG(initial string) []byte {
	safe := html.EscapeString(initial)
	svg := fmt.Sprintf(
		`<svg xmlns="http://www.w3.org/2000/svg" width="128" height="128" viewBox="0 0 128 128"><rect width="128" height="128" rx="64" fill="#e2e8f0"/><text x="50%%" y="54%%" text-anchor="middle" dominant-baseline="middle" font-family="Arial, sans-serif" font-size="56" fill="#64748b">%s</text></svg>`,
		safe,
	)
	return []byte(svg)
}
