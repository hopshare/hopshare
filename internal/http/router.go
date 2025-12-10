package http

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"log"
	"net/http"
	"strings"

	"github.com/a-h/templ"

	"hopshare/internal/auth"
	"hopshare/internal/service"
	"hopshare/internal/types"
	"hopshare/web/templates"
)

// Server bundles dependencies for HTTP handlers.
type Server struct {
	db       *sql.DB
	users    *auth.UserStore
	sessions *auth.SessionManager
}

type HandlerFunc func(http.ResponseWriter, *http.Request)
type Middleware func(HandlerFunc) HandlerFunc

type contextKey string

const userContextKey contextKey = "currentUser"

// NewRouter wires the base HTTP routes.
func NewRouter(db *sql.DB) http.Handler {
	srv := &Server{
		db:       db,
		users:    auth.NewUserStore(),
		sessions: auth.NewSessionManager(),
	}

	mux := http.NewServeMux()
	staticFS := http.FileServer(http.Dir("web/static"))
	mux.Handle("/static/", http.StripPrefix("/static/", staticFS))
	srv.register(mux, "/", srv.handleLanding, srv.requireMethod(http.MethodGet))
	srv.register(mux, "/login", srv.handleLogin, srv.requireMethod(http.MethodGet, http.MethodPost))
	srv.register(mux, "/signup", srv.handleSignup, srv.requireMethod(http.MethodGet, http.MethodPost))
	srv.register(mux, "/signup-success", srv.handleSignupSuccess, srv.requireMethod(http.MethodGet))
	srv.register(mux, "/forgot-password", srv.handleForgotPassword, srv.requireMethod(http.MethodGet, http.MethodPost))
	srv.register(mux, "/reset-password", srv.handleResetPassword, srv.requireMethod(http.MethodGet, http.MethodPost))
	srv.register(mux, "/my-hopshare", srv.handleMyHopshare, srv.requireAuth(), srv.requireMethod(http.MethodGet))
	srv.register(mux, "/logout", srv.handleLogout, srv.requireMethod(http.MethodPost, http.MethodGet))
	mux.HandleFunc("/healthz", handleHealthz)

	return mux
}

func (s *Server) handleLanding(w http.ResponseWriter, r *http.Request) {
	render(w, r, templates.Landing(s.currentUserEmailPtr(r)))
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if s.currentUser(r) != nil {
			http.Redirect(w, r, "/my-hopshare", http.StatusSeeOther)
			return
		}
		successMsg := r.URL.Query().Get("success")
		render(w, r, templates.Login(nil, "", successMsg))
	case http.MethodPost:
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid form", http.StatusBadRequest)
			return
		}
		email := strings.TrimSpace(r.FormValue("email"))
		password := r.FormValue("password")

		user, ok := s.users.Authenticate(email, password)
		if !ok {
			render(w, r, templates.Login(nil, "Invalid email or password.", ""))
			return
		}

		token, err := s.sessions.Create(user.Email)
		if err != nil {
			http.Error(w, "could not create session", http.StatusInternalServerError)
			return
		}

		http.SetCookie(w, &http.Cookie{
			Name:     s.sessions.CookieName(),
			Value:    token,
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		})

		http.Redirect(w, r, "/my-hopshare", http.StatusSeeOther)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleSignup(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		render(w, r, templates.Signup(s.currentUserEmailPtr(r), "", ""))
	case http.MethodPost:
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid form", http.StatusBadRequest)
			return
		}

		name := strings.TrimSpace(r.FormValue("name"))
		email := strings.TrimSpace(r.FormValue("email"))
		org := strings.TrimSpace(r.FormValue("organization"))
		message := strings.TrimSpace(r.FormValue("message"))
		password := r.FormValue("password")

		member := types.Member{
			Username:               deriveUsername(name, email),
			Email:                  email,
			PasswordHash:           hashPassword(password),
			PreferredContactMethod: types.ContactMethodEmail,
			PreferredContact:       email,
			Enabled:                false,
			Verified:               false,
		}

		created, err := service.CreateMember(r.Context(), s.db, member)
		if err != nil {
			log.Printf("create member failed: %v", err)
			render(w, r, templates.Signup(s.currentUserEmailPtr(r), "", "We could not process your request right now. Please try again."))
			return
		}

		log.Printf("signup request: name=%q email=%q organization=%q message=%q member_id=%d", name, email, org, message, created.ID)
		http.Redirect(w, r, "/signup-success", http.StatusSeeOther)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleSignupSuccess(w http.ResponseWriter, r *http.Request) {
	render(w, r, templates.SignupSuccess(s.currentUserEmailPtr(r)))
}

