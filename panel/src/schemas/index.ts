/**
 * Zod-схемы API-ответов сервера. Единый источник истины для типов и валидации.
 * Совместимы с серверными моделями (server/internal/store/models.go).
 */
import { z } from 'zod';

/** Статус команды. */
export const CommandStatusSchema = z.enum([
  'queued',
  'running',
  'done',
  'error',
  'timeout',
  'cancelled',
]);
export type CommandStatus = z.infer<typeof CommandStatusSchema>;

/** Устройство. */
export const DeviceSchema = z.object({
  device_id: z.string(),
  name: z.string().default(''),
  hostname: z.string().default(''),
  os: z.string().default(''),
  arch: z.string().default(''),
  cpu_brand: z.string().default(''),
  cpu_cores: z.number().default(0),
  mem_total: z.number().default(0),
  agent_version: z.string().default(''),
  first_seen: z.string(),
  last_seen: z.string(),
  online: z.boolean().default(false),
});
export type Device = z.infer<typeof DeviceSchema>;

/** Ответ с результатом команды (опциональный). */
export const ResultSchema = z.object({
  command_id: z.string(),
  exit_code: z.number(),
  stdout_b64: z.string().default(''),
  stderr_b64: z.string().default(''),
  finished_at: z.string(),
  duration_ms: z.number().default(0),
});
export type Result = z.infer<typeof ResultSchema>;

/** Команда (с возможным результатом). */
export const CommandSchema = z.object({
  id: z.string(),
  device_id: z.string(),
  command: z.string(),
  timeout_sec: z.number().default(60),
  status: CommandStatusSchema,
  created_at: z.string(),
  dispatched_at: z.string().nullable().optional(),
  finished_at: z.string().nullable().optional(),
  created_by: z.string().default('admin'),
  result: ResultSchema.optional(),
  has_result: z.boolean().optional(),
});
export type Command = z.infer<typeof CommandSchema>;

/** Enrollment-токен. */
export const EnrollmentSchema = z.object({
  token: z.string(),
  note: z.string().default(''),
  pub_b64: z.string().default(''),
  created_at: z.string(),
  expires_at: z.string(),
  used_at: z.string().nullable().optional(),
  used_by: z.string().optional(),
});
export type Enrollment = z.infer<typeof EnrollmentSchema>;

/** Ответ создания enrollment (с секретами — показать один раз). */
export const CreatedEnrollmentSchema = z.object({
  token: z.string(),
  key: z.string(),
  server_pub: z.string(),
  expires_at: z.string(),
  note: z.string().default(''),
});
export type CreatedEnrollment = z.infer<typeof CreatedEnrollmentSchema>;

/** Запись аудита. */
export const AuditEntrySchema = z.object({
  id: z.number(),
  actor: z.string(),
  action: z.string(),
  target: z.string().default(''),
  detail: z.string().default(''),
  at: z.number(),
});
export type AuditEntry = z.infer<typeof AuditEntrySchema>;

/**
 * Обёртки-ответы list-эндпоинтов. Поля nullable: если сервер вернёт null
 * вместо массива (пустой список в Go без make([])), приводим к [].
 */
const arrayOrEmpty = <T extends z.ZodTypeAny>(el: T) =>
  z.union([z.array(el), z.null(), z.undefined()]).transform((v) => v ?? []);

export const DeviceListSchema = z.object({ devices: arrayOrEmpty(DeviceSchema) });
export const EnrollmentListSchema = z.object({
  enrollments: arrayOrEmpty(EnrollmentSchema),
});
export const CommandListSchema = z.object({ commands: arrayOrEmpty(CommandSchema) });
export const AuditListSchema = z.object({ audit: arrayOrEmpty(AuditEntrySchema) });

/** Входные схемы для запросов. */
export const EnqueueCommandSchema = z.object({
  command: z.string().min(1, 'Команда не может быть пустой'),
  timeout_sec: z.number().int().min(1).max(3600).default(60),
});
export type EnqueueCommandInput = z.infer<typeof EnqueueCommandSchema>;

export const CreateEnrollmentSchema = z.object({
  note: z.string().max(200).default(''),
});

export const SetCommandsEnabledSchema = z.object({
  enabled: z.boolean(),
});
