//! 管家监控（主动对话）：后台采样系统进程，检测到异常（CPU 突增/内存吃紧/未知进程）
//! 时，先本地判断是否值得打扰，再通过 WebUI 的 queue API 给运行中的会话入队
//! 一条「主动消息」，让顾清影先判断再询问用户。
//!
//! 用法：`gqy watch --every 30s`（前台跑）或由 LaunchAgent 托管。
//! 设计原则：采样开销极小（一条 ps 命令），默认不打扰（阈值内静默）。

use crate::paths::GqyPaths;
use anyhow::{Context, Result};
use std::process::Command;

/// 单次采样结果：CPU 突增 / 内存吃紧的进程列表。
pub struct WatchSample {
    /// 超过 CPU 阈值的进程（名字, CPU%, 内存%）
    pub hot_processes: Vec<(String, f32, f32)>,
    /// 系统已用内存占比（%），取自内核 kern.memorystatus_level
    pub memory_pressure: f32,
}

/// 系统已用内存占比。`kern.memorystatus_level` 是内核给出的**可用**内存百分比
/// （与 /usr/bin/memory_pressure 的 "free percentage" 一致），取反即已用。
///
/// 不能用 `ps` 的 %MEM 求和代替：共享内存被每个进程重复计上，空载机器就能超过
/// 100%，会让 should_alert 恒为真。
fn memory_used_percent() -> f32 {
    let Ok(output) = Command::new("sysctl").args(["-n", "kern.memorystatus_level"]).output() else {
        return 0.0;
    };
    String::from_utf8_lossy(&output.stdout)
        .trim()
        .parse::<f32>()
        .map(|available| (100.0 - available).clamp(0.0, 100.0))
        .unwrap_or(0.0)
}

/// 解析一行 `ps -axo %cpu=,%mem=,comm=` 输出 →（进程名, CPU%, 内存%）。
///
/// 必须用 `split_whitespace`：ps 把数值右对齐补空格，**每一行都有前导空格**，
/// 也常出现连续空格。曾用 `splitn(3, char::is_whitespace).filter(非空)`，
/// 前导空格产生的空片段被滤掉后三元组永远凑不齐，导致每行都被跳过、监控全盲。
/// 进程名可能自带空格（如 "Google Chrome Helper"），所以取剩余全部字段。
fn parse_ps_line(line: &str) -> Option<(String, f32, f32)> {
    let mut parts = line.split_whitespace();
    let cpu = parts.next()?.parse::<f32>().ok()?;
    let mem = parts.next()?.parse::<f32>().ok()?;
    let comm = parts.collect::<Vec<_>>().join(" ");
    if comm.is_empty() {
        return None;
    }
    Some((comm, cpu, mem))
}

/// 采样一次系统状态。macOS 用 `ps -axo` 列全部进程并取 CPU/内存。
pub fn sample_system() -> Result<WatchSample> {
    let output = Command::new("ps")
        .args(["-axo", "%cpu=,%mem=,comm="])
        .output()
        .with_context(|| "failed to run ps")?;
    let text = String::from_utf8_lossy(&output.stdout);
    let mut hot = Vec::new();
    for line in text.lines() {
        let Some((comm, cpu, mem)) = parse_ps_line(line) else {
            continue;
        };
        // 只看用户进程（排除内核/自身/监控常见进程）
        if cpu >= 80.0 && !is_noise_process(&comm) {
            hot.push((comm, cpu, mem));
        }
    }
    hot.sort_by(|a, b| b.1.total_cmp(&a.1));
    hot.truncate(5);
    Ok(WatchSample {
        hot_processes: hot,
        memory_pressure: memory_used_percent(),
    })
}

/// 排除不值得报告的进程（监控工具自身、系统常驻等）。
fn is_noise_process(comm: &str) -> bool {
    let name = comm.rsplit('/').next().unwrap_or(comm);
    matches!(
        name,
        "gqy" | "ps" | "top" | "WindowServer" | "kernel_task"
            | "launchd" | "mds" | "mdworker" | "Spotlight" | "backupd" | "cloudd"
            | "VTDecoderXPCService" | "rapportd" | "opendirectoryd"
    ) || name.starts_with("zcode")
}

