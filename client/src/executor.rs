//! Выполнение консольных команд с таймаутом и сбором stdout/stderr/exit-code.
//!
//! Использует системный shell (sh на Unix, cmd на Windows), чтобы поддерживать
//! произвольные команды — как и просит ТЗ (удобная удалённая консоль).

use std::process::Stdio;
use std::time::{Duration, Instant};

use anyhow::Result;
use tokio::io::AsyncReadExt;
use tokio::process::Command;

use crate::crypto;
use crate::proto::WireResult;

/// Результат выполнения команды.
#[derive(Debug)]
pub struct ExecOutcome {
    pub exit_code: i32,
    pub stdout: Vec<u8>,
    pub stderr: Vec<u8>,
    pub duration_ms: i64,
    pub timed_out: bool,
}

/// Выполнить команду через системный shell с ограничением по времени.
///
/// - `command` — строка команды как есть.
/// - `timeout_sec` — максимум секунд; 0 = без таймаута (но ставим предельный 1ч).
pub async fn execute(command: &str, timeout_sec: u32) -> Result<ExecOutcome> {
    let timeout = if timeout_sec == 0 {
        Duration::from_secs(3600)
    } else {
        Duration::from_secs(timeout_sec as u64)
    };

    let mut cmd = shell_command(command);
    cmd.stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        // убиваем всю группу процессов при таймауте (Unix)
        .kill_on_drop(true);

    let start = Instant::now();
    let mut child = cmd.spawn()?;

    let mut stdout = child.stdout.take().expect("piped stdout");
    let mut stderr = child.stderr.take().expect("piped stderr");

    // читаем параллельно в отдельных задачах, чтобы не зависеть от порядка
    let out_task = tokio::spawn(async move { read_all(&mut stdout).await });
    let err_task = tokio::spawn(async move { read_all(&mut stderr).await });

    let exit_code;
    let timed_out = match tokio::time::timeout(timeout, child.wait()).await {
        Ok(status) => {
            exit_code = status?.code().unwrap_or(-1);
            false
        }
        Err(_) => {
            // таймаут: child убивается через kill_on_drop при drop
            return Ok(ExecOutcome {
                exit_code: 124,
                stdout: out_task.await.unwrap_or_default(),
                stderr: err_task.await.unwrap_or_default(),
                duration_ms: start.elapsed().as_millis() as i64,
                timed_out: true,
            });
        }
    };

    Ok(ExecOutcome {
        exit_code,
        stdout: out_task.await.unwrap_or_default(),
        stderr: err_task.await.unwrap_or_default(),
        duration_ms: start.elapsed().as_millis() as i64,
        timed_out,
    })
}

/// Превратить исход выполнения в wire-результат для отправки на сервер.
impl ExecOutcome {
    pub fn to_wire(&self, command_id: &str) -> WireResult {
        WireResult {
            command_id: command_id.to_string(),
            exit_code: self.exit_code,
            stdout_b64: crypto::b64_encode(&self.stdout),
            stderr_b64: crypto::b64_encode(&self.stderr),
            duration_ms: self.duration_ms,
            error: if self.timed_out {
                "execution timed out".to_string()
            } else {
                String::new()
            },
        }
    }
}

/// Построить Command под текущую ОС.
fn shell_command(command: &str) -> Command {
    if cfg!(target_os = "windows") {
        let mut c = Command::new("cmd");
        c.args(["/C", command]);
        c
    } else {
        let mut c = Command::new("sh");
        c.args(["-c", command]);
        c
    }
}

/// Прочитать весь поток в Vec<u8>.
async fn read_all<R: AsyncReadExt + Unpin>(r: &mut R) -> Vec<u8> {
    let mut buf = Vec::new();
    let _ = r.read_to_end(&mut buf).await;
    buf
}

#[cfg(test)]
mod tests {
    use super::*;

    #[tokio::test]
    async fn execute_echo() {
        let cmd = if cfg!(windows) { "echo hello" } else { "echo hello" };
        let out = execute(cmd, 5).await.unwrap();
        assert!(!out.timed_out);
        assert!(String::from_utf8_lossy(&out.stdout).contains("hello"));
    }

    #[tokio::test]
    async fn execute_timeout() {
        // бесконечный sleep должен упереться в таймаут
        let cmd = if cfg!(windows) { "ping -n 30 127.0.0.1 > nul" } else { "sleep 30" };
        let out = execute(cmd, 1).await.unwrap();
        assert!(out.timed_out, "ожидался таймаут: {:?}", out);
    }

    #[tokio::test]
    async fn execute_exit_code() {
        let cmd = if cfg!(windows) { "exit /b 3" } else { "exit 3" };
        let out = execute(cmd, 5).await.unwrap();
        // на Windows exit /b через cmd может дать код; допускаем 3 илиsignal
        assert_eq!(out.exit_code, 3);
    }

    #[test]
    fn to_wire_encodes_output() {
        let o = ExecOutcome {
            exit_code: 0,
            stdout: b"hi".to_vec(),
            stderr: vec![],
            duration_ms: 5,
            timed_out: false,
        };
        let w = o.to_wire("cmd-1");
        assert_eq!(w.command_id, "cmd-1");
        assert!(!w.stdout_b64.is_empty());
    }
}
