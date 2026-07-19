/**
 * Terminal — интерактивный xterm.js, подключённый к агентскому PTY через
 * двунаправленный WS /admin/devices/{id}/stream.
 *
 * Поток: mount → terminal.open → агент создаёт PTY.
 * Ввод пользователя → terminal.input → агент пишет в PTY.
 * Resize контейнера → terminal.resize.
 * agent → terminal.output → term.write (стриминг).
 */
import { useEffect, useRef, useState } from 'react';
import { Terminal as XTerm } from '@xterm/xterm';
import { FitAddon } from '@xterm/addon-fit';
import { WebLinksAddon } from '@xterm/addon-web-links';
import '@xterm/xterm/css/xterm.css';
import { getAdminKey } from '@/lib/api';

interface TerminalProps {
  deviceId: string;
}

/** Сгенерировать короткий session_id. */
function newSessionId(): string {
  const arr = new Uint8Array(8);
  crypto.getRandomValues(arr);
  return Array.from(arr).map((b) => b.toString(16).padStart(2, '0')).join('');
}

export function TerminalView({ deviceId }: TerminalProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const termRef = useRef<XTerm | null>(null);
  const wsRef = useRef<WebSocket | null>(null);
  const sessionIdRef = useRef<string>(newSessionId());
  const [status, setStatus] = useState<'connecting' | 'live' | 'closed'>('connecting');

  useEffect(() => {
    if (!containerRef.current) return;
    const key = getAdminKey();
    if (!key) return;

    const term = new XTerm({
      cursorBlink: true,
      fontSize: 13,
      fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace',
      theme: {
        background: '#0e1117',
        foreground: '#cdd5e0',
        cursor: '#7dd3fc',
      },
    });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.loadAddon(new WebLinksAddon());
    term.open(containerRef.current);
    fit.fit();
    termRef.current = term;
    term.writeln('\x1b[36m▶ подключение к терминалу…\x1b[0m');

    const proto = location.protocol === 'https:' ? 'wss' : 'ws';
    // Авторизация через subprotocol bearer.<key> (query режется reverse-proxy на WS-upgrade).
    const url = `${proto}://${location.host}/admin/devices/${deviceId}/stream`;
    const ws = new WebSocket(url, [`bearer.${key}`]);
    wsRef.current = ws;

    let opened = false;
    ws.onopen = () => {
      setStatus('live');
      // Шлём terminal.open с текущими размерами.
      const cols = term.cols;
      const rows = term.rows;
      ws.send(JSON.stringify({ type: 'terminal.open', session_id: sessionIdRef.current, cols, rows }));
      opened = true;
      term.focus();
    };
    ws.onmessage = (ev) => {
      let msg: { type: string; session_id?: string; data_b64?: string; exit_code?: number };
      try {
        msg = JSON.parse(ev.data);
      } catch {
        return;
      }
      // payload может быть в корне (AgentEvent) или вложен.
      const payload = (msg as any).payload ?? msg;
      const ptype = payload.type ?? msg.type;
      if (ptype === 'terminal.output' && payload.data_b64) {
        term.write(decodeB64(payload.data_b64));
      } else if (ptype === 'terminal.exit') {
        term.writeln(`\r\n\x1b[33m[процесс завершён, exit=${payload.exit_code ?? '?'}]\x1b[0m`);
      }
    };
    ws.onerror = () => {
      setStatus('closed');
      term.writeln('\r\n\x1b[31m✗ ошибка соединения\x1b[0m');
    };
    ws.onclose = () => {
      setStatus('closed');
      if (opened) term.writeln('\r\n\x1b[33m■ соединение закрыто\x1b[0m');
    };

    // Ввод пользователя → terminal.input.
    const inputDisp = term.onData((data) => {
      if (ws.readyState === WebSocket.OPEN) {
        ws.send(
          JSON.stringify({
            type: 'terminal.input',
            session_id: sessionIdRef.current,
            data_b64: btoa(unescape(encodeURIComponent(data))),
          }),
        );
      }
    });

    // Resize контейнера → terminal.resize.
    const fitDisp = term.onResize(({ cols, rows }) => {
      if (ws.readyState === WebSocket.OPEN) {
        ws.send(
          JSON.stringify({ type: 'terminal.resize', session_id: sessionIdRef.current, cols, rows }),
        );
      }
    });
    const onResize = () => fit.fit();
    window.addEventListener('resize', onResize);

    return () => {
      inputDisp.dispose();
      fitDisp.dispose();
      window.removeEventListener('resize', onResize);
      // Закрыть PTY на агенте.
      if (ws.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify({ type: 'terminal.close', session_id: sessionIdRef.current }));
      }
      ws.close();
      term.dispose();
    };
  }, [deviceId]);

  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center gap-2 text-sm">
        <span
          className={`badge ${
            status === 'live'
              ? 'bg-success/20 text-success'
              : status === 'connecting'
                ? 'bg-warning/20 text-warning'
                : 'bg-destructive/20 text-destructive'
          }`}
        >
          {status === 'live' ? '● live' : status === 'connecting' ? '○ подключение' : '✕ закрыто'}
        </span>
        <span className="text-muted-foreground font-mono text-xs">session {sessionIdRef.current}</span>
      </div>
      <div
        ref={containerRef}
        className="h-[480px] overflow-hidden rounded-lg border bg-[#0e1117] p-2"
      />
    </div>
  );
}

/** Декодировать base64 в строку (UTF-8 безопасно). */
function decodeB64(b64: string): string {
  try {
    const bin = atob(b64);
    return decodeURIComponent(escape(bin));
  } catch {
    return b64;
  }
}
