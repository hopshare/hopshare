package http_test

import (
	"context"
	"database/sql"
	"strconv"
	"testing"

	"hopshare/internal/service"
	"hopshare/internal/types"
)

func TestMessagesHTTPMatrix(t *testing.T) {
	db := requireHTTPTestDB(t)

	t.Run("MSG-01 GET /messages renders inbox", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		member := createSeededMember(t, ctx, db, "messages_get", uniqueTestSuffix())
		server := newHTTPServer(t, db)
		actor := newTestActor(t, "member", server.URL, member.Member.Email, member.Password)
		actor.Login()
		body := requireStatus(t, actor.Get("/messages"), 200)
		requireBodyContains(t, body, "Messages")
	})

	t.Run("MSG-02 selecting message marks it read", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		sender := createSeededMember(t, ctx, db, "messages_sender", suffix)
		recipient := createSeededMember(t, ctx, db, "messages_recipient", suffix)
		senderID := sender.Member.ID
		if err := service.SendMessage(ctx, db, service.SendMessageParams{
			SenderID:    &senderID,
			SenderName:  "Sender",
			RecipientID: recipient.Member.ID,
			Subject:     "Read marker message " + suffix,
			Body:        "Please read me.",
		}); err != nil {
			t.Fatalf("send message: %v", err)
		}
		msg := findMessageBySubjectForRecipient(t, ctx, db, recipient.Member.ID, "Read marker message "+suffix)

		server := newHTTPServer(t, db)
		actor := newTestActor(t, "recipient", server.URL, recipient.Member.Email, recipient.Password)
		actor.Login()
		requireStatus(t, actor.Get("/messages?message_id="+strconv.FormatInt(msg.ID, 10)), 200)

		reloaded, err := service.GetMessageForMember(ctx, db, msg.ID, recipient.Member.ID)
		if err != nil {
			t.Fatalf("reload message: %v", err)
		}
		if reloaded.ReadAt == nil {
			t.Fatalf("expected message to be marked read")
		}
	})

	t.Run("MSG-03 invalid message id shows error without crashing", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		member := createSeededMember(t, ctx, db, "messages_invalid_id", uniqueTestSuffix())
		server := newHTTPServer(t, db)
		actor := newTestActor(t, "member", server.URL, member.Member.Email, member.Password)
		actor.Login()
		body := requireStatus(t, actor.Get("/messages?message_id=bad"), 200)
		requireBodyContains(t, body, "Invalid message.")
	})

	t.Run("MSG-04 unread badge empty when no unread messages", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		member := createSeededMember(t, ctx, db, "messages_unread_zero", uniqueTestSuffix())
		server := newHTTPServer(t, db)
		actor := newTestActor(t, "member", server.URL, member.Member.Email, member.Password)
		actor.Login()
		body := requireStatus(t, actor.Get("/messages/unread-count"), 200)
		if body != "" {
			t.Fatalf("expected empty unread badge body, got %q", body)
		}
	})

	t.Run("MSG-05 unread badge returns count html when unread exists", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		sender := createSeededMember(t, ctx, db, "messages_unread_sender", suffix)
		recipient := createSeededMember(t, ctx, db, "messages_unread_recipient", suffix)
		senderID := sender.Member.ID
		if err := service.SendMessage(ctx, db, service.SendMessageParams{
			SenderID:    &senderID,
			SenderName:  "Sender",
			RecipientID: recipient.Member.ID,
			Subject:     "Unread badge message " + suffix,
			Body:        "Unread body.",
		}); err != nil {
			t.Fatalf("send message: %v", err)
		}
		server := newHTTPServer(t, db)
		actor := newTestActor(t, "recipient", server.URL, recipient.Member.Email, recipient.Password)
		actor.Login()
		body := requireStatus(t, actor.Get("/messages/unread-count"), 200)
		requireBodyContains(t, body, "<span")
		requireBodyContains(t, body, "1")
	})

	t.Run("MSG-06 delete own message succeeds", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		sender := createSeededMember(t, ctx, db, "messages_delete_sender", suffix)
		recipient := createSeededMember(t, ctx, db, "messages_delete_recipient", suffix)
		senderID := sender.Member.ID
		if err := service.SendMessage(ctx, db, service.SendMessageParams{
			SenderID:    &senderID,
			SenderName:  "Sender",
			RecipientID: recipient.Member.ID,
			Subject:     "Delete message " + suffix,
			Body:        "Delete me.",
		}); err != nil {
			t.Fatalf("send message: %v", err)
		}
		msg := findMessageBySubjectForRecipient(t, ctx, db, recipient.Member.ID, "Delete message "+suffix)

		server := newHTTPServer(t, db)
		actor := newTestActor(t, "recipient", server.URL, recipient.Member.Email, recipient.Password)
		actor.Login()
		loc := requireRedirectPath(t, actor.PostForm("/messages/delete", formKV(
			"message_id", strconv.FormatInt(msg.ID, 10),
		)), "/messages")
		requireQueryValue(t, loc, "success", "Message deleted.")
	})

	t.Run("MSG-07 delete another user's message is rejected", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		sender := createSeededMember(t, ctx, db, "messages_delete_other_sender", suffix)
		recipientA := createSeededMember(t, ctx, db, "messages_delete_other_recipient_a", suffix)
		recipientB := createSeededMember(t, ctx, db, "messages_delete_other_recipient_b", suffix)
		senderID := sender.Member.ID
		if err := service.SendMessage(ctx, db, service.SendMessageParams{
			SenderID:    &senderID,
			SenderName:  "Sender",
			RecipientID: recipientA.Member.ID,
			Subject:     "Delete other message " + suffix,
			Body:        "Not yours.",
		}); err != nil {
			t.Fatalf("send message: %v", err)
		}
		msg := findMessageBySubjectForRecipient(t, ctx, db, recipientA.Member.ID, "Delete other message "+suffix)

		server := newHTTPServer(t, db)
		actorB := newTestActor(t, "recipientB", server.URL, recipientB.Member.Email, recipientB.Password)
		actorB.Login()
		loc := requireRedirectPath(t, actorB.PostForm("/messages/delete", formKV(
			"message_id", strconv.FormatInt(msg.ID, 10),
		)), "/messages")
		requireQueryValue(t, loc, "error", "Could not delete message.")
	})

	t.Run("MSG-08 reply to information message succeeds", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		sender := createSeededMember(t, ctx, db, "messages_reply_sender", suffix)
		recipient := createSeededMember(t, ctx, db, "messages_reply_recipient", suffix)
		senderID := sender.Member.ID
		if err := service.SendMessage(ctx, db, service.SendMessageParams{
			SenderID:    &senderID,
			SenderName:  "Sender",
			RecipientID: recipient.Member.ID,
			Subject:     "Reply message " + suffix,
			Body:        "Reply to me.",
		}); err != nil {
			t.Fatalf("send message: %v", err)
		}
		msg := findMessageBySubjectForRecipient(t, ctx, db, recipient.Member.ID, "Reply message "+suffix)

		server := newHTTPServer(t, db)
		actor := newTestActor(t, "recipient", server.URL, recipient.Member.Email, recipient.Password)
		actor.Login()
		loc := requireRedirectPath(t, actor.PostForm("/messages/reply", formKV(
			"message_id", strconv.FormatInt(msg.ID, 10),
			"body", "Thanks for your message.",
		)), "/messages")
		requireQueryValue(t, loc, "success", "Reply sent.")
	})

	t.Run("MSG-09 empty reply body is rejected", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		sender := createSeededMember(t, ctx, db, "messages_empty_reply_sender", suffix)
		recipient := createSeededMember(t, ctx, db, "messages_empty_reply_recipient", suffix)
		senderID := sender.Member.ID
		if err := service.SendMessage(ctx, db, service.SendMessageParams{
			SenderID:    &senderID,
			SenderName:  "Sender",
			RecipientID: recipient.Member.ID,
			Subject:     "Empty reply message " + suffix,
			Body:        "Reply to me.",
		}); err != nil {
			t.Fatalf("send message: %v", err)
		}
		msg := findMessageBySubjectForRecipient(t, ctx, db, recipient.Member.ID, "Empty reply message "+suffix)
		server := newHTTPServer(t, db)
		actor := newTestActor(t, "recipient", server.URL, recipient.Member.Email, recipient.Password)
		actor.Login()
		loc := requireRedirectPath(t, actor.PostForm("/messages/reply", formKV(
			"message_id", strconv.FormatInt(msg.ID, 10),
			"body", "",
		)), "/messages")
		requireQueryValue(t, loc, "error", "Reply cannot be empty.")
	})

	t.Run("MSG-10 replying to hop offer notification succeeds", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner", "helper")
		hop, err := service.CreateHop(ctx, db, service.CreateHopParams{
			OrganizationID: org.ID,
			MemberID:       members["owner"].Member.ID,
			Title:          "Action reply hop " + suffix,
			Details:        "Action message setup.",
			EstimatedHours: 1,
			NeededByKind:   types.HopNeededByAnytime,
			IsPrivate:      false,
		})
		if err != nil {
			t.Fatalf("create hop: %v", err)
		}
		if err := service.OfferHopHelp(ctx, db, service.OfferHopParams{
			OrganizationID: org.ID,
			HopID:          hop.ID,
			OffererID:      members["helper"].Member.ID,
			OffererName:    "helper",
		}); err != nil {
			t.Fatalf("offer hop: %v", err)
		}
		offerMsg := findMessageBySubjectForRecipient(t, ctx, db, members["owner"].Member.ID, "Hop help offer")
		server := newHTTPServer(t, db)
		ownerActor := newTestActor(t, "owner", server.URL, members["owner"].Member.Email, members["owner"].Password)
		ownerActor.Login()
		loc := requireRedirectPath(t, ownerActor.PostForm("/messages/reply", formKV(
			"message_id", strconv.FormatInt(offerMsg.ID, 10),
			"body", "Thanks for offering. I will review from the hop page.",
		)), "/messages")
		requireQueryValue(t, loc, "success", "Reply sent.")
	})

	t.Run("MSG-11 replying to senderless message is rejected", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		recipient := createSeededMember(t, ctx, db, "messages_senderless_recipient", suffix)
		if err := service.SendMessage(ctx, db, service.SendMessageParams{
			SenderID:    nil,
			SenderName:  "System",
			RecipientID: recipient.Member.ID,
			Subject:     "System notice " + suffix,
			Body:        "No reply target.",
		}); err != nil {
			t.Fatalf("send system message: %v", err)
		}
		msg := findMessageBySubjectForRecipient(t, ctx, db, recipient.Member.ID, "System notice "+suffix)
		server := newHTTPServer(t, db)
		actor := newTestActor(t, "recipient", server.URL, recipient.Member.Email, recipient.Password)
		actor.Login()
		loc := requireRedirectPath(t, actor.PostForm("/messages/reply", formKV(
			"message_id", strconv.FormatInt(msg.ID, 10),
			"body", "No reply possible.",
		)), "/messages")
		requireQueryValue(t, loc, "error", "Replies are not available for this message.")
	})

	t.Run("MSG-12 message actions redirect to hop details guidance", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		member := createSeededMember(t, ctx, db, "messages_invalid_action", uniqueTestSuffix())
		server := newHTTPServer(t, db)
		actor := newTestActor(t, "member", server.URL, member.Member.Email, member.Password)
		actor.Login()
		loc := requireRedirectPath(t, actor.PostForm("/messages/action", formKV(
			"message_id", "1",
			"action", "maybe",
		)), "/messages")
		requireQueryValue(t, loc, "error", "Manage hop offers from the Hop Detail page.")
	})

	t.Run("MSG-13 message action by non-recipient is still blocked by redirect guidance", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner", "helper", "other")
		hop, err := service.CreateHop(ctx, db, service.CreateHopParams{
			OrganizationID: org.ID,
			MemberID:       members["owner"].Member.ID,
			Title:          "Action non-recipient hop " + suffix,
			Details:        "Action non-recipient setup.",
			EstimatedHours: 1,
			NeededByKind:   types.HopNeededByAnytime,
			IsPrivate:      false,
		})
		if err != nil {
			t.Fatalf("create hop: %v", err)
		}
		if err := service.OfferHopHelp(ctx, db, service.OfferHopParams{
			OrganizationID: org.ID,
			HopID:          hop.ID,
			OffererID:      members["helper"].Member.ID,
			OffererName:    "helper",
		}); err != nil {
			t.Fatalf("offer hop: %v", err)
		}
		offerMsg := findMessageBySubjectForRecipient(t, ctx, db, members["owner"].Member.ID, "Hop help offer")
		server := newHTTPServer(t, db)
		other := newTestActor(t, "other", server.URL, members["other"].Member.Email, members["other"].Password)
		other.Login()
		loc := requireRedirectPath(t, other.PostForm("/messages/action", formKV(
			"message_id", strconv.FormatInt(offerMsg.ID, 10),
			"action", "accept",
		)), "/messages")
		requireQueryValue(t, loc, "error", "Manage hop offers from the Hop Detail page.")
	})

	t.Run("MSG-14 repeated message action posts always redirect to hop details guidance", func(t *testing.T) {
		ctx, cancel := newTestContext(t)
		defer cancel()
		suffix := uniqueTestSuffix()
		org, members := createOrganizationWithMembers(t, ctx, db, suffix, "owner", "helper")
		hop, err := service.CreateHop(ctx, db, service.CreateHopParams{
			OrganizationID: org.ID,
			MemberID:       members["owner"].Member.ID,
			Title:          "Action replay hop " + suffix,
			Details:        "Action replay setup.",
			EstimatedHours: 1,
			NeededByKind:   types.HopNeededByAnytime,
			IsPrivate:      false,
		})
		if err != nil {
			t.Fatalf("create hop: %v", err)
		}
		if err := service.OfferHopHelp(ctx, db, service.OfferHopParams{
			OrganizationID: org.ID,
			HopID:          hop.ID,
			OffererID:      members["helper"].Member.ID,
			OffererName:    "helper",
		}); err != nil {
			t.Fatalf("offer hop: %v", err)
		}
		offerMsg := findMessageBySubjectForRecipient(t, ctx, db, members["owner"].Member.ID, "Hop help offer")
		server := newHTTPServer(t, db)
		owner := newTestActor(t, "owner", server.URL, members["owner"].Member.Email, members["owner"].Password)
		owner.Login()

		loc := requireRedirectPath(t, owner.PostForm("/messages/action", formKV(
			"message_id", strconv.FormatInt(offerMsg.ID, 10),
			"action", "accept",
		)), "/messages")
		requireQueryValue(t, loc, "error", "Manage hop offers from the Hop Detail page.")

		loc = requireRedirectPath(t, owner.PostForm("/messages/action", formKV(
			"message_id", strconv.FormatInt(offerMsg.ID, 10),
			"action", "accept",
		)), "/messages")
		requireQueryValue(t, loc, "error", "Manage hop offers from the Hop Detail page.")
	})
}

func findMessageBySubjectForRecipient(t *testing.T, ctx context.Context, db *sql.DB, recipientID int64, subject string) types.Message {
	t.Helper()
	msgs, err := service.ListMessages(ctx, db, recipientID)
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	for _, m := range msgs {
		if m.Subject == subject {
			return m
		}
	}
	t.Fatalf("message subject %q not found for recipient %d", subject, recipientID)
	return types.Message{}
}
