// Package hub реализует реестр активных WebSocket-соединений агентов и
// администраторов. Через hub сервер:
//
//   - отправляет команды конкретному устройству (по device_id),
//   - принимает от устройства ack/результаты,
//   - рассылает события (device online/offline, результат команды) всем
//     подключённым сессиям администратора для live-обновления панели.
//
// Hub потокобезопасен.
package hub

import (
	"context"
	"sync"
	"time"

	"github.com/nopass0/spider/server/internal/crypto"
)

// AgentSink — интерфейс записи в соединение агента. Изолирует hub от деталей
// WebSocket/long-poll (удобно для тестов и единообразия — DRY).
type AgentSink interface {
	// SendEnv пытается доставить envelope. Возвращает false, если канал закрыт
	// (устройство отключилось) — тогда hub снимает подписку.
	SendEnv(crypto.Envelope) bool
	// Done возвращает канал, закрываемый при разрыве соединения.
	Done() <-chan struct{}
}

// AdminEvent — событие, рассылаемое в панель по WS /admin/events.
type AdminEvent struct {
	Type     string `json:"type"`              // device.online | device.offline | command.result | ...
	DeviceID string `json:"device_id,omitempty"`
	Payload  any    `json:"payload,omitempty"`
}

// AdminSink — канал доставки событий в конкретное WS-соединение панели.
type AdminSink interface {
	SendEvent(AdminEvent) bool
	Done() <-chan struct{}
}

// Hub — центральный реестр.
type Hub struct {
	mu       sync.RWMutex
	agents   map[string]AgentSink     // device_id -> sink
	admins   map[AdminSink]struct{}   // множество активных sink-ов панели
	subs     map[string]map[AdminSink]struct{} // device_id -> множество админ-синков, подписанных на него
}

// New создаёт пустой hub.
func New() *Hub {
	return &Hub{
		agents: make(map[string]AgentSink),
		admins: make(map[AdminSink]struct{}),
		subs:   make(map[string]map[AdminSink]struct{}),
	}
}

// RegisterAgent добавляет агентский sink под device_id. Если устройство уже
// подключено — старое соединение вытесняется (SendEnv на нём перестанет работать).
// Возвращает функцию отмены подписки (вызывать в defer из обработчика).
func (h *Hub) RegisterAgent(deviceID string, sink AgentSink) func() {
	h.mu.Lock()
	_, ok := h.agents[deviceID]
	if ok {
		// Вытесняем старое — удаляем из карты; оно закроется само.
		delete(h.agents, deviceID)
	}
	h.agents[deviceID] = sink
	h.mu.Unlock()
	if ok {
		h.Broadcast(AdminEvent{Type: "device.replaced", DeviceID: deviceID})
	}
	h.Broadcast(AdminEvent{Type: "device.online", DeviceID: deviceID})

	return func() {
		h.mu.Lock()
		// Удаляем, только если это всё ещё наш sink (а не вытеснивший нас).
		if cur, still := h.agents[deviceID]; still && cur == sink {
			delete(h.agents, deviceID)
		}
		h.mu.Unlock()
		h.Broadcast(AdminEvent{Type: "device.offline", DeviceID: deviceID})
	}
}

// IsOnline сообщает, есть ли активное WS-соединение у устройства.
func (h *Hub) IsOnline(deviceID string) bool {
	h.mu.RLock()
	_, ok := h.agents[deviceID]
	h.mu.RUnlock()
	return ok
}

// OnlineDevices возвращает snapshot device_id, у которых есть активное WS.
func (h *Hub) OnlineDevices() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]string, 0, len(h.agents))
	for id := range h.agents {
		out = append(out, id)
	}
	return out
}

// SendToAgent доставляет envelope конкретному устройству.
// Возвращает false, если устройство не в WS (тогда нужен long-poll fallback).
func (h *Hub) SendToAgent(deviceID string, env crypto.Envelope) bool {
	h.mu.RLock()
	sink, ok := h.agents[deviceID]
	h.mu.RUnlock()
	if !ok {
		return false
	}
	return sink.SendEnv(env)
}

// RegisterAdmin добавляет sink панели и возвращает функцию отписки.
func (h *Hub) RegisterAdmin(sink AdminSink) func() {
	h.mu.Lock()
	h.admins[sink] = struct{}{}
	h.mu.Unlock()
	return func() {
		h.mu.Lock()
		delete(h.admins, sink)
		h.mu.Unlock()
	}
}

