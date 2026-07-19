package api

import (
	"encoding/base64"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestScreenshotSaveAndGet — сохранение скриншота и отдача файла.
func TestScreenshotSaveAndGet(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("SPIDER_SCREENSHOTS_DIR", tmp)
	_, srv, auth, devID := newTestAPI(t)

	// фейковый JPEG (минимальные байты)
	jpeg := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0xFF, 0xD9}
	b64 := base64.StdEncoding.EncodeToString(jpeg)

	// сохранение
	req, _ := http.NewRequest("POST", srv.URL+"/admin/devices/"+devID+"/screenshots",
		strings.NewReader(`{"frame_b64":"`+b64+`"}`))
	req.Header.Set("Authorization", auth)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("save status = %d", resp.StatusCode)
	}

	// листинг
	req, _ = http.NewRequest("GET", srv.URL+"/admin/devices/"+devID+"/screenshots", nil)
	req.Header.Set("Authorization", auth)
	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != 200 {
		t.Fatalf("list status = %d", resp.StatusCode)
	}

	// файл должен существовать на диске
	matches, err := filepath.Glob(filepath.Join(tmp, devID, "*.jpg"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("ожидался 1 файл скриншота, matches=%v err=%v", matches, err)
	}
	got, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(jpeg) {
		t.Fatalf("размер файла = %d, want %d", len(got), len(jpeg))
	}
}

// TestScreenshotSaveBadBase64 — некорректный base64 → 400.
func TestScreenshotSaveBadBase64(t *testing.T) {
	t.Setenv("SPIDER_SCREENSHOTS_DIR", t.TempDir())
	_, srv, auth, devID := newTestAPI(t)
	req, _ := http.NewRequest("POST", srv.URL+"/admin/devices/"+devID+"/screenshots",
		strings.NewReader(`{"frame_b64":"!!not-base64!!"}`))
	req.Header.Set("Authorization", auth)
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("ожидался 400, got %d", resp.StatusCode)
	}
}

func TestIsNum(t *testing.T) {
	if !isNum("123") {
		t.Fatal("123 должно быть числом")
	}
	if isNum("12a") {
		t.Fatal("12a не число")
	}
	if isNum("") {
		t.Fatal("пустая строка не число")
	}
	if parseNum("42") != 42 {
		t.Fatalf("parseNum(42) = %d", parseNum("42"))
	}
}
