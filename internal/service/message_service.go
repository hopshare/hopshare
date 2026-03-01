package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"hopshare/internal/types"
)

const defaultConversationMessageLimit = 100

type SendMessageParams struct {
	SenderID    *int64
	SenderName  string
	RecipientID int64
	MessageType string
	HopID       *int64
	Subject     string
	Body        string
}

func SendMessage(ctx context.Context, db *sql.DB, p SendMessageParams) error {
	if db == nil {
		return ErrNilDB
	}
	if p.RecipientID == 0 {
		return ErrMissingMemberID
	}

	subject := strings.TrimSpace(p.Subject)
	body := strings.TrimSpace(p.Body)
	if subject == "" || body == "" {
		return ErrMissingField
	}

	messageType := strings.TrimSpace(p.MessageType)
	if messageType == "" {
		messageType = types.MessageTypeInformation
	}
	switch messageType {
	case types.MessageTypeInformation:
	case types.MessageTypeAction:
		if p.HopID == nil || *p.HopID == 0 {
			return ErrMissingField
		}
	default:
		return ErrInvalidMessage
	}

	senderName := strings.TrimSpace(p.SenderName)
	if p.SenderID != nil {
		if *p.SenderID == 0 {
			return ErrMissingMemberID
		}
		if senderName == "" {
			var firstName string
			var lastName string
			var email string
			if err := db.QueryRowContext(ctx, `
					SELECT first_name, last_name, email
					FROM members
					WHERE id = $1
				`, *p.SenderID).Scan(&firstName, &lastName, &email); err != nil {
				return fmt.Errorf("load sender name: %w", err)
			}
			senderName = memberDisplayName(firstName, lastName, email)
		}
	} else if senderName == "" {
		return ErrMissingField
	}

	if err := insertMessage(ctx, db, p.RecipientID, p.SenderID, senderName, messageType, p.HopID, nil, nil, subject, body); err != nil {
		return err
	}
	return nil
}

