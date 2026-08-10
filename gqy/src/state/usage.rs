use crate::config::{BillingRate, UsageBillingConfig};
use crate::llm::Usage;
use anyhow::Result;
use chrono::{Datelike, TimeZone};
use serde::{Deserialize, Serialize};
use std::collections::HashMap;
use std::io::Write;
use std::path::Path;

#[derive(Default, Serialize, Deserialize)]
struct UsageState {
    requests: u64,
    prompt_tokens: u64,
    completion_tokens: u64,
    total_tokens: u64,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    last_usage: Option<Usage>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    last_conversation_usage: Option<Usage>,
}

#[allow(dead_code)]
#[derive(Debug, Clone, Default)]
pub struct UsageSnapshot {
    pub requests: u64,
    pub prompt_tokens: u64,
    pub completion_tokens: u64,
    pub total_tokens: u64,
    pub last_usage: Option<Usage>,
    pub last_conversation_usage: Option<Usage>,
}

impl From<UsageState> for UsageSnapshot {
    fn from(state: UsageState) -> Self {
        let last_conversation_usage = state
            .last_conversation_usage
            .clone()
            .or_else(|| state.last_usage.clone());
        Self {
            requests: state.requests,
            prompt_tokens: state.prompt_tokens,
            completion_tokens: state.completion_tokens,
            total_tokens: state.total_tokens,
            last_usage: state.last_usage,
            last_conversation_usage,
        }
    }
}

pub fn add_usage(path: &Path, usage: &Usage) -> Result<()> {
    add_usage_with_scope(path, usage, true)
}

pub fn add_auxiliary_usage(path: &Path, usage: &Usage) -> Result<()> {
    add_usage_with_scope(path, usage, false)
}

fn add_usage_with_scope(path: &Path, usage: &Usage, is_conversation: bool) -> Result<()> {
    let mut state = if path.exists() {
        let raw = std::fs::read_to_string(path)?;
        serde_json::from_str(&raw).unwrap_or_default()
    } else {
        UsageState::default()
    };
    state.requests += 1;
    state.prompt_tokens += usage.prompt_tokens;
    state.completion_tokens += usage.completion_tokens;
    state.total_tokens += usage.effective_total_tokens();
    state.last_usage = Some(usage.clone());
    if is_conversation {
        state.last_conversation_usage = Some(usage.clone());
    }
    atomic_write(path, &state)?;
    Ok(())
}

/// 临时文件 + rename 原子落盘：避免 CLI 与 WebUI 并发读改写时留下半截文件。
fn atomic_write(path: &Path, state: &UsageState) -> Result<()> {
    let content = format!("{}\n", serde_json::to_string_pretty(state)?);
    if let Some(parent) = path.parent() {
        std::fs::create_dir_all(parent)?;
    }
    let temp = tempfile::NamedTempFile::new_in(
        path.parent()
            .ok_or_else(|| anyhow::anyhow!("usage path has no parent: {}", path.display()))?,
    )?;
    std::fs::write(temp.path(), content)?;
    temp.persist(path)?;
    Ok(())
}

pub fn snapshot(path: &Path) -> Result<UsageSnapshot> {
    if !path.exists() {
        return Ok(UsageSnapshot::default());
    }
    let raw = std::fs::read_to_string(path)?;
    let state = serde_json::from_str::<UsageState>(&raw).unwrap_or_default();
    Ok(state.into())
}

pub fn clear_last_usage(path: &Path) -> Result<()> {
    if !path.exists() {
        return Ok(());
    }
    let raw = std::fs::read_to_string(path)?;
    let mut state = serde_json::from_str::<UsageState>(&raw).unwrap_or_default();
    state.last_usage = None;
    state.last_conversation_usage = None;
    atomic_write(path, &state)?;
    Ok(())
}

// ─────────────────────────── 用量历史（贡献图数据源） ───────────────────────────
// usage-history.jsonl 每行一条调用记录，append-only（O_APPEND 单行写入原子），
// 由 WebUI「用量」视图按日/按模型聚合展示。

#[derive(Default, Serialize, Deserialize)]
struct UsageRecord {
    #[serde(default)]
    ts: i64,
    #[serde(default)]
    provider: String,
    #[serde(default)]
    model: String,
    #[serde(default)]
    prompt: u64,
    #[serde(default)]
    completion: u64,
    #[serde(default)]
    total: u64,
    #[serde(default)]
    aux: bool,
    /// 命中缓存读取的输入 token（Anthropic cache_read / DeepSeek cached_tokens 等）
    #[serde(default, skip_serializing_if = "Option::is_none")]
    cache_read: Option<u64>,
    /// 本次新建缓存写入的输入 token
    #[serde(default, skip_serializing_if = "Option::is_none")]
    cache_creation: Option<u64>,
}

