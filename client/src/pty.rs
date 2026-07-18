//! Интерактивный терминал через PTY (псевдотерминал).
//!
//! Использует крейт `portable-pty` — кросс-платформенный (Windows ConPTY,
//! Unix openpty), без внешних библиотек на машине запуска.
//!
//! Сценарий: сервер присылает `terminal.open` → агент создаёт PTY с shell,
//! запускает задачу чтения вывода → стримит чанки обратно через `terminal.output`.
//! Ввод от админа (`terminal.input`) пишется в PTY. При выходе процесса —
//! `terminal.exit` с кодом.

use std::collections::HashMap;
use std::io::Read;
use std::sync::Arc;

use anyhow::{Context, Result};
use portable_pty::{native_pty_system, CommandBuilder, MasterPty, PtySize};
use tokio::sync::Mutex;
use tracing::{info, warn};

/// Обёртка над master PTY: Box<dyn MasterPty + Send> за std::sync::Mutex,
/// чтобы Arc<MasterCell> был Send + Sync и мог клонироваться в async-задачи.
struct MasterCell(std::sync::Mutex<Option<Box<dyn MasterPty + Send>>>);

impl MasterCell {
    fn new(m: Box<dyn MasterPty + Send>) -> Arc<Self> {
        Arc::new(Self(std::sync::Mutex::new(Some(m))))
    }
    fn with_master<R>(&self, f: impl FnOnce(&mut Box<dyn MasterPty + Send>) -> R) -> R {
        let mut guard = self.0.lock().expect("pty master lock");
        f(guard.as_mut().expect("pty master present"))
    }
}

/// Менеджер активных PTY-сессий (по session_id).
pub struct PtyManager {
    sessions: Arc<Mutex<HashMap<String, Arc<MasterCell>>>>,
}

impl PtyManager {
    pub fn new() -> Self {
        Self {
            sessions: Arc::new(Mutex::new(HashMap::new())),
        }
    }

    /// Открыть PTY с системным shell. Вывод стримится в `on_output` (base64 чанки).
    /// Возвращает JoinHandle задачи чтения (чтобы можно было дождаться EOF).
    pub async fn open<F>(
        &self,
        session_id: &str,
        cols: u16,
        rows: u16,
        on_output: F,
    ) -> Result<tokio::task::JoinHandle<()>>
    where
        F: Fn(String) + Send + Sync + 'static,
    {
        let pty_system = native_pty_system();
        let pair = pty_system
            .openpty(PtySize {
                rows,
                cols,
                pixel_width: 0,
                pixel_height: 0,
            })
            .context("openpty")?;

        let mut cmd = default_shell();
        // cwd() возвращает () — без ?, по умолчанию рабочий каталог агента.
        cmd.cwd("/");
        cmd.env("TERM", "xterm-256color");

        let _child = pair
            .slave
            .spawn_command(cmd)
            .context("spawn shell in pty")?;
        drop(pair.slave); // важно: иначе чтение не получит EOF

        // reader/writer берём из master (master остаётся жив в Arc<MasterCell>).
        let master_cell = MasterCell::new(pair.master);
        let reader = master_cell.with_master(|m| m.try_clone_reader()).context("clone reader")?;

        self.sessions
            .lock()
            .await
            .insert(session_id.to_string(), master_cell.clone());

        info!(session = session_id, "PTY открыт");

        let sid = session_id.to_string();
        let handle = tokio::task::spawn_blocking(move || {
            let mut reader = reader;
            let mut buf = [0u8; 8192];
            loop {
                match reader.read(&mut buf) {
                    Ok(0) => break,
                    Ok(n) => {
                        on_output(crate::crypto::b64_encode(&buf[..n]));
                    }
                    Err(e) => {
                        warn!(session = %sid, "pty read err: {e}");
                        break;
                    }
                }
            }
            info!(session = %sid, "PTY EOF");
        });

        Ok(handle)
    }

    /// Записать байты ввода в PTY (от админа).
    pub async fn write(&self, session_id: &str, data: &[u8]) -> Result<()> {
        let cell = {
            let sessions = self.sessions.lock().await;
            sessions
                .get(session_id)
                .cloned()
                .ok_or_else(|| anyhow::anyhow!("pty session not found: {session_id}"))?
        };
        let mut writer = cell.with_master(|m| m.take_writer()).context("take writer")?;
        writer.write_all(data)?;
        writer.flush()?;
        Ok(())
    }

    /// Изменить размер PTY.
    pub async fn resize(&self, session_id: &str, cols: u16, rows: u16) -> Result<()> {
        let cell = {
            let sessions = self.sessions.lock().await;
            sessions
                .get(session_id)
                .cloned()
                .ok_or_else(|| anyhow::anyhow!("pty session not found: {session_id}"))?
        };
        cell.with_master(|m| {
            m.resize(PtySize {
                rows,
                cols,
                pixel_width: 0,
                pixel_height: 0,
            })
        })
        .context("pty resize")?;
        Ok(())
    }

    /// Закрыть PTY-сессию.
    pub async fn close(&self, session_id: &str) -> Result<()> {
        if self.sessions.lock().await.remove(session_id).is_some() {
            info!(session = session_id, "PTY закрыт");
        }
        Ok(())
    }
}

/// Дефолтный shell под текущую ОС.
fn default_shell() -> CommandBuilder {
    if cfg!(target_os = "windows") {
        CommandBuilder::new("cmd.exe")
    } else {
        let shell = std::env::var("SHELL").unwrap_or_else(|_| "/bin/sh".to_string());
        CommandBuilder::new(shell)
    }
}
