/**
 * Страница «Настройки»: глобальный тумблер диспетчеризации команд + аудит.
 */
import { useEffect, useState } from 'react';
import { ShieldAlert, History } from 'lucide-react';
import { Card } from '@/components/ui/card';
import { Toggle } from '@/components/ui/toggle';
import { Badge } from '@/components/ui/badge';
import { useSettingsStore } from '@/stores/settings';
import * as api from '@/lib/api';
import type { AuditEntry } from '@/schemas';
import { formatTime } from '@/lib/utils';

export function SettingsPage() {
  const { commandsEnabled, loading, fetch, toggle } = useSettingsStore();
  const [audit, setAudit] = useState<AuditEntry[]>([]);

  useEffect(() => {
    fetch();
    api.listAudit().then(setAudit).catch(() => {});
  }, [fetch]);

  return (
    <div className="flex flex-col gap-4">
      <h1 className="text-xl font-bold">Настройки</h1>

      <Card className="p-5">
        <div className="flex items-start justify-between gap-4">
          <div className="flex items-start gap-3">
            <div className="flex h-9 w-9 items-center justify-center rounded-lg bg-warning/20 text-warning">
              <ShieldAlert size={18} />
            </div>
            <div>
              <p className="font-medium">Диспетчеризация команд</p>
              <p className="text-sm text-muted-foreground">
                Глобальный тумблер. Когда выключено — команды не ставятся в очередь и не
                отправляются ни одному устройству.
              </p>
            </div>
          </div>
          <Toggle
            checked={commandsEnabled}
            disabled={loading}
            onChange={(v) => toggle(v)}
          />
        </div>
        <div className="mt-3">
          <Badge tone={commandsEnabled ? 'success' : 'destructive'}>
            {commandsEnabled ? 'включено' : 'выключено'}
          </Badge>
        </div>
      </Card>

      <Card className="p-5">
        <div className="mb-3 flex items-center gap-2">
          <History size={18} className="text-muted-foreground" />
          <h2 className="font-semibold">Журнал аудита</h2>
        </div>
        <div className="flex flex-col gap-1.5">
          {audit.map((a) => (
            <div
              key={a.id}
              className="flex items-center justify-between rounded-lg bg-background px-3 py-2 text-sm"
            >
              <div className="flex items-center gap-2">
                <code className="rounded bg-muted px-1.5 py-0.5 text-xs">{a.action}</code>
                <span className="text-muted-foreground">{a.actor}</span>
                {a.target && <span className="text-xs">→ {a.target}</span>}
                {a.detail && <span className="text-xs text-muted-foreground">({a.detail})</span>}
              </div>
              <span className="text-xs text-muted-foreground">{formatTime(a.at)}</span>
            </div>
          ))}
          {audit.length === 0 && (
            <p className="py-4 text-center text-sm text-muted-foreground">Журнал пуст.</p>
          )}
        </div>
      </Card>
    </div>
  );
}
