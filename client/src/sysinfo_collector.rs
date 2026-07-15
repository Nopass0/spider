//! Сбор базовой информации о системе для регистрации/heartbeat.
//! Использует крейт `sysinfo` (без внешних зависимостей на машине).

use crate::proto::SystemInfo;

/// Версия агента (подставляется при сборке или берётся из Cargo).
pub const AGENT_VERSION: &str = env!("CARGO_PKG_VERSION");

/// Собрать SystemInfo о текущей машине.
pub fn collect() -> SystemInfo {
    let mut sys = sysinfo::System::new_all();
    sys.refresh_cpu_usage();
    // CPU usage требует "прогрева" —.refresh_all достаточно для brand/cores.

    let hostname = sysinfo::System::host_name().unwrap_or_default();
    let os = format_os(&sys);
    let arch = std::env::consts::ARCH.to_string();
    let cpu_brand = sys
        .cpus()
        .first()
        .map(|c| c.brand().to_string())
        .unwrap_or_default();
    let cpu_cores = sys.cpus().len() as i32;
    let mem_total = sys.total_memory();

    SystemInfo {
        hostname,
        os,
        arch,
        cpu_brand,
        cpu_cores,
        mem_total,
        agent_version: AGENT_VERSION.to_string(),
    }
}

/// Человекочитаемая строка ОС.
fn format_os(_sys: &sysinfo::System) -> String {
    let name = sysinfo::System::name().unwrap_or_default();
    let version = sysinfo::System::os_version().unwrap_or_default();
    let long = sysinfo::System::long_os_version().unwrap_or_default();
    if !long.is_empty() {
        long
    } else if !name.is_empty() {
        format!("{name} {version}").trim().to_string()
    } else {
        std::env::consts::OS.to_string()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn collect_returns_nonempty() {
        let info = collect();
        // hostname/arch/cpu не должны быть пустыми на реальной машине
        assert!(!info.arch.is_empty());
        assert!(info.cpu_cores >= 1);
        assert!(!info.agent_version.is_empty());
    }
}
