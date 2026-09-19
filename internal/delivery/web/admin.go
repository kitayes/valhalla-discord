package web

import (
	"context"
	"crypto/subtle"
	"embed"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"blackwatch/internal/application"
)

//go:embed templates/*
var templateFS embed.FS

const sessionCookieName = "bw_admin_session"

// AdminServer is a lightweight web dashboard for server owners.
type AdminServer struct {
	services *application.Service
	logger   application.Logger
	port     string
	apiKey   string
	tmpl     *template.Template
	sessions *sessionStore
	throttle *loginThrottle
	// trustedProxies are the peers whose X-Forwarded-For header may be believed.
	// Empty means nobody: the direct peer address is used.
	trustedProxies []netip.Prefix
	adminIDs       map[int64]bool
	debtorNotifier func(ctx context.Context, adminChatID int64, msg string) error
	// photoUploader hands a screenshot to Telegram and returns the file ID it
	// answers with. The mini app can only send raw bytes, while everything
	// downstream addresses photos by file ID, so this is the bridge between
	// the two.
	photoUploader func(ctx context.Context, data []byte, filename string) (string, error)
	sseBroker     *SSEBroker
	srv           *http.Server
	startedAt     time.Time
	// botUsername is the bot the mini app is served from; it turns an invite
	// token into a t.me deep link. Empty means the app hands out bare tokens.
	botUsername string
}

// NewAdminServer creates a new web admin server.
//
// trustedProxyCIDRs lists the reverse proxies allowed to rewrite the client
// address; see clientAddr for why an unconditional X-Forwarded-For is not safe.
func NewAdminServer(services *application.Service, logger application.Logger, port, apiKey string, trustedProxyCIDRs []string, botToken string) (*AdminServer, error) {
	tmpl, err := template.ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("web: failed to parse templates: %w", err)
	}
	proxies, err := parseTrustedProxies(trustedProxyCIDRs)
	if err != nil {
		return nil, err
	}
	s := &AdminServer{
		services:       services,
		logger:         logger,
		port:           port,
		apiKey:         apiKey,
		tmpl:           tmpl,
		sessions:       newSessionStore(),
		throttle:       newLoginThrottle(),
		trustedProxies: proxies,
		sseBroker:      NewSSEBroker(),
		startedAt:      time.Now(),
	}

	// The server is built here, not in Start. Start runs on its own goroutine
	// while main calls Shutdown from another: assigning s.srv inside Start was a
	// data race, and a shutdown that won it read a nil srv, returned nil, and
	// left the listener running while main went on to close the database.
	mux := http.NewServeMux()
	if apiKey != "" {
		mux.HandleFunc("/", s.authMiddleware(s.handleDashboard))
		mux.HandleFunc("/api/license", s.authMiddleware(s.handleLicenseAPI))
		mux.HandleFunc("/login", s.handleLogin)
		mux.HandleFunc("/logout", s.handleLogout)
	}

	// Telegram Mini App routes
	mux.HandleFunc("/app", s.handleApp)

	tmaAuth := func(h http.HandlerFunc) http.HandlerFunc {
		return TMAAuthMiddleware(botToken, 24*time.Hour, h)
	}

	mux.HandleFunc("/api/events", tmaAuth(s.handleEventsSSE))
	mux.HandleFunc("/api/me", tmaAuth(s.handleMe))
	mux.HandleFunc("/api/bracket", s.handleBracket)
	mux.HandleFunc("/api/bracket/match_details", s.handleBracketMatchDetails)
	mux.HandleFunc("/api/team/details", s.handleTeamDetails)
	mux.HandleFunc("/api/match/active", tmaAuth(s.handleActiveMatch))
	mux.HandleFunc("/api/match/ready", tmaAuth(s.handleMatchReady))
	mux.HandleFunc("/api/match/referee", tmaAuth(s.handleMatchReferee))
	mux.HandleFunc("/api/match/report", tmaAuth(s.handleMatchReport))
	mux.HandleFunc("/api/match/result/confirm", tmaAuth(s.handleReportConfirm))
	mux.HandleFunc("/api/match/result/dispute", tmaAuth(s.handleReportDispute))
	mux.HandleFunc("/api/team/checkin", tmaAuth(s.handleCheckIn))
	mux.HandleFunc("/api/team/player", tmaAuth(s.handleUpdateTeamPlayer))
	mux.HandleFunc("/api/team/player/add", tmaAuth(s.handleAddTeamPlayer))
	mux.HandleFunc("/api/team/create", tmaAuth(s.handleCreateTeam))
	mux.HandleFunc("/api/team/invite", tmaAuth(s.handleGenerateInvite))
	mux.HandleFunc("/api/team/join", tmaAuth(s.handleJoinTeam))
	mux.HandleFunc("/api/team/kick", tmaAuth(s.handleKickPlayer))
	mux.HandleFunc("/api/team/transfer", tmaAuth(s.handleTransferCaptain))
	mux.HandleFunc("/api/admin/desk", tmaAuth(s.handleAdminDesk))
	mux.HandleFunc("/api/admin/match_action", tmaAuth(s.handleAdminMatchAction))
	mux.HandleFunc("/api/admin/ping_debtors", tmaAuth(s.handleAdminPingDebtors))
	mux.HandleFunc("/api/admin/disqualify_uncheck", tmaAuth(s.handleAdminDisqualifyUncheck))

	s.srv = &http.Server{
		Addr:    ":" + port,
		Handler: securityHeaders(mux),
		// Without these an idle or slow client can hold a connection forever.
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	return s, nil
}

