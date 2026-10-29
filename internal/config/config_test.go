package config

import (
	"strings"
	"testing"
	"time"
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

func TestLoad_TrustedProxies(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("JWT_SECRET", strings.Repeat("a", 32))
	t.Setenv("TRUSTED_PROXIES", "10.0.0.0/8, 192.168.1.7")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(cfg.TrustedProxies) != 2 || cfg.TrustedProxies[0].String() != "10.0.0.0/8" || cfg.TrustedProxies[1].String() != "192.168.1.7/32" {
		t.Fatalf("got %v", cfg.TrustedProxies)
	}
}

func TestLoad_RejectsBadTrustedProxy(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("JWT_SECRET", strings.Repeat("a", 32))
	t.Setenv("TRUSTED_PROXIES", "not-an-ip")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "TRUSTED_PROXIES") {
		t.Fatalf("expected TRUSTED_PROXIES error, got %v", err)
	}
}

func TestLoad_DefaultsTrustNoProxy(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("JWT_SECRET", strings.Repeat("a", 32))
	t.Setenv("TRUSTED_PROXIES", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(cfg.TrustedProxies) != 0 {
		t.Fatalf("expected no trusted proxies by default, got %v", cfg.TrustedProxies)
	}
	if cfg.DBMaxConns != 25 || cfg.DBMinConns != 5 || cfg.LockWait != 10*time.Second {
		t.Fatalf("unexpected defaults: max=%d min=%d wait=%v", cfg.DBMaxConns, cfg.DBMinConns, cfg.LockWait)
	}
}

func TestLoad_RejectsInvalidPoolBounds(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("JWT_SECRET", strings.Repeat("a", 32))
	t.Setenv("DB_MAX_CONNS", "4")
	t.Setenv("DB_MIN_CONNS", "5")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "DB_MAX_CONNS") {
		t.Fatalf("expected pool bounds error, got %v", err)
	}
}
