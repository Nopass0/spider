/**
 * Auth-стор: хранит admin-key и статус входа.
 *
 * При логине ключ (а) сохраняется в localStorage для REST-запросов с Bearer,
 * (б) отправляется на POST /admin/login, чтобы сервер поставил cookie
 * spider_token — браузерные WebSocket (Terminal/ScreenView/events) авторизуются
 * через эту cookie (new WebSocket не умеет кастомные заголовки, а query/subprotocol
 * режутся рядом reverse-proxy).
 */
import { create } from 'zustand';
import { getAdminKey, setAdminKey, clearAdminKey } from '@/lib/api';

interface AuthState {
  key: string | null;
  ready: boolean;
  login: (key: string) => Promise<void>;
  logout: () => void;
}

export const useAuthStore = create<AuthState>((set) => ({
  key: getAdminKey(),
  ready: true,
  login: async (key) => {
    // Ставим cookie на сервере (для WS-авторизации).
    try {
      await fetch('/admin/login', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ key }),
        credentials: 'include',
      });
    } catch {
      /* если не вышло — REST всё ещё работает через Bearer из localStorage */
    }
    setAdminKey(key);
    set({ key });
  },
  logout: () => {
    clearAdminKey();
    // Очистить cookie на сервере.
    fetch('/admin/login', { method: 'DELETE', credentials: 'include' }).catch(() => {});
    set({ key: null });
  },
}));
