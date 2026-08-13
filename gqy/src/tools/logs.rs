//! 日志查询工具（read_logs）：让顾清影自己翻运行日志/活动记录排查故障。
//! 与 WebUI 日志视图（/api/logs）同一数据源，行为一致。

use super::ToolRegistry;
use crate::config::AppConfig;
use crate::paths::GqyPaths;
use anyhow::Result;
use serde_json::{json, Value};

pub fn register(registry: &mut ToolRegistry, config: AppConfig, paths: GqyPaths) {
    registry.register(super::ToolSpec::new(
        "read_logs",
        "读取 GQY 的运行日志或活动记录（最近 N 行，可按关键词过滤）。出现错误、行为异常或需要排查原因时使用；运行日志含完整错误堆栈，活动记录含工具调用成败。",
        json!({
            "type": "object",
            "properties": {
                "kind": { "type": "string", "enum": ["run", "activity"], "description": "run = 运行日志（默认，含错误/警告），activity = 活动记录（工具调用/事件）" },
                "lines": { "type": "integer", "description": "行数，默认 100，上限 1000" },
                "query": { "type": "string", "description": "关键词过滤（如 error、工具名、模型名）" }
            }
        }),
        move |args| {
            let paths = paths.clone();
            let _ = &config;
            async move { read_logs_impl(args, &paths).await }
        },
    ));
}

async fn read_logs_impl(args: Value, paths: &GqyPaths) -> Result<String> {
    let kind = args
        .get("kind")
        .and_then(Value::as_str)
        .unwrap_or("run")
        .trim()
        .to_string();
    let limit = args
        .get("lines")
        .and_then(Value::as_u64)
        .unwrap_or(100)
        .clamp(1, 1000) as usize;
    let query = args
        .get("query")
        .and_then(Value::as_str)
        .map(str::trim)
        .filter(|value| !value.is_empty())
        .map(str::to_lowercase);

    let logs_dir = paths.logs_dir();
    let (file_path, display_name) = if kind == "activity" {
        (logs_dir.join("activity.jsonl"), "activity.jsonl".to_string())
    } else {
        let mut candidates = Vec::new();
        if let Ok(entries) = std::fs::read_dir(&logs_dir) {
            for entry in entries.flatten() {
                let name = entry.file_name().to_string_lossy().to_string();
                if name.starts_with("gqy.") && name.ends_with(".log") {
                    candidates.push((entry.path(), name));
                }
            }
        }
        candidates.sort_by(|a, b| b.1.cmp(&a.1));
        let Some((path, name)) = candidates.first() else {
            return Ok(json!({ "ok": true, "file": "", "lines": [] }).to_string());
        };
        (path.clone(), name.clone())
    };

    let Ok(content) = std::fs::read_to_string(&file_path) else {
        return Ok(json!({ "ok": true, "file": display_name, "lines": [] }).to_string());
    };
    let mut all: Vec<&str> = content.lines().collect();
    if let Some(needle) = &query {
        all.retain(|line| line.to_lowercase().contains(needle));
    }
    let tail: Vec<&str> = all.iter().rev().take(limit).copied().collect();
    let mut tail = tail;
    tail.reverse();
    Ok(json!({
        "ok": true,
        "file": display_name,
        "total": all.len(),
        "lines": tail,
    })
    .to_string())
}