/// 本地判断：是否值得打扰（CPU 突增进程存在且非瞬时）。
/// 简单启发式：存在 ≥2 个高 CPU 进程，或单个 ≥150% CPU（多核满载），
/// 或系统整体内存占用超过 90%（内存吃紧）。
pub fn should_alert(sample: &WatchSample) -> bool {
    let heavy = sample.hot_processes.iter().filter(|(_, cpu, _)| *cpu >= 150.0).count();
    heavy >= 1 || sample.hot_processes.len() >= 2 || sample.memory_pressure >= 90.0
}

/// 构造主动消息（给顾清影的判断材料，不直接打扰用户）。
pub fn alert_message(sample: &WatchSample) -> String {
    let procs = sample
        .hot_processes
        .iter()
        .map(|(name, cpu, mem)| format!("{name}（CPU {cpu:.0}% / 内存 {mem:.0}%）"))
        .collect::<Vec<_>>()
        .join("、");
    format!(
        "【主动提醒】我检测到系统里 {} 的 CPU 占用异常偏高，可能卡顿或出问题。\
         请先自己判断这是否正常（比如编译/渲染/下载任务），\
         如果值得关注再主动告诉我；不要无事打扰用户。",
        procs
    )
}

/// 主动提醒的冷却期：一次编译能持续几十分钟，逐次采样都报警等于每个间隔烧一轮 LLM。
/// ponytail: 单一全局冷却够用；要按异常类型分别冷却再拆成多个 stamp 文件。
const ALERT_COOLDOWN_SECS: u64 = 30 * 60;

fn alert_stamp_file(paths: &GqyPaths) -> std::path::PathBuf {
    paths.state_dir.join("last_watch_alert")
}

/// 距上次成功提醒是否还在冷却期内。取 stamp 文件 mtime，不需要读内容。
fn in_cooldown(paths: &GqyPaths) -> bool {
    std::fs::metadata(alert_stamp_file(paths))
        .and_then(|meta| meta.modified())
        .ok()
        .and_then(|modified| modified.elapsed().ok())
        .is_some_and(|elapsed| elapsed.as_secs() < ALERT_COOLDOWN_SECS)
}

/// 主动提醒的投递结果，run_watch 据此打印真实的诊断文案（避免 409 被误报成「WebUI 未运行」）。
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum AlertOutcome {
    /// 已入队，会话将在回复结尾接续
    Delivered,
    /// 冷却期内，静默跳过
    Cooldown,
    /// WebUI 不在跑（连接失败）
    WebUiUnreachable,
    /// WebUI 在跑但没有进行中的会话，消息无处挂
    NoRunningTurn,
    /// 其他拒绝（如 401/403/500）
    Rejected,
}

