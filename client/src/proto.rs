//! Типы сообщений протокола Spider. Зеркало серверных wire-структур
//! (`server/internal/commands`). Должны быть сериализационно совместимы.

use serde::{Deserialize, Serialize};

/// Типы сообщений (значения поля `type` конверта).
pub mod msg {
    pub const ENROLL_REQUEST: &str = "enroll.request";
    pub const ENROLL_RESPONSE: &str = "enroll.response";
    pub const COMMAND: &str = "command";
    pub const COMMAND_ACK: &str = "command.ack";
    pub const COMMAND_RESULT: &str = "command.result";
    pub const HEARTBEAT: &str = "heartbeat";
    pub const SERVER_INFO: &str = "server.info";
    pub const PING: &str = "ping";
    pub const PONG: &str = "pong";
}

/// Команда сервер → агент.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct WireCommand {
    pub id: String,
    pub command: String,
    #[serde(default = "default_timeout")]
    pub timeout_sec: u32,
}

fn default_timeout() -> u32 {
    60
}

/// Результат агент → сервер.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct WireResult {
    pub command_id: String,
    pub exit_code: i32,
    pub stdout_b64: String,
    pub stderr_b64: String,
    pub duration_ms: i64,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub error: String,
}

/// Системная информация (heartbeat / enroll).
#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct SystemInfo {
    pub hostname: String,
    pub os: String,
    pub arch: String,
    pub cpu_brand: String,
    pub cpu_cores: i32,
    pub mem_total: u64,
    pub agent_version: String,
}

/// Информация от сервера (тумблер команд).
#[derive(Debug, Clone, Deserialize)]
pub struct ServerInfo {
    pub commands_enabled: bool,
}

/// Ответ эндпоинта enroll.
#[derive(Debug, Clone, Deserialize)]
pub struct EnrollResponse {
    pub device_id: String,
    #[serde(rename = "key")]
    pub key_b64: String,
}

/// Тело запроса enroll.
#[derive(Debug, Clone, Serialize)]
pub struct EnrollRequest {
    pub token: String,
    pub public_key: String,
    pub system: SystemInfo,
    pub agent_version: String,
}

/// Batch-ответ long-poll.
#[derive(Debug, Clone, Deserialize)]
pub struct LongPollOut {
    #[serde(default)]
    pub commands: Vec<crate::crypto::Envelope>,
    pub info: Option<ServerInfo>,
}
