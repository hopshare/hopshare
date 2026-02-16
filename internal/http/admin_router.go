package http

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"hopshare/internal/service"
	"hopshare/web/templates"
)

const (
	adminTabApp                 = "app"
	adminOverviewLeaderboardCap = 5
)

func (s *Server) handleAdmin(w http.ResponseWriter, r *http.Request) {
	user := s.currentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	activeTab := normalizeAdminTab(r.URL.Query().Get("tab"))

	overview, err := service.AdminAppOverview(r.Context(), s.db, adminOverviewLeaderboardCap)
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

	render(w, r, templates.Admin(s.currentUserEmailPtr(r), activeTab, overview))
}

func normalizeAdminTab(raw string) string {
	tab := strings.TrimSpace(strings.ToLower(raw))
	switch tab {
	case "", adminTabApp:
		return adminTabApp
	default:
		return adminTabApp
	}
}
