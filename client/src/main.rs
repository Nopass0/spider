//! Точка входа агента Spider.
//!
//! Режимы запуска:
//! - **без аргументов** (`spider-agent.exe` двойным кликом) — GUI-режим:
//!   иконка в трее + автоматическое подключение к серверу (по сохранённому
//!   state). Консольное окно не появляется. В трее: статус / «Открыть панель» / «Выход».
//! - `run` — CLI-режим (enroll + транспорт, в консоли);
//! - `status`, `autostart`, `update` — служебные подкоманды.
//!
//! Для первого запуска в GUI-режиме нужно один раз зарегистрировать устройство
//! через `spider-agent run --server <URL> --enroll-token <TOKEN> --yes` (state
//! сохранится), дальше двойной клик по exe сразу подключает.
//!
//! На Windows консольное окно убирается через `#![windows_subsystem = "windows"]`
//! — но это ломает `println!`, поэтому включаем только в release.

#![cfg_attr(all(windows, not(debug_assertions)), windows_subsystem = "windows")]

mod autostart;
mod config;
mod crypto;
mod enroll;
mod executor;
mod proto;
mod pty;
#[cfg(feature = "screen")]
mod screen;
mod sysinfo_collector;
mod tray;
mod transport;
#[cfg(feature = "self-update")]
mod update;

use std::time::Duration;

use anyhow::{anyhow, Context, Result};
use clap::Parser;
use config::{Cli, Command};
use tracing::{error, info};
use tracing_subscriber::EnvFilter;

fn main() {
    if let Err(e) = real_main() {
        error!("{e:#}");
        std::process::exit(1);
    }
}

/// Синхронная точка входа: разбирает CLI. Без подкоманды → GUI-режим (tray
/// в главном потоке, tokio-агент в отдельном). С подкомандой → async-CLI.
fn real_main() -> Result<()> {
    init_logging();

    // Если подкоманды нет — GUI-режим (двойной клик по exe).
    let args: Vec<String> = std::env::args().skip(1).collect();
    let has_subcommand = args
        .first()
        .map(|a| !a.starts_with('-'))
        .unwrap_or(false);

    if !has_subcommand {
        return run_gui();
    }

    // CLI-режим — обычный async.
    let rt = tokio::runtime::Runtime::new()?;
    rt.block_on(cli_main())
}

fn init_logging() {
    let filter =
        EnvFilter::try_from_default_env().unwrap_or_else(|_| EnvFilter::new("info"));
    tracing_subscriber::fmt()
        .with_env_filter(filter)
        .with_target(false)
        .init();
}

async fn cli_main() -> Result<()> {
    let cli = Cli::parse();
    match cli.command {
        Command::Run {
            server,
            enroll_token,
            yes,
        } => run_agent(&cli.state, &server, enroll_token.as_deref(), yes).await,
        Command::Status => {
            print_status(&cli.state)?;
            Ok(())
        }
        #[cfg(feature = "autostart")]
        Command::Autostart { action } => autostart_cmd(action),
        #[cfg(feature = "self-update")]
        Command::Update { from } => update_cmd(from.as_deref()).await,
    }
}

/// GUI-режим: tray в главном потоке (требование tray-icon), агент в отдельном.
fn run_gui() -> Result<()> {
    let (evt_tx, cmd_rx, holder) = tray::build_tray();

    // Агента запускаем в отдельном потоке со своим tokio-рантаймом.
    let agent_handle = std::thread::spawn(move || {
        let rt = match tokio::runtime::Runtime::new() {
            Ok(r) => r,
            Err(e) => {
                error!("tokio runtime: {e}");
                return;
            }
        };
        rt.block_on(gui_agent_loop(evt_tx, cmd_rx));
    });

    // Tray event-loop в ГЛАВНОМ потоке — блокирует до «Выход» (шлёт Quit в cmd_rx).
    tray::run_event_loop(&holder);

    // После «Выход» агент выйдет из recv() и завершится.
    let _ = agent_handle.join();
    Ok(())
}

