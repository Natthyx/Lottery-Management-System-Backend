package config

import (
	"strings"
	"testing"
)

func TestLoad_RequiresJWTSecret(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("JWT_SECRET", "")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "JWT_SECRET") {
		t.Fatalf("expected JWT_SECRET error, got %v", err)
	}
}

func TestLoad_RejectsShortJWTSecret(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("JWT_SECRET", "too-short")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "at least 32") {
		t.Fatalf("expected length error, got %v", err)
	}
}

func TestLoad_RequiresDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("JWT_SECRET", strings.Repeat("a", 32))
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("expected DATABASE_URL error, got %v", err)
	}
}

func TestLoad_PrefersRedisURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("JWT_SECRET", strings.Repeat("a", 32))
	t.Setenv("REDIS_URL", "redis://localhost:6380/2")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.RedisOptions.Addr != "localhost:6380" {
		t.Errorf("addr: got %q want localhost:6380", cfg.RedisOptions.Addr)
	}
	if cfg.RedisOptions.DB != 2 {
		t.Errorf("db: got %d want 2", cfg.RedisOptions.DB)
	}
}

func TestLoad_FallsBackToRedisAddr(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("JWT_SECRET", strings.Repeat("a", 32))
	t.Setenv("REDIS_URL", "")
	t.Setenv("REDIS_ADDR", "redis-host:6379")
	t.Setenv("REDIS_DB", "3")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.RedisOptions.Addr != "redis-host:6379" {
		t.Errorf("addr: got %q want redis-host:6379", cfg.RedisOptions.Addr)
	}
	if cfg.RedisOptions.DB != 3 {
		t.Errorf("db: got %d want 3", cfg.RedisOptions.DB)
	}
}

func TestSplitCSV(t *testing.T) {
	in := "https://a.com, https://b.com ,, "
	got := splitCSV(in)
	want := []string{"https://a.com", "https://b.com"}
	if len(got) != len(want) {
		t.Fatalf("len: got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("idx %d: got %q want %q", i, got[i], want[i])
		}
	}
}
