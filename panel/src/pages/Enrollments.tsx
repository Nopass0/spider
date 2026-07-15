/**
 * Страница «Enrollment-токены»: создание и листинг одноразовых токенов.
 */
import { useEffect, useState } from 'react';
import { motion } from 'motion/react';
import { Plus, Trash2, Copy, Check, Ticket } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Card } from '@/components/ui/card';
import { Badge } from '@/components/ui/badge';
import { Input } from '@/components/ui/input';
import * as api from '@/lib/api';
import type { CreatedEnrollment, Enrollment } from '@/schemas';
import { formatTime } from '@/lib/utils';

export function EnrollmentsPage() {
  const [list, setList] = useState<Enrollment[]>([]);
  const [note, setNote] = useState('');
  const [created, setCreated] = useState<CreatedEnrollment | null>(null);
  const [copied, setCopied] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const load = async () => {
    try {
      setList(await api.listEnrollments());
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  };

  useEffect(() => {
    load();
  }, []);

  const create = async (e: React.FormEvent) => {
    e.preventDefault();
    setLoading(true);
    setError(null);
    try {
      const c = await api.createEnrollment(note);
      setCreated(c);
      setNote('');
      await load();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  };

  const remove = async (token: string) => {
    try {
      await api.deleteEnrollment(token);
      await load();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  };

  const copyCmd = () => {
    if (!created) return;
    const cmd = `spider-agent run --server ${location.origin} --enroll-token ${created.token} --yes`;
    navigator.clipboard.writeText(cmd);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };

  return (
    <div className="flex flex-col gap-4">
      <h1 className="text-xl font-bold">Enrollment-токены</h1>

      <Card className="p-4">
        <form onSubmit={create} className="flex gap-2">
          <Input
            placeholder="примечание (напр. «офис-ПК-3»)"
            value={note}
            onChange={(e) => setNote(e.target.value)}
          />
          <Button type="submit" disabled={loading}>
            <Plus size={16} /> Создать
          </Button>
        </form>
        {error && <p className="mt-2 text-sm text-destructive">{error}</p>}
      </Card>

      {created && (
        <motion.div initial={{ opacity: 0, y: 8 }} animate={{ opacity: 1, y: 0 }}>
          <Card className="border-primary/40 p-4">
            <div className="mb-2 flex items-center gap-2 text-primary">
              <Ticket size={16} />
              <span className="text-sm font-semibold">Новый токен (показать один раз)</span>
            </div>
            <code className="block rounded-lg bg-background p-3 font-mono text-sm">
              {created.token}
            </code>
            <div className="mt-3">
              <p className="mb-1 text-xs text-muted-foreground">Команда для запуска на машине:</p>
              <div className="flex gap-2">
                <code className="flex-1 rounded-lg bg-background p-3 font-mono text-xs">
                  spider-agent run --server {location.origin} --enroll-token {created.token} --yes
                </code>
                <Button variant="secondary" size="icon" onClick={copyCmd}>
                  {copied ? <Check size={16} /> : <Copy size={16} />}
                </Button>
              </div>
            </div>
          </Card>
        </motion.div>
      )}

      <div className="flex flex-col gap-2">
        {list.map((e) => (
          <Card key={e.token} className="flex items-center justify-between gap-3 p-4">
            <div className="min-w-0">
              <div className="flex items-center gap-2">
                <code className="truncate font-mono text-sm">{e.token}</code>
                {e.used_at ? (
                  <Badge tone="default">использован</Badge>
                ) : (
                  <Badge tone="success">активен</Badge>
                )}
              </div>
              <p className="mt-0.5 text-xs text-muted-foreground">
                {e.note && `${e.note} · `}
                истекает {formatTime(e.expires_at)}
                {e.used_by && ` · ${e.used_by}`}
              </p>
            </div>
            <Button variant="ghost" size="icon" onClick={() => remove(e.token)}>
              <Trash2 size={16} />
            </Button>
          </Card>
        ))}
        {list.length === 0 && (
          <Card className="p-6 text-center text-sm text-muted-foreground">
            Токенов пока нет.
          </Card>
        )}
      </div>
    </div>
  );
}
