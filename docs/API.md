# API Spider

Все admin-эндпоинты требуют заголовок `Authorization: Bearer <ADMIN_KEY>`.
Agent-эндпоинты авторизуются по `device_id` + ключу сессии (крипто-проверка).

## Health

### `GET /healthz`
Проверка живости. Без авторизации.
```json
{"status":"ok"}
```

## Admin — Devices

### `GET /admin/devices`
Список всех устройств (без ключей сессии).
```json
{"devices":[{"device_id":"...","hostname":"PC","online":true, ...}]}
```

### `GET /admin/devices/:id`
Детали одного устройства.

### `PATCH /admin/devices/:id`
Переименование.
```json
{"name":"офис-ПК-3"}
```

### `DELETE /admin/devices/:id`
Удаление устройства каскадно (с командами и результатами).

## Admin — Commands

### `POST /admin/devices/:id/commands`
Постановка команды в очередь.
```json
{"command":"uname -a","timeout_sec":60}
```
Ответ `202`:
```json
{"command":{"id":"...","status":"queued", ...},"delivered":true}
```
`delivered=true` — команда отправлена мгновенно по WS. `false` — осталась в
очереди, агент заберёт при следующем подключении.

### `GET /admin/devices/:id/commands`
История команд устройства (с результатами).

### `GET /admin/commands/:id`
Одна команда + результат (если есть).

## Admin — Enrollments

### `POST /admin/enrollments`
Создание одноразового токена. Тело:
```json
{"note":"офис-ПК-3"}
```
Ответ `201` (секреты показываются **один раз**):
```json
{
  "token":"e618...",
  "key":"xKn8...=",
  "server_pub":"1HIy...=",
  "expires_at":"2026-07-16T11:42:30Z",
  "note":"офис-ПК-3"
}
```

### `GET /admin/enrollments`
Листинг токенов (без секретов: `key` скрыт).

### `DELETE /admin/enrollments/:token`
Удаление токена.

## Admin — Settings & Audit

### `GET /admin/settings/commands`
```json
{"commands_enabled":true}
```

### `PUT /admin/settings/commands`
Глобальный тумблер диспетчеризации.
```json
{"enabled":false}
```

### `GET /admin/audit`
Журнал действий администратора.

### `GET /admin/info`
Базовая инфа о сервере (online-счётчик, время).

### `GET /admin/events` (WebSocket)
Live-стрим событий панели. Авторизация: query `?token=<ADMIN_KEY>`
(WS из браузера не шлёт кастомные заголовки). События:
```json
{"type":"device.online","device_id":"..."}
{"type":"command.result","device_id":"...","payload":{...}}
{"type":"commands.toggle","payload":false}
```

## Agent endpoints

### `POST /agent/enroll`
Регистрация по токену.
```json
{"token":"...","public_key":"...","system":{...},"agent_version":"0.1.0"}
```
Ответ:
```json
{"device_id":"...","key":"<base64 ключ сессии>"}
```

### `GET /agent/ws?device_id=...`
WebSocket: двунаправленный шифрованный канал (команды/результаты/heartbeat).

### `GET /agent/connect?device_id=...`
Long-poll: держит соединение до появления команд, отдаёт batch.
`POST /agent/connect` — отправка batch результатов.

## Ошибки

Все ошибки в формате:
```json
{"error":"сообщение"}
```
HTTP-коды: `400` (bad request), `401` (no auth), `403` (disabled), `404`
(not found), `409` (conflict), `410` (expired), `500` (server).
