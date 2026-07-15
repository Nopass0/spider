// Команда spider — точка входа сервера Spider.
//
// Конфигурация — из переменных окружения (см. .env.example).
// Логи — slog в JSON в stdout (под systemd/journalctl).
//
// Запуск:
//
//	SPIDER_ADMIN_KEY=... go run ./cmd/spider
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nopass0/spider/server/internal/api"
	"github.com/nopass0/spider/server/internal/commands"
	"github.com/nopass0/spider/server/internal/config"
	"github.com/nopass0/spider/server/internal/hub"
	"github.com/nopass0/spider/server/internal/panel"
	"github.com/nopass0/spider/server/internal/store"
	"github.com/nopass0/spider/server/internal/version"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("config load failed", "err", err)
		os.Exit(1)
	}
	log := newLogger(cfg.LogLevel)
	log.Info("starting spider server", "version", version.Version, "addr", cfg.HTTPAddr)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Хранилище.
	st, err := store.New(ctx, cfg.DBPath)
	if err != nil {
		log.Error("store init failed", "err", err)
		os.Exit(1)
	}
	defer st.Close()
	// При старте все устройства помечаем офлайн (соединений ещё нет).
	_ = st.MarkAllOffline(ctx)

	// Hub + диспетчер.
	h := hub.New()
	dispatcher := commands.New(st, h, log)

	// HTTP API.
	a := api.New(cfg, st, h, dispatcher, log)
	mux := http.NewServeMux()
	mux.Handle("/", panel.Handler())   // SPA (встроенная панель)
	mux.Handle("/agent/", a.Router())  // NB: Router() монтирует /agent и /admin
	mux.Handle("/admin/", a.Router())
	mux.Handle("/healthz", a.Router())

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           withLogging(log)(mux),
		ReadHeaderTimeout: 10 * time.Second,
		// За Caddy ставим большие таймауты для long-poll и WS.
		IdleTimeout: 120 * time.Second,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server error", "err", err)
			os.Exit(1)
		}
	}()
	log.Info("server listening", "addr", cfg.HTTPAddr)

	<-ctx.Done()
	log.Info("shutting down")
	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutCtx)
}

// newLogger настраивает slog по уровню из конфига.
func newLogger(level string) *slog.Logger {
	var lv slog.Level
	switch level {
	case "debug":
		lv = slog.LevelDebug
	case "warn":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	default:
		lv = slog.LevelInfo
	}
	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lv})
	return slog.New(h)
}

// withLogging — minimal access-лог (метод, путь, статус, длительность).
func withLogging(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rw := &statusRecorder{ResponseWriter: w, status: 200}
			next.ServeHTTP(rw, r)
			log.Info("http",
				"method", r.Method, "path", r.URL.Path,
				"status", rw.status, "dur_ms", time.Since(start).Milliseconds())
		})
	}
}

// statusRecorder перехватывает статус-код для логирования.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}
