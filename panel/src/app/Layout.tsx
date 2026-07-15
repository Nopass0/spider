/**
 * Layout — боковая навигация + контейнер страниц. Объявляет WS-подписку.
 */
import { NavLink, Outlet } from 'react-router-dom';
import { Monitor, Ticket, Settings, Terminal, LogOut } from 'lucide-react';
import { cn } from '@/lib/utils';
import { useAuthStore } from '@/stores/auth';
import { useEvents } from '@/hooks/useEvents';

const navItems = [
  { to: '/devices', label: 'Устройства', icon: Monitor },
  { to: '/enrollments', label: 'Токены', icon: Ticket },
  { to: '/settings', label: 'Настройки', icon: Settings },
];

export function Layout() {
  const logout = useAuthStore((s) => s.logout);
  // подписка на live-события (обновляет devices-стор).
  useEvents();

  return (
    <div className="flex min-h-screen">
      <aside className="flex w-60 flex-col border-r bg-card">
        <div className="flex items-center gap-2 border-b p-5">
          <div className="flex h-9 w-9 items-center justify-center rounded-xl bg-primary/15 text-primary">
            <Terminal size={20} />
          </div>
          <span className="text-lg font-bold">Spider</span>
        </div>
        <nav className="flex flex-1 flex-col gap-1 p-3">
          {navItems.map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              className={({ isActive }) =>
                cn(
                  'flex items-center gap-3 rounded-lg px-3 py-2 text-sm transition-colors',
                  isActive
                    ? 'bg-primary/15 text-primary'
                    : 'text-muted-foreground hover:bg-muted hover:text-foreground',
                )
              }
            >
              <item.icon size={18} />
              {item.label}
            </NavLink>
          ))}
        </nav>
        <div className="border-t p-3">
          <button
            onClick={logout}
            className="flex w-full items-center gap-3 rounded-lg px-3 py-2 text-sm text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
          >
            <LogOut size={18} /> Выйти
          </button>
        </div>
      </aside>
      <main className="flex-1 overflow-auto p-6">
        <Outlet />
      </main>
    </div>
  );
}
