/**
 * Settings-стор: глобальный тумблер диспетчеризации команд.
 */
import { create } from 'zustand';
import * as api from '@/lib/api';

interface SettingsState {
  commandsEnabled: boolean;
  loading: boolean;
  fetch: () => Promise<void>;
  toggle: (enabled: boolean) => Promise<void>;
}

export const useSettingsStore = create<SettingsState>((set) => ({
  commandsEnabled: true,
  loading: false,
  fetch: async () => {
    try {
      const enabled = await api.getCommandsEnabled();
      set({ commandsEnabled: enabled });
    } catch {
      /* молча — возможно, нет авторизации */
    }
  },
  toggle: async (enabled) => {
    set({ loading: true });
    try {
      const v = await api.setCommandsEnabled(enabled);
      set({ commandsEnabled: v, loading: false });
    } catch {
      set({ loading: false });
    }
  },
}));
