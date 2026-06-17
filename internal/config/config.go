package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// Config holds all runtime configuration loaded from environment variables.
// In production these come from a secret manager (Kubernetes Secrets, AWS
// Secrets Manager, HashiCorp Vault). Locally we source them from a .env file.
type Config struct {
	// ── Server ──────────────────────────────────────────────
	Env             string
	Port            string
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	IdleTimeout     time.Duration
	ShutdownTimeout time.Duration
	MaxRequestBytes int64

	// ── PostgreSQL ──────────────────────────────────────────
	DatabaseURL    string
	MigrateOnStart bool

	// ── Redis ───────────────────────────────────────────────
	RedisOptions *redis.Options

	// ── JWT ─────────────────────────────────────────────────
	JWTSecret string
	JWTExpiry time.Duration

	// ── Lottery / booking ───────────────────────────────────
	LockTTL    time.Duration
	MaxWinners int

	// ── Auth rate limiting ──────────────────────────────────
	AuthLimitRequests int
	AuthLimitWindow   time.Duration
	APILimitRequests  int
	APILimitWindow    time.Duration

	// ── CORS ────────────────────────────────────────────────
	CORSAllowedOrigins []string

	// ── Admin bootstrap ─────────────────────────────────────
	BootstrapAdminEmail    string
	BootstrapAdminPassword string
	BootstrapAdminName     string
}

// Load reads every required env var and returns a validated Config.
// Missing required values produce an error instead of a panic so callers
// can format diagnostics.
func Load() (*Config, error) {
	jwtSecret := strings.TrimSpace(os.Getenv("JWT_SECRET"))
	if jwtSecret == "" {
		return nil, fmt.Errorf("JWT_SECRET is required")
	}
	if len(jwtSecret) < 32 {
		return nil, fmt.Errorf("JWT_SECRET must be at least 32 characters (got %d)", len(jwtSecret))
	}

	dbURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if dbURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}

	redisOpts, err := loadRedisOptions()
	if err != nil {
		return nil, err
	}

	cfg := &Config{
		Env:             getEnv("ENV", "development"),
		Port:            getEnv("PORT", "8080"),
		ReadTimeout:     getDuration("READ_TIMEOUT", 10*time.Second),
		WriteTimeout:    getDuration("WRITE_TIMEOUT", 30*time.Second),
		IdleTimeout:     getDuration("IDLE_TIMEOUT", 120*time.Second),
		ShutdownTimeout: getDuration("SHUTDOWN_TIMEOUT", 30*time.Second),
		MaxRequestBytes: int64(getInt("MAX_REQUEST_BYTES", 1<<20)), // 1 MiB

		DatabaseURL:    dbURL,
		MigrateOnStart: getBool("MIGRATE_ON_START", true),

		RedisOptions: redisOpts,

		JWTSecret: jwtSecret,
		JWTExpiry: getDuration("JWT_EXPIRY", 24*time.Hour),

		LockTTL:    getDuration("LOCK_TTL", 5*time.Second),
		MaxWinners: getInt("MAX_WINNERS", 100),

		AuthLimitRequests: getInt("AUTH_RATE_LIMIT_REQUESTS", 10),
		AuthLimitWindow:   getDuration("AUTH_RATE_LIMIT_WINDOW", 1*time.Minute),
		APILimitRequests:  getInt("API_RATE_LIMIT_REQUESTS", 100),
		APILimitWindow:    getDuration("API_RATE_LIMIT_WINDOW", 1*time.Minute),

		CORSAllowedOrigins: splitCSV(getEnv("CORS_ALLOWED_ORIGINS", "")),

		BootstrapAdminEmail:    strings.TrimSpace(os.Getenv("BOOTSTRAP_ADMIN_EMAIL")),
		BootstrapAdminPassword: os.Getenv("BOOTSTRAP_ADMIN_PASSWORD"),
		BootstrapAdminName:     getEnv("BOOTSTRAP_ADMIN_NAME", "System Admin"),
	}

	return cfg, nil
}

// loadRedisOptions accepts either:
//   - REDIS_URL=redis://[user:pass@]host:port/db    (preferred)
//   - REDIS_ADDR=host:port + REDIS_PASSWORD + REDIS_DB
//
// We prefer REDIS_URL because it's standard across managed providers.
func loadRedisOptions() (*redis.Options, error) {
	if url := strings.TrimSpace(os.Getenv("REDIS_URL")); url != "" {
		opts, err := redis.ParseURL(url)
		if err != nil {
			return nil, fmt.Errorf("REDIS_URL invalid: %w", err)
		}
		applyRedisPoolDefaults(opts)
		return opts, nil
	}

	opts := &redis.Options{
		Addr:     getEnv("REDIS_ADDR", "localhost:6379"),
		Password: os.Getenv("REDIS_PASSWORD"),
		DB:       getInt("REDIS_DB", 0),
	}
	applyRedisPoolDefaults(opts)
	return opts, nil
}

func applyRedisPoolDefaults(o *redis.Options) {
	if o.PoolSize == 0 {
		o.PoolSize = 20
	}
	if o.MinIdleConns == 0 {
		o.MinIdleConns = 5
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func getBool(key string, fallback bool) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if v == "" {
		return fallback
	}
	switch v {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return fallback
}

func getDuration(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fallback
	}
	return d
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
