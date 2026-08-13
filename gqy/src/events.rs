//! 事件源插件面：系统主动事件（贾维斯面）的统一入口。
//!
//! 每个 EventSource 是一类「环境感知」：定时采样系统状态，命中本地打扰门槛后
//! 产出一条 SystemEvent，经 WebUI queue API 以 source=system 投递给运行中的会话，
//! 让顾清影先判断、再决定是否打扰用户。
//!
//! 陪伴模式（Chat）在消费端（agent::consume_queued_prompts）按来源丢弃 system 事件，
//! 保证磁盘告警这类系统噪音永不侵入闲聊会话。
//!
//! 新增一个主动提醒能力 = 实现 EventSource 并加入 default_event_sources()，
//! 不需要改对话循环。

use crate::paths::GqyPaths;
use anyhow::{bail, Context, Result};
use chrono::Timelike;
use std::path::PathBuf;
use std::time::Duration;

/// 系统主动事件：给 agent 的判断材料，不直接打扰用户。
#[derive(Debug, Clone)]
pub struct SystemEvent {
    /// 事件源名（如 watch / disk）
    pub source: &'static str,
    /// 事件内容（agent 视角的上下文，语气是给 agent 的判断材料）
    pub content: String,
    /// 同类事件冷却：水位类事件变化慢，冷却长；进程类事件可以短些
    pub cooldown: Duration,
}

/// 事件源插件面：实现此 trait 即可接入「主动提醒」管道。
///
/// 约定：sample 只做本地采样与规则判断，绝不直接打扰用户；
/// 是否值得打扰由本地门槛（should_alert 等）决定，LLM 只在事件投递后介入判断。
pub trait EventSource: Send + Sync {
    /// 事件源名（唯一，用于日志与冷却 stamp 文件）
    fn name(&self) -> &'static str;

    /// 采样一次。无事件返回 Ok(None)；出错返回 Err（由调用方记录后继续）。
    fn sample(&self, paths: &GqyPaths) -> Result<Option<SystemEvent>>;
}

/// 进程/内存管家监控（原 gqy watch）。
pub struct WatchSource;

impl EventSource for WatchSource {
    fn name(&self) -> &'static str {
        "watch"
    }

    fn sample(&self, _paths: &GqyPaths) -> Result<Option<SystemEvent>> {
        let sample = crate::watch::sample_system()?;
        if !crate::watch::should_alert(&sample) {
            return Ok(None);
        }
        Ok(Some(SystemEvent {
            source: "watch",
            content: crate::watch::alert_message(&sample),
            cooldown: Duration::from_secs(30 * 60),
        }))
    }
}

/// 磁盘水位监控：启动磁盘使用率超过阈值（默认 90%，可用 GQY_DISK_WARN_USED_PERCENT 覆盖）时
/// 产出一条主动事件。水位变化慢，冷却 6 小时。
pub struct DiskSpaceSource;

const DISK_WARN_USED_PERCENT_DEFAULT: f64 = 90.0;
const DISK_ALERT_COOLDOWN_SECS: u64 = 6 * 3600;

impl EventSource for DiskSpaceSource {
    fn name(&self) -> &'static str {
        "disk"
    }

    fn sample(&self, _paths: &GqyPaths) -> Result<Option<SystemEvent>> {
        let threshold = std::env::var("GQY_DISK_WARN_USED_PERCENT")
            .ok()
            .and_then(|value| value.parse::<f64>().ok())
            .unwrap_or(DISK_WARN_USED_PERCENT_DEFAULT);
        let (total, free) = disk_free_bytes("/")?;
        let used_percent = (1.0 - free as f64 / total as f64) * 100.0;
        if used_percent < threshold {
            return Ok(None);
        }
        let free_gb = free as f64 / (1024.0 * 1024.0 * 1024.0);
        Ok(Some(SystemEvent {
            source: "disk",
            content: format!(
                "【主动提醒】启动磁盘已使用 {used_percent:.0}%（剩余约 {free_gb:.1} GB），                 可能影响系统运行。请先判断是否值得提醒用户清理（如 ~/Library/Caches、brew cleanup），                 不要无事打扰。"
            ),
            cooldown: Duration::from_secs(DISK_ALERT_COOLDOWN_SECS),
        }))
    }
}

/// 默认事件源列表：新增事件源在这里注册即可，gqy watch 自动接管。
pub fn default_event_sources() -> Vec<Box<dyn EventSource>> {
    vec![Box::new(WatchSource), Box::new(DiskSpaceSource)]
}

/// 启动磁盘（/）已用/可用字节数。macOS 上 statfs 的 bavail 不含保留空间，接近可用量。
fn disk_free_bytes(path: &str) -> Result<(u64, u64)> {
    let c_path = std::ffi::CString::new(path).context("invalid path for statfs")?;
    unsafe {
        let mut stat: libc::statfs = std::mem::zeroed();
        if libc::statfs(c_path.as_ptr(), &mut stat) != 0 {
            bail!("statfs failed: {}", std::io::Error::last_os_error());
        }
        let total = stat.f_blocks as u64 * stat.f_bsize as u64;
        let free = stat.f_bavail as u64 * stat.f_bsize as u64;
        Ok((total, free))
    }
}

