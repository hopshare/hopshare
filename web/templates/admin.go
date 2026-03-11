package templates

import (
	"context"
	"strconv"
	"strings"

	"hopshare/internal/types"
)

type adminContextKey struct{}

func WithAdmin(ctx context.Context, isAdmin bool) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, adminContextKey{}, isAdmin)
}

func IsAdmin(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	isAdmin, _ := ctx.Value(adminContextKey{}).(bool)
	return isAdmin
}

const (
	pageSectionMyHopShare    = "my-hopshare"
	pageSectionMessages      = "messages"
	pageSectionProfile       = "profile"
	pageSectionAdmin         = "admin"
	pageSectionOrganizations = "organizations"
	pageSectionHelp          = "help"
)

type PageBreadcrumb struct {
	Label string
	Href  string
}

type PageOrganization struct {
	Name    string
	Href    string
	LogoSrc string
}

type PageContext struct {
	BrowserTitle  string
	ActiveSection string
	Breadcrumbs   []PageBreadcrumb
	CurrentOrg    *PageOrganization
}

func defaultPageContext(browserTitle string) PageContext {
	return PageContext{BrowserTitle: browserTitle}
}

func newPageContext(browserTitle string, activeSection string, breadcrumbs []PageBreadcrumb, currentOrg *PageOrganization) PageContext {
	return PageContext{
		BrowserTitle:  browserTitle,
		ActiveSection: strings.TrimSpace(activeSection),
		Breadcrumbs:   compactBreadcrumbs(breadcrumbs),
		CurrentOrg:    currentOrg,
	}
}

func newBreadcrumb(label string, href string) PageBreadcrumb {
	return PageBreadcrumb{
		Label: strings.TrimSpace(label),
		Href:  strings.TrimSpace(href),
	}
}

func compactBreadcrumbs(in []PageBreadcrumb) []PageBreadcrumb {
	out := make([]PageBreadcrumb, 0, len(in))
	for _, item := range in {
		label := strings.TrimSpace(item.Label)
		if label == "" {
			continue
		}
		out = append(out, PageBreadcrumb{
			Label: label,
			Href:  strings.TrimSpace(item.Href),
		})
	}
	return out
}

func breadcrumbIsCurrent(items []PageBreadcrumb, idx int) bool {
	return idx >= 0 && idx == len(items)-1
}

func breadcrumbCurrent(items []PageBreadcrumb) *PageBreadcrumb {
	if len(items) == 0 {
		return nil
	}
	item := items[len(items)-1]
	return &item
}

func breadcrumbBack(items []PageBreadcrumb) *PageBreadcrumb {
	if len(items) < 2 {
		return nil
	}
	item := items[len(items)-2]
	if item.Href == "" {
		return nil
	}
	return &item
}

func headerTextLinkClass(active bool) string {
	if active {
		return "rounded-lg bg-sky-100 px-3 py-2 font-semibold text-sky-800"
	}
	return "rounded-lg px-3 py-2 hover:bg-slate-50 hover:text-slate-900"
}

func headerIconLinkClass(active bool) string {
	if active {
		return "bg-sky-100 text-sky-800"
	}
	return "text-slate-700 hover:bg-slate-50 hover:text-slate-900"
}

func headerProfileButtonClass(active bool) string {
	if active {
		return "flex h-9 w-9 items-center justify-center rounded-full ring-2 ring-sky-500 ring-offset-2 ring-offset-white"
	}
	return "flex h-9 w-9 items-center justify-center rounded-full ring-1 ring-slate-200 hover:ring-slate-300"
}

func mobileMenuLinkClass(active bool) string {
	if active {
		return "block rounded-lg bg-sky-100 px-4 py-3 font-semibold text-sky-800"
	}
	return "block rounded-lg px-4 py-3 font-semibold text-slate-800 hover:bg-slate-50"
}

func breadcrumbLinkClass(active bool) string {
	if active {
		return "font-semibold text-slate-900"
	}
	return "text-slate-600 hover:text-slate-900"
}

func memberRootHref(currentOrgID int64) string {
	if currentOrgID > 0 {
		return "/my-hopshare?org_id=" + strconv.FormatInt(currentOrgID, 10)
	}
	return "/my-hopshare"
}

func organizationHref(org types.Organization) string {
	if strings.TrimSpace(org.URLName) == "" {
		return ""
	}
	return "/organization/" + org.URLName
}