func ListMessages(ctx context.Context, db *sql.DB, recipientID int64) ([]types.Message, error) {
	if db == nil {
		return nil, ErrNilDB
	}
	if recipientID == 0 {
		return nil, ErrMissingMemberID
	}

	rows, err := db.QueryContext(ctx, `
		SELECT id, sender_member_id, sender_name, message_type, hop_id, action_status, action_taken_at, subject, read_at, created_at
		FROM messages
		WHERE recipient_member_id = $1
		ORDER BY created_at DESC
	`, recipientID)
	if err != nil {
		return nil, fmt.Errorf("list messages: %w", err)
	}
	defer rows.Close()

	var out []types.Message
	for rows.Next() {
		var msg types.Message
		var senderID sql.NullInt64
		var hopID sql.NullInt64
		var actionStatus sql.NullString
		var actionTakenAt sql.NullTime
		var readAt sql.NullTime
		if err := rows.Scan(
			&msg.ID,
			&senderID,
			&msg.SenderName,
			&msg.MessageType,
			&hopID,
			&actionStatus,
			&actionTakenAt,
			&msg.Subject,
			&readAt,
			&msg.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		msg.RecipientID = recipientID
		if senderID.Valid {
			msg.SenderID = &senderID.Int64
		}
		if hopID.Valid {
			msg.HopID = &hopID.Int64
		}
		if actionStatus.Valid {
			msg.ActionStatus = &actionStatus.String
		}
		if actionTakenAt.Valid {
			msg.ActionTakenAt = &actionTakenAt.Time
		}
		if readAt.Valid {
			msg.ReadAt = &readAt.Time
		}
		out = append(out, msg)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list messages: %w", err)
	}
	return out, nil
}

func ListMessagesBetweenMembers(ctx context.Context, db *sql.DB, memberAID, memberBID int64, limit int) ([]types.Message, error) {
	if db == nil {
		return nil, ErrNilDB
	}
	if memberAID == 0 || memberBID == 0 {
		return nil, ErrMissingMemberID
	}
	if memberAID == memberBID {
		return []types.Message{}, nil
	}
	if limit <= 0 {
		limit = defaultConversationMessageLimit
	}

	rows, err := db.QueryContext(ctx, `
		SELECT
			id,
			recipient_member_id,
			sender_member_id,
			sender_name,
			message_type,
			hop_id,
			action_status,
			action_taken_at,
			subject,
			body,
			read_at,
			created_at
		FROM (
			SELECT
				id,
				recipient_member_id,
				sender_member_id,
				sender_name,
				message_type,
				hop_id,
				action_status,
				action_taken_at,
				subject,
				body,
				read_at,
				created_at
			FROM messages
			WHERE
				(recipient_member_id = $1 AND sender_member_id = $2)
				OR (recipient_member_id = $2 AND sender_member_id = $1)
			ORDER BY created_at DESC, id DESC
			LIMIT $3
		) AS conversation
		ORDER BY created_at ASC, id ASC
	`, memberAID, memberBID, limit)
	if err != nil {
		return nil, fmt.Errorf("list member conversation: %w", err)
	}
	defer rows.Close()

	out := make([]types.Message, 0)
	for rows.Next() {
		var msg types.Message
		var senderID sql.NullInt64
		var hopID sql.NullInt64
		var actionStatus sql.NullString
		var actionTakenAt sql.NullTime
		var readAt sql.NullTime
		if err := rows.Scan(
			&msg.ID,
			&msg.RecipientID,
			&senderID,
			&msg.SenderName,
			&msg.MessageType,
			&hopID,
			&actionStatus,
			&actionTakenAt,
			&msg.Subject,
			&msg.Body,
			&readAt,
			&msg.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan member conversation message: %w", err)
		}
		if senderID.Valid {
			msg.SenderID = &senderID.Int64
		}
		if hopID.Valid {
			msg.HopID = &hopID.Int64
		}
		if actionStatus.Valid {
			msg.ActionStatus = &actionStatus.String
		}
		if actionTakenAt.Valid {
			msg.ActionTakenAt = &actionTakenAt.Time
		}
		if readAt.Valid {
			msg.ReadAt = &readAt.Time
		}
		out = append(out, msg)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list member conversation: %w", err)
	}
	return out, nil
}

func GetMessageForMember(ctx context.Context, db *sql.DB, messageID, recipientID int64) (types.Message, error) {
	if db == nil {
		return types.Message{}, ErrNilDB
	}
	if recipientID == 0 {
		return types.Message{}, ErrMissingMemberID
	}
	if messageID == 0 {
		return types.Message{}, ErrMessageNotFound
	}

	row := db.QueryRowContext(ctx, `
		SELECT id, recipient_member_id, sender_member_id, sender_name, message_type, hop_id, action_status, action_taken_at, subject, body, read_at, created_at
		FROM messages
		WHERE id = $1 AND recipient_member_id = $2
	`, messageID, recipientID)

	var msg types.Message
	var senderID sql.NullInt64
	var hopID sql.NullInt64
	var actionStatus sql.NullString
	var actionTakenAt sql.NullTime
	var readAt sql.NullTime
	if err := row.Scan(
		&msg.ID,
		&msg.RecipientID,
		&senderID,
		&msg.SenderName,
		&msg.MessageType,
		&hopID,
		&actionStatus,
		&actionTakenAt,
		&msg.Subject,
		&msg.Body,
		&readAt,
		&msg.CreatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return types.Message{}, ErrMessageNotFound
		}
		return types.Message{}, fmt.Errorf("get message: %w", err)
	}
	if senderID.Valid {
		msg.SenderID = &senderID.Int64
	}
	if hopID.Valid {
		msg.HopID = &hopID.Int64
	}
	if actionStatus.Valid {
		msg.ActionStatus = &actionStatus.String
	}
	if actionTakenAt.Valid {
		msg.ActionTakenAt = &actionTakenAt.Time
	}
	if readAt.Valid {
		msg.ReadAt = &readAt.Time
	}
	return msg, nil
}

func MarkMessageRead(ctx context.Context, db *sql.DB, messageID, recipientID int64, readAt time.Time) error {
	if db == nil {
		return ErrNilDB
	}
	if recipientID == 0 {
		return ErrMissingMemberID
	}
	if messageID == 0 {
		return ErrMessageNotFound
	}

	if _, err := db.ExecContext(ctx, `
		UPDATE messages
		SET read_at = $1
		WHERE id = $2 AND recipient_member_id = $3 AND read_at IS NULL
	`, readAt, messageID, recipientID); err != nil {
		return fmt.Errorf("mark message read: %w", err)
	}
	return nil
}

