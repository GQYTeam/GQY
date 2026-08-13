//! QQ 会话查看工具（qq_conversations）：让顾清影在终端/WebUI 主通道里
//! 查看 QQ 平台上的会话列表与对话内容——主人不在 QQ 侧时也能知道
//! 群里/私聊发生了什么，可主动跟进或转告。

use super::ToolRegistry;
use crate::config::AppConfig;
use crate::paths::GqyPaths;
use crate::state::StateStore;
use anyhow::Result;
use serde_json::{json, Value};

pub fn register(registry: &mut ToolRegistry, config: AppConfig, paths: GqyPaths) {
    registry.register(super::ToolSpec::new(
        "qq_conversations",
        "查看 QQ 平台上的会话（channel=qq）：列会话列表或读取某个会话的对话内容。主人想了解 QQ 那边发生了什么、或你需要跟进某个 QQ 会话时使用。",
        json!({
            "type": "object",
            "properties": {
                "action": { "type": "string", "enum": ["list", "read"], "description": "list = 列出所有 QQ 会话（默认）；read = 读取某会话内容" },
                "conversation_id": { "type": "string", "description": "read 时必填：会话 id（list 结果里的 conversation_id，形如 qq-p-<QQ> 或 qq-g-<群号>）" },
                "limit": { "type": "integer", "description": "最多读几条轮次，默认 10，上限 30" }
            }
        }),
        move |args| {
            let paths = paths.clone();
            let _ = &config;
            async move { qq_conversations_impl(args, &paths).await }
        },
    ));
}

async fn qq_conversations_impl(args: Value, paths: &GqyPaths) -> Result<String> {
    let action = args.get("action").and_then(Value::as_str).unwrap_or("list").trim().to_string();
    let state = StateStore::new(paths)?;
    match action.as_str() {
        "read" => {
            let conversation_id = args.get("conversation_id").and_then(Value::as_str).unwrap_or("").trim();
            if conversation_id.is_empty() {
                return Ok("请提供 conversation_id（先用 action=list 查看有哪些会话）。".to_string());
            }
            let limit = args.get("limit").and_then(Value::as_u64).unwrap_or(10).clamp(1, 30) as usize;
            let turns = state.load_turns_for_conversation("qq", conversation_id)?;
            if turns.is_empty() {
                return Ok(format!("会话 {conversation_id} 暂无记录。"));
            }
            let mut lines = Vec::new();
            lines.push(format!("会话 {conversation_id}（共 {} 轮，显示最近 {limit} 轮）", turns.len()));
            for turn in turns.iter().rev().take(limit).rev() {
                let time = turn.user_timestamp.trim();
                let user = first_line(&turn.user_content);
                lines.push(format!("[用户 {time}] {user}"));
                if !turn.assistant_content.trim().is_empty() {
                    lines.push(format!("[清影] {}", first_line(&turn.assistant_content)));
                }
                if turn.token_total > 0 {
                    if let Some(last) = lines.last_mut() {
                        last.push_str(&format!("（{} tokens）", turn.token_total));
                    }
                }
            }
            Ok(lines.join("\n"))
        }
        _ => {
            let summaries = state.conversation_summaries_for_channel("qq")?;
            if summaries.is_empty() {
                return Ok("QQ 平台暂无会话记录（还没有 QQ 消息进来，或 channel 不是 qq）。".to_string());
            }
            let mut lines = Vec::new();
            lines.push(format!("QQ 平台共 {} 个会话：", summaries.len()));
            for s in summaries {
                let title = if s.conversation_id.starts_with("qq-g-") {
                    format!("群聊 {}", s.conversation_id.trim_start_matches("qq-g-"))
                } else if s.conversation_id.starts_with("qq-p-") {
                    format!("私聊 QQ {}", s.conversation_id.trim_start_matches("qq-p-"))
                } else {
                    s.conversation_id.clone()
                };
                let when = s.timestamp.as_deref().unwrap_or("");
                lines.push(format!(
                    "- {title}（id: {}，{} 轮，最近 {when}）\n  {}",
                    s.conversation_id, s.turn_count, s.snippet
                ));
            }
            lines.push("读取某会话内容：action=read + conversation_id=<id>。".to_string());
            Ok(lines.join("\n"))
        }
    }
}

fn first_line(text: &str) -> String {
    // 剥掉注入的发送者上下文前缀（[QQ 私聊/群聊] …），让对话内容更干净
    let text = text.trim();
    let text = if let Some(idx) = text.find("\n") {
        let first = text[..idx].trim();
        let rest = text[idx..].trim();
        if first.starts_with("[QQ ") && first.ends_with("。") {
            rest
        } else {
            text
        }
    } else {
        text
    };
    text.lines().next().unwrap_or("").trim().to_string()
}

#[cfg(test)]
mod tests {
    use super::first_line;

    #[test]
    fn first_line_strips_sender_context() {
        assert_eq!(
            first_line("[QQ 私聊] 发消息的人 老朋友（QQ 555111）。\n你好呀"),
            "你好呀"
        );
        assert_eq!(
            first_line("[QQ 群聊] 群「夜猫子之家」（群号 777），发消息的人 A。\n在吗"),
            "在吗"
        );
        // 无前缀的普通文本原样
        assert_eq!(first_line("普通消息"), "普通消息");
        // 多行取第一行
        assert_eq!(first_line("第一行\n第二行"), "第一行");
    }
}
