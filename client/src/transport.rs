//! Транспортный слой агента: WebSocket (основной) с авто-fallback на long-poll.
//!
//! Дизайн: ключ сессии хранится в [`SharedKey`] (Arc<[u8;32]>), и каждая задача
//! создаёт короткоживущую [`Session`] по необходимости — это обходит то, что
//! Aes256Gcm не реализует Clone. Канал [`Outbound`] доставляет все исходящие
//! сообщения (команды, PTY-вывод, кадры экрана) в писатель транспорта.

use std::sync::Arc;
use std::time::Duration;

use anyhow::{Context, Result};
use futures_util::{SinkExt, StreamExt};
use tokio::sync::mpsc;
use tokio_tungstenite::tungstenite::Message;
use tracing::{info, warn};

use crate::crypto::{Envelope, Session, KEY_SIZE};
use crate::executor;
use crate::proto::{
    self, ServerInfo, WireCommand, WireResult, WireScreenFrame, WireScreenshotDone,
    WireTerminalExit, WireTerminalOutput,
};
use crate::pty::PtyManager;
#[cfg(feature = "screen")]
use crate::screen::ScreenManager;

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
    TerminalOpened { session_id: String },
    TerminalClosed { session_id: String, exit_code: i32 },
    #[cfg(feature = "screen")]
    ScreenStarted { session_id: String, fps: u32 },
    #[cfg(feature = "screen")]
    ScreenStopped { session_id: String },
}

/// Все исходящие сообщения агента → сервер. Канал этого типа доставляет
/// результаты команд, PTY-вывод, кадры экрана и скриншоты в писатель транспорта.
#[derive(Debug, Clone)]
pub enum Outbound {
    /// Финальный результат команды (WS шифрует, long-poll шлёт как WireResult).
    CommandResult(WireResult),
    /// Чанк вывода PTY (шифруется в Envelope).
    TerminalOutput(WireTerminalOutput),
    /// PTY завершён.
    TerminalExit(WireTerminalExit),
    /// JPEG-кадр экрана.
    ScreenFrame(WireScreenFrame),
    /// Скриншот готов.
    ScreenshotDone(WireScreenshotDone),
}

impl Outbound {
    /// Тип wire-сообщения (для envelope).
    fn msg_type(&self) -> &'static str {
        match self {
            Outbound::CommandResult(_) => proto::msg::COMMAND_RESULT,
            Outbound::TerminalOutput(_) => proto::msg::TERMINAL_OUTPUT,
            Outbound::TerminalExit(_) => proto::msg::TERMINAL_EXIT,
            Outbound::ScreenFrame(_) => proto::msg::SCREEN_FRAME,
            Outbound::ScreenshotDone(_) => proto::msg::SCREENSHOT_DONE,
        }
    }
}

/// Главный цикл агента. Блокирует до критической ошибки.
pub async fn run(
    server: &str,
    device_id: &str,
    key: SharedKey,
    event_tx: mpsc::UnboundedSender<AgentEvent>,
) -> Result<()> {
    // Канал всех исходящих сообщений (команды, PTY, экран).
    let (outbound_tx, mut outbound_rx) = mpsc::unbounded_channel::<Outbound>();
    let pty_mgr = Arc::new(PtyManager::new());
    #[cfg(feature = "screen")]
    let screen_mgr = Arc::new(ScreenManager::new());

    loop {
        match ws_run(server, device_id, &key, &event_tx, &outbound_tx, &mut outbound_rx, &pty_mgr,
            #[cfg(feature = "screen")]
            &screen_mgr,
        ).await {
            Ok(()) => return Ok(()),
            Err(e) => {
                warn!("WebSocket упал: {e}; переключаемся на long-poll");
                let _ = event_tx.send(AgentEvent::TransportSwitched("long-poll"));
                if let Err(e) = longpoll_run(server, device_id, &key, &event_tx, &outbound_tx,
                    &mut outbound_rx, &pty_mgr,
                    #[cfg(feature = "screen")]
                    &screen_mgr,
                ).await {
                    warn!("long-poll ошибка: {e}; переподключение через 5с");
                    tokio::time::sleep(Duration::from_secs(5)).await;
                }
            }
        }
    }
}

/// Обработать одну команду: выполнить и вернуть результат (без шифрования —
/// шифрует WS-транспорт; long-poll отправляет как есть).
pub async fn handle_command(key: &SharedKey, env: &Envelope) -> Option<Outbound> {
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
            return Some(Outbound::CommandResult(WireResult {
                command_id: cmd.id.clone(),
                exit_code: -1,
                stdout_b64: String::new(),
                stderr_b64: String::new(),
                duration_ms: 0,
                error: format!("executor error: {e}"),
            }));
        }
    };
    Some(Outbound::CommandResult(outcome.to_wire(&cmd.id)))
}

