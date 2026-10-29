package middleware

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func clientIPOf(t *testing.T, trusted []string, remote string, headers map[string]string) string {
	t.Helper()
	var prefixes []netip.Prefix
	for _, s := range trusted {
		prefixes = append(prefixes, netip.MustParsePrefix(s))
	}
	var got string
	h := ClientIP(prefixes)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got = r.RemoteAddr }))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = remote
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	h.ServeHTTP(httptest.NewRecorder(), req)
	return got
}

func TestClientIP_IgnoresHeadersWithoutTrustedProxies(t *testing.T) {
	got := clientIPOf(t, nil, "198.51.100.9:5555", map[string]string{
		"X-Forwarded-For": "203.0.113.1", "X-Real-IP": "203.0.113.2",
	})
	if got != "198.51.100.9:5555" {
		t.Fatalf("spoofed header changed the client IP: %q", got)
	}
}

func TestClientIP_IgnoresHeadersFromUntrustedPeer(t *testing.T) {
	got := clientIPOf(t, []string{"10.0.0.0/8"}, "198.51.100.9:5555", map[string]string{"X-Forwarded-For": "203.0.113.1"})
	if got != "198.51.100.9:5555" {
		t.Fatalf("untrusted peer could set the client IP: %q", got)
	}
}

func TestClientIP_UsesRightmostUntrustedHop(t *testing.T) {
	// Client sends a forged left-most entry; the trusted proxy appends the real peer.
	got := clientIPOf(t, []string{"10.0.0.0/8"}, "10.0.0.5:443", map[string]string{
		"X-Forwarded-For": "1.2.3.4, 203.0.113.7, 10.0.0.9",
	})
	if got != "203.0.113.7" {
		t.Fatalf("got %q want 203.0.113.7", got)
	}
}

func TestClientIP_FallsBackToXRealIP(t *testing.T) {
	got := clientIPOf(t, []string{"10.0.0.0/8"}, "10.0.0.5:443", map[string]string{"X-Real-IP": "203.0.113.8"})
	if got != "203.0.113.8" {
		t.Fatalf("got %q want 203.0.113.8", got)
	}
}

func TestClientIP_MalformedChainKeepsPeer(t *testing.T) {
	got := clientIPOf(t, []string{"10.0.0.0/8"}, "10.0.0.5:443", map[string]string{"X-Forwarded-For": "garbage, 10.0.0.9"})
	if got != "10.0.0.5:443" {
		t.Fatalf("got %q want peer unchanged", got)
	}
}

func TestLoginAccountKey_NormalisesAndRestoresBody(t *testing.T) {
	body := `{"email":"  Alice@Example.COM ","password":"x"}`
	req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewBufferString(body))
	key, err := LoginAccountKey(req)
	if err != nil || key != "login:alice@example.com" {
		t.Fatalf("key=%q err=%v", key, err)
	}
	rest, _ := io.ReadAll(req.Body)
	if string(rest) != body {
		t.Fatalf("body not restored: %q", rest)
	}
}

func TestSecurityHeaders(t *testing.T) {
	rw := httptest.NewRecorder()
	SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})).ServeHTTP(rw, httptest.NewRequest(http.MethodGet, "/", nil))
	for k, want := range map[string]string{"X-Content-Type-Options": "nosniff", "Cache-Control": "no-store", "X-Frame-Options": "DENY"} {
		if got := rw.Header().Get(k); got != want {
			t.Errorf("%s = %q want %q", k, got, want)
		}
	}
}