// Start begins listening on the configured port.
func (s *AdminServer) Start() error {
	s.logger.Info("web: server listening on :%s", s.port)
	return s.srv.ListenAndServe()
}

// Shutdown gracefully stops the server.
func (s *AdminServer) Shutdown(ctx context.Context) error {
	return s.srv.Shutdown(ctx)
}

// WithBotUsername enables invite deep links of the form t.me/<bot>?start=join_<token>.
func (s *AdminServer) WithBotUsername(name string) *AdminServer {
	s.botUsername = strings.TrimPrefix(strings.TrimSpace(name), "@")
	return s
}

// inviteLink is the one-tap join link for a token, or "" when the bot's
// username is unknown.
func (s *AdminServer) inviteLink(token string) string {
	if s.botUsername == "" || token == "" {
		return ""
	}
	return "https://t.me/" + s.botUsername + "?start=" + InviteStartPayload(token)
}

func (s *AdminServer) WithAdminIDs(adminIDs []int64) *AdminServer {
	s.adminIDs = make(map[int64]bool, len(adminIDs))
	for _, id := range adminIDs {
		s.adminIDs[id] = true
	}
	return s
}

func (s *AdminServer) WithDebtorNotifier(fn func(ctx context.Context, adminChatID int64, msg string) error) *AdminServer {
	s.debtorNotifier = fn
	return s
}

// WithPhotoUploader supplies the function that turns screenshot bytes into a
// Telegram file ID. Without it the mini app cannot accept match results,
// because a report that reaches the referee without its proof is worse than
// one that is refused outright.
func (s *AdminServer) WithPhotoUploader(fn func(ctx context.Context, data []byte, filename string) (string, error)) *AdminServer {
	s.photoUploader = fn
	return s
}

