/**
 * Тесты утилит: форматирование, base64, относительное время.
 */
import { describe, it, expect } from 'vitest';
import { cn, decodeB64, formatBytes, formatTime, timeAgo } from '@/lib/utils';

describe('cn', () => {
  it('объединяет классы', () => {
    expect(cn('a', 'b')).toBe('a b');
  });

  it('разрешает конфликты tailwind', () => {
    expect(cn('p-2', 'p-4')).toBe('p-4');
  });
});

describe('decodeB64', () => {
  it('декодирует текст', () => {
    expect(decodeB64(btoa('hello'))).toBe('hello');
  });

  it('возвращает исходную строку при ошибке', () => {
    expect(decodeB64('!!invalid')).toBe('!!invalid');
  });
});

describe('formatBytes', () => {
  it('форматирует значения', () => {
    expect(formatBytes(0)).toBe('0 B');
    expect(formatBytes(1024)).toBe('1.0 KB');
    expect(formatBytes(1048576)).toBe('1.0 MB');
  });
});

describe('formatTime', () => {
  it('форматирует ISO-строку', () => {
    const s = formatTime('2024-01-15T10:30:00Z');
    expect(s).not.toBe('—');
    expect(s.length).toBeGreaterThan(5);
  });

  it('возвращает тире для пустого значения', () => {
    expect(formatTime(undefined)).toBe('—');
    expect(formatTime(null)).toBe('—');
  });
});

describe('timeAgo', () => {
  it('возвращает «никогда» для пустого', () => {
    expect(timeAgo(null)).toBe('никогда');
  });

  it('возвращает относительное для недавнего', () => {
    const recent = new Date(Date.now() - 30000).toISOString();
    expect(timeAgo(recent)).toMatch(/с назад/);
  });
});