func (s *Server) handleForgotPassword(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		render(w, r, templates.ForgotPassword(s.currentUserEmailPtr(r), false, ""))
	case http.MethodPost:
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid form", http.StatusBadRequest)
			return
		}
		email := strings.TrimSpace(r.FormValue("email"))
		token, ok := s.users.RequestReset(email)
		if ok {
			link := "/reset-password?token=" + token
			log.Printf("password reset link for %s: %s", email, link)
			render(w, r, templates.ForgotPassword(s.currentUserEmailPtr(r), true, link))
			return
		}
		render(w, r, templates.ForgotPassword(s.currentUserEmailPtr(r), true, ""))
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleResetPassword(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		token := r.URL.Query().Get("token")
		if token == "" {
			render(w, r, templates.ResetPassword(s.currentUserEmailPtr(r), "", "Missing token.", ""))
			return
		}
		render(w, r, templates.ResetPassword(s.currentUserEmailPtr(r), token, "", ""))
	case http.MethodPost:
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid form", http.StatusBadRequest)
			return
		}
		token := r.FormValue("token")
		newPassword := r.FormValue("new_password")
		confirmPassword := r.FormValue("confirm_password")

		if token == "" {
			render(w, r, templates.ResetPassword(s.currentUserEmailPtr(r), token, "Missing token.", ""))
			return
		}
		if newPassword == "" || confirmPassword == "" {
			render(w, r, templates.ResetPassword(s.currentUserEmailPtr(r), token, "Please enter and confirm your new password.", ""))
			return
		}
		if newPassword != confirmPassword {
			render(w, r, templates.ResetPassword(s.currentUserEmailPtr(r), token, "Passwords do not match.", ""))
			return
		}

		if _, err := s.users.ResetPassword(token, newPassword); err != nil {
			render(w, r, templates.ResetPassword(s.currentUserEmailPtr(r), token, err.Error(), ""))
			return
		}

		http.Redirect(w, r, "/login?success=Password+reset+successful%2C+please+log+in.", http.StatusSeeOther)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleMyHopshare(w http.ResponseWriter, r *http.Request) {
	user := s.currentUser(r)
	render(w, r, templates.MyHopshare(user.Email))
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie(s.sessions.CookieName())
	if err == nil {
		s.sessions.Delete(c.Value)
		http.SetCookie(w, &http.Cookie{
			Name:     s.sessions.CookieName(),
			Value:    "",
			Path:     "/",
			MaxAge:   -1,
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		})
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (s *Server) currentUser(r *http.Request) *auth.User {
	return currentUserFromContext(r.Context())
}

func (s *Server) currentUserEmailPtr(r *http.Request) *string {
	if user := s.currentUser(r); user != nil {
		return &user.Email
	}
	return nil
}

func render(w http.ResponseWriter, r *http.Request, component templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := component.Render(r.Context(), w); err != nil {
		log.Printf("templ render error: %v", err)
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}

func (s *Server) register(mux *http.ServeMux, path string, h HandlerFunc, middlewares ...Middleware) {
	// withUser runs first so downstream middleware/handlers can rely on context user.
	all := append([]Middleware{s.withUser()}, middlewares...)
	mux.HandleFunc(path, chain(h, all...))
}

func chain(h HandlerFunc, m ...Middleware) http.HandlerFunc {
	for i := len(m) - 1; i >= 0; i-- {
		h = m[i](h)
	}
	return func(w http.ResponseWriter, r *http.Request) {
		h(w, r)
	}
}

func (s *Server) withUser() Middleware {
	return func(next HandlerFunc) HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if c, err := r.Cookie(s.sessions.CookieName()); err == nil {
				if email, ok := s.sessions.Get(c.Value); ok {
					if user, ok := s.users.Get(email); ok {
						ctx := context.WithValue(r.Context(), userContextKey, user)
						r = r.WithContext(ctx)
					}
				}
			}
			next(w, r)
		}
	}
}

func (s *Server) requireAuth() Middleware {
	return func(next HandlerFunc) HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if currentUserFromContext(r.Context()) == nil {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}
			next(w, r)
		}
	}
}

func (s *Server) requireMethod(methods ...string) Middleware {
	allowed := make(map[string]struct{}, len(methods))
	for _, m := range methods {
		allowed[m] = struct{}{}
	}
	allowHeader := strings.Join(methods, ", ")

	return func(next HandlerFunc) HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if _, ok := allowed[r.Method]; !ok {
				w.Header().Set("Allow", allowHeader)
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			next(w, r)
		}
	}
}

func currentUserFromContext(ctx context.Context) *auth.User {
	if ctx == nil {
		return nil
	}
	if u, ok := ctx.Value(userContextKey).(*auth.User); ok {
		return u
	}
	return nil
}

func deriveUsername(name, email string) string {
	if name != "" {
		n := strings.ToLower(strings.TrimSpace(name))
		n = strings.ReplaceAll(n, " ", "_")
		return n
	}
	parts := strings.Split(email, "@")
	if len(parts) > 0 && parts[0] != "" {
		return parts[0]
	}
	return "user"
}

func hashPassword(pw string) string {
	sum := sha256.Sum256([]byte(pw))
	return hex.EncodeToString(sum[:])
}
