package middleware

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// ClientIP replaces chi's RealIP. chi's version trusts X-Forwarded-For /
// X-Real-IP from anyone, which let a client pick its own rate-limit key
// (a spoofed header reset the per-IP limiter). This version only honours
// those headers when the TCP peer is one of the configured trusted proxies,
// and then takes the right-most X-Forwarded-For entry that is not itself a
// trusted proxy (entries to the left of it are client-controlled).
//
// With no trusted proxies (the default) r.RemoteAddr is left untouched.
func ClientIP(trusted []netip.Prefix) func(http.Handler) http.Handler {
	isTrusted := func(a netip.Addr) bool {
		a = a.Unmap()
		for _, p := range trusted {
			if p.Contains(a) {
				return true
			}
		}
		return false
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if len(trusted) == 0 {
				next.ServeHTTP(w, r)
				return
			}
			peer, ok := peerAddr(r.RemoteAddr)
			if !ok || !isTrusted(peer) {
				next.ServeHTTP(w, r)
				return
			}
			if ip, ok := fromForwardedFor(r.Header.Values("X-Forwarded-For"), isTrusted); ok {
				r.RemoteAddr = ip.String()
			} else if ip, err := netip.ParseAddr(strings.TrimSpace(r.Header.Get("X-Real-IP"))); err == nil {
				r.RemoteAddr = ip.Unmap().String()
			}
			next.ServeHTTP(w, r)
		})
	}
}

func peerAddr(remoteAddr string) (netip.Addr, bool) {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	a, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, false
	}
	return a.Unmap(), true
}

func fromForwardedFor(values []string, isTrusted func(netip.Addr) bool) (netip.Addr, bool) {
	var hops []string
	for _, v := range values {
		hops = append(hops, strings.Split(v, ",")...)
	}
	for i := len(hops) - 1; i >= 0; i-- {
		a, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			return netip.Addr{}, false // malformed chain: don't guess
		}
		if a = a.Unmap(); !isTrusted(a) {
			return a, true
		}
	}
	return netip.Addr{}, false
}

// SecurityHeaders sets conservative headers for a JSON API. Responses can
// carry JWTs, so nothing may be cached by shared caches.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

// LoginAccountKey is an httprate key function that keys POST /auth/login by
// the (normalised) email in the body, so a credential-stuffing attack spread
// over many IPs is still limited per account. The body is restored for the
// handler. Requests without a parsable email share one bucket.
func LoginAccountKey(r *http.Request) (string, error) {
	if r.Body == nil {
		return "login:none", nil
	}
	body, err := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewReader(body))
	if err != nil {
		return "login:none", nil
	}
	var req struct {
		Email string `json:"email"`
	}
	if json.Unmarshal(body, &req) != nil || strings.TrimSpace(req.Email) == "" {
		return "login:none", nil
	}
	return "login:" + strings.ToLower(strings.TrimSpace(req.Email)), nil
}
