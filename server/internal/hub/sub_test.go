package hub

import (
	"context"
	"testing"
	"time"
)

// TestSubscribeAndSendToAdminsOf — направленная доставка по подписке.
func TestSubscribeAndSendToAdminsOf(t *testing.T) {
	h := New()

	// два админа подписываются на разные устройства
	alice := NewChanAdminSink(8)
	defer alice.Close()
	bob := NewChanAdminSink(8)
	defer bob.Close()

	unsubA := h.SubscribeAdminToDevice("dev-1", alice)
	defer unsubA()
	unsubB := h.SubscribeAdminToDevice("dev-2", bob)
	defer unsubB()

	if got := h.CountDeviceSubs("dev-1"); got != 1 {
		t.Fatalf("subs dev-1 = %d, want 1", got)
	}

	// событие для dev-1 → получает только alice
	h.SendToAdminsOf("dev-1", AdminEvent{Type: "terminal.output", DeviceID: "dev-1"})
	ev, ok := WaitForEvent(context.Background(), alice, time.Second, "terminal.output")
	if !ok {
		t.Fatal("alice не получил событие dev-1")
	}
	if ev.DeviceID != "dev-1" {
		t.Fatalf("device_id = %s", ev.DeviceID)
	}
	// bob не должен получить
	select {
	case e := <-bob.Recv():
		t.Fatalf("bob получил чужое событие: %+v", e)
	case <-time.After(200 * time.Millisecond):
	}

	// событие для dev-2 → только bob
	h.SendToAdminsOf("dev-2", AdminEvent{Type: "screen.frame", DeviceID: "dev-2"})
	if _, ok := WaitForEvent(context.Background(), bob, time.Second, "screen.frame"); !ok {
		t.Fatal("bob не получил событие dev-2")
	}
}

// TestUnsubscribeDevice — отписка снимает подписку.
func TestUnsubscribeDevice(t *testing.T) {
	h := New()
	sink := NewChanAdminSink(4)
	defer sink.Close()

	unsub := h.SubscribeAdminToDevice("dev", sink)
	if got := h.CountDeviceSubs("dev"); got != 1 {
		t.Fatalf("subs = %d", got)
	}
	unsub()
	if got := h.CountDeviceSubs("dev"); got != 0 {
		t.Fatalf("после unsub subs = %d, want 0", got)
	}

	// событие не должно доставиться
	h.SendToAdminsOf("dev", AdminEvent{Type: "x"})
	select {
	case e := <-sink.Recv():
		t.Fatalf("получено после отписки: %+v", e)
	case <-time.After(200 * time.Millisecond):
	}
}

// TestSubscribeMultipleAdminsSameDevice — несколько админов на одно устройство.
func TestSubscribeMultipleAdminsSameDevice(t *testing.T) {
	h := New()
	a := NewChanAdminSink(4)
	defer a.Close()
	b := NewChanAdminSink(4)
	defer b.Close()

	h.SubscribeAdminToDevice("dev", a)
	h.SubscribeAdminToDevice("dev", b)

	if got := h.CountDeviceSubs("dev"); got != 2 {
		t.Fatalf("subs = %d, want 2", got)
	}

	h.SendToAdminsOf("dev", AdminEvent{Type: "screen.frame"})
	// оба получают
	for _, s := range []*ChanAdminSink{a, b} {
		if _, ok := WaitForEvent(context.Background(), s, time.Second, "screen.frame"); !ok {
			t.Fatal("один из админов не получил событие")
		}
	}
}
