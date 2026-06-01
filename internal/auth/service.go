package auth

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/natannan/lottery-system/internal/middleware"
	"github.com/natannan/lottery-system/internal/models"
	"golang.org/x/crypto/bcrypt"
)

// Service handles user registration and authentication.
type Service struct {
	db        *pgxpool.Pool
	jwtSecret string
	jwtExpiry time.Duration
}

func NewService(db *pgxpool.Pool, jwtSecret string, jwtExpiry time.Duration) *Service {
	return &Service{db: db, jwtSecret: jwtSecret, jwtExpiry: jwtExpiry}
}

// Register creates a new user account.
// Passwords are hashed with bcrypt (cost 12) — never stored in plaintext.
func (s *Service) Register(ctx context.Context, req models.RegisterRequest) (*models.User, error) {
	if req.Email == "" || req.Password == "" || req.FullName == "" {
		return nil, fmt.Errorf("email, password, and full_name are required")
	}
	if len(req.Password) < 8 {
		return nil, fmt.Errorf("password must be at least 8 characters")
	}

	// bcrypt cost 12: ~300ms on modern hardware.
	// High enough to make brute-force attacks impractical,
	// low enough that legitimate logins are not noticeably slow.
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), 12)
	if err != nil {
		return nil, fmt.Errorf("hashing password: %w", err)
	}

	var user models.User
	err = s.db.QueryRow(ctx, `
		INSERT INTO users (email, password_hash, full_name)
		VALUES ($1, $2, $3)
		RETURNING id, email, full_name, role, created_at, updated_at
	`, req.Email, string(hash), req.FullName).Scan(
		&user.ID, &user.Email, &user.FullName, &user.Role,
		&user.CreatedAt, &user.UpdatedAt,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, fmt.Errorf("email already registered")
		}
		return nil, fmt.Errorf("creating user: %w", err)
	}

	return &user, nil
}

// Login validates credentials and returns a signed JWT.
func (s *Service) Login(ctx context.Context, req models.LoginRequest) (*models.LoginResponse, error) {
	var user models.User
	err := s.db.QueryRow(ctx, `
		SELECT id, email, full_name, role, password_hash, created_at, updated_at
		FROM users WHERE email = $1
	`, req.Email).Scan(
		&user.ID, &user.Email, &user.FullName, &user.Role, &user.PasswordHash,
		&user.CreatedAt, &user.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		// Return the same error for "user not found" and "wrong password".
		// This prevents user enumeration attacks.
		return nil, fmt.Errorf("invalid credentials")
	}
	if err != nil {
		return nil, fmt.Errorf("fetching user: %w", err)
	}

	// Constant-time comparison — bcrypt.CompareHashAndPassword is not
	// vulnerable to timing attacks because bcrypt's cost makes timing
	// differences negligible.
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
		return nil, fmt.Errorf("invalid credentials")
	}

	token, err := middleware.GenerateToken(user.ID, user.Role, s.jwtSecret, s.jwtExpiry)
	if err != nil {
		return nil, fmt.Errorf("generating token: %w", err)
	}

	// Zero out the hash before returning — belt and suspenders
	user.PasswordHash = ""

	return &models.LoginResponse{Token: token, User: user}, nil
}

func isUniqueViolation(err error) bool {
	return err != nil && (contains(err.Error(), "23505") || contains(err.Error(), "unique"))
}

func contains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