func memberDisplayName(firstName, lastName, fallback string) string {
	full := strings.TrimSpace(strings.TrimSpace(firstName) + " " + strings.TrimSpace(lastName))
	if full != "" {
		return full
	}
	fallback = strings.TrimSpace(fallback)
	return fallback
}

func DeleteMessage(ctx context.Context, db *sql.DB, messageID, recipientID int64) error {
	if db == nil {
		return ErrNilDB
	}
	if recipientID == 0 {
		return ErrMissingMemberID
	}
	if messageID == 0 {
		return ErrMessageNotFound
	}

	res, err := db.ExecContext(ctx, `
		DELETE FROM messages
		WHERE id = $1 AND recipient_member_id = $2
	`, messageID, recipientID)
	if err != nil {
		return fmt.Errorf("delete message: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete message rows affected: %w", err)
	}
	if affected == 0 {
		return ErrMessageNotFound
	}
	return nil
}

func loadPendingActionMessage(ctx context.Context, q queryer, messageID, recipientID int64) (int64, int64, error) {
	row := q.QueryRowContext(ctx, `
		SELECT sender_member_id, hop_id, message_type, action_status
		FROM messages
		WHERE id = $1 AND recipient_member_id = $2
		FOR UPDATE
	`, messageID, recipientID)

	var senderID sql.NullInt64
	var hopID sql.NullInt64
	var messageType string
	var actionStatus sql.NullString
	if err := row.Scan(&senderID, &hopID, &messageType, &actionStatus); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, 0, ErrMessageNotFound
		}
		return 0, 0, fmt.Errorf("load action message: %w", err)
	}
	if messageType != types.MessageTypeAction || !senderID.Valid || !hopID.Valid {
		return 0, 0, ErrInvalidMessage
	}
	if actionStatus.Valid {
		return 0, 0, ErrInvalidMessage
	}
	return senderID.Int64, hopID.Int64, nil
}

func markActionMessage(ctx context.Context, e execer, messageID, recipientID int64, status string, now time.Time) error {
	if status != types.MessageActionAccepted && status != types.MessageActionDeclined {
		return ErrInvalidMessage
	}

	res, err := e.ExecContext(ctx, `
		UPDATE messages
		SET action_status = $1, action_taken_at = $2, read_at = COALESCE(read_at, $2)
		WHERE id = $3 AND recipient_member_id = $4 AND message_type = $5 AND action_status IS NULL
	`, status, now, messageID, recipientID, types.MessageTypeAction)
	if err != nil {
		return fmt.Errorf("update action message: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update action message rows affected: %w", err)
	}
	if affected == 0 {
		return ErrInvalidMessage
	}
	return nil
}

func UnreadMessageCount(ctx context.Context, db *sql.DB, recipientID int64) (int, error) {
	if db == nil {
		return 0, ErrNilDB
	}
	if recipientID == 0 {
		return 0, ErrMissingMemberID
	}

	var count int
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM messages
		WHERE recipient_member_id = $1 AND read_at IS NULL
	`, recipientID).Scan(&count); err != nil {
		return 0, fmt.Errorf("count unread messages: %w", err)
	}
	return count, nil
}

func nullableInt64(id *int64) interface{} {
	if id == nil || *id == 0 {
		return nil
	}
	return *id
}

func nullableStringPtr(v *string) interface{} {
	if v == nil {
		return nil
	}
	if strings.TrimSpace(*v) == "" {
		return nil
	}
	return *v
}

func nullableTimePtr(t *time.Time) interface{} {
	if t == nil {
		return nil
	}
	return *t
}

func insertMessage(ctx context.Context, e execer, recipientID int64, senderID *int64, senderName, messageType string, hopID *int64, actionStatus *string, actionTakenAt *time.Time, subject, body string) error {
	if _, err := e.ExecContext(ctx, `
		INSERT INTO messages (
			recipient_member_id,
			sender_member_id,
			sender_name,
			message_type,
			hop_id,
			action_status,
			action_taken_at,
			subject,
			body
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, recipientID, nullableInt64(senderID), senderName, messageType, nullableInt64(hopID), nullableStringPtr(actionStatus), nullableTimePtr(actionTakenAt), subject, body); err != nil {
		return fmt.Errorf("send message: %w", err)
	}
	return nil
}