func (s *AdminServer) isAdmin(tgID int64) bool {
	return s.adminIDs != nil && s.adminIDs[tgID]
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		if strings.HasPrefix(r.URL.Path, "/app") || strings.HasPrefix(r.URL.Path, "/api/") {
			// Telegram Desktop (Linux/Windows/macOS) loads Mini Apps in a native
			// webview — the parent frame origin is NOT https://telegram.org but a
			// tg:// scheme or a null origin, so restricting frame-ancestors to
			// *.telegram.org blocks the desktop client entirely.
			// Using '*' satisfies every Telegram client variant while still
			// preventing completely unrelated sites from embedding the app
			// (they would need a valid Telegram initData to do anything useful).
			h.Set("Content-Security-Policy", "frame-ancestors *;")
		} else {
			h.Set("X-Frame-Options", "DENY")
			h.Set("Content-Security-Policy", "frame-ancestors 'none';")
		}
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

// authMiddleware resolves the session cookie into a session.
func (s *AdminServer) authMiddleware(next func(http.ResponseWriter, *http.Request, session)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		sess, ok := s.sessions.get(cookie.Value)
		if !ok {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next(w, r, sess)
	}
}

// handleLogin shows the login form and processes login attempts.
func (s *AdminServer) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.render(w, "login.html", loginView{})
		return
	}

	addr := s.clientAddr(r)
	if !s.throttle.allow(addr) {
		s.logger.Warn("web: login throttled for %s", addr)
		w.WriteHeader(http.StatusTooManyRequests)
		s.render(w, "login.html", loginView{Error: "Too many attempts. Try again later."})
		return
	}

	// Constant-time comparison so the key cannot be recovered byte by byte.
	key := r.FormValue("key")
	if subtle.ConstantTimeCompare([]byte(key), []byte(s.apiKey)) != 1 {
		s.logger.Warn("web: failed admin login from %s", addr)
		w.WriteHeader(http.StatusUnauthorized)
		s.render(w, "login.html", loginView{Error: "Invalid API key"})
		return
	}

	token, _, err := s.sessions.create()
	if err != nil {
		s.logger.Error("web: failed to create session: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.throttle.reset(addr)

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.isTLS(r),
		SameSite: http.SameSiteStrictMode,
		Expires:  time.Now().Add(sessionTTL),
	})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// handleLogout clears the auth cookie and drops the server-side session.
