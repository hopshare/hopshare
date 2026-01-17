package templates

import (
	"strings"

	"hopshare/internal/types"
)

func HopFilterStatus(status string) string {
	switch status {
	case types.HopStatusOpen:
		return "created"
	case types.HopStatusAccepted:
		return "accepted"
	case types.HopStatusCanceled:
		return "canceled"
	case types.HopStatusCompleted:
		return "completed"
	default:
		return status
	}
}

func HopSearchText(hop types.Hop) string {
	var b strings.Builder
	b.WriteString(hop.Title)
	b.WriteString(" ")
	b.WriteString(hop.CreatedByName)
	if hop.Details != nil && *hop.Details != "" {
		b.WriteString(" ")
		b.WriteString(*hop.Details)
	}
	if hop.AcceptedByName != nil && *hop.AcceptedByName != "" {
		b.WriteString(" ")
		b.WriteString(*hop.AcceptedByName)
	}
	if hop.CompletionComment != nil && *hop.CompletionComment != "" {
		b.WriteString(" ")
		b.WriteString(*hop.CompletionComment)
	}
	return b.String()
}
