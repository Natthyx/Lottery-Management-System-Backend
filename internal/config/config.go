package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config holds all runtime configuration loaded from environment variables.
// In production these are injected via Kubernetes secrets or a vault.
// Locally, we use a .env file sourced before running the binary.
type Config struct {
	// Server
	Port         string
	ReadTimeout  time.Duration
	WriteTimeout time.Duration

	// PostgreSQL
	DatabaseURL string

	// Redis
	RedisAddr     string
	RedisPassword string
	RedisDB       int

	// JWT
	JWTSecret string
	JWTExpiry time.Duration

	// Lottery
	LockTTL    time.Duration // how long a Redis booking lock is held
	MaxWinners int           // global cap on winners per draw
}

// Load reads every required env var and returns a validated Config.
// It fails fast: if anything is missing the process panics at startup.
func Load() (*Config, error) {
	cfg := &Config{
		Port:          getEnv("PORT", "8080"),
		ReadTimeout:   getDuration("READ_TIMEOUT", 10*time.Second),
		WriteTimeout:  getDuration("WRITE_TIMEOUT", 30*time.Second),
		DatabaseURL:   mustGetEnv("DATABASE_URL"),
		RedisAddr:     getEnv("REDIS_ADDR", "localhost:6379"),
		RedisPassword: getEnv("REDIS_PASSWORD", ""),
		RedisDB:       getInt("REDIS_DB", 0),
		JWTSecret:     mustGetEnv("JWT_SECRET"),
		JWTExpiry:     getDuration("JWT_EXPIRY", 24*time.Hour),
		LockTTL:       getDuration("LOCK_TTL", 5*time.Second),
		MaxWinners:    getInt("MAX_WINNERS", 100),
	}
	return cfg, nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func mustGetEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		panic(fmt.Sprintf("required environment variable %q is not set", key))
	}
	return v
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
