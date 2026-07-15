/**
 * API-клиент панели. Все вызовы идут под Bearer ADMIN_KEY, который хранится
 * в zustand auth-сторе (localStorage). Единая обёртка fetch с типобезопасным
 * парсингом через zod.
 */
import { z } from 'zod';
import {
  AuditListSchema,
  CommandListSchema,
  CreatedEnrollmentSchema,
  DeviceListSchema,
  DeviceSchema,
  CommandSchema,
  EnrollmentListSchema,
  SetCommandsEnabledSchema,
  type AuditEntry,
  type Command,
  type CreatedEnrollment,
  type Device,
  type Enrollment,
  type EnqueueCommandInput,
} from '@/schemas';

const STORAGE_KEY = 'spider.admin_key';

/** Получить admin-ключ из localStorage. */
export function getAdminKey(): string | null {
  return localStorage.getItem(STORAGE_KEY);
}

/** Сохранить admin-ключ. */
export function setAdminKey(key: string): void {
  localStorage.setItem(STORAGE_KEY, key);
}

/** Очистить admin-ключ (logout). */
export function clearAdminKey(): void {
  localStorage.removeItem(STORAGE_KEY);
}

/** Ошибка API с HTTP-статусом. */
export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message);
    this.name = 'ApiError';
  }
}

/** Базовый запрос с авторизацией и zod-валидацией ответа. */
async function request<T>(
  path: string,
  schema: { parse: (v: unknown) => T },
  init?: RequestInit,
): Promise<T> {
  const key = getAdminKey();
  if (!key) throw new ApiError(401, 'admin key не задан');
  const resp = await fetch(path, {
    ...init,
    headers: {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${key}`,
      ...(init?.headers ?? {}),
    },
  });
  if (!resp.ok) {
    let msg = `HTTP ${resp.status}`;
    try {
      const body = await resp.json();
      msg = body.error ?? msg;
    } catch {
      /* не JSON */
    }
    throw new ApiError(resp.status, msg);
  }
  const data = await resp.json();
  return schema.parse(data);
}

// --- Devices ---

/** GET /admin/devices. */
export async function listDevices(): Promise<Device[]> {
  const r = await request('/admin/devices', DeviceListSchema);
  return r.devices;
}

/** GET /admin/devices/:id. */
export async function getDevice(id: string): Promise<Device> {
  return request(`/admin/devices/${id}`, DeviceSchema);
}

/** DELETE /admin/devices/:id. */
export async function deleteDevice(id: string): Promise<void> {
  await request(`/admin/devices/${id}`, zLiteral('deleted'), { method: 'DELETE' });
}

/** PATCH /admin/devices/:id — переименование. */
export async function renameDevice(id: string, name: string): Promise<Device> {
  return request(`/admin/devices/${id}`, DeviceSchema, {
    method: 'PATCH',
    body: JSON.stringify({ name }),
  });
}

// --- Commands ---

/** POST /admin/devices/:id/commands. */
export async function enqueueCommand(
  deviceId: string,
  input: EnqueueCommandInput,
): Promise<{ command: Command; delivered: boolean }> {
  return request(`/admin/devices/${deviceId}/commands`, zCommandAck, {
    method: 'POST',
    body: JSON.stringify(input),
  });
}

/** GET /admin/devices/:id/commands. */
export async function listCommands(deviceId: string): Promise<Command[]> {
  const r = await request(`/admin/devices/${deviceId}/commands`, CommandListSchema);
  return r.commands;
}

/** GET /admin/commands/:id. */
export async function getCommand(id: string): Promise<{ command: Command; result?: unknown }> {
  return request(`/admin/commands/${id}`, z.any());
}

// --- Enrollments ---

/** POST /admin/enrollments. */
export async function createEnrollment(note = ''): Promise<CreatedEnrollment> {
  return request('/admin/enrollments', CreatedEnrollmentSchema, {
    method: 'POST',
    body: JSON.stringify({ note }),
  });
}

/** GET /admin/enrollments. */
export async function listEnrollments(): Promise<Enrollment[]> {
  const r = await request('/admin/enrollments', EnrollmentListSchema);
  return r.enrollments;
}

/** DELETE /admin/enrollments/:token. */
export async function deleteEnrollment(token: string): Promise<void> {
  await request(`/admin/enrollments/${token}`, zLiteral('deleted'), { method: 'DELETE' });
}

// --- Settings / Audit ---

/** GET /admin/settings/commands. */
export async function getCommandsEnabled(): Promise<boolean> {
  const r = await request('/admin/settings/commands', SetCommandsEnabledSchema);
  return r.enabled;
}

/** PUT /admin/settings/commands. */
export async function setCommandsEnabled(enabled: boolean): Promise<boolean> {
  const r = await request('/admin/settings/commands', SetCommandsEnabledSchema, {
    method: 'PUT',
    body: JSON.stringify({ enabled }),
  });
  return r.enabled;
}

/** GET /admin/audit. */
export async function listAudit(): Promise<AuditEntry[]> {
  const r = await request('/admin/audit', AuditListSchema);
  return r.audit;
}

// --- внутренние zod-хелперы ---

const zLiteral = (v: string) =>
  z.object({ status: z.literal(v) });
const zCommandAck = z.object({
  command: CommandSchema,
  delivered: z.boolean(),
});