func organizationLogoSrc(org types.Organization) string {
	if org.ID == 0 {
		return ""
	}
	return "/organizations/logo?org_id=" + strconv.FormatInt(org.ID, 10) + "&v=" + strconv.FormatInt(org.UpdatedAt.Unix(), 10)
}

func pageOrganizationFromOrg(org types.Organization) *PageOrganization {
	if org.ID == 0 || strings.TrimSpace(org.Name) == "" {
		return nil
	}
	return &PageOrganization{
		Name:    org.Name,
		Href:    organizationHref(org),
		LogoSrc: organizationLogoSrc(org),
	}
}

func pageOrganizationFromMemberOrgs(orgs []types.Organization, currentOrgID int64) *PageOrganization {
	for _, org := range orgs {
		if org.ID == currentOrgID {
			return pageOrganizationFromOrg(org)
		}
	}
	return nil
}

func organizationByID(orgs []types.Organization, currentOrgID int64) *types.Organization {
	for i := range orgs {
		if orgs[i].ID == currentOrgID {
			return &orgs[i]
		}
	}
	return nil
}

func myHopSharePageContext(orgs []types.Organization, currentOrgID int64) PageContext {
	return newPageContext(
		"hopShare | My hopShare",
		pageSectionMyHopShare,
		[]PageBreadcrumb{
			newBreadcrumb("My hopShare", ""),
		},
		pageOrganizationFromMemberOrgs(orgs, currentOrgID),
	)
}

func myHopShareOrganizationsPageContext(orgs []types.Organization, currentOrgID int64) PageContext {
	return newPageContext(
		"hopShare | Organization Switcher",
		pageSectionMyHopShare,
		[]PageBreadcrumb{
			newBreadcrumb("My hopShare", memberRootHref(currentOrgID)),
			newBreadcrumb("Switch organization", ""),
		},
		pageOrganizationFromMemberOrgs(orgs, currentOrgID),
	)
}

func myHopsPageContext(orgs []types.Organization, currentOrgID int64, viewKey string) PageContext {
	breadcrumbs := []PageBreadcrumb{
		newBreadcrumb("My hopShare", memberRootHref(currentOrgID)),
	}
	if org := organizationByID(orgs, currentOrgID); org != nil {
		breadcrumbs = append(breadcrumbs, newBreadcrumb(org.Name, organizationHref(*org)))
	}
	breadcrumbs = append(breadcrumbs,
		newBreadcrumb("My Hops", "/my-hops?org_id="+strconv.FormatInt(currentOrgID, 10)),
		newBreadcrumb(myHopsViewLabel(viewKey), ""),
	)
	return newPageContext("hopShare | My hops", pageSectionMyHopShare, breadcrumbs, pageOrganizationFromMemberOrgs(orgs, currentOrgID))
}

func requestHopPageContext(orgs []types.Organization, currentOrgID int64) PageContext {
	breadcrumbs := []PageBreadcrumb{
		newBreadcrumb("My hopShare", memberRootHref(currentOrgID)),
	}
	if org := organizationByID(orgs, currentOrgID); org != nil {
		breadcrumbs = append(breadcrumbs, newBreadcrumb(org.Name, organizationHref(*org)))
	}
	breadcrumbs = append(breadcrumbs, newBreadcrumb("Request a hop", ""))
	return newPageContext("hopShare | Request a hop", pageSectionMyHopShare, breadcrumbs, pageOrganizationFromMemberOrgs(orgs, currentOrgID))
}

func completeHopPageContext(org types.Organization) PageContext {
	return newPageContext(
		"hopShare | Complete a hop",
		pageSectionMyHopShare,
		[]PageBreadcrumb{
			newBreadcrumb("My hopShare", memberRootHref(org.ID)),
			newBreadcrumb(org.Name, organizationHref(org)),
			newBreadcrumb("Complete a hop", ""),
		},
		pageOrganizationFromOrg(org),
	)
}

func messagesPageContext(selected *types.Message) PageContext {
	breadcrumbs := []PageBreadcrumb{
		newBreadcrumb("My hopShare", "/my-hopshare"),
		newBreadcrumb("Messages", "/messages"),
	}
	if selected != nil {
		subject := strings.TrimSpace(selected.Subject)
		if subject == "" {
			subject = "Message"
		}
		breadcrumbs = append(breadcrumbs, newBreadcrumb(subject, ""))
	} else {
		breadcrumbs[len(breadcrumbs)-1].Href = ""
	}
	return newPageContext("hopShare | Messages", pageSectionMessages, breadcrumbs, nil)
}