/// 追加一条调用记录（token 明细），供贡献图/模型统计使用。
pub fn record_usage(
    path: &Path,
    usage: &Usage,
    provider_id: &str,
    model: &str,
    auxiliary: bool,
) -> Result<()> {
    record_usage_at(path, usage, provider_id, model, auxiliary, chrono::Utc::now().timestamp())
}

fn record_usage_at(
    path: &Path,
    usage: &Usage,
    provider_id: &str,
    model: &str,
    auxiliary: bool,
    ts: i64,
) -> Result<()> {
    if let Some(parent) = path.parent() {
        std::fs::create_dir_all(parent)?;
    }
    let record = UsageRecord {
        ts,
        provider: provider_id.to_string(),
        model: model.to_string(),
        prompt: usage.prompt_tokens,
        completion: usage.completion_tokens,
        total: usage.effective_total_tokens(),
        aux: auxiliary,
        cache_read: usage.cache_read_input_tokens,
        cache_creation: usage.cache_creation_input_tokens,
    };
    let mut file = std::fs::OpenOptions::new()
        .create(true)
        .append(true)
        .open(path)?;
    writeln!(file, "{}", serde_json::to_string(&record)?)?;
    Ok(())
}

#[derive(Default, Serialize)]
pub struct UsageAggregate {
    pub requests: u64,
    pub prompt_tokens: u64,
    pub completion_tokens: u64,
    pub total_tokens: u64,
    pub cost: f64,
}

#[derive(Serialize)]
pub struct DailyUsage {
    pub date: String,
    pub tokens: u64,
    pub requests: u64,
    /// 「主动消耗」token（不含缓存读取）；实际计费 = 未命中部分输入 + 输出 + 缓存读取
    pub prompt: u64,
    pub completion: u64,
    pub cache_read: u64,
    pub cost: f64,
}

#[derive(Serialize)]
pub struct ModelUsage {
    pub provider_id: String,
    pub model: String,
    pub requests: u64,
    pub prompt_tokens: u64,
    pub completion_tokens: u64,
    pub total_tokens: u64,
    pub cache_read_tokens: u64,
    pub cost: f64,
}

#[derive(Serialize)]
pub struct UsageBilling {
    pub default: BillingRate,
    pub providers: HashMap<String, BillingRate>,
}

#[derive(Serialize)]
pub struct UsageStats {
    pub total: UsageAggregate,
    pub today: UsageAggregate,
    pub this_week: UsageAggregate,
    pub this_month: UsageAggregate,
    /// 最近 365 天（含今天），无记录的日子 tokens/requests 为 0
    pub daily: Vec<DailyUsage>,
    /// 按 提供方/模型 聚合，按总 token 降序
    pub models: Vec<ModelUsage>,
    /// 计费单价配置
    pub billing: UsageBilling,
}

