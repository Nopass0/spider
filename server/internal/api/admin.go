package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/nopass0/spider/server/internal/commands"
	"github.com/nopass0/spider/server/internal/crypto"
	"github.com/nopass0/spider/server/internal/hub"
	"github.com/nopass0/spider/server/internal/store"
)

// ===========================================================================
// Devices
// ===========================================================================

// adminListDevices — GET /admin/devices → список всех устройств.
func (a *API) adminListDevices(w http.ResponseWriter, r *http.Request) {
	devs, err := a.store.ListDevices(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Обогащаем online-статусом из hub (WS-соединение = online).
	online := make(map[string]bool, len(devs))
	for _, id := range a.hub.OnlineDevices() {
		online[id] = true
	}
	for i := range devs {
		devs[i].Online = online[devs[i].DeviceID]
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": devs})
}

// adminGetDevice — GET /admin/devices/{id}.
func (a *API) adminGetDevice(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	d, err := a.store.GetDevice(r.Context(), id)
	if err != nil {
		code, msg := statusForError(err)
		writeError(w, code, msg)
		return
	}
	d.Online = a.hub.IsOnline(id)
	writeJSON(w, http.StatusOK, d)
}

// adminDeleteDevice — DELETE /admin/devices/{id}.
func (a *API) adminDeleteDevice(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := a.store.DeleteDevice(r.Context(), id); err != nil {
		code, msg := statusForError(err)
		writeError(w, code, msg)
		return
	}
	_ = a.store.AppendAudit(r.Context(), "admin", "device.delete", id, "")
	a.hub.Broadcast(hub.AdminEvent{Type: "device.deleted", DeviceID: id})
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// adminPatchDevice — PATCH /admin/devices/{id} (сейчас — только переименование).
func (a *API) adminPatchDevice(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Name *string `json:"name,omitempty"`
	}
	if err := decodeJSON(r, &body, 4<<10); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if body.Name != nil {
		if err := a.store.SetDeviceName(r.Context(), id, *body.Name); err != nil {
			code, msg := statusForError(err)
			writeError(w, code, msg)
			return
		}
		_ = a.store.AppendAudit(r.Context(), "admin", "device.rename", id, *body.Name)
	}
	d, _ := a.store.GetDevice(r.Context(), id)
	writeJSON(w, http.StatusOK, d)
}

// ===========================================================================
// Commands
// ===========================================================================

// enqueueCommandRequest — тело POST /admin/devices/{id}/commands.
type enqueueCommandRequest struct {
	Command    string `json:"command"`
	TimeoutSec int    `json:"timeout_sec,omitempty"`
}

// adminEnqueueCommand — POST /admin/devices/{id}/commands → постановка команды.
func (a *API) adminEnqueueCommand(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	// проверяем существование устройства
	if _, err := a.store.GetDevice(r.Context(), id); err != nil {
		code, msg := statusForError(err)
		writeError(w, code, msg)
		return
	}
	var body enqueueCommandRequest
	if err := decodeJSON(r, &body, 64<<10); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	body.Command = strings.TrimSpace(body.Command)
	if body.Command == "" {
		writeError(w, http.StatusBadRequest, "command is required")
		return
	}
	cmd, delivered, err := a.dispatcher.Dispatch(r.Context(), id, body.Command, body.TimeoutSec, "admin")
	if err != nil {
		if errors.Is(err, commands.ErrCommandsDisabled) {
			writeError(w, http.StatusForbidden, "commands dispatch is disabled")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"command":   cmd,
		"delivered": delivered,
	})
}

// adminListCommands — GET /admin/devices/{id}/commands → история команд.
func (a *API) adminListCommands(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	list, err := a.store.ListDeviceCommands(r.Context(), id, 50)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// обогащаем результатами
	type cmdWithResult struct {
		store.Command
		Result   *store.Result `json:"result,omitempty"`
		HasResult bool         `json:"has_result"`
	}
	out := make([]cmdWithResult, 0, len(list))
	for _, c := range list {
		row := cmdWithResult{Command: c}
		if r, ok, _ := a.store.GetResult(r.Context(), c.ID); ok {
			row.Result = &r
			row.HasResult = true
		}
		out = append(out, row)
	}
	writeJSON(w, http.StatusOK, map[string]any{"commands": out})
}

// adminGetCommand — GET /admin/commands/{id} → команда + результат.
func (a *API) adminGetCommand(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	cmd, err := a.store.GetCommand(r.Context(), id)
	if err != nil {
		code, msg := statusForError(err)
		writeError(w, code, msg)
		return
	}
	resp := map[string]any{"command": cmd}
	if res, ok, _ := a.store.GetResult(r.Context(), id); ok {
		resp["result"] = res
	}
	writeJSON(w, http.StatusOK, resp)
}

// ===========================================================================
// Enrollments
// ===========================================================================

// createEnrollmentRequest — тело POST /admin/enrollments.
type createEnrollmentRequest struct {
	Note string `json:"note"`
}

// createEnrollmentResponse — ответ со значением токена и ключом (показать один раз).
type createEnrollmentResponse struct {
	Token   string    `json:"token"`
	KeyB64  string    `json:"key"`
	PubB64  string    `json:"server_pub"`
	ExpiresAt time.Time `json:"expires_at"`
	Note    string    `json:"note"`
}

// adminCreateEnrollment — POST /admin/enrollments.
// Генерирует токен, эфемерную пару X25519 сервера и симметричный ключ сессии.
func (a *API) adminCreateEnrollment(w http.ResponseWriter, r *http.Request) {
	var body createEnrollmentRequest
	_ = decodeJSON(r, &body, 4<<10)

	token, err := crypto.RandomHex(20) // 40 hex символов
	if err != nil {
		writeError(w, http.StatusInternalServerError, "token gen")
		return
	}
	// Симметричный ключ сессии для будущего устройства.
	key, err := crypto.RandomBytes(crypto.KeySize)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "key gen")
		return
	}
	// Эфемерная пара X25519 сервера — её публичную часть получит агент при enroll.
	priv, err := crypto.GenerateECDHPrivateKey()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "ecdh gen")
		return
	}
	pub := crypto.PublicKeyBytes(priv)

	keyB64 := b64(key)
	pubB64 := b64(pub)
	if err := a.store.CreateEnrollment(r.Context(), token, body.Note, keyB64, pubB64, a.cfg.EnrollTTL); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = a.store.AppendAudit(r.Context(), "admin", "enrollment.create", token, body.Note)
	resp := createEnrollmentResponse{
		Token: token, KeyB64: keyB64, PubB64: pubB64,
		ExpiresAt: time.Now().UTC().Add(a.cfg.EnrollTTL), Note: body.Note,
	}
	writeJSON(w, http.StatusCreated, resp)
}

