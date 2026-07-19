//! Трансляция экрана (MJPEG) и одиночные скриншоты.
//!
//! Использует `xcap` для захвата (кросс-платформенно, без внешних либ) и
//! `image` для JPEG-энкодинга. Цикл захвата крутится в блокирующей задаче и
//! стримит JPEG-кадры в callback с заданным FPS. Остановка — через AtomicBool.

use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Arc;
use std::time::Duration;

use anyhow::{anyhow, Result};
use image::codecs::jpeg::JpegEncoder;
use image::ExtendedColorType;
use tokio::sync::Mutex;
use tracing::{info, warn};

/// Менеджер активных screen-сессий. Хранит флаг отмены для каждой.
pub struct ScreenManager {
    cancellations: Mutex<std::collections::HashMap<String, Arc<AtomicBool>>>,
}

impl ScreenManager {
    pub fn new() -> Self {
        Self {
            cancellations: Mutex::new(std::collections::HashMap::new()),
        }
    }

    /// Сделать одиночный скриншот основного монитора → JPEG-байты + размеры.
    pub fn snapshot() -> Result<(Vec<u8>, u32, u32)> {
        let monitor = xcap::Monitor::all()
            .map_err(|e| anyhow!("xcap Monitor::all: {e}"))?
            .into_iter()
            .next()
            .ok_or_else(|| anyhow!("нет доступных мониторов"))?;
        let img = monitor
            .capture_image()
            .map_err(|e| anyhow!("capture: {e}"))?;
        encode_jpeg(&img, 75)
    }

    /// Запустить цикл захвата с FPS. Каждый кадр → callback. Останавливается
    /// через `stop(session_id)` или при критической ошибке захвата.
    pub async fn start<F>(&self, session_id: &str, fps: u32, quality: u8, on_frame: F) -> Result<()>
    where
        F: Fn(Vec<u8>, u32, u32) + Send + Sync + 'static,
    {
        if fps == 0 {
            return Err(anyhow!("fps должен быть > 0"));
        }
        let cancel = Arc::new(AtomicBool::new(false));
        self.cancellations
            .lock()
            .await
            .insert(session_id.to_string(), cancel.clone());

        let sid = session_id.to_string();
        let interval = Duration::from_secs_f64(1.0 / fps as f64);
        tokio::task::spawn_blocking(move || {
            let monitors = match xcap::Monitor::all() {
                Ok(m) => m,
                Err(e) => {
                    warn!(session = %sid, "screen: monitors list failed: {e}");
                    return;
                }
            };
            let monitor = match monitors.into_iter().next() {
                Some(m) => m,
                None => {
                    warn!(session = %sid, "screen: нет мониторов");
                    return;
                }
            };
            let q = if quality == 0 { 60 } else { quality };
            loop {
                if cancel.load(Ordering::Relaxed) {
                    info!(session = %sid, "screen stop");
                    return;
                }
                std::thread::sleep(interval);
                if cancel.load(Ordering::Relaxed) {
                    info!(session = %sid, "screen stop");
                    return;
                }
                let img = match monitor.capture_image() {
                    Ok(i) => i,
                    Err(e) => {
                        warn!(session = %sid, "screen capture err: {e}");
                        continue;
                    }
                };
                match encode_jpeg(&img, q) {
                    Ok((bytes, w, h)) => on_frame(bytes, w, h),
                    Err(e) => warn!(session = %sid, "jpeg encode err: {e}"),
                }
            }
        });
        info!(session = session_id, fps, "screen стрим запущен");
        Ok(())
    }

    /// Остановить цикл захвата.
    pub async fn stop(&self, session_id: &str) -> Result<()> {
        if let Some(cancel) = self.cancellations.lock().await.remove(session_id) {
            cancel.store(true, Ordering::Relaxed);
        }
        Ok(())
    }
}

/// Закодировать image::RgbaImage в JPEG. Возвращает (bytes, w, h).
fn encode_jpeg(img: &image::RgbaImage, quality: u8) -> Result<(Vec<u8>, u32, u32)> {
    let (w, h) = img.dimensions();
    let mut out = Vec::with_capacity((w * h) as usize / 6);
    JpegEncoder::new_with_quality(&mut out, quality)
        .encode(img.as_raw(), w, h, ExtendedColorType::Rgba8)
        .map_err(|e| anyhow!("jpeg encode: {e}"))?;
    Ok((out, w, h))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    #[cfg(feature = "screen")]
    fn snapshot_returns_jpeg() {
        // На CI/headless xcap может не найти монитор — допустим обе ветви.
        match ScreenManager::snapshot() {
            Ok((bytes, w, h)) => {
                assert!(!bytes.is_empty(), "JPEG не должен быть пустым");
                // JPEG magic: FF D8
                assert_eq!(bytes[0], 0xFF, "первый байт JPEG");
                assert_eq!(bytes[1], 0xD8, "второй байт JPEG");
                assert!(w > 0 && h > 0, "размеры > 0: {w}x{h}");
            }
            Err(e) => {
                // headless-окружение без монитора — тест пропускаем.
                eprintln!("screen snapshot недоступен (headless?): {e}");
            }
        }
    }

    #[test]
    fn screen_manager_constructs() {
        let _ = ScreenManager::new();
    }

    #[test]
    fn start_zero_fps_errors() {
        let mgr = ScreenManager::new();
        let rt = tokio::runtime::Runtime::new().unwrap();
        let err = rt.block_on(mgr.start("s", 0, 60, |_, _, _| {}));
        assert!(err.is_err(), "fps=0 должен дать ошибку");
    }
}
