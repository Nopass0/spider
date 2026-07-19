/**
 * Страница устройства: сист.инфо + табы Консоль/Терминал/Экран.
 *
 * - Консоль: история команд + отправка разовой команды (request/response).
 * - Терминал: интерактивный PTY (xterm.js, как SSH).
 * - Экран: MJPEG-трансляция + скриншоты.
 */
import { useCallback, useEffect, useState } from 'react';
import { useParams, useNavigate } from 'react-router-dom';
import { motion } from 'motion/react';
import { ArrowLeft, Send, Trash2, SquareTerminal, Monitor } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Card } from '@/components/ui/card';
import { Badge } from '@/components/ui/badge';
import { Input } from '@/components/ui/input';
import * as api from '@/lib/api';
import { EnqueueCommandSchema, type Command } from '@/schemas';
import { decodeB64, formatBytes, formatTime } from '@/lib/utils';
import { useDevicesStore } from '@/stores/devices';
import { useEvents } from '@/hooks/useEvents';
import { TerminalView } from '@/components/Terminal';
import { ScreenView } from '@/components/ScreenView';

type Tab = 'console' | 'terminal' | 'screen';

export function DeviceDetailPage() {
  const { id = '' } = useParams();
  const navigate = useNavigate();
  const device = useDevicesStore((s) => s.devices.find((d) => d.device_id === id));
  const remove = useDevicesStore((s) => s.remove);

  const [command, setCommand] = useState('');
  const [history, setHistory] = useState<Command[]>([]);
  const [sending, setSending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [tab, setTab] = useState<Tab>('console');

  const loadHistory = useCallback(async () => {
    try {
      setHistory(await api.listCommands(id));
    } catch {
      /* ignore */
    }
  }, [id]);

  useEffect(() => {
    loadHistory();
  }, [loadHistory]);

  // Live-обновление истории при результате по WS.
  useEvents({
    onCommandResult: (deviceId) => {
      if (deviceId === id) loadHistory();
    },
  });

  const send = async (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);
    const parsed = EnqueueCommandSchema.safeParse({
      command: command.trim(),
      timeout_sec: 60,
    });
    if (!parsed.success) {
      setError(parsed.error.issues[0]?.message ?? 'ошибка');
      return;
    }
    setSending(true);
    try {
      await api.enqueueCommand(id, parsed.data);
      setCommand('');
      await loadHistory();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setSending(false);
    }
  };

  const del = async () => {
    if (!confirm('Удалить устройство и всю его историю команд?')) return;
    try {
      await api.deleteDevice(id);
      remove(id);
      navigate('/devices');
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  };

  return (
    <div className="flex flex-col gap-4">
      <header className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          <Button variant="ghost" size="icon" onClick={() => navigate('/devices')}>
            <ArrowLeft size={18} />
          </Button>
          <h1 className="text-xl font-bold">
            {device?.name || device?.hostname || id}
          </h1>
          {device && (
            <Badge tone={device.online ? 'success' : 'default'}>
              {device.online ? 'онлайн' : 'офлайн'}
            </Badge>
          )}
        </div>
        <Button variant="danger" size="sm" onClick={del}>
          <Trash2 size={14} /> Удалить
        </Button>
      </header>

      {device && (
        <Card className="grid grid-cols-2 gap-3 p-4 text-sm md:grid-cols-4">
          <Info label="hostname" value={device.hostname} />
          <Info label="ОС" value={`${device.os} ${device.arch}`} />
          <Info label="CPU" value={`${device.cpu_brand} (${device.cpu_cores} ядер)`} />
          <Info label="Память" value={formatBytes(device.mem_total)} />
          <Info label="Агент" value={device.agent_version} />
          <Info label="Первый вход" value={formatTime(device.first_seen)} />
          <Info label="Последний" value={formatTime(device.last_seen)} />
          <Info label="ID" value={device.device_id} mono />
        </Card>
      )}

      {/* Табы: Консоль / Терминал / Экран */}
      <div className="flex gap-1 border-b">
        <TabButton active={tab === 'console'} onClick={() => setTab('console')} icon={<Send size={14} />}>
          Консоль
        </TabButton>
        <TabButton active={tab === 'terminal'} onClick={() => setTab('terminal')} icon={<SquareTerminal size={14} />}>
          Терминал
        </TabButton>
        <TabButton active={tab === 'screen'} onClick={() => setTab('screen')} icon={<Monitor size={14} />}>
          Экран
        </TabButton>
      </div>

      {tab === 'terminal' && <TerminalView deviceId={id} />}
      {tab === 'screen' && <ScreenView deviceId={id} />}

      {tab === 'console' && (
        <>
          <Card className="p-4">
            <form onSubmit={send} className="flex gap-2">
              <div className="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg bg-primary/15 text-primary">
                <Send size={18} />
              </div>
              <Input
                placeholder="введите консольную команду…"
                value={command}
                onChange={(e) => setCommand(e.target.value)}
                className="font-mono"
              />
              <Button type="submit" disabled={sending || !command.trim()}>
                <Send size={16} /> Отправить
              </Button>
            </form>
            {error && <p className="mt-2 text-sm text-destructive">{error}</p>}
          </Card>

          <div className="flex flex-col gap-2">
            <h2 className="text-sm font-semibold text-muted-foreground">История команд</h2>
            {history.map((c) => (
              <motion.div
                key={c.id}
                initial={{ opacity: 0 }}
                animate={{ opacity: 1 }}
          >
            <Card className="p-4">
              <div className="mb-2 flex items-center justify-between gap-2">
                <code className="text-sm font-mono text-primary">{c.command}</code>
                <div className="flex items-center gap-2">
                  <CommandStatusBadge status={c.status} />
                  {c.has_result && c.result && (
                    <Badge tone={c.result.exit_code === 0 ? 'success' : 'destructive'}>
                      exit {c.result.exit_code}
                    </Badge>
                  )}
                  <span className="text-xs text-muted-foreground">{formatTime(c.created_at)}</span>
                </div>
              </div>
              {c.has_result && c.result && (
                <div className="grid gap-2 md:grid-cols-2">
                  <Output label="stdout" text={decodeB64(c.result.stdout_b64)} />
                  <Output label="stderr" text={decodeB64(c.result.stderr_b64)} tone="error" />
                </div>
              )}
            </Card>
          </motion.div>
        ))}
        {history.length === 0 && (
          <Card className="p-6 text-center text-sm text-muted-foreground">
            Команды ещё не отправлялись.
          </Card>
        )}
      </div>
        </>
      )}
    </div>
  );
}

function TabButton({
  active,
  onClick,
  icon,
  children,
}: {
  active: boolean;
  onClick: () => void;
  icon: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <button
      onClick={onClick}
      className={`flex items-center gap-2 border-b-2 px-4 py-2 text-sm font-medium transition-colors ${
        active
          ? 'border-primary text-primary'
          : 'border-transparent text-muted-foreground hover:text-foreground'
      }`}
    >
      {icon}
      {children}
    </button>
  );
}

function Info({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div>
      <p className="text-xs text-muted-foreground">{label}</p>
      <p className={`truncate ${mono ? 'font-mono text-xs' : ''}`}>{value || '—'}</p>
    </div>
  );
}

function Output({ label, text, tone }: { label: string; text: string; tone?: 'error' }) {
  if (!text) return null;
  return (
    <div>
      <p className="mb-1 text-xs text-muted-foreground">{label}</p>
      <pre
        className={`max-h-60 overflow-auto rounded-lg bg-background p-3 text-xs ${
          tone === 'error' ? 'text-destructive' : ''
        }`}
      >
        {text}
      </pre>
    </div>
  );
}

function CommandStatusBadge({ status }: { status: Command['status'] }) {
  const tone =
    status === 'done'
      ? 'success'
      : status === 'error' || status === 'timeout'
        ? 'destructive'
        : status === 'running'
          ? 'primary'
          : 'default';
  return <Badge tone={tone}>{status}</Badge>;
}
