//! Конфигурация агента: CLI-аргументы + персистентный state-файл.
//!
//! Персистентный state (`state.toml` рядом с бинарём) хранит device_id и
//! ключ сессии после успешного enrollment. Без state-файла требуется
//! `--enroll-token`.

use std::path::PathBuf;

use anyhow::{Context, Result};
use clap::{Parser, Subcommand};
use serde::{Deserialize, Serialize};

/// Агент Spider — удалённая консоль для собственных машин.
#[derive(Parser, Debug)]
#[command(name = "spider-agent", version, about, long_about = None)]
pub struct Cli {
    /// Путь к state-файлу (device_id + ключ сессии).
    #[arg(long, env = "SPIDER_STATE", global = true, default_value = "spider-state.toml")]
    pub state: PathBuf,

    #[command(subcommand)]
    pub command: Command,
}

/// Подкоманды агента.
#[derive(Subcommand, Debug)]
pub enum Command {
    /// Запустить агента (основной режим). Без state-файла требует --enroll-token.
    Run {
        /// Адрес сервера (https://...).
        #[arg(long, env = "SPIDER_SERVER")]
        server: String,

        /// Одноразовый токен регистрации (только для первого запуска).
        #[arg(long, env = "SPIDER_ENROLL_TOKEN")]
        enroll_token: Option<String>,

        /// Не требовать интерактивного подтверждения при первом запуске.
        #[arg(long, default_value_t = false)]
        yes: bool,
    },
    /// Показать состояние агента (device_id, сервер, версия).
    Status,
    /// Управление автозапуском (только с feature `autostart`).
    #[cfg(feature = "autostart")]
    Autostart {
        #[command(subcommand)]
        action: AutostartAction,
    },
    /// Проверить и установить обновление (только с feature `self-update`).
    #[cfg(feature = "self-update")]
    Update {
        /// URL манифеста/архива обновления (по умолчанию берётся из state).
        #[arg(long)]
        from: Option<String>,
    },
}

/// Действия для подкоманды autostart.
#[cfg(feature = "autostart")]
#[derive(Subcommand, Debug)]
pub enum AutostartAction {
    /// Добавить агента в автозапуск (systemd unit / Windows Service).
    Install,
    /// Убрать агента из автозапуска.
    Remove,
    /// Показать статус автозапуска.
    Status,
}

/// Персистентное состояние агента.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct State {
    /// device_id, выданный сервером при enroll.
    pub device_id: String,
    /// base64 симметричного ключа сессии.
    pub key_b64: String,
    /// Адрес сервера.
    pub server: String,
    /// Версия агента.
    pub agent_version: String,
}

impl State {
    /// Загрузить state из файла (ошибка, если файл отсутствует/повреждён).
    pub fn load(path: &PathBuf) -> Result<Self> {
        let raw = std::fs::read_to_string(path)
            .with_context(|| format!("не удалось прочитать state-файл {}", path.display()))?;
        let state: State = toml::from_str(&raw).context("state-файл повреждён")?;
        Ok(state)
    }

    /// Загрузить state; вернуть None, если файла ещё нет (первый запуск).
    pub fn load_optional(path: &PathBuf) -> Result<Option<Self>> {
        match std::fs::read_to_string(path) {
            Ok(raw) => Ok(Some(toml::from_str(&raw).context("state-файл повреждён")?)),
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => Ok(None),
            Err(e) => Err(anyhow::anyhow!("state read: {e}")),
        }
    }

    /// Сохранить state в файл.
    pub fn save(&self, path: &PathBuf) -> Result<()> {
        if let Some(parent) = path.parent() {
            std::fs::create_dir_all(parent).ok();
        }
        let raw = toml::to_string_pretty(self).context("state serialize")?;
        // Атомарная запись: временный файл + rename.
        let tmp = path.with_extension("toml.tmp");
        std::fs::write(&tmp, raw).context("state write tmp")?;
        std::fs::rename(&tmp, path).context("state rename")?;
        Ok(())
    }
}
