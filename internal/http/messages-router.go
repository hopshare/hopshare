package http

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"hopshare/internal/service"
	"hopshare/internal/types"
	"hopshare/web/templates"
)

func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	user := s.currentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	successMsg := r.URL.Query().Get("success")
	errorMsg := r.URL.Query().Get("error")

	var selected *types.Message
	msgIDStr := strings.TrimSpace(r.URL.Query().Get("message_id"))
	if msgIDStr != "" {
		msgID, err := strconv.ParseInt(msgIDStr, 10, 64)
		if err != nil || msgID <= 0 {
			errorMsg = "Invalid message."
		} else {
			msg, err := service.GetMessageForMember(r.Context(), s.db, msgID, user.ID)
			if err != nil {
				if errors.Is(err, service.ErrMessageNotFound) {
					errorMsg = "Message not found."
				} else {
					log.Printf("load message failed: %v", err)
					errorMsg = "Could not load message."
				}
			} else {
				if msg.ReadAt == nil {
					now := time.Now().UTC()
					if err := service.MarkMessageRead(r.Context(), s.db, msg.ID, user.ID, now); err != nil {
						log.Printf("mark message read failed: %v", err)
					} else {
						msg.ReadAt = &now
					}
				}
				selected = &msg
			}
		}
	}

	messages, err := service.ListMessages(r.Context(), s.db, user.ID)
	if err != nil {
		log.Printf("list messages failed: %v", err)
		http.Error(w, "could not load messages", http.StatusInternalServerError)
		return
	}

	render(w, r, templates.Messages(
		user.Email,
		messages,
		selected,
		successMsg,
		errorMsg,
	))
}

func (s *Server) handleUnreadMessageCount(w http.ResponseWriter, r *http.Request) {
	user := s.currentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	count, err := service.UnreadMessageCount(r.Context(), s.db, user.ID)
	if err != nil {
		log.Printf("count unread messages failed: %v", err)
		w.WriteHeader(http.StatusOK)
		return
	}

	if count <= 0 {
		w.WriteHeader(http.StatusOK)
		return
	}

	badge := fmt.Sprintf(`<span class="absolute -top-3 -right-3 min-w-[20px] rounded-full bg-red-600 px-1.5 py-0.5 text-center text-[10px] font-semibold text-white">%d</span>`, count)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(badge))
}

func (s *Server) handleDeleteMessage(w http.ResponseWriter, r *http.Request) {
	user := s.currentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	messageID, _ := strconv.ParseInt(r.FormValue("message_id"), 10, 64)
	if messageID <= 0 {
		http.Redirect(w, r, "/messages?error="+url.QueryEscape("Invalid message."), http.StatusSeeOther)
		return
	}

	if err := service.DeleteMessage(r.Context(), s.db, messageID, user.ID); err != nil {
		log.Printf("delete message failed: %v", err)
		http.Redirect(w, r, "/messages?error="+url.QueryEscape("Could not delete message."), http.StatusSeeOther)
		return
	}

	http.Redirect(w, r, "/messages?success="+url.QueryEscape("Message deleted."), http.StatusSeeOther)
}

func (s *Server) handleReplyMessage(w http.ResponseWriter, r *http.Request) {
	user := s.currentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	messageID, _ := strconv.ParseInt(r.FormValue("message_id"), 10, 64)
	if messageID <= 0 {
		http.Redirect(w, r, "/messages?error="+url.QueryEscape("Invalid message."), http.StatusSeeOther)
		return
	}

	original, err := service.GetMessageForMember(r.Context(), s.db, messageID, user.ID)
	if err != nil {
		log.Printf("load message for reply failed: %v", err)
		http.Redirect(w, r, "/messages?error="+url.QueryEscape("Could not load message."), http.StatusSeeOther)
		return
	}
	if original.MessageType == types.MessageTypeAction {
		http.Redirect(w, r, "/messages?message_id="+strconv.FormatInt(messageID, 10)+"&error="+url.QueryEscape("Replies are not available for this message."), http.StatusSeeOther)
		return
	}
	if original.SenderID == nil {
		http.Redirect(w, r, "/messages?message_id="+strconv.FormatInt(messageID, 10)+"&error="+url.QueryEscape("Replies are not available for this message."), http.StatusSeeOther)
		return
	}

	body := strings.TrimSpace(r.FormValue("body"))
	if body == "" {
		http.Redirect(w, r, "/messages?message_id="+strconv.FormatInt(messageID, 10)+"&error="+url.QueryEscape("Reply cannot be empty."), http.StatusSeeOther)
		return
	}

	senderName := memberDisplayName(user)

	senderID := user.ID
	subject := "Re: " + original.Subject
	if err := service.SendMessage(r.Context(), s.db, service.SendMessageParams{
		SenderID:    &senderID,
		SenderName:  senderName,
		RecipientID: *original.SenderID,
		Subject:     subject,
		Body:        body,
	}); err != nil {
		log.Printf("send reply failed: %v", err)
		http.Redirect(w, r, "/messages?message_id="+strconv.FormatInt(messageID, 10)+"&error="+url.QueryEscape("Could not send reply."), http.StatusSeeOther)
		return
	}

	http.Redirect(w, r, "/messages?message_id="+strconv.FormatInt(messageID, 10)+"&success="+url.QueryEscape("Reply sent."), http.StatusSeeOther)
}

func (s *Server) handleMessageAction(w http.ResponseWriter, r *http.Request) {
	user := s.currentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/messages?error="+url.QueryEscape("Manage hop offers from the Hop Detail page."), http.StatusSeeOther)
}
