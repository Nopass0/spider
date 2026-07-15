package hub

import (
	"context"
	"testing"
	"time"

	"github.com/nopass0/spider/server/internal/crypto"
)

func TestRegisterAndSend(t *testing.T) {
	h := New()
	sink := NewChanAgentSink(4)
	defer sink.Close()

	unsub := h.RegisterAgent("dev-1", sink)
	defer unsub()

	if !h.IsOnline("dev-1") {
		t.Fatal("ожидалось online")
	}
	if h.IsOnline("dev-2") {
		t.Fatal("dev-2 не должен быть online")
	}

	env := crypto.Envelope{Type: crypto.MsgCommand, Data: "x"}
	if !h.SendToAgent("dev-1", env) {
		t.Fatal("send вернул false")
	}
	select {
	case got := <-sink.Recv():
		if got.Type != crypto.MsgCommand {
			t.Fatalf("получен неверный тип: %s", got.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("envelope не доставлен")
	}
}

func TestUnregisterMarksOffline(t *testing.T) {
	h := New()
	admin := NewChanAdminSink(8)
	defer admin.Close()
	h.RegisterAdmin(admin)

	sink := NewChanAgentSink(4)
	unsub := h.RegisterAgent("dev-1", sink)
	// ждём online-событие
	if _, ok := WaitForEvent(context.Background(), admin, time.Second, "device.online"); !ok {
		t.Fatal("online-событие не пришло")
	}
	unsub()
	if h.IsOnline("dev-1") {
		t.Fatal("ожидалось offline после unsub")
	}
	if _, ok := WaitForEvent(context.Background(), admin, time.Second, "device.offline"); !ok {
		t.Fatal("offline-событие не пришло")
	}
}

func TestReplaceAgent(t *testing.T) {
	h := New()
	a := NewChanAgentSink(2)
	b := NewChanAgentSink(2)
	defer a.Close()
	defer b.Close()

	h.RegisterAgent("dev", a)
	unsubB := h.RegisterAgent("dev", b)
	// a вытеснен; SendEnv должен идти в b
	env := crypto.Envelope{Type: crypto.MsgPing}
	if !h.SendToAgent("dev", env) {
		t.Fatal("send вернул false")
	}
	select {
	case <-a.Recv():
		t.Fatal("envelope ушёл в вытесненный sink")
	case <-b.Recv():
		// ок
	case <-time.After(time.Second):
		t.Fatal("envelope не доставлен новому sink")
	}
	// отписка b снимает устройство
	unsubB()
	if h.IsOnline("dev") {
		t.Fatal("ожидалось offline после unsub b")
	}
}

func TestSendToUnknownReturnsFalse(t *testing.T) {
	h := New()
	if h.SendToAgent("ghost", crypto.Envelope{}) {
		t.Fatal("send в неизвестное устройство должен вернуть false")
	}
}

func TestOnlineDevices(t *testing.T) {
	h := New()
	h.RegisterAgent("a", NewChanAgentSink(1))
	h.RegisterAgent("b", NewChanAgentSink(1))
	if got := h.OnlineDevices(); len(got) != 2 {
		t.Fatalf("online devices = %d, want 2", len(got))
	}
}

func TestBroadcastDeliversToAllAdmins(t *testing.T) {
	h := New()
	a := NewChanAdminSink(4)
	b := NewChanAdminSink(4)
	defer a.Close()
	defer b.Close()
	h.RegisterAdmin(a)
	h.RegisterAdmin(b)
	h.Broadcast(AdminEvent{Type: "x"})
	for _, s := range []*ChanAdminSink{a, b} {
		select {
		case <-s.Recv():
		case <-time.After(time.Second):
			t.Fatal("broadcast не доставлен в один из sink")
		}
	}
}
