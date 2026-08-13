//! 打扰判断器（借鉴 Miyu real_context/judge 的五维评分思想，按 GQY 场景裁剪）。
//!
//! 系统主动事件（磁盘、进程提醒）到达时，本地规则先兜底，再由模型
//! 对「当前是否适合打扰」显式打分——替代「深夜不打扰」这类纯提示词软约束。

use crate::config::AppConfig;
use crate::llm::{ChatMessage, LlmClient};
use crate::paths::GqyPaths;
use anyhow::Result;
use serde_json::json;

const JUDGE_PROMPT: &str = r#"你是打扰判断器。给定一个系统主动提醒（磁盘水位/进程异常等）与当前时间，判断「现在是否适合向用户推送这条提醒」。请从五个维度各打 0-10 分，并给出 should_interrupt（0-10，≥6 认为值得打扰）。只输出 JSON。

维度：
- relevance：提醒与用户当前可能的处境相关度
- urgency：问题是否紧急（磁盘满/崩溃风险 = 高，一般进程波动 = 低）
- timing：当前时间是否适合打扰（深夜 23-7 点大幅降分）
- noise：会不会是无谓打扰（用户可能正在专注）
- continuity：是否最近刚提醒过同类（重复 = 降分）

输出格式：{"relevance":0,"urgency":0,"timing":0,"noise":0,"continuity":0,"should_interrupt":0,"reason":"简短原因"}"#;

/// 五维评分判断：返回 should_interrupt（0-10）。
pub async fn judge_interrupt_score(
    client: &LlmClient,
    config: &AppConfig,
    _paths: &GqyPaths,
    source: &str,
    content: &str,
    hour: u8,
) -> Result<f64> {
    let message = ChatMessage::plain(
        "user",
        format!(
            "提醒来源：{source}\n提醒内容：{content}\n当前时间：{}点\n请按提示词要求输出 JSON。",
            hour
        ),
    );
    let messages = vec![
        ChatMessage::system(JUDGE_PROMPT.to_string()),
        message,
    ];
    let result = client
        .chat_stream(messages, Vec::new(), |_| Ok(()))
        .await?;
    // 解析 JSON（容忍模型输出前后杂讯）
    let text = result.content.trim();
    let start = text.find('{').unwrap_or(0);
    let end = text.rfind('}').map(|i| i + 1).unwrap_or(text.len());
    let parsed: serde_json::Value =
        serde_json::from_str(&text[start..end]).unwrap_or_else(|_| json!({}));
    let score = parsed
        .get("should_interrupt")
        .and_then(serde_json::Value::as_f64)
        .unwrap_or(0.0);
    let score = score.clamp(0.0, 10.0);
    let _ = config; // 配置预留（阈值等）
    Ok(score)
}