func profilePageContext(activeTab string) PageContext {
	breadcrumbs := []PageBreadcrumb{
		newBreadcrumb("My hopShare", "/my-hopshare"),
		newBreadcrumb("Profile", "/profile"),
	}
	if tab := profileTabLabel(activeTab); tab != "Details" {
		breadcrumbs = append(breadcrumbs, newBreadcrumb(tab, ""))
	} else {
		breadcrumbs[len(breadcrumbs)-1].Href = ""
	}
	return newPageContext("hopShare | My Profile", pageSectionProfile, breadcrumbs, nil)
}

func manageOrganizationPageContext(org types.Organization, activeTab string) PageContext {
	breadcrumbs := []PageBreadcrumb{
		newBreadcrumb("My hopShare", memberRootHref(org.ID)),
		newBreadcrumb(org.Name, organizationHref(org)),
		newBreadcrumb("Manage organization", "/organizations/manage?org_id="+strconv.FormatInt(org.ID, 10)),
	}
	if tab := manageOrganizationTabLabel(activeTab); tab != "Details" {
		breadcrumbs = append(breadcrumbs, newBreadcrumb(tab, ""))
	} else {
		breadcrumbs[len(breadcrumbs)-1].Href = ""
	}
	return newPageContext("hopShare | Manage organization", pageSectionMyHopShare, breadcrumbs, pageOrganizationFromOrg(org))
}

func createOrganizationPageContext() PageContext {
	return newPageContext(
		"hopShare | Create organization",
		pageSectionMyHopShare,
		[]PageBreadcrumb{
			newBreadcrumb("My hopShare", "/my-hopshare"),
			newBreadcrumb("Create organization", ""),
		},
		nil,
	)
}

func organizationsPageContext() PageContext {
	return newPageContext(
		"hopShare | Organizations",
		pageSectionOrganizations,
		[]PageBreadcrumb{
			newBreadcrumb("Organizations", ""),
		},
		nil,
	)
}

func organizationPageContext(isMemberView bool, org types.Organization) PageContext {
	rootLabel := "Organizations"
	rootHref := "/organizations"
	activeSection := pageSectionOrganizations
	if isMemberView {
		rootLabel = "My hopShare"
		rootHref = memberRootHref(org.ID)
		activeSection = pageSectionMyHopShare
	}
	return newPageContext(
		"hopShare | Organization",
		activeSection,
		[]PageBreadcrumb{
			newBreadcrumb(rootLabel, rootHref),
			newBreadcrumb(org.Name, ""),
		},
		pageOrganizationFromOrg(org),
	)
}

func adminPageContext(activeTab string) PageContext {
	breadcrumbs := []PageBreadcrumb{
		newBreadcrumb("Admin", "/admin"),
	}
	if tab := adminTabLabel(activeTab); tab != "App Overview" {
		breadcrumbs = append(breadcrumbs, newBreadcrumb(tab, ""))
	} else {
		breadcrumbs[0].Href = ""
	}
	return newPageContext("hopShare | Admin", pageSectionAdmin, breadcrumbs, nil)
}

func helpPageContext() PageContext {
	return newPageContext(
		"hopShare | Help",
		pageSectionHelp,
		[]PageBreadcrumb{
			newBreadcrumb("My hopShare", "/my-hopshare"),
			newBreadcrumb("Help", ""),
		},
		nil,
	)
}

func myHopsViewLabel(viewKey string) string {
	switch strings.ToLower(strings.TrimSpace(viewKey)) {
	case "helped":
		return "Helped"
	case "offered":
		return "Offered"
	default:
		return "Requested"
	}
}

func profileTabLabel(activeTab string) string {
	switch strings.ToLower(strings.TrimSpace(activeTab)) {
	case "organizations":
		return "Organizations"
	case "skills":
		return "Skills"
	case "account":
		return "My Account"
	default:
		return "Details"
	}
}

func manageOrganizationTabLabel(activeTab string) string {
	switch strings.ToLower(strings.TrimSpace(activeTab)) {
	case "members":
		return "Members"
	case "skills":
		return "Skills"
	case "timebank":
		return "Time Bank"
	case "invite":
		return "Invite"
	default:
		return "Details"
	}
}

func adminTabLabel(activeTab string) string {
	switch strings.ToLower(strings.TrimSpace(activeTab)) {
	case "organizations":
		return "Organizations"
	case "moderation":
		return "Moderation"
	case "users":
		return "Users"
	case "messages":
		return "Messages"
	case "audit":
		return "Audit"
	default:
		return "App Overview"
	}
}
