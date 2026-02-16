package http

import (
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
	activeTab := normalizeAdminTab(r.URL.Query().Get("tab"))

	overview, err := service.AdminAppOverview(r.Context(), s.db, adminOverviewLeaderboardCap)
	if err != nil {
		log.Printf("load admin app overview: %v", err)
		http.Error(w, "could not load admin overview", http.StatusInternalServerError)
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
