package templates

import (
	"strconv"

	"hopshare/internal/types"
)

type organizationThemeOption struct {
	Value       string
	Label       string
	Description string
}

func organizationThemeOptions() []organizationThemeOption {
	return []organizationThemeOption{
		{
			Value:       types.OrganizationThemeDefault,
			Label:       "Default",
			Description: "Matches the rest of the app.",
		},
		{
			Value:       types.OrganizationThemeBright,
			Label:       "Bright",
			Description: "Vivid and energetic.",
		},
		{
			Value:       types.OrganizationThemeSerious,
			Label:       "Serious",
			Description: "Darker and more professional.",
		},
		{
			Value:       types.OrganizationThemeFun,
			Label:       "Fun",
			Description: "Playful and colorful.",
		},
	}
}

func organizationThemeValue(theme string) string {
	return types.NormalizeOrganizationTheme(theme)
}

func organizationPageThemeClass(theme string) string {
	return "organization-page theme-" + organizationThemeValue(theme)
}

func organizationBannerURL(org types.Organization) string {
	if !org.HasBanner {
		return ""
	}
	return "/organizations/banner?org_id=" + strconv.FormatInt(org.ID, 10) + "&v=" + strconv.FormatInt(org.UpdatedAt.Unix(), 10)
}

func organizationHeroStyle(org types.Organization) string {
	bannerURL := organizationBannerURL(org)
	if bannerURL == "" {
		return ""
	}
	return "background-image: url('" + bannerURL + "');"
}
