package config

import (
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("SPIDER_ADMIN_KEY", "supersecret-admin-key-12345")
	for _, k := range []string{
		"SPIDER_HTTP_ADDR", "SPIDER_PUBLIC_URL", "SPIDER_DB_PATH",
		"SPIDER_LONGPOLL_TIMEOUT", "SPIDER_ENROLL_TTL_HOURS", "SPIDER_LOG_LEVEL",
	} {
		t.Setenv(k, "")
	}
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.HTTPAddr != ":8080" {
		t.Errorf("HTTPAddr = %q", cfg.HTTPAddr)
	}
	if cfg.LongPollTimeout != 30*time.Second {
		t.Errorf("LongPollTimeout = %v", cfg.LongPollTimeout)
	}
	if cfg.EnrollTTL != 24*time.Hour {
		t.Errorf("EnrollTTL = %v", cfg.EnrollTTL)
	}
}

func TestLoadMissingAdminKey(t *testing.T) {
	t.Setenv("SPIDER_ADMIN_KEY", "")
	if _, err := Load(); err == nil {
		t.Fatal("ожидалась ошибка при отсутствии ADMIN_KEY")
	}
}

func TestLoadShortAdminKey(t *testing.T) {
	t.Setenv("SPIDER_ADMIN_KEY", "short")
	if _, err := Load(); err == nil {
		t.Fatal("ожидалась ошибка для короткого ADMIN_KEY")
	}
}

func TestLoadBadLogLevel(t *testing.T) {
	t.Setenv("SPIDER_ADMIN_KEY", "supersecret-admin-key-12345")
	t.Setenv("SPIDER_LOG_LEVEL", "trace")
	if _, err := Load(); err == nil {
		t.Fatal("ожидалась ошибка для неверного log level")
	}
}

func TestLoadDurationVariants(t *testing.T) {
	cases := map[string]time.Duration{
		"45s":  45 * time.Second,
		"60":   60 * time.Second,
		"1m":   60 * time.Second,
		"1m0s": 60 * time.Second,
	}
	for raw, want := range cases {
		t.Setenv("SPIDER_ADMIN_KEY", "supersecret-admin-key-12345")
		t.Setenv("SPIDER_LONGPOLL_TIMEOUT", raw)
		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load(%q): %v", raw, err)
		}
		if cfg.LongPollTimeout != want {
			t.Errorf("duration(%q) = %v, want %v", raw, cfg.LongPollTimeout, want)
		}
	}
}

func TestPublicURLTrimmed(t *testing.T) {
	t.Setenv("SPIDER_ADMIN_KEY", "supersecret-admin-key-12345")
	t.Setenv("SPIDER_PUBLIC_URL", "https://spider.lowkey.su/")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PublicURL != "https://spider.lowkey.su" {
		t.Errorf("trailing slash не обрезан: %q", cfg.PublicURL)
	}
}
