package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nopass0/spider/server/internal/commands"
	"github.com/nopass0/spider/server/internal/config"
	"github.com/nopass0/spider/server/internal/hub"
	"github.com/nopass0/spider/server/internal/store"
)

// newTestAPI поднимает API с in-memory store и готовым устройством.
// Возвращает сервер, admin-заголовок и id устройства.
func newTestAPI(t *testing.T) (*API, *httptest.Server, string, string) {
	t.Helper()
	ctx := context.Background()
	st, err := store.New(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte('a' + i%26)
	}
	keyB64 := base64.StdEncoding.EncodeToString(key)
	st.UpsertDevice(ctx, store.Device{DeviceID: "dev-1", KeyB64: keyB64, Hostname: "PC", Online: true})

	cfg := config.Config{AdminKey: "test-admin-key-12345", PublicURL: "http://localhost", LongPollTimeout: 1}
	h := hub.New()
	d := commands.New(st, h, slog.Default())
	a := New(cfg, st, h, d, slog.Default())
	srv := httptest.NewServer(a.Router())
	t.Cleanup(srv.Close)
	return a, srv, "Bearer " + cfg.AdminKey, "dev-1"
}

func TestHealth(t *testing.T) {
	_, srv, _, _ := newTestAPI(t)
	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestAdminRequiresToken(t *testing.T) {
	_, srv, _, _ := newTestAPI(t)
	resp, _ := http.Get(srv.URL + "/admin/devices")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("ожидался 401, got %d", resp.StatusCode)
	}
	// с неверным токеном
	req, _ := http.NewRequest("GET", srv.URL+"/admin/devices", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	resp2, _ := http.DefaultClient.Do(req)
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("ожидался 401 для неверного токена, got %d", resp2.StatusCode)
	}
}

func TestAdminListDevices(t *testing.T) {
	_, srv, auth, _ := newTestAPI(t)
	req, _ := http.NewRequest("GET", srv.URL+"/admin/devices", nil)
	req.Header.Set("Authorization", auth)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var body struct {
		Devices []store.Device `json:"devices"`
	}
	json.NewDecoder(resp.Body).Decode(&body)
	if len(body.Devices) != 1 || body.Devices[0].DeviceID != "dev-1" {
		t.Fatalf("devices: %+v", body.Devices)
	}
}

func TestEnrollFlowAndCommand(t *testing.T) {
	ctx := context.Background()
	_, srv, auth, _ := newTestAPI(t)

	// 1. Создать enrollment.
	req, _ := http.NewRequest("POST", srv.URL+"/admin/enrollments", strings.NewReader(`{"note":"office"}`))
	req.Header.Set("Authorization", auth)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 201 {
		t.Fatalf("create enrollment status = %d", resp.StatusCode)
	}
	var enr createEnrollmentResponse
	json.NewDecoder(resp.Body).Decode(&enr)
	if enr.Token == "" || enr.KeyB64 == "" {
		t.Fatalf("enrollment пустой: %+v", enr)
	}

	// 2. Листинг показывает токен (без секретов).
	req, _ = http.NewRequest("GET", srv.URL+"/admin/enrollments", nil)
	req.Header.Set("Authorization", auth)
	resp, _ = http.DefaultClient.Do(req)
	var list struct {
		Enrollments []store.Enrollment `json:"enrollments"`
	}
	json.NewDecoder(resp.Body).Decode(&list)
	if len(list.Enrollments) != 1 || list.Enrollments[0].Token != enr.Token {
		t.Fatalf("enrollments list: %+v", list.Enrollments)
	}

	// 3. Enroll устройства (имитируем агента).
	// Генерируем эфемерный ключ агента для полноты, хотя сервер в тестовом flow
	// уже имеет готовый симметричный ключ.
	enrollBody := map[string]any{
		"token":      enr.Token,
		"public_key": enr.PubB64,
		"system":     map[string]any{"hostname": "NEW-PC", "os": "linux", "arch": "amd64"},
		"agent_version": "0.1.0",
	}
	b, _ := json.Marshal(enrollBody)
	req, _ = http.NewRequest("POST", srv.URL+"/agent/enroll", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("enroll status = %d", resp.StatusCode)
	}
	var er enrollResponse
	json.NewDecoder(resp.Body).Decode(&er)
	if er.DeviceID == "" || er.KeyB64 != enr.KeyB64 {
		t.Fatalf("enroll response: %+v", er)
	}

	// 4. Повторный enroll тем же токеном — 409 conflict.
	req, _ = http.NewRequest("POST", srv.URL+"/agent/enroll", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("повторный enroll: status = %d", resp.StatusCode)
	}

	// 5. Поставить команду новому устройству.
	cmdBody := `{"command":"echo hello","timeout_sec":5}`
	req, _ = http.NewRequest("POST", srv.URL+"/admin/devices/"+er.DeviceID+"/commands", strings.NewReader(cmdBody))
	req.Header.Set("Authorization", auth)
	req.Header.Set("Content-Type", "application/json")
	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("enqueue status = %d", resp.StatusCode)
	}

	// 6. История команд показывает созданную.
	req, _ = http.NewRequest("GET", srv.URL+"/admin/devices/"+er.DeviceID+"/commands", nil)
	req.Header.Set("Authorization", auth)
	resp, _ = http.DefaultClient.Do(req)
	var hist struct {
		Commands []store.Command `json:"commands"`
	}
	json.NewDecoder(resp.Body).Decode(&hist)
	if len(hist.Commands) != 1 || hist.Commands[0].Command != "echo hello" {
		t.Fatalf("history: %+v", hist.Commands)
	}

	// срез unused ctx если понадобится в расширениях
	_ = ctx
}

func TestCommandsToggle(t *testing.T) {
	_, srv, auth, _ := newTestAPI(t)
	// выключить
	req, _ := http.NewRequest("PUT", srv.URL+"/admin/settings/commands", strings.NewReader(`{"enabled":false}`))
	req.Header.Set("Authorization", auth)
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != 200 {
		t.Fatalf("toggle status = %d", resp.StatusCode)
	}
	// команда теперь запрещена
	cmdBody := `{"command":"x"}`
	req, _ = http.NewRequest("POST", srv.URL+"/admin/devices/dev-1/commands", strings.NewReader(cmdBody))
	req.Header.Set("Authorization", auth)
	req.Header.Set("Content-Type", "application/json")
	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("ожидался 403 при выключенных командах, got %d", resp.StatusCode)
	}
}

func TestDeleteUnknownDevice(t *testing.T) {
	_, srv, auth, _ := newTestAPI(t)
	req, _ := http.NewRequest("DELETE", srv.URL+"/admin/devices/ghost", nil)
	req.Header.Set("Authorization", auth)
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("ожидался 404, got %d", resp.StatusCode)
	}
}

func TestConstantTimeEqual(t *testing.T) {
	if !constantTimeEqual("abc", "abc") {
		t.Fatal("equal strings не совпали")
	}
	if constantTimeEqual("abc", "abd") {
		t.Fatal("разные строки признались равными")
	}
	if constantTimeEqual("abc", "abcd") {
		t.Fatal("разная длина признана равной")
	}
}

func TestOriginHost(t *testing.T) {
	cases := map[string]string{
		"https://spider.lowkey.su":  "spider.lowkey.su",
		"http://localhost:8080":     "localhost",
		"https://a.b.c.d/path":      "a.b.c.d",
	}
	for in, want := range cases {
		if got := originHost(in); got != want {
			t.Errorf("originHost(%q) = %q, want %q", in, got, want)
		}
	}
}
