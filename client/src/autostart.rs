//! Управление автозапуском агента — видимый, задокументированный системный
//! сервис (НЕ скрытый). Реализуется через:
//!
//! - **Linux**: systemd unit `/etc/systemd/system/spider-agent.service`.
//! - **Windows**: запись в `HKCU\...\Run` (видимый ключ реестра автозапуска).
//!
//! Все операции требуют прав/подтверждения и логируются.

use std::path::PathBuf;

use anyhow::{Context, Result};

/// Действие автозапуска.
#[derive(Debug, Clone, Copy)]
pub enum AutostartAction {
    Install,
    Remove,
    Status,
}

/// Текущий путь к исполняемому файлу агента.
pub fn current_exe() -> Result<PathBuf> {
    std::env::current_exe().context("не удалось определить путь к исполняемому файлу")
}

#[cfg(unix)]
mod imp {
    use super::*;

    pub const UNIT_PATH: &str = "/etc/systemd/system/spider-agent.service";

    /// systemd unit-файл.
    pub fn unit_contents(exe: &PathBuf, state: &PathBuf) -> String {
        format!(
            "[Unit]\n\
             Description=Spider Agent — remote console for owned machines\n\
             After=network-online.target\n\
             Wants=network-online.target\n\
             \n\
             [Service]\n\
             Type=simple\n\
             ExecStart={exe} run --state {state}\n\
             Restart=on-failure\n\
             RestartSec=5\n\
             # Явно видимый сервис. Не скрываем.\n\
             \n\
             [Install]\n\
             WantedBy=multi-user.target\n",
            exe = exe.display(),
            state = state.display(),
        )
    }

    pub fn install(exe: &PathBuf, state: &PathBuf) -> Result<()> {
        let unit = unit_contents(exe, state);
        std::fs::write(UNIT_PATH, unit)
            .with_context(|| format!("write {UNIT_PATH} (нужны права root/sudo)"))?;
        run("systemctl", &["daemon-reload"])?;
        run("systemctl", &["enable", "spider-agent.service"])?;
        run("systemctl", &["restart", "spider-agent.service"])?;
        println!("✓ spider-agent установлен как systemd-сервис ({UNIT_PATH})");
        Ok(())
    }

    pub fn remove() -> Result<()> {
        run("systemctl", &["stop", "spider-agent.service"]).ok();
        run("systemctl", &["disable", "spider-agent.service"])?;
        std::fs::remove_file(UNIT_PATH).ok();
        run("systemctl", &["daemon-reload"])?;
        println!("✓ spider-agent удалён из автозапуска");
        Ok(())
    }

    pub fn status() -> Result<bool> {
        let out = std::process::Command::new("systemctl")
            .args(["is-enabled", "spider-agent.service"])
            .output()?;
        let enabled = String::from_utf8_lossy(&out.stdout).trim() == "enabled";
        println!("spider-agent autostart: {}", if enabled { "enabled" } else { "disabled" });
        Ok(enabled)
    }

    fn run(cmd: &str, args: &[&str]) -> Result<()> {
        let status = std::process::Command::new(cmd)
            .args(args)
            .status()
            .with_context(|| format!("run {cmd}"))?;
        if !status.success() {
            return Err(anyhow::anyhow!("{cmd} {} failed", args.join(" ")));
        }
        Ok(())
    }
}

#[cfg(windows)]
mod imp {
    use super::*;

    pub fn install(exe: &PathBuf, _state: &PathBuf) -> Result<()> {
        // Видимый ключ Run для текущего пользователя.
        let exe_str = exe.to_string_lossy().replace('\\', "\\\\");
        let ps = format!(
            "$p='HKCU:\\Software\\Microsoft\\Windows\\CurrentVersion\\Run'; \
             Set-ItemProperty -Path $p -Name 'SpiderAgent' -Value '\"{exe}\" run'",
            exe = exe_str
        );
        let status = std::process::Command::new("powershell")
            .args(["-NoProfile", "-Command", &ps])
            .status()
            .context("powershell")?;
        if !status.success() {
            return Err(anyhow::anyhow!("powershell Set-ItemProperty failed"));
        }
        println!("✓ Spider Agent добавлен в автозапуск (HKCU Run)");
        Ok(())
    }

    pub fn remove() -> Result<()> {
        let ps = "$p='HKCU:\\Software\\Microsoft\\Windows\\CurrentVersion\\Run'; \
                 Remove-ItemProperty -Path $p -Name 'SpiderAgent' -ErrorAction SilentlyContinue";
        let status = std::process::Command::new("powershell")
            .args(["-NoProfile", "-Command", ps])
            .status()?;
        if !status.success() {
            return Err(anyhow::anyhow!("powershell Remove-ItemProperty failed"));
        }
        println!("✓ Spider Agent удалён из автозапуска");
        Ok(())
    }

    pub fn status() -> Result<bool> {
        let ps = "$p='HKCU:\\Software\\Microsoft\\Windows\\CurrentVersion\\Run'; \
                 (Get-ItemProperty -Path $p -Name 'SpiderAgent' -ErrorAction SilentlyContinue).SpiderAgent";
        let out = std::process::Command::new("powershell")
            .args(["-NoProfile", "-Command", ps])
            .output()?;
        let enabled = !out.stdout.is_empty();
        println!("Spider Agent autostart: {}", if enabled { "enabled" } else { "disabled" });
        Ok(enabled)
    }
}

/// Универсальный публичный API (делегирует в платформенный imp).
pub fn install(exe: &PathBuf, state: &PathBuf) -> Result<()> {
    imp::install(exe, state)
}
pub fn remove() -> Result<()> {
    imp::remove()
}
pub fn status() -> Result<bool> {
    imp::status()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[cfg(unix)]
    #[test]
    fn unit_contents_has_execstart() {
        let exe = PathBuf::from("/usr/local/bin/spider-agent");
        let state = PathBuf::from("/var/lib/spider/state.toml");
        let unit = imp::unit_contents(&exe, &state);
        assert!(unit.contains("ExecStart=/usr/local/bin/spider-agent run --state"));
        assert!(unit.contains("WantedBy=multi-user.target"));
        assert!(unit.contains("Description=Spider Agent"));
    }
}
