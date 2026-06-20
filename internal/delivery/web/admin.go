package web

import (
	"embed"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"time"

	"blackwatch/internal/application"
)

//go:embed templates/*
var templateFS embed.FS

// AdminServer is a lightweight web dashboard for server owners.
type AdminServer struct {
	services *application.Service
	logger   application.Logger
	port     string
	apiKey   string
	tmpl     *template.Template
}

// NewAdminServer creates a new web admin server.
func NewAdminServer(services *application.Service, logger application.Logger, port, apiKey string) (*AdminServer, error) {
	tmpl, err := template.ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("web: failed to parse templates: %w", err)
	}
	return &AdminServer{
		services: services,
		logger:   logger,
		port:     port,
		apiKey:   apiKey,
		tmpl:     tmpl,
	}, nil
}

// Start begins listening on the configured port.
func (s *AdminServer) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.authMiddleware(s.handleDashboard))
	mux.HandleFunc("/api/license", s.authMiddleware(s.handleLicenseAPI))
	mux.HandleFunc("/login", s.handleLogin)
	mux.HandleFunc("/logout", s.handleLogout)

	s.logger.Info("web: admin dashboard listening on :%s", s.port)
	return http.ListenAndServe(":"+s.port, mux)
}

// authMiddleware checks for the admin API key in a cookie.
func (s *AdminServer) authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("bw_admin_key")
		if err != nil || cookie.Value != s.apiKey {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next(w, r)
	}
}

// handleLogin shows the login form and processes login attempts.
func (s *AdminServer) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		key := r.FormValue("key")
		if key == s.apiKey {
			http.SetCookie(w, &http.Cookie{
				Name:     "bw_admin_key",
				Value:    key,
				Path:     "/",
				HttpOnly: true,
				Expires:  time.Now().Add(24 * time.Hour),
			})
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		s.tmpl.ExecuteTemplate(w, "login.html", map[string]string{"Error": "Invalid API key"})
		return
	}
	s.tmpl.ExecuteTemplate(w, "login.html", nil)
}

// handleLogout clears the auth cookie.
func (s *AdminServer) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: "bw_admin_key", Value: "", Path: "/", MaxAge: -1})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// handleDashboard renders the main admin panel with stats.
func (s *AdminServer) handleDashboard(w http.ResponseWriter, r *http.Request) {
	players, _ := s.services.MatchService.GetPlayerList()
	leaderboard, _ := s.services.MatchService.GetLeaderboard("kda")

	// Count active guilds (licenses)
	activeServers := 0
	todayMatches := 0
	// These would need additional repo methods; show placeholders for now
	_ = activeServers
	_ = todayMatches

	data := map[string]interface{}{
		"PlayerCount": len(players),
		"TopPlayers":  leaderboard,
		"GuildCount":  0,
		"MatchCount":  0,
	}
	s.tmpl.ExecuteTemplate(w, "dashboard.html", data)
}

// handleLicenseAPI handles license management POST requests.
func (s *AdminServer) handleLicenseAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	action := r.FormValue("action")
	guildID := r.FormValue("guild_id")

	switch action {
	case "upgrade":
		days, _ := strconv.Atoi(r.FormValue("days"))
		if days <= 0 {
			days = 30
		}
		expiresAt := time.Now().AddDate(0, 0, days)
		err := s.services.LicenseService.UpgradeLicense(guildID, expiresAt)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	case "expire":
		err := s.services.LicenseService.ExpireLicense(guildID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}
