package pgerr

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestPgErrCodes(t *testing.T) {
	cases := []struct {
		code string
		want func(error) bool
	}{
		{"23505", IsUnique},
		{"23503", IsForeignKey},
		{"23514", IsCheck},
		{"23502", IsNotNull},
		{"40001", IsSerialization},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			err := &pgconn.PgError{Code: tc.code, Message: "x"}
			if !tc.want(err) {
				t.Fatalf("classifier missed code %s", tc.code)
			}
			if tc.want(errors.New("plain")) {
				t.Fatalf("classifier matched non-pg error for code %s", tc.code)
			}
		})
	}
}

func TestPgErrUnwrapped(t *testing.T) {
	// Real-world usage: services wrap with fmt.Errorf("...: %w", err).
	// IsUnique must look through that wrapping.
	wrapped := fmt.Errorf("inserting row: %w", &pgconn.PgError{Code: "23505"})
	if !IsUnique(wrapped) {
		t.Fatal("IsUnique should unwrap fmt.Errorf %w wrapping")
	}
}