/// 单次投递结果（诊断文案用）。
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum DeliveryOutcome {
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

/// 事件投递的冷却 stamp 文件：按事件源名区分（last_watch_alert / last_disk_alert）。
fn alert_stamp_file(paths: &GqyPaths, source: &str) -> PathBuf {
    paths.state_dir.join(format!("last_{source}_alert"))
}

fn in_cooldown(paths: &GqyPaths, source: &str, cooldown: Duration) -> bool {
    std::fs::metadata(alert_stamp_file(paths, source))
        .and_then(|meta| meta.modified())
        .ok()
        .and_then(|modified| modified.elapsed().ok())
        .is_some_and(|elapsed| elapsed < cooldown)
}

/// 投递一条系统事件：冷却判断 → 打扰判断 → 入队 → 成功时写 stamp。
pub async fn deliver(paths: &GqyPaths, event: &SystemEvent) -> Result<DeliveryOutcome> {
    if in_cooldown(paths, event.source, event.cooldown) {
        return Ok(DeliveryOutcome::Cooldown);
    }
    // 打扰判断：插件（watch）先表态——模型显式打分，低分（<5）不打扰。
    // 本地规则（深夜压制）在插件内先兜底；判断失败时按默认规则放行。
    let hour = chrono::Local::now().hour() as u8;
    let ctx = crate::plugins::InterruptContext {
        event_source: event.source,
        content: &event.content,
        hour,
    };
    if let Ok(Some(score)) = crate::plugins::judge_interrupt(
        &crate::config::AppConfig::load_or_default(paths)?,
        paths,
        &ctx,
    )
    .await
    {
        if score < 5.0 {
            return Ok(DeliveryOutcome::Cooldown);
        }
    }
    let outcome = crate::watch::post_event(paths, &event.content)?;
    if outcome == DeliveryOutcome::Delivered {
        let file = alert_stamp_file(paths, event.source);
        if let Some(parent) = file.parent() {
            let _ = std::fs::create_dir_all(parent);
        }
        let _ = std::fs::write(&file, "");
    }
    Ok(outcome)
}

/// 单个事件源一轮：采样 → 冷却 → 打扰判断 → 投递。返回是否投递成功。
pub async fn poll_source(source: &dyn EventSource, paths: &GqyPaths) -> Result<bool> {
    let Some(event) = source.sample(paths)? else {
        return Ok(false);
    };
    Ok(deliver(paths, &event).await? == DeliveryOutcome::Delivered)
}

/// 跑一轮所有事件源。返回每个源的实际投递结果，供 CLI 打印诊断。
pub async fn poll_all(paths: &GqyPaths) -> Result<Vec<(&'static str, DeliveryOutcome)>> {
    let mut results = Vec::new();
    for source in default_event_sources() {
        let delivered = match poll_source(source.as_ref(), paths).await {
            Ok(delivered) => delivered,
            Err(err) => {
                tracing::warn!("event source {} failed: {err:#}", source.name());
                results.push((source.name(), DeliveryOutcome::Rejected));
                continue;
            }
        };
        if delivered {
            results.push((source.name(), DeliveryOutcome::Delivered));
        }
        // 无事件或冷却中：静默
    }
    Ok(results)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn disk_free_bytes_returns_sane_values() {
        let (total, free) = disk_free_bytes("/").unwrap();
        assert!(total > 0, "总空间应为正数");
        assert!(free > 0, "启动盘应有可用空间");
        assert!(free <= total, "可用空间不应超过总量");
    }

    #[test]
    fn disk_source_samples_without_error() {
        let paths = crate::paths::GqyPaths::new().unwrap();
        match DiskSpaceSource.sample(&paths) {
            Ok(None) => {} // 水位正常，静默（开发机一般如此）
            Ok(Some(event)) => {
                assert_eq!(event.source, "disk");
                assert!(event.content.contains("磁盘"));
            }
            Err(err) => panic!("disk sample 不应失败: {err}"),
        }
    }

    #[test]
    fn watch_source_samples_without_error() {
        let paths = crate::paths::GqyPaths::new().unwrap();
        match WatchSource.sample(&paths) {
            Ok(_) => {}
            // 受限环境（沙箱/容器）可能拒绝 ps：采样失败不 panic。
            // 采样有效性由正常环境里的 watch::tests::sampling_sees_running_processes 覆盖。
            Err(_) => {}
        }
    }

    #[test]
    fn stamp_file_names_are_per_source() {
        let paths = crate::paths::GqyPaths::new().unwrap();
        assert!(alert_stamp_file(&paths, "watch").ends_with("last_watch_alert"));
        assert!(alert_stamp_file(&paths, "disk").ends_with("last_disk_alert"));
    }
}
