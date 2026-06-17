package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const testSecret = "test-secret-with-enough-length-for-hmac"

func TestGenerateAndValidateToken(t *testing.T) {
	tok, err := GenerateToken(42, "admin", testSecret, time.Hour)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	called := false
	h := Authenticate(testSecret)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uid, ok := GetUserID(r.Context())
		if !ok || uid != 42 {
			t.Errorf("user id not propagated: %v ok=%v", uid, ok)
		}
		role, _ := GetRole(r.Context())
		if role != "admin" {
			t.Errorf("role not propagated: %q", role)
		}
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rw.Code, rw.Body.String())
	}
	if !called {
		t.Fatal("downstream handler not invoked")
	}
}

func TestAuthenticateRejectsMissingHeader(t *testing.T) {
	h := Authenticate(testSecret)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("should not be reached")
	}))
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, httptest.NewRequest(http.MethodGet, "/", nil))
	if rw.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rw.Code)
	}
}

func TestAuthenticateRejectsWrongAlg(t *testing.T) {
	// Forge a token with alg=none. The verifier must refuse it.
	claims := jwt.MapClaims{
		"user_id": 1, "role": "admin", "iss": "lottery-system",
		"exp": time.Now().Add(time.Hour).Unix(),
	}
	tok, err := jwt.NewWithClaims(jwt.SigningMethodNone, claims).SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("forge: %v", err)
	}
	h := Authenticate(testSecret)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("should not be reached")
	}))
	rw := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	h.ServeHTTP(rw, req)
	if rw.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rw.Code)
	}
}

func TestRequireAdmin(t *testing.T) {
	cases := []struct {
		role string
		code int
	}{
		{"admin", http.StatusOK},
		{"user", http.StatusForbidden},
		{"", http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.role, func(t *testing.T) {
			h := RequireAdmin(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			ctx := context.WithValue(req.Context(), ContextKeyRole, tc.role)
			req = req.WithContext(ctx)
			rw := httptest.NewRecorder()
			h.ServeHTTP(rw, req)
			if rw.Code != tc.code {
				t.Fatalf("role=%q want %d got %d", tc.role, tc.code, rw.Code)
			}
		})
	}
}
