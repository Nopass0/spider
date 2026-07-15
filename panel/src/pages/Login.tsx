/**
 * Страница входа: ввод admin-ключа. Ключ сохраняется в localStorage.
 */
import { useState } from 'react';
import { motion } from 'motion/react';
import { Lock, Terminal, ShieldCheck } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { useAuthStore } from '@/stores/auth';

export function LoginPage() {
  const login = useAuthStore((s) => s.login);
  const [key, setKey] = useState('');
  const [error, setError] = useState<string | null>(null);

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    if (key.trim().length < 8) {
      setError('Ключ слишком короткий (минимум 8 символов)');
      return;
    }
    setError(null);
    login(key.trim());
  };

  return (
    <div className="flex min-h-screen items-center justify-center p-6">
      <motion.div
        initial={{ opacity: 0, y: 12 }}
        animate={{ opacity: 1, y: 0 }}
        className="card w-full max-w-md p-8"
      >
        <div className="mb-6 flex flex-col items-center gap-3 text-center">
          <div className="flex h-14 w-14 items-center justify-center rounded-2xl bg-primary/15 text-primary">
            <Terminal size={28} />
          </div>
          <h1 className="text-2xl font-bold">Spider</h1>
          <p className="text-sm text-muted-foreground">
            Панель удалённого управления парком машин
          </p>
        </div>

        <form onSubmit={submit} className="flex flex-col gap-4">
          <div className="flex flex-col gap-1.5">
            <label className="text-sm font-medium flex items-center gap-1.5">
              <Lock size={14} /> Admin-ключ
            </label>
            <Input
              type="password"
              autoFocus
              placeholder="введите ADMIN_KEY"
              value={key}
              onChange={(e) => setKey(e.target.value)}
            />
          </div>
          {error && <p className="text-sm text-destructive">{error}</p>}
          <Button type="submit" className="w-full">
            <ShieldCheck size={16} /> Войти
          </Button>
        </form>
      </motion.div>
    </div>
  );
}