// adminListEnrollments — GET /admin/enrollments (без секретов).
func (a *API) adminListEnrollments(w http.ResponseWriter, r *http.Request) {
	list, err := a.store.ListEnrollments(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enrollments": list})
}

// adminDeleteEnrollment — DELETE /admin/enrollments/{token}.
func (a *API) adminDeleteEnrollment(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if err := a.store.DeleteEnrollment(r.Context(), token); err != nil {
		code, msg := statusForError(err)
		writeError(w, code, msg)
		return
	}
	_ = a.store.AppendAudit(r.Context(), "admin", "enrollment.delete", token, "")
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ===========================================================================
// Settings / Audit / Info / Events
// ===========================================================================

// adminGetCommandsEnabled — GET /admin/settings/commands.
func (a *API) adminGetCommandsEnabled(w http.ResponseWriter, r *http.Request) {
	enabled, err := a.dispatcher.CommandsEnabled(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"commands_enabled": enabled})
}

// adminSetCommandsEnabled — PUT /admin/settings/commands.
func (a *API) adminSetCommandsEnabled(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := decodeJSON(r, &body, 1<<10); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if err := a.dispatcher.SetCommandsEnabled(r.Context(), body.Enabled); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"commands_enabled": body.Enabled})
}

// adminListAudit — GET /admin/audit.
func (a *API) adminListAudit(w http.ResponseWriter, r *http.Request) {
	list, err := a.store.ListAudit(r.Context(), 100)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"audit": list})
}

// adminInfo — GET /admin/info (базовая инфа о сервере для панели).
func (a *API) adminInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"public_url":    a.cfg.PublicURL,
		"online_count":  a.hub.CountAgents(),
		"server_time":   time.Now().UTC(),
	})
}