func (s *AdminServer) handleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		s.sessions.delete(cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   s.isTLS(r),
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// dashboardView is the template's contract.
//
// It used to be a map[string]interface{}, which meant the template and the
// handler agreed on field names by convention only. The Guilds and Matches
// tiles it fed were hardcoded zeroes and the Uptime tile a hardcoded "24h" —
// three numbers the operator had no way to tell from real ones. The two with no
// source behind them are gone; Uptime is measured.
type dashboardView struct {
	PlayerCount int
	TopPlayers  []*application.PlayerStats
	Uptime      string
	CSRFToken   string
}

// loginView is the contract of the login page.
type loginView struct {
	Error string
}

// handleDashboard renders the main admin panel with stats.
func (s *AdminServer) handleDashboard(w http.ResponseWriter, r *http.Request, sess session) {
	// A failed query is a 500, not a dashboard full of zeroes: the operator
	// opens this page to find out whether the bot is healthy, and "0 players"
	// was indistinguishable from "the database is down".
	players, err := s.services.MatchService.GetPlayerList(r.Context())
	if err != nil {
		s.logger.Error("web: failed to load player list: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	leaderboard, err := s.services.MatchService.GetLeaderboard(r.Context(), "kda")
	if err != nil {
		s.logger.Error("web: failed to load leaderboard: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	s.render(w, "dashboard.html", dashboardView{
		PlayerCount: len(players),
		TopPlayers:  leaderboard,
		Uptime:      formatUptime(time.Since(s.startedAt)),
		CSRFToken:   sess.csrfToken,
	})
}

// handleLicenseAPI handles license management POST requests.
func (s *AdminServer) handleLicenseAPI(w http.ResponseWriter, r *http.Request, sess session) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// The session cookie alone would let any site submit this form on the
	// admin's behalf, so require the token that came with the rendered page.
	if subtle.ConstantTimeCompare([]byte(r.FormValue("csrf_token")), []byte(sess.csrfToken)) != 1 {
		s.logger.Warn("web: rejected license request with bad CSRF token from %s", s.clientAddr(r))
		http.Error(w, "Invalid CSRF token", http.StatusForbidden)
		return
	}

	action := r.FormValue("action")
	guildID := r.FormValue("guild_id")
	if guildID == "" {
		http.Error(w, "guild_id is required", http.StatusBadRequest)
		return
	}

	switch action {
	case "upgrade":
		days, err := strconv.Atoi(r.FormValue("days"))
		if err != nil || days <= 0 {
			days = 30
		}
		if days > 365 {
			days = 365
		}
		expiresAt := time.Now().AddDate(0, 0, days)
		if err := s.services.LicenseService.UpgradeLicense(r.Context(), guildID, expiresAt); err != nil {
			// The error text is a driver message; it goes to the log, not to
			// the HTTP response.
			s.logger.Error("web: failed to upgrade license for %s: %v", guildID, err)
			http.Error(w, "failed to upgrade license", http.StatusInternalServerError)
			return
		}
		s.logger.Info("web: license upgraded for guild %s (%d days)", guildID, days)
	case "expire":
		if err := s.services.LicenseService.ExpireLicense(r.Context(), guildID); err != nil {
			s.logger.Error("web: failed to expire license for %s: %v", guildID, err)
			http.Error(w, "failed to expire license", http.StatusInternalServerError)
			return
		}
		s.logger.Info("web: license expired for guild %s", guildID)
	default:
		http.Error(w, "unknown action", http.StatusBadRequest)
		return
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// render executes a template and reports failures instead of swallowing them.
func (s *AdminServer) render(w http.ResponseWriter, name string, data interface{}) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, name, data); err != nil {
		s.logger.Error("web: failed to render %s: %v", name, err)
	}
}

// formatUptime renders a duration as the dashboard tile shows it.
func formatUptime(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// isTLS reports whether the request reached us over HTTPS, and so whether the
// session cookie may be marked Secure. X-Forwarded-Proto is subject to the same
// trust check as X-Forwarded-For — an untrusted peer must not get to describe
// its own connection.
func (s *AdminServer) isTLS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	peer := r.RemoteAddr
	if host, _, err := net.SplitHostPort(peer); err == nil {
		peer = host
	}
	return s.trustsPeer(peer) && r.Header.Get("X-Forwarded-Proto") == "https"
}

// parseTrustedProxies turns the configured CIDR list into prefixes. A bare
// address is accepted and treated as a single host.
func parseTrustedProxies(cidrs []string) ([]netip.Prefix, error) {
	prefixes := make([]netip.Prefix, 0, len(cidrs))
	for _, raw := range cidrs {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if prefix, err := netip.ParsePrefix(raw); err == nil {
			prefixes = append(prefixes, prefix.Masked())
			continue
		}
		addr, err := netip.ParseAddr(raw)
		if err != nil {
			return nil, fmt.Errorf("web: %q is not a valid CIDR or IP address: %w", raw, err)
		}
		prefixes = append(prefixes, netip.PrefixFrom(addr, addr.BitLen()))
	}
	return prefixes, nil
}

// clientAddr identifies the caller for login throttling.
//
// X-Forwarded-For is believed only when the direct peer is a configured trusted
// proxy. Reading it unconditionally — which is what this did — handed every
// caller a fresh throttle bucket for the price of one header, so the five-attempt
// limit protecting WEB_ADMIN_KEY from brute force did nothing at all against
// anyone who could reach the port directly.
func (s *AdminServer) clientAddr(r *http.Request) string {
	peer := r.RemoteAddr
	if host, _, err := net.SplitHostPort(peer); err == nil {
		peer = host
	}

	if !s.trustsPeer(peer) {
		return peer
	}

	fwd := r.Header.Get("X-Forwarded-For")
	if fwd == "" {
		return peer
	}
	first, _, _ := strings.Cut(fwd, ",")
	if first = strings.TrimSpace(first); first != "" {
		return first
	}
	return peer
}

func (s *AdminServer) trustsPeer(peer string) bool {
	if len(s.trustedProxies) == 0 {
		return false
	}
	addr, err := netip.ParseAddr(peer)
	if err != nil {
		return false
	}
	addr = addr.Unmap()
	for _, prefix := range s.trustedProxies {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}
