//! Точка входа агента Spider.
//!
//! Подкоманды:
//! - `run` — основной режим (enroll при первом запуске + транспорт WS/long-poll);
//! - `status` — показать текущее состояние;
//! - `autostart install|remove|status` — управление автозапуском (feature);
//! - `update` — применить обновление (feature).

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
mod transport;
#[cfg(feature = "self-update")]
mod update;

use std::time::Duration;

use anyhow::{anyhow, Context, Result};
use clap::Parser;
use config::{Cli, Command};
use tracing::{error, info};
use tracing_subscriber::EnvFilter;

#[tokio::main]
async fn main() {
    if let Err(e) = real_main().await {
        error!("{e:#}");
        std::process::exit(1);
    }
}

async fn real_main() -> Result<()> {
    // Инициализация логирования (JSON в stdout; уровень через RUST_LOG).
    let filter = EnvFilter::try_from_default_env()
        .unwrap_or_else(|_| EnvFilter::new("info"));
    tracing_subscriber::fmt()
        .with_env_filter(filter)
        .with_target(false)
        .init();

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

/// Основной режим агента.
async fn run_agent(state_path: &std::path::PathBuf, server: &str, enroll_token: Option<&str>, yes: bool) -> Result<()> {
    // 1. Загрузить или создать state.
    let state = match config::State::load_optional(state_path)? {
        Some(s) => {
            info!("состояние загружено: device_id={}", s.device_id);
            s
        }
        None => {
            // Первый запуск: нужен enroll-токен.
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
        return Err(anyhow!(
            "ключ сессии в state повреждён ({} байт)",
            key_bytes.len()
        ));
    }
    let mut key_arr = [0u8; crypto::KEY_SIZE];
    key_arr.copy_from_slice(&key_bytes);
    let key = transport::SharedKey::new(key_arr);

    // 3. Канал событий для логирования.
    let (event_tx, mut event_rx) = tokio::sync::mpsc::unbounded_channel();
    let server_url = state.server.clone();
    let device_id = state.device_id.clone();

    // задача-логгер событий
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

    // 4. Главный цикл транспорта (бесконечный, с реконнектами).
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
