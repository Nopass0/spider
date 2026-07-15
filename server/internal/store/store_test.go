package store

import (
	"context"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// заморозка времени для детерминированных TTL-тестов
func freezeTime(t *testing.T, ts string) {
	t.Helper()
	tt := time.Unix(1_700_000_000, 0)
	if ts != "" {
		var err error
		tt, err = time.Parse(time.RFC3339, ts)
		if err != nil {
			t.Fatal(err)
		}
	}
	prev := nowUTC
	nowUTC = func() time.Time { return tt }
	t.Cleanup(func() { nowUTC = prev })
}

func TestSettingsRoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	got, err := s.GetSetting(ctx, "missing", "default")
	if err != nil || got != "default" {
		t.Fatalf("default: got %q err %v", got, err)
	}
	if err := s.SetSetting(ctx, "k", "v"); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.GetSetting(ctx, "k", ""); v != "v" {
		t.Fatalf("after set: %q", v)
	}
	if err := s.SetSetting(ctx, "k", "v2"); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.GetSetting(ctx, "k", ""); v != "v2" {
		t.Fatalf("upsert: %q", v)
	}
}

func TestCommandsEnabledDefault(t *testing.T) {
	s := newTestStore(t)
	v, err := s.GetSetting(context.Background(), "commands_enabled", "")
	if err != nil {
		t.Fatal(err)
	}
	if v != "1" {
		t.Fatalf("commands_enabled default = %q, want 1", v)
	}
}

func TestEnrollmentLifecycle(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	freezeTime(t, "")

	if err := s.CreateEnrollment(ctx, "tok", "office", "KEY", "PUB", 24*time.Hour); err != nil {
		t.Fatal(err)
	}
	// consume
	key, pub, err := s.ConsumeEnrollment(ctx, "tok", "dev-1")
	if err != nil || key != "KEY" || pub != "PUB" {
		t.Fatalf("consume: err=%v key=%q pub=%q", err, key, pub)
	}
	// повторно — ошибка
	if _, _, err := s.ConsumeEnrollment(ctx, "tok", "dev-1"); err != ErrEnrollmentAlreadyUsed {
		t.Fatalf("ожидалась AlreadyUsed, got %v", err)
	}
	// листинг показывает used_at
	list, err := s.ListEnrollments(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Token != "tok" || list[0].UsedBy != "dev-1" {
		t.Fatalf("list: %+v", list)
	}
}

func TestEnrollmentExpired(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	freezeTime(t, "") // базовое время = 1_700_000_000
	base := time.Unix(1_700_000_000, 0)
	s.CreateEnrollment(ctx, "old", "", "K", "P", 1*time.Hour)
	// expires_at = base + 1h; сдвигаем nowUTC за пределы TTL.
	nowUTC = func() time.Time { return base.Add(2 * time.Hour) }
	if _, _, err := s.ConsumeEnrollment(ctx, "old", "d"); err != ErrEnrollmentExpired {
		t.Fatalf("ожидалась Expired, got %v", err)
	}
}

func TestEnrollmentNotFound(t *testing.T) {
	s := newTestStore(t)
	if _, _, err := s.ConsumeEnrollment(context.Background(), "nope", "d"); err != ErrEnrollmentNotFound {
		t.Fatalf("ожидалась NotFound, got %v", err)
	}
}

func TestDeviceUpsertAndTouch(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	d := Device{
		DeviceID: "dev-1", Name: "office", KeyB64: "K",
		Hostname: "PC1", OS: "linux", Arch: "amd64", Online: true,
	}
	if err := s.UpsertDevice(ctx, d); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetDevice(ctx, "dev-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Hostname != "PC1" || !got.Online {
		t.Fatalf("get device: %+v", got)
	}
	// upsert обновляет
	d.Hostname = "PC1-renamed"
	s.UpsertDevice(ctx, d)
	got, _ = s.GetDevice(ctx, "dev-1")
	if got.Hostname != "PC1-renamed" {
		t.Fatalf("upsert не обновил: %s", got.Hostname)
	}
	// touch offline
	if err := s.TouchDevice(ctx, "dev-1", false); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetDevice(ctx, "dev-1")
	if got.Online {
		t.Fatal("touch offline не сработал")
	}
	// session key
	k, _ := s.GetSessionKey(ctx, "dev-1")
	if k != "K" {
		t.Fatalf("session key: %q", k)
	}
}

func TestDeviceDelete(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	s.UpsertDevice(ctx, Device{DeviceID: "d", KeyB64: "K"})
	if err := s.DeleteDevice(ctx, "d"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteDevice(ctx, "d"); err != ErrDeviceNotFound {
		t.Fatalf("ожидалась NotFound, got %v", err)
	}
}

func TestCommandEnqueueDequeueResult(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	s.UpsertDevice(ctx, Device{DeviceID: "dev", KeyB64: "K"})

	c, err := s.EnqueueCommand(ctx, Command{
		ID: "cmd-1", DeviceID: "dev", Command: "echo hi", TimeoutSec: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if c.Status != StatusQueued {
		t.Fatalf("status = %s", c.Status)
	}
	// dequeue переводит в running
	got, err := s.DequeueCommands(ctx, "dev", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "cmd-1" || got[0].Status != StatusRunning {
		t.Fatalf("dequeue: %+v", got)
	}
	// повторный dequeue пуст
	got2, _ := s.DequeueCommands(ctx, "dev", 10)
	if len(got2) != 0 {
		t.Fatalf("очередь не пуста: %+v", got2)
	}
	// результат
	err = s.SaveResult(ctx, "cmd-1", StatusDone, Result{
		ExitCode: 0, StdoutB64: "aGk=", FinishedAt: nowUTC(), DurationMs: 12,
	})
	if err != nil {
		t.Fatal(err)
	}
	cc, _ := s.GetCommand(ctx, "cmd-1")
	if cc.Status != StatusDone || cc.FinishedAt == nil {
		t.Fatalf("command after result: %+v", cc)
	}
	r, ok, err := s.GetResult(ctx, "cmd-1")
	if err != nil || !ok || r.ExitCode != 0 || r.StdoutB64 != "aGk=" {
		t.Fatalf("result: ok=%v %+v err=%v", ok, r, err)
	}
}

func TestListDeviceCommands(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	s.UpsertDevice(ctx, Device{DeviceID: "dev", KeyB64: "K"})
	for i := 0; i < 3; i++ {
		s.EnqueueCommand(ctx, Command{ID: "c" + string(rune('1'+i)), DeviceID: "dev", Command: "x"})
	}
	list, err := s.ListDeviceCommands(ctx, "dev", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 {
		t.Fatalf("list len = %d", len(list))
	}
}

func TestCancelQueued(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	s.UpsertDevice(ctx, Device{DeviceID: "dev", KeyB64: "K"})
	s.EnqueueCommand(ctx, Command{ID: "c1", DeviceID: "dev", Command: "x"})
	s.EnqueueCommand(ctx, Command{ID: "c2", DeviceID: "dev", Command: "y"})
	if err := s.CancelQueued(ctx, "dev"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.DequeueCommands(ctx, "dev", 10)
	if len(got) != 0 {
		t.Fatalf("queued не отменены: %+v", got)
	}
}

func TestAudit(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	s.AppendAudit(ctx, "admin", "command.enqueue", "dev", "echo hi")
	s.AppendAudit(ctx, "admin", "device.delete", "dev2", "")
	list, err := s.ListAudit(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("audit len = %d", len(list))
	}
}
