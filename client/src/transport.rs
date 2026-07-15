//! Транспортный слой агента: WebSocket (основной) с авто-fallback на long-poll.
//!
//! Дизайн: ключ сессии хранится в [`SharedKey`] (Arc<[u8;32]>), и каждая задача
//! создаёт короткоживущую [`Session`] по необходимости — это обходит то, что
//! Aes256Gcm не реализует Clone. Канал результатов (`Envelope`) используется для
//! доставки outcome-ов обратно в писатель транспортного соединения.

use std::sync::Arc;
use std::time::Duration;

use anyhow::{Context, Result};
use futures_util::{SinkExt, StreamExt};
use tokio::sync::mpsc;
use tokio_tungstenite::tungstenite::Message;
use tracing::{info, warn};

use crate::crypto::{Envelope, Session, KEY_SIZE};
use crate::executor;
use crate::proto::{self, ServerInfo, WireCommand, WireResult};

/// Разделяемый ключ сессии — из него задачи создают Session по мере надобности.
#[derive(Clone)]
pub struct SharedKey(Arc<[u8; KEY_SIZE]>);

impl SharedKey {
    pub fn new(key: [u8; KEY_SIZE]) -> Self {
        Self(Arc::new(key))
    }
    /// Создать Session из ключа.
    pub fn session(&self) -> Result<Session> {
        Session::new(&self.0[..])
    }
}