/// 聚合全部历史记录（上限防呆：只读最近 20 万行）。
/// `billing` 用于计算每个模型/每日的费用。
pub fn usage_stats(path: &Path, billing: &UsageBillingConfig) -> Result<UsageStats> {
    use std::io::{BufRead, BufReader};

    let mut records: Vec<UsageRecord> = Vec::new();
    if path.exists() {
        let file = std::fs::File::open(path)?;
        let reader = BufReader::new(file).lines();
        for line in reader {
            let line = line?;
            if line.trim().is_empty() {
                continue;
            }
            if let Ok(record) = serde_json::from_str::<UsageRecord>(&line) {
                records.push(record);
            }
            if records.len() >= 200_000 {
                break;
            }
        }
    }

    let local_now = chrono::Local::now();
    let today = local_now.date_naive();
    let week_start = today
        .checked_sub_days(chrono::Days::new(today.weekday().num_days_from_monday() as u64))
        .unwrap_or(today);
    let month_start = chrono::NaiveDate::from_ymd_opt(today.year(), today.month(), 1)
        .unwrap_or(today);

    let mut total = UsageAggregate::default();
    let mut today_agg = UsageAggregate::default();
    let mut week_agg = UsageAggregate::default();
    let mut month_agg = UsageAggregate::default();
    let mut daily_map: HashMap<chrono::NaiveDate, (UsageAggregate, u64, u64, u64)> = HashMap::new();
    let mut model_map: HashMap<(String, String), (UsageAggregate, u64)> = HashMap::new();

    for record in &records {
        let local = chrono::Local
            .timestamp_opt(record.ts, 0)
            .single()
            .map(|dt| dt.date_naive());
        let date = local.unwrap_or(today);
        let provider = if record.provider.is_empty() { "unknown" } else { &record.provider };
        let provider_key = provider.to_string();
        let model = if record.model.is_empty() { "(未标注)" } else { &record.model };
        let model_key = model.to_string();

        let cost = record_cost(record, &provider_key, billing);
        let cache_read = record.cache_read.unwrap_or(0);

        let mut agg_one = |value: &mut UsageAggregate| {
            value.requests += 1;
            value.prompt_tokens = value.prompt_tokens.saturating_add(record.prompt);
            value.completion_tokens = value.completion_tokens.saturating_add(record.completion);
            value.total_tokens = value.total_tokens.saturating_add(record.total);
            value.cost += cost;
        };
        agg_one(&mut total);
        if date == today { agg_one(&mut today_agg); }
        if date >= week_start && date <= today { agg_one(&mut week_agg); }
        if date >= month_start && date <= today { agg_one(&mut month_agg); }
        daily_map.entry(date).or_default();
        if let Some((entry, p, c, cr)) = daily_map.get_mut(&date) {
            agg_one(entry);
            *p = p.saturating_add(record.prompt);
            *c = c.saturating_add(record.completion);
            *cr = cr.saturating_add(cache_read);
        }
        let entry = model_map
            .entry((provider_key.clone(), model_key))
            .or_default();
        agg_one(&mut entry.0);
        entry.1 = entry.1.saturating_add(cache_read);
    }

    let mut daily: Vec<DailyUsage> = Vec::with_capacity(365);
    for offset in (0..365).rev() {
        let date = today
            .checked_sub_days(chrono::Days::new(offset))
            .unwrap_or(today);
        let entry = daily_map.get(&date);
        daily.push(DailyUsage {
            date: date.format("%Y-%m-%d").to_string(),
            tokens: entry.map_or(0, |e| e.0.total_tokens),
            requests: entry.map_or(0, |e| e.0.requests),
            prompt: entry.map_or(0, |e| e.1),
            completion: entry.map_or(0, |e| e.2),
            cache_read: entry.map_or(0, |e| e.3),
            cost: entry.map_or(0.0, |e| e.0.cost),
        });
    }

    let mut models: Vec<ModelUsage> = model_map
        .into_iter()
        .map(|((provider_id, model), (value, cr))| ModelUsage {
            provider_id,
            model,
            requests: value.requests,
            prompt_tokens: value.prompt_tokens,
            completion_tokens: value.completion_tokens,
            total_tokens: value.total_tokens,
            cache_read_tokens: cr,
            cost: value.cost,
        })
        .collect();
    models.sort_by(|a, b| b.total_tokens.cmp(&a.total_tokens));

    let billing_response = UsageBilling {
        default: billing.default.clone().unwrap_or(BillingRate { input: 2.0, output: 8.0, cache_read: 0.2 }),
        providers: billing.providers.clone(),
    };

    Ok(UsageStats {
        total,
        today: today_agg,
        this_week: week_agg,
        this_month: month_agg,
        daily,
        models,
        billing: billing_response,
    })
}

fn record_cost(record: &UsageRecord, provider_id: &str, billing: &UsageBillingConfig) -> f64 {
    let rate = billing
        .providers
        .get(provider_id)
        .or(billing.default.as_ref())
        .unwrap_or(&BillingRate { input: 2.0, output: 8.0, cache_read: 0.2 });
    (record.prompt as f64 * rate.input
        + record.completion as f64 * rate.output
        + record.cache_read.unwrap_or(0) as f64 * rate.cache_read)
        / 1_000_000.0
}

/// 最近调用明细记录（用量页列表 / 模型详情）
#[derive(Serialize)]
pub struct UsageDetailRecord {
    pub ts: i64,
    pub provider: String,
    pub model: String,
    pub prompt: u64,
    pub completion: u64,
    pub total: u64,
    pub aux: bool,
    pub cache_read: Option<u64>,
    pub cache_creation: Option<u64>,
}

