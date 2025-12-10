package http

import (
	"database/sql"
	"log"
	"net/http"
	"strings"

	"github.com/a-h/templ"

	"hopshare/internal/auth"
	"hopshare/web/templates"
)

// Server bundles dependencies for HTTP handlers.
type Server struct {
	db       *sql.DB
	users    *auth.UserStore
	sessions *auth.SessionManager
}

// NewRouter wires the base HTTP routes.
func NewRouter(db *sql.DB) http.Handler {
	srv := &Server{
		db:       db,
		users:    auth.NewUserStore(),
		sessions: auth.NewSessionManager(),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", srv.handleLanding)
	mux.HandleFunc("/login", srv.handleLogin)
	mux.HandleFunc("/signup", srv.handleSignup)
	mux.HandleFunc("/signup-success", srv.handleSignupSuccess)
	mux.HandleFunc("/forgot-password", srv.handleForgotPassword)
	mux.HandleFunc("/reset-password", srv.handleResetPassword)
	mux.HandleFunc("/my-hopshare", srv.handleMyHopshare)
	mux.HandleFunc("/logout", srv.handleLogout)
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

		log.Printf("signup request: name=%q email=%q organization=%q message=%q", name, email, org, message)
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
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	render(w, r, templates.MyHopshare(user.Email))
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
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
	c, err := r.Cookie(s.sessions.CookieName())
	if err != nil {
		return nil
	}
	email, ok := s.sessions.Get(c.Value)
	if !ok {
		return nil
	}
	user, ok := s.users.Get(email)
	if !ok {
		return nil
	}
	return user
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