// Broadcast рассылает событие всем подключённым админам (best-effort).
func (h *Hub) Broadcast(ev AdminEvent) {
	h.mu.RLock()
	sinks := make([]AdminSink, 0, len(h.admins))
	for s := range h.admins {
		sinks = append(sinks, s)
	}
	h.mu.RUnlock()
	for _, s := range sinks {
		s.SendEvent(ev)
	}
}

// SubscribeAdminToDevice подписывает admin-sink на события конкретного устройства
// (terminal.output, screen.frame и т.п.). Возвращает функцию отписки.
// Используется двунаправленным stream-WS для направленной доставки agent→admin.
func (h *Hub) SubscribeAdminToDevice(deviceID string, sink AdminSink) func() {
	h.mu.Lock()
	m, ok := h.subs[deviceID]
	if !ok {
		m = make(map[AdminSink]struct{})
		h.subs[deviceID] = m
	}
	m[sink] = struct{}{}
	h.mu.Unlock()

	return func() {
		h.mu.Lock()
		if m, ok := h.subs[deviceID]; ok {
			delete(m, sink)
			if len(m) == 0 {
				delete(h.subs, deviceID)
			}
		}
		h.mu.Unlock()
	}
}

// SendToAdminsOf рассылает событие только админам, подписанным на устройство.
// Используется для тяжёлого потокового трафика (terminal/screen), чтобы не
// дублировать его во все открытые панели.
func (h *Hub) SendToAdminsOf(deviceID string, ev AdminEvent) {
	h.mu.RLock()
	m := h.subs[deviceID]
	sinks := make([]AdminSink, 0, len(m))
	for s := range m {
		sinks = append(sinks, s)
	}
	h.mu.RUnlock()
	for _, s := range sinks {
		s.SendEvent(ev)
	}
}

// CountAgents/CountAdmins — для метрик и тестов.
func (h *Hub) CountAgents() int { h.mu.RLock(); defer h.mu.RUnlock(); return len(h.agents) }
func (h *Hub) CountAdmins() int { h.mu.RLock(); defer h.mu.RUnlock(); return len(h.admins) }
// CountDeviceSubs — число админ-подписок на устройство (для тестов/метрик).
func (h *Hub) CountDeviceSubs(deviceID string) int {
	h.mu.RLock(); defer h.mu.RUnlock(); return len(h.subs[deviceID])
}

// --- Готовые реализации sink ---

// ChanAgentSink — простейший sink на буферизованном канале.
type ChanAgentSink struct {
	envs chan crypto.Envelope
	done chan struct{}
}

// NewChanAgentSink создаёт sink с буфером capacity.
func NewChanAgentSink(capacity int) *ChanAgentSink {
	if capacity < 1 {
		capacity = 8
	}
	return &ChanAgentSink{envs: make(chan crypto.Envelope, capacity), done: make(chan struct{})}
}

// SendEnv кладёт envelope в канал; при переполнении возвращает false (drop).
func (c *ChanAgentSink) SendEnv(e crypto.Envelope) bool {
	select {
	case c.envs <- e:
		return true
	default:
		return false
	}
}
func (c *ChanAgentSink) Done() <-chan struct{}        { return c.done }
func (c *ChanAgentSink) Recv() <-chan crypto.Envelope { return c.envs }
func (c *ChanAgentSink) Close()                       { close(c.done) }

// ChanAdminSink — sink событий панели на буферизованном канале.
type ChanAdminSink struct {
	evs  chan AdminEvent
	done chan struct{}
}

// NewChanAdminSink создаёт sink с буфером capacity.
func NewChanAdminSink(capacity int) *ChanAdminSink {
	if capacity < 1 {
		capacity = 16
	}
	return &ChanAdminSink{evs: make(chan AdminEvent, capacity), done: make(chan struct{})}
}

func (c *ChanAdminSink) SendEvent(e AdminEvent) bool {
	select {
	case c.evs <- e:
		return true
	default:
		return false
	}
}
func (c *ChanAdminSink) Done() <-chan struct{}   { return c.done }
func (c *ChanAdminSink) Recv() <-chan AdminEvent { return c.evs }
func (c *ChanAdminSink) Close()                  { close(c.done) }

// WaitForEvent ждёт событие заданного типа до timeout (для тестов/панели).
func WaitForEvent(ctx context.Context, sink *ChanAdminSink, timeout time.Duration, want string) (AdminEvent, bool) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		select {
		case ev := <-sink.Recv():
			if ev.Type == want {
				return ev, true
			}
		case <-ctx.Done():
			return AdminEvent{}, false
		}
	}
}