/// Обработать streaming-сообщение (terminal/screen/screenshot) — расшифровать
/// payload и передать в нужный менеджер.
async fn handle_stream_msg(
    key: &SharedKey,
    raw_env: &Envelope,
    event_tx: &mpsc::UnboundedSender<AgentEvent>,
    outbound_tx: &mpsc::UnboundedSender<Outbound>,
    pty_mgr: &Arc<PtyManager>,
    #[cfg(feature = "screen")] screen_mgr: &Arc<ScreenManager>,
) {
    let session = match key.session() {
        Ok(s) => s,
        Err(e) => {
            warn!("session create (stream): {e}");
            return;
        }
    };
    let ty = raw_env.ty.as_str();
    match ty {
        proto::msg::TERMINAL_OPEN => {
            if let Ok(req) = raw_env.open::<proto::WireTerminalOpen>(&session) {
                let _ = event_tx.send(AgentEvent::TerminalOpened { session_id: req.session_id.clone() });
                let tx = outbound_tx.clone();
                let sid = req.session_id.clone();
                let mgr = pty_mgr.clone();
                // запускаем PTY; вывод стримится в outbound_tx.
                if let Err(e) = mgr.open(&req.session_id, req.cols, req.rows, move |chunk| {
                    let _ = tx.send(Outbound::TerminalOutput(WireTerminalOutput {
                        session_id: sid.clone(),
                        data_b64: chunk,
                    }));
                }).await {
                    warn!("pty open: {e}");
                }
            }
        }
        proto::msg::TERMINAL_INPUT => {
            if let Ok(req) = raw_env.open::<proto::WireTerminalInput>(&session) {
                if let Ok(bytes) = crate::crypto::b64_decode(&req.data_b64) {
                    if let Err(e) = pty_mgr.write(&req.session_id, &bytes).await {
                        warn!("pty write: {e}");
                    }
                }
            }
        }
        proto::msg::TERMINAL_RESIZE => {
            if let Ok(req) = raw_env.open::<proto::WireTerminalResize>(&session) {
                if let Err(e) = pty_mgr.resize(&req.session_id, req.cols, req.rows).await {
                    warn!("pty resize: {e}");
                }
            }
        }
        proto::msg::TERMINAL_CLOSE => {
            if let Ok(req) = raw_env.open::<proto::WireTerminalClose>(&session) {
                let _ = pty_mgr.close(&req.session_id).await;
                let _ = event_tx.send(AgentEvent::TerminalClosed { session_id: req.session_id, exit_code: 0 });
            }
        }
        #[cfg(feature = "screen")]
        proto::msg::SCREEN_START => {
            if let Ok(req) = raw_env.open::<proto::WireScreenStart>(&session) {
                let _ = event_tx.send(AgentEvent::ScreenStarted { session_id: req.session_id.clone(), fps: req.fps });
                let tx = outbound_tx.clone();
                let sid = req.session_id.clone();
                let mgr = screen_mgr.clone();
                if let Err(e) = mgr.start(&req.session_id, req.fps, req.quality.max(0) as u8, move |frame, w, h| {
                    let _ = tx.send(Outbound::ScreenFrame(WireScreenFrame {
                        session_id: sid.clone(), frame_b64: crate::crypto::b64_encode(&frame), w, h,
                    }));
                }).await {
                    warn!("screen start: {e}");
                }
            }
        }
        #[cfg(feature = "screen")]
        proto::msg::SCREEN_STOP => {
            if let Ok(req) = raw_env.open::<proto::WireScreenStop>(&session) {
                let _ = screen_mgr.stop(&req.session_id).await;
                let _ = event_tx.send(AgentEvent::ScreenStopped { session_id: req.session_id });
            }
        }
        #[cfg(feature = "screen")]
        proto::msg::SCREENSHOT_SNAP => {
            if let Ok(req) = raw_env.open::<proto::WireScreenshotSnap>(&session) {
                let tx = outbound_tx.clone();
                let sid = req.session_id.clone();
                tokio::task::spawn_blocking(move || {
                    match crate::screen::ScreenManager::snapshot() {
                        Ok((frame, w, h)) => {
                            let _ = tx.send(Outbound::ScreenshotDone(WireScreenshotDone {
                                session_id: sid, frame_b64: crate::crypto::b64_encode(&frame), w, h,
                            }));
                        }
                        Err(e) => warn!("screenshot: {e}"),
                    }
                });
            }
        }
        _ => {}
    }
}

// ---------------------------------------------------------------------------
// WebSocket transport
// ---------------------------------------------------------------------------

