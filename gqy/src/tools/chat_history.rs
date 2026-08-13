//! 对话历史搜索：搜完整 conversation.db（含已移出上下文窗口的旧轮次）。
//! 顾清影的“翻旧账”入口——记忆库只有近期日记，完整历史在这里。

use crate::i18n::text as t;
use crate::paths::GqyPaths;
use crate::state::ConversationDb;
use crate::tools::registry::{ToolRegistry, ToolSpec};
use anyhow::{bail, Result};
use serde_json::{json, Value};

pub fn register(registry: &mut ToolRegistry, paths: GqyPaths) {
    registry.register(ToolSpec::new(
        "search_chat_history",
        t(
            "Search the full conversation history (including old turns moved out of the context window). Use when the user asks things like \"did we talk about X before\", \"do you remember I said X\", or wants to find an earlier conversation, and memory recall (recall_memories / recall_past_events) found nothing — the memory diary only covers recent days, while the complete history lives here. Also use it to verify whether something the user quotes was actually said.",
            "搜索完整对话历史（含已移出上下文窗口的旧轮次）。当用户问「我们以前聊过…吗」「你记得我之前说过…」「帮我把那次对话找出来」这类回顾性问题，而记忆检索（recall_memories / recall_past_events）没有结果时使用——记忆日记只覆盖最近几天，完整对话历史在这里。用户转述「以前说过的话」时，也可用它核对是否属实。",
        ),
        json!({
            "type": "object",
            "properties": {
                "query": { "type": "string", "description": t("Search keywords or a quote.", "关键词或原话片段。") },
                "max_results": { "type": "integer", "description": t("Optional result limit (default 5).", "可选结果数量（默认 5）。") }
            },
            "required": ["query"],
            "additionalProperties": false
        }),
        {
            let paths = paths.clone();
            move |args| {
                let paths = paths.clone();
                async move { search_chat_history(args, paths).await }
            }
        },
    ));
}

async fn search_chat_history(args: Value, paths: GqyPaths) -> Result<String> {
    let query = required_str(&args, "query")?;
    let limit = args
        .get("max_results")
        .and_then(Value::as_u64)
        .unwrap_or(5)
        .clamp(1, 50) as usize;
    let db = ConversationDb::open(&paths.state_dir)?;
    let hits = db.search_history(query, limit)?;
    let results: Vec<Value> = hits
        .iter()
        .map(|hit| {
            json!({
                "time": hit.timestamp,
                "user": truncate_chars(&hit.user_content, 240),
                "assistant": truncate_chars(&hit.assistant_content, 360),
            })
        })
        .collect();
    Ok(json!({ "ok": true, "query": query, "results": results }).to_string())
}

fn required_str<'a>(args: &'a Value, name: &str) -> Result<&'a str> {
    let value = args
        .get(name)
        .and_then(Value::as_str)
        .unwrap_or_default()
        .trim();
    if value.is_empty() {
        bail!("{}: {name}", t("required argument missing", "缺少必需参数"));
    }
    Ok(value)
}

fn truncate_chars(text: &str, max_chars: usize) -> String {
    if text.chars().count() <= max_chars {
        return text.to_string();
    }
    format!(
        "{}...",
        text.chars().take(max_chars.saturating_sub(3)).collect::<String>()
    )
}