/// Цикл агента в GUI-режиме. Крутится в отдельном потоке, ждёт Quit через cmd_rx.
async fn gui_agent_loop(
    evt_tx: std::sync::mpsc::Sender<tray::TrayEvent>,
    cmd_rx: std::sync::mpsc::Receiver<tray::TrayCmd>,
) {
    let state_path = std::path::PathBuf::from("spider-state.toml");
    let server_url = std::env::var("SPIDER_SERVER")
        .unwrap_or_else(|_| "https://spider.lowkey.su".to_string());

    let state = match resolve_state(&state_path, &server_url).await {
        Ok(s) => s,
        Err(e) => {
            error!("GUI: state: {e}");
            let _ = evt_tx.send(tray::TrayEvent::Status(
                "требуется регистрация (запустите с --enroll-token)".to_string(),
            ));
            wait_quit(cmd_rx);
            return;
        }
    };

    let key_bytes = match crypto::b64_decode(&state.key_b64) {
        Ok(b) if b.len() == crypto::KEY_SIZE => b,
        _ => {
            let _ = evt_tx.send(tray::TrayEvent::Status("state повреждён".to_string()));
            wait_quit(cmd_rx);
            return;
        }
    };
    let mut key_arr = [0u8; crypto::KEY_SIZE];
    key_arr.copy_from_slice(&key_bytes);
    let key = transport::SharedKey::new(key_arr);

    let (event_tx, mut event_rx) = tokio::sync::mpsc::unbounded_channel();
    let server_url = state.server.clone();
    let device_id = state.device_id.clone();
    let evt_tx2 = evt_tx.clone();

    // Логгер событий + проброс статуса в трей.
    let logger = tokio::spawn(async move {
        let mut last = String::new();
        while let Some(ev) = event_rx.recv().await {
            if let transport::AgentEvent::TransportSwitched(t) = ev {
                let s = match t {
                    "websocket" => {
                        format!("онлайн: {}", device_id.chars().take(8).collect::<String>())
                    }
                    other => format!("транспорт: {other}"),
                };
                if s != last {
                    last = s.clone();
                    let _ = evt_tx2.send(tray::TrayEvent::Status(s));
                }
            }
        }
    });

    let _ = evt_tx.send(tray::TrayEvent::Status(format!(
        "онлайн: {}",
        state.device_id.chars().take(8).collect::<String>()
    )));

    // Для transport — отдельные клоны (logger уже забрал device_id/server_url).
    let transport_handle = tokio::spawn({
        let server = state.server.clone();
        let dev = state.device_id.clone();
        async move { transport::run(&server, &dev, key, event_tx).await }
    });

    // Переносим блокирующий cmd_rx в async — ждём Quit, после чего выходим.
    let quit = tokio::task::spawn_blocking(move || wait_quit(cmd_rx));
    tokio::select! {
        _ = quit => {}
        _ = transport_handle => {}
    }
    logger.abort();
}

/// Ждать «Выход» (блокирующе). Используется в GUI-режиме.
fn wait_quit(cmd_rx: std::sync::mpsc::Receiver<tray::TrayCmd>) {
    while let Ok(cmd) = cmd_rx.recv() {
        if matches!(cmd, tray::TrayCmd::Quit) {
            return;
        }
    }
}

/// Загрузить state. Если state-файла нет — пробуем enroll по env-токену
/// (SPIDER_ENROLL_TOKEN) в тихом режиме (yes=true).
async fn resolve_state(
    state_path: &std::path::PathBuf,
    server_url: &str,
) -> Result<config::State> {
    if let Some(s) = config::State::load_optional(state_path)? {
        return Ok(s);
    }
    // Тихий enroll по env-токену (для GUI — без подтверждения).
    if let Ok(token) = std::env::var("SPIDER_ENROLL_TOKEN") {
        let s = enroll::run_enrollment(server_url, &token, true).await?;
        s.save(state_path).context("сохранение state")?;
        return Ok(s);
    }
    Err(anyhow!("нет state и нет SPIDER_ENROLL_TOKEN"))
}

