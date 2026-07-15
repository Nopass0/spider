/**
 * Общие утилиты панели. DRY-хелперы для классов, форматирования и т.д.
 */
import { type ClassValue, clsx } from 'clsx';
import { twMerge } from 'tailwind-merge';

/**
 * Объединить tailwind-классы с разрешением конфликтов (cn из shadcn).
 */
export function cn(...inputs: ClassValue[]): string {
  return twMerge(clsx(inputs));
}

/**
 * Декодировать base64-строку в человекочитаемый текст (для вывода команд).
 * Падает gracefully на бинарные данные.
 */
export function decodeB64(b64: string): string {
  try {
    const bin = atob(b64);
    // проверяем, похоже ли на UTF-8 текст
    return new TextDecoder('utf-8', { fatal: false }).decode(
      Uint8Array.from(bin, (c) => c.charCodeAt(0)),
    );
  } catch {
    return b64;
  }
}

/**
 * Форматировать байты в человекочитаемый вид.
 */
export function formatBytes(bytes: number): string {
  if (bytes === 0) return '0 B';
  const k = 1024;
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  return `${(bytes / Math.pow(k, i)).toFixed(1)} ${units[i]}`;
}

/**
 * Форматировать Unix-время (секунды) или ISO-строку в локальное время.
 */
export function formatTime(value: string | number | undefined | null): string {
  if (!value) return '—';
  const date =
    typeof value === 'number' ? new Date(value * 1000) : new Date(value);
  if (Number.isNaN(date.getTime())) return '—';
  return date.toLocaleString('ru-RU', {
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    day: '2-digit',
    month: '2-digit',
    year: 'numeric',
  });
}

/**
 * Относительное время «был в сети N назад».
 */
export function timeAgo(value: string | number | undefined | null): string {
  if (!value) return 'никогда';
  const date =
    typeof value === 'number' ? new Date(value * 1000) : new Date(value);
  const diff = Date.now() - date.getTime();
  const sec = Math.floor(diff / 1000);
  if (sec < 60) return `${sec}с назад`;
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min}мин назад`;
  const hr = Math.floor(min / 60);
  if (hr < 24) return `${hr}ч назад`;
  const day = Math.floor(hr / 24);
  return `${day}д назад`;
}
