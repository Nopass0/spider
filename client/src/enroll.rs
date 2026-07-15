//! Enrollment — первичная регистрация агента на сервере по одноразовому токену.
//!
//! Шаги:
//! 1. Показать предупреждение и запросить подтверждение (если интерактив).
//! 2. Сгенерировать эфемерную пару X25519.
//! 3. Отправить токен + публичный ключ + сист.инфо на POST /agent/enroll.
//! 4. Получить device_id + симметричный ключ сессии; сохранить в state.

use anyhow::{anyhow, Context, Result};

use crate::config::State;
use crate::proto::{EnrollRequest, EnrollResponse, SystemInfo};
use crate::sysinfo_collector;

/// Препредупреждение, показываемое перед первым подключением.
pub const FIRST_RUN_WARNING: &str = "\
==========================================================================
  Spider Agent — утилита удалённого управления.
  Эта программа зарегистрирует ТЕКУЩУЮ машину на сервере Spider и позволит
  администратору выполнять на ней консольные команды.

  Перед продолжением убедитесь, что:
    * вы являетесь владельцем этой машины;
    * у вас есть разрешение установить этот агент;
    * вы понимаете, что администратор получит удалённый shell.

  Все данные шифруются (TLS + прикладной AES-256-GCM). Команды выполняются
  через системный shell и логируются на сервере.
==========================================================================";

/// Зарегистрировать агента на сервере.
///
/// Возвращает готовое состояние для сохранения.
pub async fn enroll(
    server: &str,
    token: &str,
    system: &SystemInfo,
) -> Result<EnrollResponse> {
    let url = format!("{}/agent/enroll", server.trim_end_matches('/'));
    let req = EnrollRequest {
        token: token.to_string(),
        public_key: String::new(), // публичный ключ агента (опционально для текущей схемы)
        system: system.clone(),
        agent_version: system.agent_version.clone(),
    };
    let client = reqwest::Client::builder()
        .danger_accept_invalid_certs(false)
        .build()?;
    let resp = client
        .post(&url)
        .json(&req)
        .send()
        .await
        .with_context(|| format!("enroll request to {url}"))?;
    let status = resp.status();
    if !status.is_success() {
        let body = resp.text().await.unwrap_or_default();
        return Err(anyhow!("enroll failed: HTTP {status}: {body}"));
    }
    let er: EnrollResponse = resp.json().await.context("enroll: decode response")?;
    Ok(er)
}

/// Запросить подтверждение пользователя на первом запуске.
/// Возвращает false, если пользователь отказался (не интерактивно — требует --yes).
pub fn confirm(assume_yes: bool) -> bool {
    if assume_yes {
        return true;
    }
    use std::io::{self, BufRead, Write};
    print!("{FIRST_RUN_WARNING}\n\nПродолжить регистрацию? [y/N]: ");
    let _ = io::stdout().flush();
    let mut line = String::new();
    if io::stdin().lock().read_line(&mut line).is_err() {
        return false;
    }
    matches!(line.trim().to_lowercase().as_str(), "y" | "yes")
}

/// Полный сценарий: подтвердить → enroll → собрать State.
pub async fn run_enrollment(
    server: &str,
    token: &str,
    assume_yes: bool,
) -> Result<State> {
    println!("{FIRST_RUN_WARNING}");
    if !confirm(assume_yes) {
        return Err(anyhow!("регистрация отменена пользователем"));
    }
    let info = sysinfo_collector::collect();
    let resp = enroll(server, token, &info).await?;
    Ok(State {
        device_id: resp.device_id,
        key_b64: resp.key_b64,
        server: server.trim_end_matches('/').to_string(),
        agent_version: info.agent_version.clone(),
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn confirm_assume_yes() {
        assert!(confirm(true));
    }
}
