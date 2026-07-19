/**
 * ScreenView — просмотр трансляции экрана (MJPEG из base64-кадров) +
 * кнопки «Старт/Стоп» и «Скриншот».
 *
 * Использует тот же stream-WS, что и терминал. Кадры приходят как
 * screen.frame {frame_b64, w, h} — обновляем src <img>.
 * Скриншот: screenshot.snap → screenshot.done → POST на сервер для сохранения.
 */
import { useEffect, useRef, useState } from 'react';
import { Play, Square, Camera, MonitorOff } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { getAdminKey } from '@/lib/api';

interface ScreenViewProps {
  deviceId: string;
}

/** Сгенерировать короткий session_id. */
function newSessionId(): string {
  const arr = new Uint8Array(8);
  crypto.getRandomValues(arr);
  return Array.from(arr).map((b) => b.toString(16).padStart(2, '0')).join('');
}

export function ScreenView({ deviceId }: ScreenViewProps) {
  const [frame, setFrame] = useState<string | null>(null);
  const [running, setRunning] = useState(false);
  const [snapshotSaved, setSnapshotSaved] = useState(false);
  const wsRef = useRef<WebSocket | null>(null);
  const sessionIdRef = useRef<string>(newSessionId());

  const connect = () => {
    const key = getAdminKey();
    if (!key) return;
    const proto = location.protocol === 'https:' ? 'wss' : 'ws';
    const url = `${proto}://${location.host}/admin/devices/${deviceId}/stream`;
    const ws = new WebSocket(url, [`bearer.${key}`]);
    wsRef.current = ws;

    ws.onmessage = (ev) => {
      let msg: any;
      try {
        msg = JSON.parse(ev.data);
      } catch {
        return;
      }
      const payload = msg.payload ?? msg;
      const ptype = payload.type ?? msg.type;
      if (ptype === 'screen.frame' && payload.frame_b64) {
        setFrame(`data:image/jpeg;base64,${payload.frame_b64}`);
      } else if (ptype === 'screenshot.done' && payload.frame_b64) {
        // сохраняем на сервере
        saveScreenshot(deviceId, payload.frame_b64);
        setSnapshotSaved(true);
        setTimeout(() => setSnapshotSaved(false), 2000);
      }
    };
    ws.onclose = () => setRunning(false);
    return ws;
  };

  const start = () => {
    const ws = connect();
    if (!ws) return;
    ws.onopen = () => {
      setRunning(true);
      ws.send(
        JSON.stringify({
          type: 'screen.start',
          session_id: sessionIdRef.current,
          fps: 8,
          quality: 60,
        }),
      );
    };
  };

  const stop = () => {
    const ws = wsRef.current;
    if (ws?.readyState === WebSocket.OPEN) {
      ws.send(JSON.stringify({ type: 'screen.stop', session_id: sessionIdRef.current }));
    }
    ws?.close();
    setRunning(false);
    setFrame(null);
  };

  const snap = () => {
    // Для одиночного скриншота открываем отдельное WS-соединение (короткое).
    const key = getAdminKey();
    if (!key) return;
    const proto = location.protocol === 'https:' ? 'wss' : 'ws';
    const url = `${proto}://${location.host}/admin/devices/${deviceId}/stream`;
    const ws = new WebSocket(url, [`bearer.${key}`]);
    ws.onopen = () => {
      ws.send(
        JSON.stringify({ type: 'screenshot.snap', session_id: sessionIdRef.current + '-snap' }),
      );
    };
    ws.onmessage = (ev) => {
      let msg: any;
      try {
        msg = JSON.parse(ev.data);
      } catch {
        return;
      }
      const payload = msg.payload ?? msg;
      const ptype = payload.type ?? msg.type;
      if (ptype === 'screenshot.done' && payload.frame_b64) {
        saveScreenshot(deviceId, payload.frame_b64);
        setFrame(`data:image/jpeg;base64,${payload.frame_b64}`);
        setSnapshotSaved(true);
        setTimeout(() => setSnapshotSaved(false), 2000);
        ws.close();
      }
    };
  };

  useEffect(() => {
    return () => {
      wsRef.current?.close();
    };
  }, []);

  return (
    <div className="flex flex-col gap-3">
      <div className="flex items-center gap-2">
        {!running ? (
          <Button onClick={start} size="sm">
            <Play size={14} /> Старт трансляции
          </Button>
        ) : (
          <Button onClick={stop} variant="danger" size="sm">
            <Square size={14} /> Стоп
          </Button>
        )}
        <Button onClick={snap} variant="secondary" size="sm" disabled={running}>
          <Camera size={14} /> {snapshotSaved ? '✓ сохранён' : 'Скриншот'}
        </Button>
        <span className="text-xs text-muted-foreground">
          {running ? 'MJPEG ~8fps' : snapshotSaved ? 'скриншот сохранён на сервере' : ''}
        </span>
      </div>
      <div className="overflow-hidden rounded-lg border bg-black">
        {frame ? (
          <img src={frame} alt="screen" className="w-full" />
        ) : (
          <div className="flex h-64 flex-col items-center justify-center gap-2 text-muted-foreground">
            <MonitorOff size={32} />
            <span className="text-sm">Трансляция выключена</span>
          </div>
        )}
      </div>
    </div>
  );
}

/** Сохранить скриншот на сервере через REST API. */
async function saveScreenshot(deviceId: string, frameB64: string) {
  const key = getAdminKey();
  if (!key) return;
  try {
    await fetch(`/admin/devices/${deviceId}/screenshots`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        Authorization: `Bearer ${key}`,
      },
      body: JSON.stringify({ frame_b64: frameB64 }),
    });
  } catch {
    /* ignore */
  }
}
