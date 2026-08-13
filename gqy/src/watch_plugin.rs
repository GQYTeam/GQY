//! watch 管家插件：把 watch 事件源收编进插件框架，并提供打扰判断入口。
//! 事件投递前，本地规则（冷却/深夜阈值）先兜底，模型判断器再显式打分。

use crate::config::AppConfig;
use crate::paths::GqyPaths;
use crate::plugins::{BoxFuture, InterruptContext, Plugin, PluginDescriptor};
use anyhow::Result;

/// watch 管家插件。
pub struct WatchPlugin;

impl Plugin for WatchPlugin {
    fn descriptor(&self) -> PluginDescriptor {
        PluginDescriptor {
            id: "watch",
            priority: 10,
            default_enabled: true,
        }
    }

    /// 打扰判断：深夜（23-7 点）默认降权；其余交给模型判断器。
    fn judge_interrupt(
        &self,
        config: &AppConfig,
        paths: &GqyPaths,
        ctx: &InterruptContext<'_>,
    ) -> BoxFuture<'_, Result<Option<f64>>> {
        let config = config.clone();
        let paths = paths.clone();
        let source = ctx.event_source.to_string();
        let content = ctx.content.to_string();
        let hour = ctx.hour;
        Box::pin(async move {
            // 深夜 23-7 点：本地规则直接压制（除非磁盘告急之类在 content 里有强信号）
            if hour >= 23 || hour < 7 {
                let critical = content.contains("磁盘") && content.contains("剩余");
                if !critical {
                    return Ok(Some(1.0));
                }
            }
            let client = crate::llm::LlmClient::from_config(&config, &paths)?;
            let score = crate::judge::judge_interrupt_score(
                &client,
                &config,
                &paths,
                &source,
                &content,
                hour,
            )
            .await?;
            Ok(Some(score))
        })
    }
}
