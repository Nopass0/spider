/**
 * Тесты auth-стора.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { useAuthStore } from './auth';

describe('useAuthStore', () => {
  beforeEach(() => {
    localStorage.clear();
    useAuthStore.setState({ key: null, ready: true });
    vi.restoreAllMocks();
  });

  it('логинит и сохраняет ключ в localStorage', async () => {
    // мокаем fetch (POST /admin/login) — нет реального сервера
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue({ ok: true } as Response),
    );
    await useAuthStore.getState().login('supersecret-admin-key');
    expect(useAuthStore.getState().key).toBe('supersecret-admin-key');
    expect(localStorage.getItem('spider.admin_key')).toBe('supersecret-admin-key');
  });

  it('logout очищает ключ', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue({ ok: true } as Response),
    );
    await useAuthStore.getState().login('key-123');
    useAuthStore.getState().logout();
    expect(useAuthStore.getState().key).toBeNull();
    expect(localStorage.getItem('spider.admin_key')).toBeNull();
  });

  it('login переживает ошибку сети (ключ всё равно сохраняется)', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockRejectedValue(new Error('network')),
    );
    await useAuthStore.getState().login('offline-key');
    expect(useAuthStore.getState().key).toBe('offline-key');
    expect(localStorage.getItem('spider.admin_key')).toBe('offline-key');
  });
});
