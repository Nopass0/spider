/**
 * Devices-стор: список устройств, загрузка, обновление через WS-события.
 */
import { create } from 'zustand';
import * as api from '@/lib/api';
import type { Device } from '@/schemas';

interface DevicesState {
  devices: Device[];
  loading: boolean;
  error: string | null;
  fetch: () => Promise<void>;
  setOnline: (deviceId: string, online: boolean) => void;
  remove: (deviceId: string) => void;
}

export const useDevicesStore = create<DevicesState>((set) => ({
  devices: [],
  loading: false,
  error: null,
  fetch: async () => {
    set({ loading: true, error: null });
    try {
      const devices = await api.listDevices();
      set({ devices, loading: false });
    } catch (e) {
      set({ error: errMsg(e), loading: false });
    }
  },
  setOnline: (deviceId, online) =>
    set((s) => ({
      devices: s.devices.map((d) =>
        d.device_id === deviceId ? { ...d, online } : d,
      ),
    })),
  remove: (deviceId) =>
    set((s) => ({ devices: s.devices.filter((d) => d.device_id !== deviceId) })),
}));

function errMsg(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}