/// 通过 WebUI queue API 给运行中的会话入队主动消息。
/// 需要 WebUI 在跑（默认 127.0.0.1:4096）；不在跑、或处于冷却期就静默跳过。
pub fn enqueue_alert(paths: &GqyPaths, message: &str) -> Result<AlertOutcome> {
    if in_cooldown(paths) {
        return Ok(AlertOutcome::Cooldown);
    }
    let port = std::env::var("GQY_WEB_PORT")
        .ok()
        .and_then(|value| value.parse::<u16>().ok())
        .unwrap_or(4096);
    let url = format!("http://127.0.0.1:{port}/api/queue");
    let body = serde_json::json!({ "content": message }).to_string();
    let Ok(response) = Command::new("curl")
        .args(["-s", "-o", "/dev/null", "-w", "%{http_code}", "-X", "POST", "-H", "Content-Type: application/json", "-d", &body, &url])
        .output()
    else {
        return Ok(AlertOutcome::WebUiUnreachable);
    };
    let code = String::from_utf8_lossy(&response.stdout);
    let code = code.trim();
    // 回环请求由 loopback_autofill 自动带会话令牌，本机免密；无 running turn 时返回 409
    let outcome = match code {
        "200" | "202" => {
            let file = alert_stamp_file(paths);
            if let Some(parent) = file.parent() {
                let _ = std::fs::create_dir_all(parent);
            }
            let _ = std::fs::write(&file, "");
            AlertOutcome::Delivered
        }
        "409" => AlertOutcome::NoRunningTurn,
        _ => AlertOutcome::Rejected,
    };
    Ok(outcome)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn noise_processes_are_filtered() {
        assert!(is_noise_process("/usr/bin/ps"));
        assert!(is_noise_process("WindowServer"));
        assert!(is_noise_process("zcode"));
        assert!(!is_noise_process("/usr/local/bin/cargo"));
        assert!(!is_noise_process("node"));
    }

    #[test]
    fn alert_thresholds() {
        // 单进程 150%+ CPU → 报警
        let heavy = WatchSample {
            hot_processes: vec![("node".to_string(), 180.0, 12.0)],
            memory_pressure: 50.0,
        };
        assert!(should_alert(&heavy));
        // 低 CPU → 不报警
        let calm = WatchSample {
            hot_processes: vec![("node".to_string(), 30.0, 5.0)],
            memory_pressure: 40.0,
        };
        assert!(!should_alert(&calm));
    }

    #[test]
    fn alert_message_lists_processes() {
        let sample = WatchSample {
            hot_processes: vec![("node".to_string(), 200.0, 15.0)],
            memory_pressure: 60.0,
        };
        let message = alert_message(&sample);
        assert!(message.contains("node"));
        assert!(message.contains("CPU 200%"));
    }

    /// 回归：ps 每行都有前导空格、数值间常有连续空格。旧解析用
    /// `splitn(3, is_whitespace).filter(非空)`，空片段被滤掉后三元组凑不齐 →
    /// 每行都被跳过 → 采样恒为空、should_alert 恒为 false，监控全盲。
    #[test]
    fn parses_real_ps_output_shapes() {
        // 真实格式：数值右对齐补空格，行首必有空格
        assert_eq!(
            parse_ps_line("  0.1  0.1 /sbin/launchd"),
            Some(("/sbin/launchd".to_string(), 0.1, 0.1))
        );
        // 三位数 CPU：行首无空格，字段间仍是连续空格
        assert_eq!(
            parse_ps_line("100.0   5.2 /usr/bin/rustc"),
            Some(("/usr/bin/rustc".to_string(), 100.0, 5.2))
        );
        // 进程名自带空格
        assert_eq!(
            parse_ps_line("  2.0  1.0 /Applications/Google Chrome.app/Google Chrome Helper"),
            Some((
                "/Applications/Google Chrome.app/Google Chrome Helper".to_string(),
                2.0,
                1.0
            ))
        );
        // 垃圾行不 panic
        assert_eq!(parse_ps_line(""), None);
        assert_eq!(parse_ps_line("  0.1  0.1"), None);
        assert_eq!(parse_ps_line("not a number here"), None);
    }

    /// 采样必须真的看到进程：全盲时 hot_processes 恒空，这个断言会挂。
    #[test]
    fn sampling_sees_running_processes() {
        let parsed = std::process::Command::new("ps")
            .args(["-axo", "%cpu=,%mem=,comm="])
            .output()
            .map(|out| {
                String::from_utf8_lossy(&out.stdout)
                    .lines()
                    .filter_map(parse_ps_line)
                    .count()
            })
            .unwrap_or(0);
        assert!(parsed > 10, "只解析出 {parsed} 个进程，采样疑似失效");
    }

    /// 回归：内存指标曾是所有进程 %MEM 求和（共享内存重复计数），空载机器就超过
    /// 100%。真实值必须在 0~100。
    #[test]
    fn memory_used_percent_is_a_real_percentage() {
        let used = memory_used_percent();
        assert!((0.0..=100.0).contains(&used), "已用内存 {used}% 越界");
    }

    /// 空载采样不该报警：CPU 无热点 + 内存是真实占比时，静默。
    #[test]
    fn idle_sample_stays_silent() {
        let idle = WatchSample {
            hot_processes: vec![],
            memory_pressure: memory_used_percent(),
        };
        assert!(
            !should_alert(&idle) || idle.memory_pressure >= 90.0,
            "空载却报警，内存占比 {}%",
            idle.memory_pressure
        );
    }
}
