package middleware

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/Natthyx/lottery-system/internal/httpx"
)

type contextKey string

const (
	ContextKeyUserID contextKey = "user_id"
	ContextKeyRole   contextKey = "role"
)

// Claims is the JWT payload embedded in every token.
type Claims struct {
	UserID int64  `json:"user_id"`
	Role   string `json:"role"`
	jwt.RegisteredClaims
}

// GenerateToken creates a signed JWT (HMAC-SHA256) for a given user.
func GenerateToken(userID int64, role, secret string, expiry time.Duration) (string, error) {
	now := time.Now()
	claims := &Claims{
		UserID: userID,
		Role:   role,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(expiry)),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			Issuer:    "lottery-system",
			Subject:   subjectFromUserID(userID),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

// Authenticate validates the JWT in the Authorization header and injects
// user_id and role into the request context.
func Authenticate(secret string) func(http.Handler) http.Handler {
	keyFunc := func(t *jwt.Token) (interface{}, error) {
		// Guard against "alg: none" and any non-HMAC token.
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, jwt.ErrSignatureInvalid
		}
		return []byte(secret), nil
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				httpx.Unauthorized(w, "missing Authorization header")
				return
			}
			parts := strings.SplitN(authHeader, " ", 2)
			if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") || parts[1] == "" {
				httpx.Unauthorized(w, "malformed Authorization header")
				return
			}

			claims := &Claims{}
			token, err := jwt.ParseWithClaims(parts[1], claims, keyFunc,
				jwt.WithValidMethods([]string{"HS256"}),
				jwt.WithIssuer("lottery-system"),
			)
			if err != nil || !token.Valid {
				httpx.Unauthorized(w, "invalid or expired token")
				return
			}

			ctx := context.WithValue(r.Context(), ContextKeyUserID, claims.UserID)
			ctx = context.WithValue(ctx, ContextKeyRole, claims.Role)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireAdmin ensures the authenticated user is an admin. Must come after Authenticate.
func RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		role, ok := r.Context().Value(ContextKeyRole).(string)
		if !ok || role != "admin" {
			httpx.Forbidden(w, "forbidden: admin only")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// GetUserID extracts the authenticated user ID from context.
func GetUserID(ctx context.Context) (int64, bool) {
	id, ok := ctx.Value(ContextKeyUserID).(int64)
	return id, ok
}

// GetRole extracts the authenticated user role from context.
func GetRole(ctx context.Context) (string, bool) {
	r, ok := ctx.Value(ContextKeyRole).(string)
	return r, ok
}

func subjectFromUserID(id int64) string {
	return "user:" + strconv.FormatInt(id, 10)
}
