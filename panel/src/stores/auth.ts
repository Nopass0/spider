/**
 * Auth-стор: хранит admin-key и статус входа.
 */
import { create } from 'zustand';
import { getAdminKey, setAdminKey, clearAdminKey } from '@/lib/api';

interface AuthState {
  key: string | null;
  ready: boolean;
  login: (key: string) => void;
  logout: () => void;
}

export const useAuthStore = create<AuthState>((set) => ({
  key: getAdminKey(),
  ready: true,
  login: (key) => {
    setAdminKey(key);
    set({ key });
  },
  logout: () => {
    clearAdminKey();
    set({ key: null });
  },
}));
