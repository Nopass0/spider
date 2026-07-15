/**
 * Страница «Устройства»: таблица всех устройств с online-статусом и сист.инфо.
 */
import { useEffect } from 'react';
import { useNavigate } from 'react-router-dom';
import { Monitor, Cpu, MemoryStick, Clock } from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import { Card } from '@/components/ui/card';
import { useDevicesStore } from '@/stores/devices';
import { formatBytes, timeAgo } from '@/lib/utils';

export function DevicesPage() {
  const { devices, loading, error, fetch } = useDevicesStore();
  const navigate = useNavigate();

  useEffect(() => {
    fetch();
    const id = setInterval(fetch, 15000);
    return () => clearInterval(id);
  }, [fetch]);

  return (
    <div className="flex flex-col gap-4">
      <header className="flex items-center justify-between">
        <h1 className="text-xl font-bold">Устройства</h1>
        <span className="text-sm text-muted-foreground">
          {devices.length} всего · {devices.filter((d) => d.online).length} онлайн
        </span>
      </header>

      {error && <p className="text-sm text-destructive">{error}</p>}

      <div className="grid gap-3">
        {loading && devices.length === 0 && (
          <Card className="p-6 text-sm text-muted-foreground">Загрузка…</Card>
        )}
        {devices.map((d) => (
          <Card
            key={d.device_id}
            className="cursor-pointer p-4 transition-colors hover:border-primary/50"
            onClick={() => navigate(`/devices/${d.device_id}`)}
          >
            <div className="flex items-start justify-between gap-4">
              <div className="flex items-start gap-3">
                <div className="mt-0.5 flex h-9 w-9 items-center justify-center rounded-lg bg-primary/15 text-primary">
                  <Monitor size={18} />
                </div>
                <div>
                  <div className="flex items-center gap-2">
                    <span className="font-medium">{d.name || d.hostname || d.device_id}</span>
                    <Badge tone={d.online ? 'success' : 'default'}>
                      {d.online ? 'онлайн' : 'офлайн'}
                    </Badge>
                  </div>
                  <p className="mt-0.5 text-xs text-muted-foreground font-mono">{d.device_id}</p>
                </div>
              </div>
              <div className="flex flex-col items-end gap-1 text-xs text-muted-foreground">
                <span className="flex items-center gap-1">
                  <Cpu size={12} /> {d.cpu_cores}яд · {d.os} {d.arch}
                </span>
                <span className="flex items-center gap-1">
                  <MemoryStick size={12} /> {formatBytes(d.mem_total)}
                </span>
                <span className="flex items-center gap-1">
                  <Clock size={12} /> {timeAgo(d.last_seen)}
                </span>
              </div>
            </div>
          </Card>
        ))}
        {!loading && devices.length === 0 && (
          <Card className="p-8 text-center text-sm text-muted-foreground">
            Нет зарегистрированных устройств. Создайте enrollment-токен и запустите агента.
          </Card>
        )}
      </div>
    </div>
  );
}