/// События, которые агент возвращает наверх (для main/логирования).
#[derive(Debug)]
pub enum AgentEvent {
    CommandReceived(String),
    CommandDone { id: String, exit_code: i32 },
    ServerInfo(ServerInfo),
    TransportSwitched(&'static str),
}

/// Превратить WireResult в событие для логирования.
fn result_to_event(res: &WireResult) -> Result<AgentEvent> {
    Ok(AgentEvent::CommandDone {
        id: res.command_id.clone(),
        exit_code: res.exit_code,
    })
}

/// Главный цикл агента. Блокирует до критической ошибки.
pub async fn run(
    server: &str,
    device_id: &str,
    key: SharedKey,
    event_tx: mpsc::UnboundedSender<AgentEvent>,
) -> Result<()> {
    // Канал результатов: в WS режиме оборачиваются в Envelope, в long-poll
    // отправляются как есть (сервер ожидает WireResult).
    let (result_tx, mut result_rx) = mpsc::unbounded_channel::<WireResult>();

    loop {
        match ws_run(server, device_id, &key, &event_tx, &result_tx, &mut result_rx).await {
            Ok(()) => return Ok(()),
            Err(e) => {
                warn!("WebSocket упал: {e}; переключаемся на long-poll");
                let _ = event_tx.send(AgentEvent::TransportSwitched("long-poll"));
                if let Err(e) =
                    longpoll_run(server, device_id, &key, &event_tx, &result_tx, &mut result_rx).await
                {
                    warn!("long-poll ошибка: {e}; переподключение через 5с");
                    tokio::time::sleep(Duration::from_secs(5)).await;
                }
            }
        }
    }
}

/// Обработать одну команду: выполнить и вернуть результат (без шифрования —
/// шифрует WS-транспорт; long-poll отправляет как есть).
pub async fn handle_command(key: &SharedKey, env: &Envelope) -> Option<WireResult> {
    if env.ty != proto::msg::COMMAND {
        return None;
    }
    let session = match key.session() {
        Ok(s) => s,
        Err(e) => {
            warn!("session create: {e}");
            return None;
        }
    };
    let cmd: WireCommand = match env.open::<WireCommand>(&session) {
        Ok(c) => c,
        Err(e) => {
            warn!("не удалось расшифровать команду: {e}");
            return None;
        }
    };
    info!(id = %cmd.id, "получена команда: {}", cmd.command);
    let outcome = match executor::execute(&cmd.command, cmd.timeout_sec).await {
        Ok(o) => o,
        Err(e) => {
            return Some(WireResult {
                command_id: cmd.id.clone(),
                exit_code: -1,
                stdout_b64: String::new(),
                stderr_b64: String::new(),
                duration_ms: 0,
                error: format!("executor error: {e}"),
            });
        }
    };
    Some(outcome.to_wire(&cmd.id))
}

// ---------------------------------------------------------------------------
// WebSocket transport
// ---------------------------------------------------------------------------

async fn ws_run(
    server: &str,
    device_id: &str,
    key: &SharedKey,
    event_tx: &mpsc::UnboundedSender<AgentEvent>,
    result_tx: &mpsc::UnboundedSender<WireResult>,
    result_rx: &mut mpsc::UnboundedReceiver<WireResult>,
) -> Result<()> {
    let ws_url = ws_url(server, device_id);
    info!("подключение WS: {ws_url}");

    let (mut ws, _) = tokio_tungstenite::connect_async(&ws_url)
        .await
        .with_context(|| format!("ws connect {ws_url}"))?;
    let _ = event_tx.send(AgentEvent::TransportSwitched("websocket"));

    let mut hb = tokio::time::interval(Duration::from_secs(60));
    hb.tick().await;
    loop {
        tokio::select! {
            msg = ws.next() => {
                let Some(msg) = msg else { return Err(anyhow::anyhow!("ws stream closed")); };
                let msg = msg.context("ws read")?;
                match msg {
                    Message::Text(text) => {
                        match handle_text(key, &text, event_tx, result_tx).await {
                            Ok(Some(pong)) => {
                                let txt = serde_json::to_string(&pong)?;
                                let _ = ws.send(Message::Text(txt)).await;
                            }
                            Ok(None) => {}
                            Err(e) => warn!("обработка ws-сообщения: {e}"),
                        }
                    }
                    Message::Binary(b) => {
                        let text = String::from_utf8_lossy(&b).into_owned();
                        match handle_text(key, &text, event_tx, result_tx).await {
                            Ok(Some(pong)) => {
                                let txt = serde_json::to_string(&pong)?;
                                let _ = ws.send(Message::Text(txt)).await;
                            }
                            Ok(None) => {}
                            Err(e) => warn!("обработка ws-binary: {e}"),
                        }
                    }
                    Message::Ping(p) => { let _ = ws.send(Message::Pong(p)).await; }
                    Message::Close(_) => return Err(anyhow::anyhow!("ws closed by peer")),
                    _ => {}
                }
            }
            _ = hb.tick() => {
                if let Ok(session) = key.session() {
                    if let Ok(env) = Envelope::seal(&session, proto::msg::HEARTBEAT, &crate::proto::SystemInfo::default()) {
                        if let Ok(txt) = serde_json::to_string(&env) {
                            let _ = ws.send(Message::Text(txt)).await;
                        }
                    }
                }
            }
            Some(res) = result_rx.recv() => {
                // Шифруем результат перед отправкой по WS.
                if let Ok(session) = key.session() {
                    if let Ok(env) = Envelope::seal(&session, proto::msg::COMMAND_RESULT, &res) {
                        if let Ok(txt) = serde_json::to_string(&env) {
                            if ws.send(Message::Text(txt)).await.is_err() {
                                return Err(anyhow::anyhow!("ws result send failed"));
                            }
                            if let Ok(ev) = result_to_event(&res) { let _ = event_tx.send(ev); }
                        }
                    }
                }
            }
        }
    }
}

/// Разобрать и обработать текстовое ws-сообщение (envelope).
/// Возвращает optional raw-сообщение для немедленной отправки в WS (pong).
async fn handle_text(
    key: &SharedKey,
    text: &str,
    event_tx: &mpsc::UnboundedSender<AgentEvent>,
    result_tx: &mpsc::UnboundedSender<WireResult>,
) -> Result<Option<Envelope>> {
    let env: Envelope = serde_json::from_str(text).context("decode envelope")?;
    let session = key.session()?;
    match env.ty.as_str() {
        proto::msg::COMMAND => {
            if let Ok(cmd) = env.open::<WireCommand>(&session) {
                let _ = event_tx.send(AgentEvent::CommandReceived(cmd.command.clone()));
                let key = key.clone();
                let env = env.clone();
                let tx = result_tx.clone();
                tokio::spawn(async move {
                    if let Some(res) = handle_command(&key, &env).await {
                        let _ = tx.send(res);
                    }
                });
            }
        }
        proto::msg::SERVER_INFO => {
            if let Ok(info) = env.open::<ServerInfo>(&session) {
                let _ = event_tx.send(AgentEvent::ServerInfo(info));
            }
        }
        proto::msg::PING => {
            return Ok(Some(
                Envelope::seal(&session, proto::msg::PONG, &serde_json::json!({}))?,
            ));
        }
        _ => {}
    }
    Ok(None)
}

// ---------------------------------------------------------------------------
// Long-poll transport
// ---------------------------------------------------------------------------

async fn longpoll_run(
    server: &str,
    device_id: &str,
    key: &SharedKey,
    event_tx: &mpsc::UnboundedSender<AgentEvent>,
    result_tx: &mpsc::UnboundedSender<WireResult>,
    result_rx: &mut mpsc::UnboundedReceiver<WireResult>,
) -> Result<()> {
    let base = server.trim_end_matches('/').to_string();
    let url = format!("{base}/agent/connect?device_id={device_id}");
    let client = reqwest::Client::builder()
        .timeout(Duration::from_secs(120))
        .build()?;
    loop {
        // сначала отправляем накопленные результаты
        let pending = drain_results(result_rx).await;
        if !pending.is_empty() {
            let body = serde_json::json!({ "device_id": device_id, "results": pending });
            let post_url = format!("{base}/agent/connect");
            if let Err(e) = client.post(&post_url).json(&body).send().await {
                warn!("отправка результатов: {e}");
            }
        }
        let resp = client.get(&url).send().await.context("long-poll request")?;
        if !resp.status().is_success() {
            return Err(anyhow::anyhow!("long-poll HTTP {}", resp.status()));
        }
        let out: proto::LongPollOut = resp.json().await.context("long-poll decode")?;
        if let Some(info) = out.info {
            let _ = event_tx.send(AgentEvent::ServerInfo(info));
        }
        for env in out.commands {
            if env.ty == proto::msg::COMMAND {
                if let Ok(session) = key.session() {
                    if let Ok(cmd) = env.open::<WireCommand>(&session) {
                        let _ = event_tx.send(AgentEvent::CommandReceived(cmd.command.clone()));
                    }
                }
                let key = key.clone();
                let tx = result_tx.clone();
                tokio::spawn(async move {
                    if let Some(res) = handle_command(&key, &env).await {
                        let _ = tx.send(res);
                    }
                });
            }
        }
    }
}

/// Собрать накопленные результаты из канала без блокировки.
async fn drain_results(rx: &mut mpsc::UnboundedReceiver<WireResult>) -> Vec<WireResult> {
    let mut out = Vec::new();
    while let Ok(res) = rx.try_recv() {
        out.push(res);
    }
    out
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

/// Преобразовать server URL в ws(s) URL агента.
fn ws_url(server: &str, device_id: &str) -> String {
    let (scheme, host) = if let Some(h) = server.strip_prefix("https://") {
        ("wss://", h)
    } else if let Some(h) = server.strip_prefix("http://") {
        ("ws://", h)
    } else {
        ("wss://", server)
    };
    format!("{scheme}{host}/agent/ws?device_id={device_id}")
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn ws_url_https_to_wss() {
        assert_eq!(
            ws_url("https://spider.lowkey.su", "dev1"),
            "wss://spider.lowkey.su/agent/ws?device_id=dev1"
        );
    }

    #[test]
    fn ws_url_http_to_ws() {
        assert_eq!(
            ws_url("http://localhost:8080", "dev1"),
            "ws://localhost:8080/agent/ws?device_id=dev1"
        );
    }

    #[test]
    fn shared_key_clones_and_creates_session() {
        let k = SharedKey::new([7u8; KEY_SIZE]);
        let k2 = k.clone();
        let s1 = k.session().unwrap();
        let s2 = k2.session().unwrap();
        let ct = s1.encrypt(b"x").unwrap();
        assert_eq!(s2.decrypt(&ct).unwrap(), b"x");
    }
}
