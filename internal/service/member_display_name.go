package service

import "strings"

func memberDisplayName(firstName, lastName, fallback string) string {
	full := strings.TrimSpace(strings.TrimSpace(firstName) + " " + strings.TrimSpace(lastName))
	if full != "" {
		return full
	}
	return strings.TrimSpace(fallback)
}
