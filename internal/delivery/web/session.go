package web

import (
	"crypto/rand"
	"encoding/base64"
	"sync"
	"time"
)

const (
	sessionTTL      = 24 * time.Hour
	loginWindow     = 15 * time.Minute
	maxLoginAttempt = 5
)

type session struct {
	csrfToken string
	expiresAt time.Time
}

// sessionStore keeps opaque session tokens in memory. The admin key itself is
// never handed to the browser, so a leaked cookie cannot be replayed as the
// master key and sessions can be invalidated by restarting the process.
type sessionStore struct {
	mu       sync.Mutex
	sessions map[string]session
}

func newSessionStore() *sessionStore {
	return &sessionStore{sessions: make(map[string]session)}
}

// create returns a new session token and its bound CSRF token.
func (s *sessionStore) create() (token, csrf string, err error) {
	token, err = randomToken()
	if err != nil {
		return "", "", err
	}
	csrf, err = randomToken()
	if err != nil {
		return "", "", err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(time.Now())
	s.sessions[token] = session{csrfToken: csrf, expiresAt: time.Now().Add(sessionTTL)}
	return token, csrf, nil
}

// get returns the session for a token if it exists and has not expired.
func (s *sessionStore) get(token string) (session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[token]
	if !ok {
		return session{}, false
	}
	if time.Now().After(sess.expiresAt) {
		delete(s.sessions, token)
		return session{}, false
	}
	return sess, true
}

func (s *sessionStore) delete(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, token)
}

func (s *sessionStore) pruneLocked(now time.Time) {
	for token, sess := range s.sessions {
		if now.After(sess.expiresAt) {
			delete(s.sessions, token)
		}
	}
}

// loginThrottle limits password guessing per client address.
type loginThrottle struct {
	mu       sync.Mutex
	attempts map[string][]time.Time
}

func newLoginThrottle() *loginThrottle {
	return &loginThrottle{attempts: make(map[string][]time.Time)}
}

// allow reports whether another login attempt may be made from addr.
func (t *loginThrottle) allow(addr string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-loginWindow)

	// Sweep addresses whose attempts have all aged out. The dashboard is exposed
	// to the internet, so without this the map grows one entry per probing IP
	// and never shrinks.
	for a, attempts := range t.attempts {
		if len(attempts) == 0 || attempts[len(attempts)-1].Before(cutoff) {
			delete(t.attempts, a)
		}
	}

	kept := t.attempts[addr][:0]
	for _, at := range t.attempts[addr] {
		if at.After(cutoff) {
			kept = append(kept, at)
		}
	}

	if len(kept) >= maxLoginAttempt {
		t.attempts[addr] = kept
		return false
	}
	t.attempts[addr] = append(kept, now)
	return true
}

// reset clears the failure counter after a successful login.
func (t *loginThrottle) reset(addr string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.attempts, addr)
}

func randomToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
