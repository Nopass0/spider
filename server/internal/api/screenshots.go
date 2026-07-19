package api

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nopass0/spider/server/internal/hub"
	"github.com/nopass0/spider/server/internal/store"
)

// Каталог хранения скриншотов (создаётся install.sh как /var/lib/spider).
// В тестах можно переопределить через env SPIDER_SCREENSHOTS_DIR.
func screenshotsDir() string {
	if d := os.Getenv("SPIDER_SCREENSHOTS_DIR"); d != "" {
		return d
	}
	return "/var/lib/spider/screenshots"
}

// saveScreenshotFile пишет JPEG на диск в каталог устройства и возвращает
// (name, fullPath). Имя файла: {unix-ms}.jpg.
func saveScreenshotFile(deviceID string, jpegBytes []byte) (string, string, error) {
	dir := filepath.Join(screenshotsDir(), deviceID)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", "", fmt.Errorf("mkdir screenshots: %w", err)
	}
	name := fmt.Sprintf("%d.jpg", time.Now().UnixMilli())
	full := filepath.Join(dir, name)
	if err := os.WriteFile(full, jpegBytes, 0o640); err != nil {
		return "", "", fmt.Errorf("write screenshot: %w", err)
	}
	return name, full, nil
}

// adminSaveScreenshot — POST /admin/devices/{id}/screenshots.
// Тело: {"frame_b64": "<base64 jpeg>"}. Декодирует, пишет в файл, сохраняет метаданные.
func (a *API) adminSaveScreenshot(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := a.store.GetDevice(r.Context(), id); err != nil {
		code, msg := statusForError(err)
		writeError(w, code, msg)
		return
	}
	var body struct {
		FrameB64 string `json:"frame_b64"`
	}
	if err := decodeJSON(r, &body, 10<<20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if body.FrameB64 == "" {
		writeError(w, http.StatusBadRequest, "frame_b64 required")
		return
	}
	jpeg, err := base64.StdEncoding.DecodeString(body.FrameB64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid base64")
		return
	}
	name, _, err := saveScreenshotFile(id, jpeg)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	sid, err := a.store.SaveScreenshot(r.Context(), id, name, 0, 0, int64(len(jpeg)))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = a.store.AppendAudit(r.Context(), "admin", "screenshot.save", id, name)
	a.hub.Broadcast(hub.AdminEvent{Type: "screenshot.saved", DeviceID: id})
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": sid, "name": name, "device_id": id, "size_bytes": len(jpeg),
	})
}

// adminListScreenshots — GET /admin/devices/{id}/screenshots.
func (a *API) adminListScreenshots(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	list, err := a.store.ListScreenshots(r.Context(), id, 50)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"screenshots": list})
}

// adminGetScreenshotFile — GET /admin/screenshots/{id} → отдаёт JPEG-файл.
func (a *API) adminGetScreenshotFile(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var sc store.Screenshot
	var found bool
	// id может быть числовым (БД-id) или "device/name" — поддерживаем оба.
	if isNum(id) {
		sc, found, _ = a.store.GetScreenshot(r.Context(), parseNum(id))
	} else {
		// формат: {device_id}/{name}
		parts := strings.SplitN(id, "/", 2)
		if len(parts) != 2 {
			writeError(w, http.StatusBadRequest, "expected {id} or {device_id}/{name}")
			return
		}
		list, err := a.store.ListScreenshots(r.Context(), parts[0], 200)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		for _, s := range list {
			if s.Name == parts[1] {
				sc = s
				found = true
				break
			}
		}
	}
	if !found {
		writeError(w, http.StatusNotFound, "screenshot not found")
		return
	}
	full := filepath.Join(screenshotsDir(), sc.DeviceID, sc.Name)
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	http.ServeFile(w, r, full)
}

func isNum(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func parseNum(s string) int64 {
	var n int64
	for _, c := range s {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int64(c-'0')
	}
	return n
}

var _ = errors.New // гарантирует импорт, если расширится
