package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// newServerWithProxies builds the minimum AdminServer the address helpers need.
// NewAdminServer is not used here: it parses templates and builds a listener,
// neither of which these functions touch.
func newServerWithProxies(t *testing.T, cidrs ...string) *AdminServer {
	t.Helper()
	proxies, err := parseTrustedProxies(cidrs)
	if err != nil {
		t.Fatalf("parseTrustedProxies(%v) failed: %v", cidrs, err)
	}
	return &AdminServer{trustedProxies: proxies}
}

func requestFrom(peer, xff string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/login", nil)
	r.RemoteAddr = peer
	if xff != "" {
		r.Header.Set("X-Forwarded-For", xff)
	}
	return r
}

func TestClientAddrIgnoresForwardedForFromUntrustedPeer(t *testing.T) {
	// The whole point of the login throttle is that five bad guesses cost the
	// attacker their bucket. Believing X-Forwarded-For from whoever connected
	// hands them a fresh bucket per request for the price of one header.
	cases := []struct {
		name    string
		proxies []string
		peer    string
		xff     string
		want    string
	}{
		{
			name: "no proxies configured",
			peer: "203.0.113.7:51000",
			xff:  "10.9.9.9",
			want: "203.0.113.7",
		},
		{
			name:    "peer outside the trusted range",
			proxies: []string{"10.0.0.0/8"},
			peer:    "203.0.113.7:51000",
			xff:     "10.9.9.9",
			want:    "203.0.113.7",
		},
		{
			name:    "trusted proxy is believed",
			proxies: []string{"10.0.0.0/8"},
			peer:    "10.1.2.3:443",
			xff:     "198.51.100.4",
			want:    "198.51.100.4",
		},
		{
			name:    "trusted proxy, first hop of a chain wins",
			proxies: []string{"10.0.0.0/8"},
			peer:    "10.1.2.3:443",
			xff:     "198.51.100.4, 10.1.2.3",
			want:    "198.51.100.4",
		},
		{
			name:    "trusted proxy sending no header falls back to the peer",
			proxies: []string{"10.0.0.0/8"},
			peer:    "10.1.2.3:443",
			xff:     "",
			want:    "10.1.2.3",
		},
		{
			name:    "bare IP in the trust list",
			proxies: []string{"192.0.2.10"},
			peer:    "192.0.2.10:8080",
			xff:     "198.51.100.4",
			want:    "198.51.100.4",
		},
		{
			name:    "bare IP in the trust list does not cover its neighbours",
			proxies: []string{"192.0.2.10"},
			peer:    "192.0.2.11:8080",
			xff:     "198.51.100.4",
			want:    "192.0.2.11",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newServerWithProxies(t, tc.proxies...)
			if got := s.clientAddr(requestFrom(tc.peer, tc.xff)); got != tc.want {
				t.Errorf("clientAddr() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestIsTLSHonoursForwardedProtoOnlyFromTrustedPeer(t *testing.T) {
	// A spoofed X-Forwarded-Proto marks the session cookie Secure over a plain
	// HTTP connection, and the browser then refuses to send it back.
	cases := []struct {
		name    string
		proxies []string
		peer    string
		proto   string
		want    bool
	}{
		{name: "untrusted peer claiming https", peer: "203.0.113.7:51000", proto: "https", want: false},
		{name: "trusted proxy reporting https", proxies: []string{"10.0.0.0/8"}, peer: "10.1.2.3:443", proto: "https", want: true},
		{name: "trusted proxy reporting http", proxies: []string{"10.0.0.0/8"}, peer: "10.1.2.3:443", proto: "http", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newServerWithProxies(t, tc.proxies...)
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.RemoteAddr = tc.peer
			r.Header.Set("X-Forwarded-Proto", tc.proto)
			if got := s.isTLS(r); got != tc.want {
				t.Errorf("isTLS() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestParseTrustedProxiesRejectsGarbage(t *testing.T) {
	// A typo in WEB_ADMIN_TRUSTED_PROXIES must fail the boot, not silently
	// produce an empty list that looks like "trust nobody" until the day
	// somebody needs the header honoured.
	if _, err := parseTrustedProxies([]string{"10.0.0.0/8", "not-an-address"}); err == nil {
		t.Fatal("parseTrustedProxies accepted an invalid entry")
	}
	if _, err := parseTrustedProxies([]string{"  ", ""}); err != nil {
		t.Fatalf("parseTrustedProxies rejected blank entries: %v", err)
	}
}

func TestLoginThrottleBlocksAfterMaxAttempts(t *testing.T) {
	th := newLoginThrottle()
	for n := range maxLoginAttempt {
		if !th.allow("198.51.100.4") {
			t.Fatalf("attempt %d was throttled, want allowed", n+1)
		}
	}
	if th.allow("198.51.100.4") {
		t.Error("attempt past the limit was allowed")
	}
	if !th.allow("198.51.100.5") {
		t.Error("a different address was throttled by its neighbour's attempts")
	}
	th.reset("198.51.100.4")
	if !th.allow("198.51.100.4") {
		t.Error("reset after a successful login did not clear the counter")
	}
}
