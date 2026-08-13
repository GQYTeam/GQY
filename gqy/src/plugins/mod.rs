//! 插件生命周期协议（借鉴 Miyu 的 PlatformPlugin 架构，按 GQY 桌面形态裁剪）。
//!
//! 每个插件 = 一个 Plugin 实现：声明描述符（id/优先级/默认开关），
//! 在对话生命周期里挂钩子（工具注册 / 轮次完成 / 打扰判断）。
//! 系统能力（watch 管家、affection 好感度）都收编为插件，
//! 新能力 = 实现一个 Plugin 注册一行，不改 agent 循环。

use crate::config::AppConfig;
use crate::paths::GqyPaths;
use crate::tools::ToolRegistry;
use anyhow::Result;
use std::future::Future;
use std::pin::Pin;

/// 插件描述符：注册身份。
pub struct PluginDescriptor {
    pub id: &'static str,
    /// 钩子执行优先级（大者先）
    pub priority: i32,
    pub default_enabled: bool,
}

pub type BoxFuture<'a, T> = Pin<Box<dyn Future<Output = T> + Send + 'a>>;

/// 打扰判断上下文：判断器（judge）和事件投递钩子共用。
pub struct InterruptContext<'a> {
    pub event_source: &'a str,
    pub content: &'a str,
    pub hour: u8,
}

/// 插件 trait：对话生命周期的挂载点。
pub trait Plugin: Send + Sync {
    fn descriptor(&self) -> PluginDescriptor;

    /// 注册自己的工具（可选）。
    fn register_tools(&self, _registry: &mut ToolRegistry, _paths: &GqyPaths) {}

    /// 每轮对话完成后调用（可选）：好感度更新、统计等。
    fn after_turn(
        &self,
        _config: &AppConfig,
        _paths: &GqyPaths,
        _input: &str,
        _output: &str,
    ) -> BoxFuture<'_, Result<()>> {
        Box::pin(async { Ok(()) })
    }

    /// 系统事件打扰判断（可选）：返回 None = 不表态（走默认规则）；
    /// Some(score) = 判断器给出的打扰倾向（0-10，越高越该打扰）。
    fn judge_interrupt(
        &self,
        _config: &AppConfig,
        _paths: &GqyPaths,
        _ctx: &InterruptContext<'_>,
    ) -> BoxFuture<'_, Result<Option<f64>>> {
        Box::pin(async { Ok(None) })
    }
}

/// 注册表：全部插件（顺序即注册顺序）。
pub fn default_plugins() -> Vec<Box<dyn Plugin>> {
    vec![
        // 好感度：轮次完成后自动更新
        Box::new(crate::affection::AffectionPlugin),
        // watch 管家：事件打扰判断（judge 决策入口）
        Box::new(crate::watch_plugin::WatchPlugin),
    ]
}

/// 收集全部插件的工具注册。
pub fn register_all_plugin_tools(registry: &mut ToolRegistry, paths: &GqyPaths) {
    for plugin in default_plugins() {
        plugin.register_tools(registry, paths);
    }
}

/// 轮次完成钩子：通知所有插件。
pub async fn run_after_turn(config: &AppConfig, paths: &GqyPaths, input: &str, output: &str) {
    for plugin in default_plugins() {
        if let Err(err) = plugin.after_turn(config, paths, input, output).await {
            tracing::debug!(plugin = plugin.descriptor().id, error = %err, "plugin after_turn failed");
        }
    }
}

/// 打扰判断：让插件表态，返回最高分（None = 无插件表态）。
pub async fn judge_interrupt(
    config: &AppConfig,
    paths: &GqyPaths,
    ctx: &InterruptContext<'_>,
) -> Result<Option<f64>> {
    let mut best: Option<f64> = None;
    for plugin in default_plugins() {
        if let Some(score) = plugin.judge_interrupt(config, paths, ctx).await? {
            best = Some(best.map_or(score, |current| current.max(score)));
        }
    }
    Ok(best)
}
