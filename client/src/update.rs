//! Автообновление агента с проверкой ed25519-подписи.
//!
//! Процесс:
//! 1. Скачать архив новой версии (zip) + файл подписи (.sig).
//! 2. Проверить ed25519-подпись архива против встроенного публичного ключа.
//! 3. Распаковать новый бинарь во временный файл.
//! 4. Атомарно заменить текущий бинарь (rename), затем перезапустить через
//!    сервис-менеджер (systemd) или self-reexec.
//!
//! ⚠️ Без валидной подписи обновление НЕ применяется — это защита от подмены.
//!
//! Публичный ключ (`SIGNING_PUBKEY`) вшивается при релизе через env-переменную
//! сборки; по умолчанию — заглушка, при которой обновление отключено.

use anyhow::{anyhow, bail, Context, Result};
use ed25519_dalek::{Signature, Verifier, VerifyingKey};
use tracing::info;

/// Публичный ключ проверки обновлений (hex, 32 байта). Вшивается релизной сборкой.
/// Пустая строка = обновления отключены (для dev).
pub const SIGNING_PUBKEY_HEX: &str = match option_env!("SIGNING_PUBKEY") {
    Some(k) => k,
    None => "",
};

/// Установить обновление из скачанного архива и подписи.
///
/// - `archive` — байты zip-архива с новым бинарём.
/// - `signature` — байты ed25519-подписи архива (64 байта).
///
/// Возвращает путь к новому исполняемому файлу после замены.
pub fn apply_update(archive: &[u8], signature: &[u8]) -> Result<()> {
    // 1. Проверить подпись.
    verify_signature(archive, signature)?;

    // 2. Извлечь бинарь из zip (одноимённый spider-agent[-.exe]).
    let new_bin = extract_binary(archive)?;
    let target = std::env::current_exe().context("current exe")?;

    // 3. Атомарная замена: write tmp рядом, затем rename.
    let tmp = target.with_extension("new.exe.tmp");
    std::fs::write(&tmp, &new_bin).context("write new binary")?;
    atomic_replace(&tmp, &target)?;

    info!("обновление применено: {}", target.display());
    println!("✓ Обновление установлено. Перезапустите агента (systemctl restart / reboot).");
    Ok(())
}

/// Проверить ed25519-подпись данных.
pub fn verify_signature(data: &[u8], signature: &[u8]) -> Result<()> {
    let pk_hex = SIGNING_PUBKEY_HEX;
    if pk_hex.is_empty() {
        bail!("обновления отключены: SIGNING_PUBKEY не задан при сборке");
    }
    if signature.len() != 64 {
        bail!("неверная длина подписи: {} (ожидалось 64)", signature.len());
    }
    let pk_bytes = hex::decode(pk_hex).context("SIGNING_PUBKEY не hex")?;
    if pk_bytes.len() != 32 {
        bail!("SIGNING_PUBKEY должен быть 32 байта");
    }
    let mut pk_arr = [0u8; 32];
    pk_arr.copy_from_slice(&pk_bytes);
    let vk = VerifyingKey::from_bytes(&pk_arr).context("invalid verifying key")?;
    let mut sig_arr = [0u8; 64];
    sig_arr.copy_from_slice(signature);
    let sig = Signature::from_bytes(&sig_arr);
    vk.verify(data, &sig).map_err(|_| anyhow!("неверная подпись обновления"))?;
    Ok(())
}

/// Извлечь бинарь из zip-архива (первый исполняемый файл).
fn extract_binary(archive: &[u8]) -> Result<Vec<u8>> {
    let cursor = std::io::Cursor::new(archive);
    let mut zip = zip::ZipArchive::new(cursor).context("open zip")?;
    for i in 0..zip.len() {
        let mut entry = zip.by_index(i).context("zip entry")?;
        let name = entry.name().to_lowercase();
        let is_bin = name.ends_with("spider-agent") || name.ends_with("spider-agent.exe");
        if is_bin {
            let mut buf = Vec::with_capacity(entry.size() as usize);
            std::io::Read::read_to_end(&mut entry, &mut buf)?;
            return Ok(buf);
        }
    }
    bail!("в архиве нет spider-agent бинаря");
}

/// Атомарная замена файла (tmp → target). На Windows нужен workaround.
fn atomic_replace(tmp: &std::path::Path, target: &std::path::Path) -> Result<()> {
    #[cfg(unix)]
    {
        std::fs::rename(tmp, target).context("rename")?;
        Ok(())
    }
    #[cfg(windows)]
    {
        // На Windows rename поверх работающего процесса падает.
        // Помещаем старый бинарь в .old и переименовываем новый на его место.
        let old = target.with_extension("exe.old");
        let _ = std::fs::remove_file(&old);
        std::fs::rename(target, &old).ok();
        std::fs::rename(tmp, target).context("rename new into place")?;
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use ed25519_dalek::{Signer, SigningKey};

    #[test]
    fn signature_round_trip_offline() {
        // временно вшиваем ключ через подмену логики: verify_signature использует
        // константу SIGNING_PUBKEY_HEX, поэтому проверяем только механику подписи.
        let mut csprng = rand::rngs::OsRng;
        let signing = SigningKey::generate(&mut csprng);
        let verifying: VerifyingKey = signing.verifying_key();
        let data = b"archive-bytes";
        let sig = signing.sign(data);
        // 直接ная проверка (без env-ключа) — что алгоритм корректен.
        assert!(verifying.verify(data, &sig).is_ok());
        assert!(verifying.verify(b"other", &sig).is_err());
    }

    #[test]
    fn verify_rejects_when_no_pubkey() {
        // SIGNING_PUBKEY_HEX пуст в dev → должна быть ошибка.
        if SIGNING_PUBKEY_HEX.is_empty() {
            let err = verify_signature(b"x", &[0u8; 64]).unwrap_err();
            assert!(err.to_string().contains("SIGNING_PUBKEY"));
        }
    }
}
