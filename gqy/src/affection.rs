//! 好感度系统（借鉴 Miyu real_context/affection，按 GQY 桌面形态裁剪）。
//!
//! 每轮对话后自动更新主人与顾清影的关系评分（规则式 + 后续接判断器），
//! 评分、印象 note、标签随上下文注入，让「关系」成为她行为的一部分。

use crate::config::AppConfig;
use crate::paths::GqyPaths;
use crate::plugins::{BoxFuture, Plugin, PluginDescriptor};
use anyhow::Result;
use chrono::Local;
use serde::{Deserialize, Serialize};
use serde_json::{json, Value};
use std::sync::RwLock;

#[derive(Clone, Debug, Serialize, Deserialize)]
#[serde(default)]
pub struct AffectionProfile {
    pub version: u32,
    pub user_id: String,
    pub sender_name: String,
    pub score: f64,
    pub note: String,
    pub tags: Vec<String>,
    pub message_count: u64,
    pub direct_interaction_count: u64,
    pub daily_date: String,
    pub daily_gain: f64,
    pub daily_loss: f64,
    pub last_interaction_at: i64,
    pub created_at: i64,
}

impl Default for AffectionProfile {
    fn default() -> Self {
        Self {
            version: 1,
            user_id: "default".to_string(),
            sender_name: "主人".to_string(),
            score: 50.0,
            note: String::new(),
            tags: Vec::new(),
            message_count: 0,
            direct_interaction_count: 0,
            daily_date: String::new(),
            daily_gain: 0.0,
            daily_loss: 0.0,
            last_interaction_at: 0,
            created_at: 0,
        }
    }
}

fn profile_path(paths: &GqyPaths) -> std::path::PathBuf {
    paths.data_dir.join("affection.json")
}

pub fn level_label(score: f64) -> &'static str {
    if score >= 85.0 {
        "很亲近"
    } else if score >= 70.0 {
        "亲近"
    } else if score >= 55.0 {
        "熟悉"
    } else if score >= 40.0 {
        "一般"
    } else if score >= 25.0 {
        "冷淡"
    } else {
        "生疏"
    }
}

pub fn load_profile(paths: &GqyPaths) -> AffectionProfile {
    std::fs::read_to_string(profile_path(paths))
        .ok()
        .and_then(|text| serde_json::from_str(&text).ok())
        .unwrap_or_default()
}

pub fn save_profile(paths: &GqyPaths, profile: &AffectionProfile) -> Result<()> {
    if let Some(parent) = profile_path(paths).parent() {
        std::fs::create_dir_all(parent)?;
    }
    let text = serde_json::to_string_pretty(profile)?;
    std::fs::write(profile_path(paths), text)?;
    Ok(())
}

/// 规则式更新：按输入特征小幅增减，日增减封顶，晚睡/低落倾向微增陪伴分。
fn apply_rule_update(profile: &mut AffectionProfile, input: &str, output: &str) {
    let today = Local::now().format("%Y-%m-%d").to_string();
    if profile.daily_date != today {
        profile.daily_date = today;
        profile.daily_gain = 0.0;
        profile.daily_loss = 0.0;
    }
    profile.message_count += 1;
    profile.last_interaction_at = Local::now().timestamp();

    let text_len = input.chars().count();
    let mut gain = 0.2_f64;
    // 亲昵/倾诉/深夜陪伴 → 多加分；敷衍/短促指令 → 少加分
    if text_len >= 40 {
        gain += 0.2;
    }
    let lower = input.to_lowercase();
    if ["谢谢", "辛苦", "晚安", "想你", "喜欢", "爱你", "抱抱"].iter().any(|word| lower.contains(word)) {
        gain += 0.5;
    }
    if lower.contains("晚安") || lower.contains("睡") {
        gain += 0.3;
    }
    // 短促纯指令（<8 字且无情绪词）少加分
    if text_len < 8 && !lower.contains("谢谢") && !lower.contains("晚安") {
        gain = 0.1;
    }
    // 输出质量惩罚：空回复/明显敷衍
    if output.trim().is_empty() || output.contains("（本轮思考完成") {
        gain -= 0.2;
    }
    let daily_gain_cap = 3.0;
    let daily_loss_cap = 2.0;
    if gain > 0.0 {
        profile.daily_gain = (profile.daily_gain + gain).min(daily_gain_cap);
    } else {
        profile.daily_loss = (profile.daily_loss - gain).min(daily_loss_cap);
    }
    profile.score = (profile.score + gain).clamp(0.0, 100.0);
}

/// 好感度插件：每轮对话完成后自动更新。
pub struct AffectionPlugin;

impl Plugin for AffectionPlugin {
    fn descriptor(&self) -> PluginDescriptor {
        PluginDescriptor {
            id: "affection",
            priority: 100,
            default_enabled: true,
        }
    }

    fn register_tools(&self, registry: &mut crate::tools::ToolRegistry, paths: &GqyPaths) {
        let paths = paths.clone();
        registry.register(crate::tools::ToolSpec::new(
            "affection_status",
            "查询主人对你的好感度（分数/等级/印象 note/标签/日增减）。在关心关系状态或情绪场景使用。",
            serde_json::json!({
                "type": "object",
                "properties": {},
                "additionalProperties": false
            }),
            move |_args| {
                let paths = paths.clone();
                Box::pin(async move {
                    let profile = load_profile(&paths);
                    Ok(json!({
                        "ok": true,
                        "score": profile.score,
                        "level": level_label(profile.score),
                        "note": profile.note,
                        "tags": profile.tags,
                        "interactions": profile.message_count,
                        "daily_gain": profile.daily_gain,
                        "daily_loss": profile.daily_loss,
                    })
                    .to_string())
                })
            },
        ));
    }

    fn after_turn(
        &self,
        _config: &AppConfig,
        paths: &GqyPaths,
        input: &str,
        output: &str,
    ) -> BoxFuture<'_, Result<()>> {
        let paths = paths.clone();
        let input = input.to_string();
        let output = output.to_string();
        Box::pin(async move {
            let mut profile = load_profile(&paths);
            apply_rule_update(&mut profile, &input, &output);
            save_profile(&paths, &profile)?;
            Ok(())
        })
    }
}

/// 注入用：好感度上下文段（系统提示词尾部）。
pub fn context_block(paths: &GqyPaths) -> String {
    let profile = load_profile(paths);
    let tags = if profile.tags.is_empty() {
        String::new()
    } else {
        format!(" · 标签：{}", profile.tags.join("、"))
    };
    let note = if profile.note.trim().is_empty() {
        String::new()
    } else {
        format!(" · 印象：{}", profile.note.trim())
    };
    format!(
        "<affection score=\"{:.0}\" level=\"{}\" interactions=\"{}\"{}{}/>",
        profile.score,
        level_label(profile.score),
        profile.message_count,
        tags,
        note
    )
}

/// WebUI 快照。
pub fn snapshot(paths: &GqyPaths) -> Value {
    let profile = load_profile(paths);
    json!({
        "ok": true,
        "score": profile.score,
        "level": level_label(profile.score),
        "note": profile.note,
        "tags": profile.tags,
        "message_count": profile.message_count,
        "daily_gain": profile.daily_gain,
        "daily_loss": profile.daily_loss,
        "created_at": profile.created_at,
    })
}

// 供插件模块引用的 RwLock 占位（避免未使用告警）
#[allow(dead_code)]
pub(crate) fn _lock() -> &'static RwLock<()> {
    static LOCK: RwLock<()> = RwLock::new(());
    &LOCK
}
