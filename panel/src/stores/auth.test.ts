/**
 * Тесты auth-стора.
 */
import { describe, it, expect, beforeEach } from 'vitest';
import { useAuthStore } from './auth';

describe('useAuthStore', () => {
  beforeEach(() => {
    localStorage.clear();
    useAuthStore.setState({ key: null, ready: true });
  });

  it('логинит и сохраняет ключ в localStorage', () => {
    useAuthStore.getState().login('supersecret-admin-key');
    expect(useAuthStore.getState().key).toBe('supersecret-admin-key');
    expect(localStorage.getItem('spider.admin_key')).toBe('supersecret-admin-key');
  });

  it('logout очищает ключ', () => {
    useAuthStore.getState().login('key-123');
    useAuthStore.getState().logout();
    expect(useAuthStore.getState().key).toBeNull();
    expect(localStorage.getItem('spider.admin_key')).toBeNull();
  });
});
