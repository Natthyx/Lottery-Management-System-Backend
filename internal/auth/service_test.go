package auth

import (
	"errors"
	"strings"
	"testing"

	"github.com/Natthyx/lottery-system/internal/models"
)

func TestValidateRegistration(t *testing.T) {
	cases := []struct {
		name    string
		req     models.RegisterRequest
		wantErr bool
		errSub  string
	}{
		{
			name: "happy path",
			req:  models.RegisterRequest{Email: "Alice@Example.com", Password: "longenough", FullName: "Alice"},
		},
		{
			name:    "empty email",
			req:     models.RegisterRequest{Email: "  ", Password: "longenough", FullName: "Alice"},
			wantErr: true,
			errSub:  "email is required",
		},
		{
			name:    "malformed email",
			req:     models.RegisterRequest{Email: "not-an-email", Password: "longenough", FullName: "Alice"},
			wantErr: true,
			errSub:  "not a valid",
		},
		{
			name:    "no full name",
			req:     models.RegisterRequest{Email: "a@b.com", Password: "longenough", FullName: " "},
			wantErr: true,
			errSub:  "full_name is required",
		},
		{
			name:    "short password",
			req:     models.RegisterRequest{Email: "a@b.com", Password: "short", FullName: "Alice"},
			wantErr: true,
			errSub:  "at least",
		},
		{
			name:    "huge password",
			req:     models.RegisterRequest{Email: "a@b.com", Password: strings.Repeat("x", maxPasswordLen+1), FullName: "Alice"},
			wantErr: true,
			errSub:  "too long",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			email, name, err := validateRegistration(tc.req)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.errSub)
				}
				if !errors.Is(err, ErrInvalidInput) {
					t.Errorf("expected error to wrap ErrInvalidInput, got %v", err)
				}
				if !strings.Contains(err.Error(), tc.errSub) {
					t.Errorf("error %q does not contain %q", err.Error(), tc.errSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if email != strings.ToLower(strings.TrimSpace(tc.req.Email)) {
				t.Errorf("email normalisation: got %q", email)
			}
			if name != strings.TrimSpace(tc.req.FullName) {
				t.Errorf("name trim: got %q", name)
			}
		})
	}
}
