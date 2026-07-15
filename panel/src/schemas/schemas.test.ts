/**
 * Тесты zod-схем: валидация/парсинг API-ответов.
 */
import { describe, it, expect } from 'vitest';
import {
  DeviceSchema,
  CommandSchema,
  CommandStatusSchema,
  CreatedEnrollmentSchema,
  EnqueueCommandSchema,
} from '@/schemas';

describe('DeviceSchema', () => {
  it('парсит полное устройство', () => {
    const d = DeviceSchema.parse({
      device_id: 'dev-1',
      first_seen: '2024-01-01T00:00:00Z',
      last_seen: '2024-01-02T00:00:00Z',
      online: true,
    });
    expect(d.device_id).toBe('dev-1');
    expect(d.online).toBe(true);
    expect(d.cpu_cores).toBe(0);
  });

  it('заполняет дефолты', () => {
    const d = DeviceSchema.parse({
      device_id: 'd',
      first_seen: '',
      last_seen: '',
    });
    expect(d.hostname).toBe('');
    expect(d.online).toBe(false);
  });
});

describe('CommandSchema', () => {
  it('парсит команду с результатом', () => {
    const c = CommandSchema.parse({
      id: 'c1',
      device_id: 'd',
      command: 'ls',
      status: 'done',
      created_at: '2024-01-01T00:00:00Z',
      has_result: true,
      result: {
        command_id: 'c1',
        exit_code: 0,
        stdout_b64: 'aGk=',
        finished_at: '2024-01-01T00:00:01Z',
      },
    });
    expect(c.result?.exit_code).toBe(0);
  });
});

describe('CommandStatusSchema', () => {
  it('принимает валидные статусы', () => {
    expect(CommandStatusSchema.parse('queued')).toBe('queued');
    expect(CommandStatusSchema.parse('done')).toBe('done');
  });

  it('отвергает неизвестный статус', () => {
    expect(() => CommandStatusSchema.parse('weird')).toThrow();
  });
});

describe('EnqueueCommandSchema', () => {
  it('требует непустую команду', () => {
    expect(() => EnqueueCommandSchema.parse({ command: '' })).toThrow();
  });

  it('ставит дефолтный timeout', () => {
    const r = EnqueueCommandSchema.parse({ command: 'echo' });
    expect(r.timeout_sec).toBe(60);
  });
});

describe('CreatedEnrollmentSchema', () => {
  it('парсит ответ создания токена', () => {
    const r = CreatedEnrollmentSchema.parse({
      token: 'tok',
      key: 'k',
      server_pub: 'pub',
      expires_at: '2024-01-01',
    });
    expect(r.token).toBe('tok');
  });
});
