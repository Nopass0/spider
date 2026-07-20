//! Иконка системного трея + event-loop.
//!
//! Архитектура: tray-icon/muda НЕ Send (внутри Rc) — всё, что связано с меню
//! и иконкой, должно жить в одном (главном) потоке. Поэтому:
//! - tokio-агент крутится в отдельном потоке;
//! - связь со стороны агента → через `Sender<TrayEvent>` (Send, передаётся в поток агента);
//! - связь со стороны tray → через глобальный `CMD_TX` (`Sender<TrayCmd>`), который
//!   агент читает в `spawn_blocking` (cmd_rx хранится в глобале, т.к. тоже не Send
//!   принципиально — но `Receiver<TrayCmd>` где `TrayCmd` — наш enum, Send, и
//!   `std::mpsc::Receiver<T: Send>` сам Send).
//!
//! Меню: Статус / сепаратор / Открыть панель / Выход.

use std::sync::mpsc::{channel, Receiver, Sender};
use std::sync::Mutex;
use tray_icon::menu::{Menu, MenuEvent, MenuItem, PredefinedMenuItem};
use tray_icon::{Icon, TrayIcon, TrayIconBuilder};

/// Команды из трея в агент.
pub enum TrayCmd {
    /// Пользователь нажал «Выход».
    Quit,
}

/// События из агента в трей (обновление индикатора).
#[derive(Clone)]
pub enum TrayEvent {
    Status(String),
}

/// Holder держит TrayIcon и item_status живыми (drop убирает иконку).
/// Жить должен в main-потоке (MenuItem/TrayIcon не Send).
pub struct TrayHolder {
    _icon: TrayIcon,
    status_item: MenuItem,
}

/// Создаёт иконку/меню, возвращает (evt_sender, cmd_receiver, holder).
/// evt_sender — агент шлёт статусы. cmd_receiver — агент читает Quit.
/// holder — держит иконку/menu живыми, остаётся в main-потоке.
pub fn build_tray() -> (Sender<TrayEvent>, Receiver<TrayCmd>, TrayHolder) {
    let (cmd_tx, cmd_rx) = channel::<TrayCmd>();
    let (evt_tx, evt_rx) = channel::<TrayEvent>();

    let icon = make_icon();
    let menu = Menu::new();
    let item_status = MenuItem::new("● подключение…", false, None);
    let item_open = MenuItem::new("Открыть панель", true, None);
    let sep = PredefinedMenuItem::separator();
    let item_quit = MenuItem::new("Выход", true, None);
    menu.append(&item_status).ok();
    menu.append(&sep).ok();
    menu.append(&item_open).ok();
    menu.append(&item_quit).ok();

    let tray = TrayIconBuilder::new()
        .with_menu(Box::new(menu))
        .with_tooltip("Spider Agent")
        .with_icon(icon)
        .build()
        .expect("tray icon");

    // id-шники как String (Send) — loop сравнивает по строке.
    *OPEN_ID.lock().unwrap() = Some(item_open.id().as_ref().to_string());
    *QUIT_ID.lock().unwrap() = Some(item_quit.id().as_ref().to_string());
    // cmd_tx + evt_rx в глобал для run_event_loop.
    *CMD_TX.lock().unwrap() = Some(cmd_tx);
    *EVT_RX.lock().unwrap() = Some(evt_rx);

    (
        evt_tx,
        cmd_rx,
        TrayHolder {
            _icon: tray,
            status_item: item_status,
        },
    )
}

/// Цикл событий трея. Блокирует главный поток до «Выход». holder передаётся по
/// ссылке, чтобы обновлять текст item_status.
pub fn run_event_loop(holder: &TrayHolder) {
    let menu_channel = MenuEvent::receiver().clone();
    loop {
        // Клики меню.
        if let Ok(ev) = menu_channel.try_recv() {
            let id_str = ev.id.as_ref().to_string();
            if Some(&id_str) == OPEN_ID.lock().unwrap().as_ref() {
                open_panel();
            } else if Some(&id_str) == QUIT_ID.lock().unwrap().as_ref() {
                if let Some(tx) = CMD_TX.lock().unwrap().as_ref() {
                    let _ = tx.send(TrayCmd::Quit);
                }
                break;
            }
        }
        // Статус от агента → текст item_status.
        if let Some(rx) = EVT_RX.lock().unwrap().as_ref() {
            while let Ok(ev) = rx.try_recv() {
                if let TrayEvent::Status(s) = ev {
                    holder.status_item.set_text(format!("● {s}"));
                }
            }
        }
        std::thread::sleep(std::time::Duration::from_millis(50));
    }
}

// Глобальные (id-строки и каналы — все Send).
static CMD_TX: Mutex<Option<Sender<TrayCmd>>> = Mutex::new(None);
static EVT_RX: Mutex<Option<Receiver<TrayEvent>>> = Mutex::new(None);
static OPEN_ID: Mutex<Option<String>> = Mutex::new(None);
static QUIT_ID: Mutex<Option<String>> = Mutex::new(None);

/// Открыть панель сервера в браузере по умолчанию.
fn open_panel() {
    let url = stored_server_url().unwrap_or_else(|| "https://spider.lowkey.su".to_string());
    #[cfg(target_os = "windows")]
    let _ = std::process::Command::new("cmd").args(["/C", "start", "", &url]).spawn();
    #[cfg(target_os = "linux")]
    let _ = std::process::Command::new("xdg-open").arg(&url).spawn();
    #[cfg(target_os = "macos")]
    let _ = std::process::Command::new("open").arg(&url).spawn();
}

/// Прочитать адрес сервера из state-файла.
fn stored_server_url() -> Option<String> {
    let path = std::path::PathBuf::from("spider-state.toml");
    crate::config::State::load_optional(&path).ok()?.map(|s| s.server)
}

/// Сгенерировать иконку трея.
fn make_icon() -> Icon {
    let size = 64u32;
    let mut rgba = Vec::with_capacity((size * size * 4) as usize);
    let cx = size as f32 / 2.0;
    let cy = size as f32 / 2.0;
    let r = size as f32 * 0.42;
    for y in 0..size {
        for x in 0..size {
            let dx = x as f32 - cx;
            let dy = y as f32 - cy;
            let d = (dx * dx + dy * dy).sqrt();
            if d <= r {
                let t = d / r;
                let cr = (45.0 * (1.0 - t) + 14.0 * t) as u8;
                let cg = (212.0 * (1.0 - t) + 17.0 * t) as u8;
                let cb = (252.0 * (1.0 - t) + 23.0 * t) as u8;
                rgba.extend_from_slice(&[cr, cg, cb, 255]);
            } else {
                rgba.extend_from_slice(&[0, 0, 0, 0]);
            }
        }
    }
    Icon::from_rgba(rgba, size, size).expect("valid icon")
}