/// 读取最近 N 条调用明细（新→旧），供用量页「最近调用」与模型详情展示。
pub fn usage_details(path: &Path, limit: usize) -> Result<Vec<UsageDetailRecord>> {
    use std::io::{BufRead, BufReader};

    let mut records: Vec<UsageRecord> = Vec::new();
    if path.exists() {
        let file = std::fs::File::open(path)?;
        let reader = BufReader::new(file).lines();
        for line in reader {
            let line = line?;
            if line.trim().is_empty() {
                continue;
            }
            if let Ok(record) = serde_json::from_str::<UsageRecord>(&line) {
                records.push(record);
            }
        }
    }
    records.truncate(limit);
    let mut details = records
        .into_iter()
        .map(|record| UsageDetailRecord {
            ts: record.ts,
            provider: if record.provider.is_empty() {
                "unknown".to_string()
            } else {
                record.provider
            },
            model: if record.model.is_empty() {
                "(未标注)".to_string()
            } else {
                record.model
            },
            prompt: record.prompt,
            completion: record.completion,
            total: record.total,
            aux: record.aux,
            cache_read: record.cache_read,
            cache_creation: record.cache_creation,
        })
        .collect::<Vec<_>>();
    details.reverse(); // 新→旧
    Ok(details)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn records_and_clears_last_usage() {
        let temp = tempfile::tempdir().unwrap();
        let path = temp.path().join("usage.json");
        let usage = Usage {
            prompt_tokens: 10,
            completion_tokens: 5,
            total_tokens: 15,
        ..Default::default()
        };

        add_usage(&path, &usage).unwrap();
        let usage_snapshot = snapshot(&path).unwrap();
        assert_eq!(usage_snapshot.last_usage.unwrap().total_tokens, 15);
        assert_eq!(
            usage_snapshot
                .last_conversation_usage
                .unwrap()
                .prompt_tokens,
            10
        );

        clear_last_usage(&path).unwrap();
        let usage_snapshot = snapshot(&path).unwrap();
        assert_eq!(usage_snapshot.total_tokens, 15);
        assert!(usage_snapshot.last_usage.is_none());
        assert!(usage_snapshot.last_conversation_usage.is_none());
    }

    #[test]
    fn usage_stats_aggregates_daily_and_per_model() {
        let temp = tempfile::tempdir().unwrap();
        let path = temp.path().join("usage-history.jsonl");
        let billing = UsageBillingConfig::default();
        // 今天的记录（时间戳用本地当前时间，保证落在 today 桶里）
        let now = chrono::Local::now().timestamp();
        // 昨天的记录
        let yesterday = chrono::Local::now()
            .checked_sub_days(chrono::Days::new(1))
            .unwrap()
            .timestamp();
        for (ts, provider, model, prompt, completion) in [
            (now, "deepseek", "deepseek-chat", 100, 50),
            (now, "deepseek", "deepseek-reasoner", 200, 300),
            (yesterday, "openai", "gpt-4o", 1000, 500),
        ] {
            let usage = Usage {
                prompt_tokens: prompt,
                completion_tokens: completion,
                total_tokens: prompt + completion,
            ..Default::default()
            };
            record_usage_at(&path, &usage, provider, model, false, ts).unwrap();
        }

        let stats = usage_stats(&path, &billing).unwrap();
        // 总聚合
        assert_eq!(stats.total.requests, 3);
        assert_eq!(stats.total.total_tokens, 2150);
        // 今日只含前两条
        assert_eq!(stats.today.requests, 2);
        assert_eq!(stats.today.total_tokens, 650);
        // 本周/本月 至少包含今日（昨天可能是上周日，不能假定在同周/同月）
        assert!(stats.this_week.total_tokens >= 650);
        assert!(stats.this_month.total_tokens >= 650);
        // 每日序列：365 天，最后一天是今天
        assert_eq!(stats.daily.len(), 365);
        assert_eq!(stats.daily.last().unwrap().tokens, 650);
        assert_eq!(stats.daily.last().unwrap().requests, 2);
        assert_eq!(stats.daily[364 - 1].tokens, 1500); // 昨天
        // 按模型聚合，按总量降序
        assert_eq!(stats.models.len(), 3);
        assert_eq!(stats.models[0].model, "gpt-4o");
        assert_eq!(stats.models[0].total_tokens, 1500);
        assert_eq!(stats.models[1].model, "deepseek-reasoner");
        assert_eq!(stats.models[2].model, "deepseek-chat");
    }

    #[test]
    fn auxiliary_usage_does_not_replace_conversation_usage() {
        let temp = tempfile::tempdir().unwrap();
        let path = temp.path().join("usage.json");

        add_usage(
            &path,
            &Usage {
                prompt_tokens: 100,
                completion_tokens: 20,
                total_tokens: 120,
            ..Default::default()
            },
        )
        .unwrap();
        add_auxiliary_usage(
            &path,
            &Usage {
                prompt_tokens: 5,
                completion_tokens: 2,
                total_tokens: 7,
            ..Default::default()
            },
        )
        .unwrap();

        let snapshot = snapshot(&path).unwrap();
        assert_eq!(snapshot.total_tokens, 127);
        assert_eq!(snapshot.last_usage.unwrap().prompt_tokens, 5);
        assert_eq!(snapshot.last_conversation_usage.unwrap().prompt_tokens, 100);
    }

    #[test]
    fn total_tokens_falls_back_to_prompt_plus_completion() {
        let temp = tempfile::tempdir().unwrap();
        let path = temp.path().join("usage.json");

        add_usage(
            &path,
            &Usage {
                prompt_tokens: 7,
                completion_tokens: 3,
                total_tokens: 0,
            ..Default::default()
            },
        )
        .unwrap();

        let snapshot = snapshot(&path).unwrap();
        assert_eq!(snapshot.total_tokens, 10);
    }
}
