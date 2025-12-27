package templates

import (
	"strings"

	"hopshare/internal/types"
)

func RequestFilterStatus(status string) string {
	switch status {
	case types.RequestStatusOpen:
		return "created"
	case types.RequestStatusAccepted:
		return "accepted"
	case types.RequestStatusCanceled:
		return "canceled"
	case types.RequestStatusCompleted:
		return "completed"
	default:
		return status
	}
}

func RequestSearchText(req types.Request) string {
	var b strings.Builder
	b.WriteString(req.Title)
	b.WriteString(" ")
	b.WriteString(req.CreatedByName)
	if req.Details != nil && *req.Details != "" {
		b.WriteString(" ")
		b.WriteString(*req.Details)
	}
	if req.AcceptedByName != nil && *req.AcceptedByName != "" {
		b.WriteString(" ")
		b.WriteString(*req.AcceptedByName)
	}
	if req.CompletionComment != nil && *req.CompletionComment != "" {
		b.WriteString(" ")
		b.WriteString(*req.CompletionComment)
	}
	return b.String()
}
