package commands

import (
	"context"
	"encoding/base64"
	"log/slog"
	"testing"

	"github.com/nopass0/spider/server/internal/crypto"
	"github.com/nopass0/spider/server/internal/hub"
	"github.com/nopass0/spider/server/internal/store"
)

// newDispatcher поднимает store+hub+dispatcher и регистрирует устройство с
// известным ключом сессии (производным от фиксированного seed) — чтобы тесты
// могли (де)шифровать сообщения.
func newDispatcher(t *testing.T, deviceID string) (*Dispatcher, *store.Store, *hub.Hub, *crypto.Session) {
	t.Helper()
	ctx := context.Background()
	s, err := store.New(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	key := make([]byte, crypto.KeySize)
	for i := range key {
		key[i] = byte(i + 1)
	}
	keyB64 := base64.StdEncoding.EncodeToString(key)
	if err := s.UpsertDevice(ctx, store.Device{DeviceID: deviceID, KeyB64: keyB64, Online: true}); err != nil {
		t.Fatal(err)
	}
	h := hub.New()
	sess, err := crypto.NewSession(key)
	if err != nil {
		t.Fatal(err)
	}
	return New(s, h, slog.Default()), s, h, sess
}

func TestDispatchOnlineDelivers(t *testing.T) {
	ctx := context.Background()
	d, _, h, _ := newDispatcher(t, "dev")
	sink := hub.NewChanAgentSink(4)
	defer sink.Close()
	h.RegisterAgent("dev", sink)

	cmd, delivered, err := d.Dispatch(ctx, "dev", "echo hi", 10, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if !delivered {
		t.Fatal("ожидалась мгновенная доставка")
	}
	select {
	case env := <-sink.Recv():
		if env.Type != crypto.MsgCommand {
			t.Fatalf("тип = %s", env.Type)
		}
		// расшифровать и проверить
		ct, _ := base64.StdEncoding.DecodeString(env.Data)
		sess, err := d.SessionFor(ctx, "dev")
		if err != nil {
			t.Fatal(err)
		}
		raw, err := sess.Decrypt(ct)
		if err != nil {
			t.Fatal(err)
		}
		if !contains(string(raw), cmd.ID) {
			t.Fatalf("payload не содержит id команды: %s", raw)
		}
	default:
		t.Fatal("envelope не доставлен в sink")
	}
}

func TestDispatchDisabledReturnsError(t *testing.T) {
	ctx := context.Background()
	d, s, h, _ := newDispatcher(t, "dev")
	s.SetSetting(ctx, "commands_enabled", "0")
	h.RegisterAgent("dev", hub.NewChanAgentSink(2))
	if _, _, err := d.Dispatch(ctx, "dev", "x", 1, "admin"); err != ErrCommandsDisabled {
		t.Fatalf("ожидалась ErrCommandsDisabled, got %v", err)
	}
}

func TestDispatchOfflineQueues(t *testing.T) {
	ctx := context.Background()
	d, s, _, _ := newDispatcher(t, "dev")
	// устройство НЕ зарегистрировано в hub → offline
	cmd, delivered, err := d.Dispatch(ctx, "dev", "ls", 5, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if delivered {
		t.Fatal("не должно доставить (offline)")
	}
	// команда осталась queued
	list, _ := s.ListDeviceCommands(ctx, "dev", 10)
	if len(list) != 1 || list[0].ID != cmd.ID || list[0].Status != store.StatusQueued {
		t.Fatalf("очередь: %+v", list)
	}
}

func TestFlushQueuedFor(t *testing.T) {
	ctx := context.Background()
	d, s, _, sess := newDispatcher(t, "dev")
	d.store = s
	// положим 2 queued-команды напрямую
	s.EnqueueCommand(ctx, store.Command{ID: "q1", DeviceID: "dev", Command: "a"})
	s.EnqueueCommand(ctx, store.Command{ID: "q2", DeviceID: "dev", Command: "b"})

	batch, err := d.FlushQueuedFor(ctx, "dev")
	if err != nil {
		t.Fatal(err)
	}
	if len(batch) != 2 {
		t.Fatalf("batch len = %d", len(batch))
	}
	// расшифровать первую и проверить id
	ct, _ := base64.StdEncoding.DecodeString(batch[0].Data)
	raw, err := sess.Decrypt(ct)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(string(raw), "q1") {
		t.Fatalf("первый envelope не q1: %s", raw)
	}
	// повторный flush пуст (переведены в running)
	batch2, _ := d.FlushQueuedFor(ctx, "dev")
	if len(batch2) != 0 {
		t.Fatalf("ожидался пустой flush, got %d", len(batch2))
	}
}

func TestHandleResult(t *testing.T) {
	ctx := context.Background()
	d, s, h, _ := newDispatcher(t, "dev")
	admin := hub.NewChanAdminSink(8)
	defer admin.Close()
	h.RegisterAdmin(admin)

	s.EnqueueCommand(ctx, store.Command{ID: "r1", DeviceID: "dev", Command: "x", Status: store.StatusRunning})
	err := d.HandleResult(ctx, "dev", WireResult{
		CommandID: "r1", ExitCode: 0, StdoutB64: "aGk=", DurationMs: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	r, ok, err := s.GetResult(ctx, "r1")
	if err != nil || !ok || r.StdoutB64 != "aGk=" {
		t.Fatalf("result: ok=%v %+v err=%v", ok, r, err)
	}
}

func TestToggleCommands(t *testing.T) {
	ctx := context.Background()
	d, _, _, _ := newDispatcher(t, "dev")
	if err := d.SetCommandsEnabled(ctx, false); err != nil {
		t.Fatal(err)
	}
	on, _ := d.CommandsEnabled(ctx)
	if on {
		t.Fatal("ожидалось выключено")
	}
	d.SetCommandsEnabled(ctx, true)
	on, _ = d.CommandsEnabled(ctx)
	if !on {
		t.Fatal("ожидалось включено")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
