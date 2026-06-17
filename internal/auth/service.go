package auth

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"github.com/Natthyx/lottery-system/internal/middleware"
	"github.com/Natthyx/lottery-system/internal/models"
	"github.com/Natthyx/lottery-system/internal/pgerr"
)

const (
	minPasswordLen = 8
	maxPasswordLen = 128 // bcrypt input is silently truncated at 72; cap requests early to avoid confusion
	bcryptCost     = 12
)

// Domain errors. Handlers map these to HTTP status codes.
var (
	ErrInvalidInput       = errors.New("invalid input")
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrEmailTaken         = errors.New("email already registered")
	ErrUserNotFound       = errors.New("user not found")
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

// Register creates a new user account with role='user'.
// Admin accounts are created out-of-band (see BootstrapAdmin / PromoteToAdmin).
func (s *Service) Register(ctx context.Context, req models.RegisterRequest) (*models.User, error) {
	email, fullName, err := validateRegistration(req)
	if err != nil {
		return nil, err
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcryptCost)
	if err != nil {
		return nil, fmt.Errorf("hashing password: %w", err)
	}

	var user models.User
	err = s.db.QueryRow(ctx, `
		INSERT INTO users (email, password_hash, full_name)
		VALUES ($1, $2, $3)
		RETURNING id, email, full_name, role, created_at, updated_at
	`, email, string(hash), fullName).Scan(
		&user.ID, &user.Email, &user.FullName, &user.Role,
		&user.CreatedAt, &user.UpdatedAt,
	)
	if err != nil {
		if pgerr.IsUnique(err) {
			return nil, ErrEmailTaken
		}
		return nil, fmt.Errorf("creating user: %w", err)
	}
	return &user, nil
}

// Login validates credentials and returns a signed JWT plus the user.
// Returns ErrInvalidCredentials for both "no such user" and "bad password"
// to defend against user enumeration.
func (s *Service) Login(ctx context.Context, req models.LoginRequest) (*models.LoginResponse, error) {
	email := strings.TrimSpace(strings.ToLower(req.Email))
	if email == "" || req.Password == "" {
		return nil, ErrInvalidCredentials
	}

	var user models.User
	err := s.db.QueryRow(ctx, `
		SELECT id, email, full_name, role, password_hash, created_at, updated_at
		FROM users WHERE email = $1
	`, email).Scan(
		&user.ID, &user.Email, &user.FullName, &user.Role, &user.PasswordHash,
		&user.CreatedAt, &user.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Run bcrypt on a dummy hash to keep timing similar to a real
			// password check — defence in depth against timing-based
			// account enumeration.
			_ = bcrypt.CompareHashAndPassword([]byte(dummyHash), []byte(req.Password))
			return nil, ErrInvalidCredentials
		}
		return nil, fmt.Errorf("fetching user: %w", err)
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
		return nil, ErrInvalidCredentials
	}

	token, err := middleware.GenerateToken(user.ID, user.Role, s.jwtSecret, s.jwtExpiry)
	if err != nil {
		return nil, fmt.Errorf("generating token: %w", err)
	}

	user.PasswordHash = ""
	return &models.LoginResponse{Token: token, User: user}, nil
}

// Me returns the user record for the authenticated principal.
func (s *Service) Me(ctx context.Context, userID int64) (*models.User, error) {
	var u models.User
	err := s.db.QueryRow(ctx, `
		SELECT id, email, full_name, role, created_at, updated_at
		FROM users WHERE id = $1
	`, userID).Scan(&u.ID, &u.Email, &u.FullName, &u.Role, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("fetching user: %w", err)
	}
	return &u, nil
}

// PromoteToAdmin elevates a user's role. Caller must enforce that only
// admins can invoke this.
func (s *Service) PromoteToAdmin(ctx context.Context, userID int64) (*models.User, error) {
	var u models.User
	err := s.db.QueryRow(ctx, `
		UPDATE users SET role = 'admin' WHERE id = $1
		RETURNING id, email, full_name, role, created_at, updated_at
	`, userID).Scan(&u.ID, &u.Email, &u.FullName, &u.Role, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("promoting user: %w", err)
	}
	return &u, nil
}

// BootstrapAdmin idempotently ensures an admin user exists with the given
// email/password. Intended to be called at server start when configured
// via BOOTSTRAP_ADMIN_EMAIL / BOOTSTRAP_ADMIN_PASSWORD environment
// variables. Safe to omit in production once an admin is in place.
func (s *Service) BootstrapAdmin(ctx context.Context, email, password, fullName string) error {
	email = strings.TrimSpace(strings.ToLower(email))
	if email == "" || password == "" {
		return ErrInvalidInput
	}
	if _, err := mail.ParseAddress(email); err != nil {
		return fmt.Errorf("invalid admin email: %w", err)
	}
	if len(password) < minPasswordLen {
		return fmt.Errorf("admin password must be at least %d characters", minPasswordLen)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return fmt.Errorf("hashing password: %w", err)
	}

	// Idempotent upsert: create the user as admin, or promote an existing
	// account with the same email to admin. We do NOT overwrite an existing
	// password hash unless the row was just created.
	_, err = s.db.Exec(ctx, `
		INSERT INTO users (email, password_hash, full_name, role)
		VALUES ($1, $2, $3, 'admin')
		ON CONFLICT (email) DO UPDATE SET role = 'admin'
	`, email, string(hash), fullName)
	if err != nil {
		return fmt.Errorf("bootstrapping admin: %w", err)
	}
	return nil
}

// ── helpers ─────────────────────────────────────────────────────

func validateRegistration(req models.RegisterRequest) (email, fullName string, err error) {
	email = strings.TrimSpace(strings.ToLower(req.Email))
	fullName = strings.TrimSpace(req.FullName)

	if email == "" {
		return "", "", fmt.Errorf("%w: email is required", ErrInvalidInput)
	}
	if _, err := mail.ParseAddress(email); err != nil {
		return "", "", fmt.Errorf("%w: email is not a valid address", ErrInvalidInput)
	}
	if fullName == "" {
		return "", "", fmt.Errorf("%w: full_name is required", ErrInvalidInput)
	}
	if len(fullName) > 200 {
		return "", "", fmt.Errorf("%w: full_name too long", ErrInvalidInput)
	}
	if len(req.Password) < minPasswordLen {
		return "", "", fmt.Errorf("%w: password must be at least %d characters", ErrInvalidInput, minPasswordLen)
	}
	if len(req.Password) > maxPasswordLen {
		return "", "", fmt.Errorf("%w: password too long", ErrInvalidInput)
	}
	return email, fullName, nil
}

// dummyHash is a bcrypt hash of a random string we throw away. Comparing
// against it in the "user not found" branch makes login timing constant.
const dummyHash = "$2a$12$abcdefghijklmnopqrstuvOQX0Cgvb6dQ56u4yqAYAhBz3fL.gqfPe"
