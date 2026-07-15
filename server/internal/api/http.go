// Package api реализует HTTP/WS обработчики сервера Spider: admin API (под
// Bearer ADMIN_KEY), agent endpoints (enrollment, WebSocket, long-poll) и
// раздачу встроенной панели.
//
// Авторизация админа — постоянный токен сравнением constant-time.
// Авторизация агента — device_id + ключ сессии (проверяется криптослоем).
package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/nopass0/spider/server/internal/commands"
	"github.com/nopass0/spider/server/internal/config"
	"github.com/nopass0/spider/server/internal/hub"
	"github.com/nopass0/spider/server/internal/store"
)

// API держит зависимости, общие для всех обработчиков.
type API struct {
	cfg        config.Config
	store      *store.Store
	hub        *hub.Hub
	dispatcher *commands.Dispatcher
	log        *slog.Logger
}

// New создаёт API с внедрёнными зависимостями.
func New(cfg config.Config, s *store.Store, h *hub.Hub, d *commands.Dispatcher, log *slog.Logger) *API {
	if log == nil {
		log = slog.Default()
	}
	return &API{cfg: cfg, store: s, hub: h, dispatcher: d, log: log}
}

// Router собирает все маршруты в один http.ServeMux.
func (a *API) Router() http.Handler {
	mux := http.NewServeMux()

	// --- Health (без авторизации) ---
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	// --- Agent endpoints (без admin-токена; auth через крипто) ---
	mux.HandleFunc("POST /agent/enroll", a.handleEnroll)
	mux.HandleFunc("GET /agent/connect", a.handleLongPoll)
	mux.HandleFunc("GET /agent/ws", a.handleAgentWS)

	// --- Admin API (Bearer ADMIN_KEY) ---
	admin := http.NewServeMux()
	admin.HandleFunc("GET /admin/devices", a.adminListDevices)
	admin.HandleFunc("GET /admin/devices/{id}", a.adminGetDevice)
	admin.HandleFunc("DELETE /admin/devices/{id}", a.adminDeleteDevice)
	admin.HandleFunc("PATCH /admin/devices/{id}", a.adminPatchDevice)
	admin.HandleFunc("POST /admin/devices/{id}/commands", a.adminEnqueueCommand)
	admin.HandleFunc("GET /admin/devices/{id}/commands", a.adminListCommands)
	admin.HandleFunc("GET /admin/commands/{id}", a.adminGetCommand)
	admin.HandleFunc("POST /admin/enrollments", a.adminCreateEnrollment)
	admin.HandleFunc("GET /admin/enrollments", a.adminListEnrollments)
	admin.HandleFunc("DELETE /admin/enrollments/{token}", a.adminDeleteEnrollment)
	admin.HandleFunc("GET /admin/settings/commands", a.adminGetCommandsEnabled)
	admin.HandleFunc("PUT /admin/settings/commands", a.adminSetCommandsEnabled)
	admin.HandleFunc("GET /admin/audit", a.adminListAudit)
	admin.HandleFunc("GET /admin/events", a.adminEvents) // WS
	admin.HandleFunc("GET /admin/info", a.adminInfo)

	mux.Handle("/admin/", a.requireAdmin(admin))

	return mux
}

// ===========================================================================
// Auth middleware
// ===========================================================================

// requireAdmin проверяет Bearer ADMIN_KEY (constant-time) перед передачей
// запроса в admin subrouter.
func (a *API) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !checkAdminToken(r, a.cfg.AdminKey) {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// checkAdminToken извлекает Bearer-токен и сравнивает constant-time.
func checkAdminToken(r *http.Request, expected string) bool {
	auth := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(auth, prefix) {
		return false
	}
	got := strings.TrimSpace(auth[len(prefix):])
	return constantTimeEqual(got, expected)
}

// constantTimeEqual — сравнение строк за время, не зависящее от позиции первого
// отличия. Используем crypto/subtle через ручную реализацию, чтобы не плодить импорт.
func constantTimeEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var v byte
	for i := 0; i < len(a); i++ {
		v |= a[i] ^ b[i]
	}
	return v == 0
}

// ===========================================================================
// JSON helpers (DRY)
// ===========================================================================

// writeJSON пишет статус и JSON body.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError пишет стандартный JSON-ошибки.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// decodeJSON читает тело запроса в target с ограничением размера.
func decodeJSON(r *http.Request, target any, maxBytes int64) error {
	if maxBytes <= 0 {
		maxBytes = 1 << 20 // 1 MiB
	}
	r.Body = http.MaxBytesReader(nil, r.Body, maxBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		return err
	}
	return nil
}

// statusForError маппит store-ошибки в HTTP-статусы (DRY).
func statusForError(err error) (int, string) {
	switch {
	case errors.Is(err, store.ErrDeviceNotFound):
		return http.StatusNotFound, "device not found"
	case errors.Is(err, store.ErrCommandNotFound):
		return http.StatusNotFound, "command not found"
	case errors.Is(err, store.ErrEnrollmentNotFound):
		return http.StatusNotFound, "enrollment not found"
	case errors.Is(err, store.ErrEnrollmentAlreadyUsed):
		return http.StatusConflict, "enrollment already used"
	case errors.Is(err, store.ErrEnrollmentExpired):
		return http.StatusGone, "enrollment expired"
	}
	return http.StatusInternalServerError, err.Error()
}
