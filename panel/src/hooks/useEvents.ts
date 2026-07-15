/**
 * useEvents — хук, держащий WS-соединение к /admin/events для live-обновлений.
 * При входящих событиях обновляет devices-стор и пробрасывает колбэки.
 */
import { useEffect, useRef } from 'react';
import { getAdminKey } from '@/lib/api';
import { useDevicesStore } from '@/stores/devices';

export interface AdminEvent {
  type: string;
  device_id?: string;
  payload?: unknown;
}

interface UseEventsOptions {
  onCommandResult?: (deviceId: string, payload: unknown) => void;
}

/**
 * Подключиться к WS событий. Автопереподключение при разрыве.
 */
export function useEvents(opts: UseEventsOptions = {}): void {
  const setOnline = useDevicesStore((s) => s.setOnline);
  const fetchDevices = useDevicesStore((s) => s.fetch);
  const onResult = useRef(opts.onCommandResult);
  onResult.current = opts.onCommandResult;

  useEffect(() => {
    const key = getAdminKey();
    if (!key) return;

    let ws: WebSocket | null = null;
    let reconnectTimer: ReturnType<typeof setTimeout>;
    let closed = false;

    const connect = () => {
      const proto = location.protocol === 'https:' ? 'wss' : 'ws';
      const url = `${proto}://${location.host}/admin/events?token=${encodeURIComponent(key)}`;
      ws = new WebSocket(url);
      ws.onmessage = (ev) => {
        let event: AdminEvent;
        try {
          event = JSON.parse(ev.data);
        } catch {
          return;
        }
        switch (event.type) {
          case 'device.online':
          case 'device.enrolled':
            if (event.device_id) setOnline(event.device_id, true);
            fetchDevices();
            break;
          case 'device.offline':
          case 'device.deleted':
          case 'device.replaced':
            if (event.device_id) setOnline(event.device_id, false);
            fetchDevices();
            break;
          case 'command.result':
            if (event.device_id) {
              onResult.current?.(event.device_id, event.payload);
            }
            break;
          default:
            break;
        }
      };
      ws.onclose = () => {
        if (!closed) {
          reconnectTimer = setTimeout(connect, 3000);
        }
      };
      ws.onerror = () => ws?.close();
    };
    connect();

    return () => {
      closed = true;
      clearTimeout(reconnectTimer);
      ws?.close();
    };
  }, [setOnline, fetchDevices]);
}