/// CLI-режим агента: enroll + транспорт, с логированием в stdout.
async fn run_agent(state_path: &std::path::PathBuf, server: &str, enroll_token: Option<&str>, yes: bool) -> Result<()> {
    // 1. Загрузить или создать state.
    let state = match config::State::load_optional(state_path)? {
        Some(s) => {
            info!("состояние загружено: device_id={}", s.device_id);
            s
        }
        None => {
            let token = enroll_token.ok_or_else(|| {
                anyhow!(
                    "отсутствует state-файл (первый запуск). \
                     Укажите --enroll-token <TOKEN> (создаётся в админ-панели)."
                )
            })?;
            let state = enroll::run_enrollment(server, token, yes).await?;
            state.save(state_path).context("сохранение state")?;
            info!("устройство зарегистрировано: device_id={}", state.device_id);
            state
        }
    };

    // 2. Развернуть ключ сессии.
    let key_bytes = crypto::b64_decode(&state.key_b64)?;
    if key_bytes.len() != crypto::KEY_SIZE {
        return Err(anyhow!("ключ сессии в state повреждён ({} байт)", key_bytes.len()));
    }
    let mut key_arr = [0u8; crypto::KEY_SIZE];
    key_arr.copy_from_slice(&key_bytes);
    let key = transport::SharedKey::new(key_arr);

    // 3. Канал событий для логирования.
    let (event_tx, mut event_rx) = tokio::sync::mpsc::unbounded_channel();
    let server_url = state.server.clone();
    let device_id = state.device_id.clone();

    let logger = tokio::spawn(async move {
        while let Some(ev) = event_rx.recv().await {
            match ev {
                transport::AgentEvent::CommandReceived(c) => info!("→ команда: {c}"),
                transport::AgentEvent::CommandDone { id, exit_code } => {
                    info!("✓ команда {id} выполнена (exit={exit_code})")
                }
                transport::AgentEvent::ServerInfo(i) => {
                    info!("сервер: commands_enabled={}", i.commands_enabled)
                }
                transport::AgentEvent::TransportSwitched(t) => info!("транспорт: {t}"),
                transport::AgentEvent::TerminalOpened { session_id } => {
                    info!("▶ терминал открыт: {session_id}")
                }
                transport::AgentEvent::TerminalClosed { session_id, exit_code } => {
                    info!("■ терминал закрыт: {session_id} (exit={exit_code})")
                }
                #[cfg(feature = "screen")]
                transport::AgentEvent::ScreenStarted { session_id, fps } => {
                    info!("▶ трансляция экрана: {session_id} ({fps} fps)")
                }
                #[cfg(feature = "screen")]
                transport::AgentEvent::ScreenStopped { session_id } => {
                    info!("■ трансляция экрана остановлена: {session_id}")
                }
            }
        }
    });

    let result = transport::run(&server_url, &device_id, key, event_tx).await;
    logger.abort();
    result
}

/// Напечатать текущее состояние агента.
fn print_status(state_path: &std::path::PathBuf) -> Result<()> {
    match config::State::load_optional(state_path)? {
        Some(s) => {
            println!("Spider Agent — состояние");
            println!("  device_id:    {}", s.device_id);
            println!("  server:       {}", s.server);
            println!("  agent version: {}", s.agent_version);
            println!("  state file:   {}", state_path.display());
        }
        None => {
            println!("Spider Agent: устройство не зарегистрировано.");
            println!("  Запустите `spider-agent run --server <URL> --enroll-token <TOKEN>`.");
        }
    }
    Ok(())
}

/// Подкоманда autostart.
#[cfg(feature = "autostart")]
fn autostart_cmd(action: config::AutostartAction) -> Result<()> {
    use config::AutostartAction;
    let exe = autostart::current_exe()?;
    let state = std::path::PathBuf::from("spider-state.toml");
    match action {
        AutostartAction::Install => autostart::install(&exe, &state),
        AutostartAction::Remove => autostart::remove(),
        AutostartAction::Status => {
            autostart::status()?;
            Ok(())
        }
    }
}

/// Подкоманда update.
#[cfg(feature = "self-update")]
async fn update_cmd(from: Option<&str>) -> Result<()> {
    let url = from.ok_or_else(|| {
        anyhow!(
            "укажите --from <URL> (zip-архив и .sig рядом). \
             Обновление проверяется ed25519-подписью."
        )
    })?;
    let zip_url = url.to_string();
    let sig_url = format!("{zip_url}.sig");

    let client = reqwest::Client::builder()
        .timeout(Duration::from_secs(120))
        .build()?;
    let archive = client
        .get(&zip_url)
        .send()
        .await
        .context("download archive")?
        .bytes()
        .await
        .context("read archive")?;
    let signature = client
        .get(&sig_url)
        .send()
        .await
        .context("download signature")?
        .bytes()
        .await
        .context("read signature")?;

    update::apply_update(&archive, &signature)
}
