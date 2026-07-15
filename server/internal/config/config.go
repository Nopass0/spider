// Package config загружает и валидирует конфигурацию сервера Spider из
// переменных окружения. Чувствительные значения (ADMIN_KEY) обязательно
// должны быть заданы — без них сервер не стартует.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config — полная конфигурация сервера.
type Config struct {
	// AdminKey — токен доступа к admin API и панели.
	AdminKey string
	// HTTPAddr — адрес, который слушает HTTP-сервер.
	HTTPAddr string
	// PublicURL — публичный URL (для ссылок и CORS). Без слеша на конце.
	PublicURL string
	// DBPath — путь к файлу SQLite.
	DBPath string
	// LongPollTimeout — как долго держать long-poll открытым.
	LongPollTimeout time.Duration
	// EnrollTTL — срок действия enrollment-токена.
	EnrollTTL time.Duration
	// LogLevel — debug | info | warn | error.
	LogLevel string
}

// Load читает конфигурацию из окружения. Возвращает ошибку, если обязательное
// поле (ADMIN_KEY) не задано или значение невалидно.
func Load() (Config, error) {
	cfg := Config{
		AdminKey:        getEnv("SPIDER_ADMIN_KEY", ""),
		HTTPAddr:        getEnv("SPIDER_HTTP_ADDR", ":8080"),
		PublicURL:       strings.TrimRight(getEnv("SPIDER_PUBLIC_URL", "http://localhost:8080"), "/"),
		DBPath:          getEnv("SPIDER_DB_PATH", "spider.db"),
		LongPollTimeout: getDuration("SPIDER_LONGPOLL_TIMEOUT", 30*time.Second),
		EnrollTTL:       time.Duration(getInt("SPIDER_ENROLL_TTL_HOURS", 24)) * time.Hour,
		LogLevel:        strings.ToLower(getEnv("SPIDER_LOG_LEVEL", "info")),
	}

	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// validate проверяет инварианты конфигурации.
func (c Config) validate() error {
	if c.AdminKey == "" {
		return fmt.Errorf("config: SPIDER_ADMIN_KEY обязательный (см. .env.example)")
	}
	if len(c.AdminKey) < 8 {
		return fmt.Errorf("config: SPIDER_ADMIN_KEY слишком короткий (минимум 8 символов)")
	}
	if c.HTTPAddr == "" {
		return fmt.Errorf("config: SPIDER_HTTP_ADDR пуст")
	}
	if !validLogLevel(c.LogLevel) {
		return fmt.Errorf("config: SPIDER_LOG_LEVEL должен быть debug|info|warn|error, got %q", c.LogLevel)
	}
	return nil
}

func validLogLevel(l string) bool {
	switch l {
	case "debug", "info", "warn", "error":
		return true
	}
	return false
}

// --- env-хелперы (DRY) ---

func getEnv(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func getDuration(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		// Допускаем значение в секундах как голое число.
		if secs, err2 := strconv.Atoi(v); err2 == nil {
			return time.Duration(secs) * time.Second
		}
		return def
	}
	return d
}

func getInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}