async fn ws_run(
    server: &str,
    device_id: &str,
    key: &SharedKey,
    event_tx: &mpsc::UnboundedSender<AgentEvent>,
    outbound_tx: &mpsc::UnboundedSender<Outbound>,
    outbound_rx: &mut mpsc::UnboundedReceiver<Outbound>,
    pty_mgr: &Arc<PtyManager>,
    #[cfg(feature = "screen")] screen_mgr: &Arc<ScreenManager>,
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
                        match handle_text(key, &text, event_tx, outbound_tx, pty_mgr,
                            #[cfg(feature="screen")] screen_mgr).await {
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
                        match handle_text(key, &text, event_tx, outbound_tx, pty_mgr,
                            #[cfg(feature="screen")] screen_mgr).await {
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
            Some(outbound) = outbound_rx.recv() => {
                // Шифруем исходящее сообщение (command/terminal/screen) и отправляем.
                if let Ok(session) = key.session() {
                    if let Ok(env) = seal_outbound(&session, &outbound) {
                        if let Ok(txt) = serde_json::to_string(&env) {
                            if ws.send(Message::Text(txt)).await.is_err() {
                                return Err(anyhow::anyhow!("ws outbound send failed"));
                            }
                        }
                    }
                    // лог события для CommandResult
                    if let Outbound::CommandResult(res) = &outbound {
                        let _ = event_tx.send(AgentEvent::CommandDone {
                            id: res.command_id.clone(),
                            exit_code: res.exit_code,
                        });
                    }
                }
            }
        }
    }
}

/// Запечатать Outbound в Envelope под сессией.
fn seal_outbound(session: &Session, ob: &Outbound) -> Result<Envelope> {
    match ob {
        Outbound::CommandResult(r) => Envelope::seal(session, proto::msg::COMMAND_RESULT, r),
        Outbound::TerminalOutput(o) => Envelope::seal(session, proto::msg::TERMINAL_OUTPUT, o),
        Outbound::TerminalExit(e) => Envelope::seal(session, proto::msg::TERMINAL_EXIT, e),
        Outbound::ScreenFrame(f) => Envelope::seal(session, proto::msg::SCREEN_FRAME, f),
        Outbound::ScreenshotDone(d) => Envelope::seal(session, proto::msg::SCREENSHOT_DONE, d),
    }
}

/// Разобрать и обработать текстовое ws-сообщение (envelope).
/// Возвращает optional raw-сообщение для немедленной отправки в WS (pong).
async fn handle_text(
    key: &SharedKey,
    text: &str,
    event_tx: &mpsc::UnboundedSender<AgentEvent>,
    outbound_tx: &mpsc::UnboundedSender<Outbound>,
    pty_mgr: &Arc<PtyManager>,
    #[cfg(feature = "screen")] screen_mgr: &Arc<ScreenManager>,
) -> Result<Option<Envelope>> {
    let env: Envelope = serde_json::from_str(text).context("decode envelope")?;
    let session = key.session()?;
    match env.ty.as_str() {
        proto::msg::COMMAND => {
            if let Ok(cmd) = env.open::<WireCommand>(&session) {
                let _ = event_tx.send(AgentEvent::CommandReceived(cmd.command.clone()));
                let key = key.clone();
                let env = env.clone();
                let tx = outbound_tx.clone();
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
        // streaming-сообщения (terminal/screen) — отдельный обработчик.
        ty if ty.starts_with("terminal.")
            || ty.starts_with("screen.")
            || ty.starts_with("screenshot.") =>
        {
            let key = key.clone();
            let env = env.clone();
            let tx = outbound_tx.clone();
            let evt = event_tx.clone();
            let pty = pty_mgr.clone();
            #[cfg(feature = "screen")]
            let scr = screen_mgr.clone();
            tokio::spawn(async move {
                handle_stream_msg(
                    &key, &env, &evt, &tx, &pty,
                    #[cfg(feature = "screen")]
                    &scr,
                ).await;
            });
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
    outbound_tx: &mpsc::UnboundedSender<Outbound>,
    outbound_rx: &mut mpsc::UnboundedReceiver<Outbound>,
    _pty_mgr: &Arc<PtyManager>,
    #[cfg(feature = "screen")] _screen_mgr: &Arc<ScreenManager>,
) -> Result<()> {
    let base = server.trim_end_matches('/').to_string();
    let url = format!("{base}/agent/connect?device_id={device_id}");
    let client = reqwest::Client::builder()
        .timeout(Duration::from_secs(120))
        .build()?;
    loop {
        // Сначала отправляем накопленные результаты команд (только CommandResult;
        // streaming-сообщения в long-poll не поддерживаем — для них нужен WS).
        let pending = drain_results(outbound_rx).await;
        if !pending.is_empty() {
            let results: Vec<WireResult> = pending
                .into_iter()
                .filter_map(|o| match o {
                    Outbound::CommandResult(r) => Some(r),
                    _ => None,
                })
                .collect();
            if !results.is_empty() {
                let body = serde_json::json!({ "device_id": device_id, "results": results });
                let post_url = format!("{base}/agent/connect");
                if let Err(e) = client.post(&post_url).json(&body).send().await {
                    warn!("отправка результатов: {e}");
                }
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
                let tx = outbound_tx.clone();
                tokio::spawn(async move {
                    if let Some(res) = handle_command(&key, &env).await {
                        let _ = tx.send(res);
                    }
                });
            }
        }
    }
}

/// Собрать накопленные исходящие сообщения без блокировки.
async fn drain_results(rx: &mut mpsc::UnboundedReceiver<Outbound>) -> Vec<Outbound> {
    let mut out = Vec::new();
    while let Ok(ob) = rx.try_recv() {
        out.push(ob);
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
