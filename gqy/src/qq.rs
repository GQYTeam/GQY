//! QQ onebot 平台（反向 WebSocket 接入 NapCat / LLOneBot 等 onebot v11 客户端）。
//!
//! GQY 作为 WebSocket 服务端监听 reverse_ws_port，NapCat 主动连接进来；
//! 收到的私聊/群聊文本消息进入现有 agent 循环（channel = "qq"），
//! 回复经 send_msg 动作发回原会话。用量记录 src = "qq"。
//!
//! 移植自 Miyu 的 platforms/onebot.rs，按 GQY 架构裁剪：只保留文本消息
//! 双向通道 + 鉴权 + 基础指令；图片/文件/群管/限流等后续按需补。

use crate::agent::Agent;
use crate::config::AppConfig;
use crate::llm::LlmClient;
use crate::cli::build_tool_registry;
use crate::paths::GqyPaths;
use crate::state::StateStore;
use anyhow::{bail, Context, Result};
use axum::extract::ws::{Message, WebSocket, WebSocketUpgrade};
use axum::extract::{ConnectInfo, State};
use axum::http::HeaderMap;
use axum::response::IntoResponse;
use axum::routing::get;
use axum::Router;
use futures_util::stream::{SplitSink, SplitStream};
use futures_util::{SinkExt, StreamExt};
use sha2::{Digest, Sha256};
use std::net::SocketAddr;
use std::sync::Arc;
use tokio::sync::Mutex;

/// 一条入站消息（onebot v11 message 事件裁剪后的文本形态）。
struct InboundText {
    user_id: i64,
    /// "private" / "group"
    message_type: String,
    group_id: Option<i64>,
    text: String,
    /// 是否 @ 了本机器人（at 段指向 self_id）
    mentioned_bot: bool,
    /// 入站消息 id（回复时引用）
    message_id: i64,
    /// 图片段（data.url，NapCat 等客户端常直接带可访问 URL）
    image_urls: Vec<String>,
    /// 无 url 的图片 file 缓存名（需 get_image 解析）
    image_files: Vec<String>,
    /// 文件段（data.url 直连或 file_id 需 API 解析）
    files: Vec<(String, String, Option<String>)>, // (name, url_or_empty, file_id_or_empty)
    /// 引用的原消息 id（reply 段；回复某条消息时注入其内容）
    reply_to_message_id: Option<i64>,
    /// 语音段（record）：(url_or_empty, file_or_empty)
    voices: Vec<(String, String)>,
    /// 视频段（video）：(url_or_empty, file_or_empty)
    videos: Vec<(String, String)>,
    /// 原始事件（后续图片/回复消息关联用）
    #[allow(dead_code)]
    raw: serde_json::Value,
}

/// 单连接处理状态（可克隆：发送半连接 + 应答表共享）。
#[derive(Clone)]
struct Conn {
    /// WebSocket 发送半（SplitSink），经 tokio Mutex 共享给并行任务（guard 跨 await 需 Send）
    sender: Arc<tokio::sync::Mutex<SplitSink<WebSocket, Message>>>,
    /// 本机 QQ 号（NapCat 握手头 x-self-id，后续群管功能用）
    #[allow(dead_code)]
    self_id: i64,
    /// echo → 等待中的 call_api 应答通道（Miyu echo-to-oneshot 表）
    pending: Arc<std::sync::Mutex<std::collections::HashMap<String, tokio::sync::oneshot::Sender<serde_json::Value>>>>,
}

impl Conn {
    async fn send_text(&self, text: &str) -> Result<()> {
        self.sender.lock().await.send(Message::Text(text.to_string().into())).await?;
        Ok(())
    }
}

fn unix_now() -> i64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|d| d.as_secs() as i64)
        .unwrap_or(0)
}

/// 极简内存限流：每个 QQ 号在窗口内最多 N 条（默认 2 条 / 600s，仿 Miyu 非白名单默认）。
struct RateLimiter {
    /// user_id → (窗口起点, 计数)
    buckets: std::collections::HashMap<i64, (i64, u32)>,
    max_messages: u32,
    window_seconds: u32,
}

impl RateLimiter {
    fn new(max_messages: u32, window_seconds: u32) -> Self {
        Self { buckets: std::collections::HashMap::new(), max_messages, window_seconds }
    }

    /// 允许则返回 true 并记账；超限返回 false。
    fn allow(&mut self, user_id: i64, now_secs: i64) -> bool {
        if self.max_messages == 0 {
            return true; // 0 = 不限
        }
        let window = i64::from(self.window_seconds.max(1));
        match self.buckets.get_mut(&user_id) {
            Some((start, count)) => {
                if now_secs - *start >= window {
                    *start = now_secs;
                    *count = 1;
                    true
                } else if *count < self.max_messages {
                    *count += 1;
                    true
                } else {
                    false
                }
            }
            None => {
                self.buckets.insert(user_id, (now_secs, 1));
                true
            }
        }
    }
}

/// 服务端共享状态。
#[derive(Clone)]
struct QqServerState {
    paths: GqyPaths,
    config: Arc<Mutex<AppConfig>>,
    /// 全连接共享的限流器（按 QQ 号记账）
    limiter: Arc<std::sync::Mutex<RateLimiter>>,
    /// 转告 outbox：agent 工具写入，QQ 连接消费后发私信给主人
    outbox: Arc<std::sync::Mutex<Vec<String>>>,
    /// 会话并发闸：conversation_id → 进行中回合数（防单会话连发打爆）
    sessions: Arc<std::sync::Mutex<std::collections::HashMap<String, u32>>>,
    /// 私聊昵称缓存：user_id → (昵称, 过期秒)（TTL 10 分钟，仿 Miyu MentionNameCache）
    names: Arc<std::sync::Mutex<std::collections::HashMap<i64, (String, i64)>>>,
    /// 群名片缓存：(group_id, user_id) → (名片, 过期秒)
    group_names: Arc<std::sync::Mutex<std::collections::HashMap<(i64, i64), (String, i64)>>>,
    /// 群名缓存：group_id → (群名, 过期秒)
    group_name_cache: Arc<std::sync::Mutex<std::collections::HashMap<i64, (String, i64)>>>,
    /// 被 /stop 取消的会话集合（回合结束时不发回复）
    cancelled: Arc<std::sync::Mutex<std::collections::HashSet<String>>>,
    /// 活跃连接注册表：self_id → 最近活跃时间戳（多账号可见，/status 用）
    connections: Arc<std::sync::Mutex<std::collections::HashMap<i64, i64>>>,
    /// 自身禁言缓存：(group_id) → (mute_until_unix, 检查时间)；0 = 未禁言
    self_mute: Arc<std::sync::Mutex<std::collections::HashMap<i64, (i64, i64)>>>,
}

impl QqServerState {
    /// 该 QQ 号是否管理员（主人恒为管理员，或显式配置在 admin_users）
    async fn is_admin(&self, user_id: i64) -> bool {
        let config = self.config.lock().await;
        user_id == config.qq.owner_qq || config.qq.admin_users.contains(&user_id)
    }

    /// 主人 QQ 号
    async fn owner_qq(&self) -> i64 {
        self.config.lock().await.qq.owner_qq
    }

    /// 取私聊昵称（带 TTL 缓存，仿 Miyu MentionNameCache）
    fn cached_nickname(&self, user_id: i64) -> Option<String> {
        let now = unix_now();
        let cache = self.names.lock().unwrap();
        cache.get(&user_id).and_then(|(name, expires)| {
            if now < *expires { Some(name.clone()) } else { None }
        })
    }

    fn cache_nickname(&self, user_id: i64, name: &str) {
        if name.is_empty() { return; }
        self.names.lock().unwrap().insert(user_id, (name.to_string(), unix_now() + 600));
    }

    /// 取群成员名片（带 TTL 缓存）
    fn cached_group_member(&self, group_id: i64, user_id: i64) -> Option<String> {
        let now = unix_now();
        let cache = self.group_names.lock().unwrap();
        cache.get(&(group_id, user_id)).and_then(|(name, expires)| {
            if now < *expires { Some(name.clone()) } else { None }
        })
    }

    fn cache_group_member(&self, group_id: i64, user_id: i64, name: &str) {
        if name.is_empty() { return; }
        self.group_names.lock().unwrap().insert((group_id, user_id), (name.to_string(), unix_now() + 600));
    }

    /// 取群名（带 TTL 缓存）
    fn cached_group_name(&self, group_id: i64) -> Option<String> {
        let now = unix_now();
        let cache = self.group_name_cache.lock().unwrap();
        cache.get(&group_id).and_then(|(name, expires)| {
            if now < *expires { Some(name.clone()) } else { None }
        })
    }

    fn cache_group_name(&self, group_id: i64, name: &str) {
        if name.is_empty() { return; }
        self.group_name_cache.lock().unwrap().insert(group_id, (name.to_string(), unix_now() + 600));
    }

    /// 自身在群里是否被禁言（mute_until 未来时间戳 > now 即被禁言）。
    /// 带 TTL 缓存：命中且未过期就直接用，避免每条群消息都查。
    fn bot_muted(&self, group_id: i64) -> Option<bool> {
        let now = unix_now();
        let cache = self.self_mute.lock().unwrap();
        cache.get(&group_id).and_then(|(mute_until, checked_at)| {
            // 缓存 30 秒内有效；未禁言条目也缓存 30 秒
            if now - *checked_at < 30 {
                Some(now < *mute_until)
            } else {
                None
            }
        })
    }

    fn cache_bot_mute(&self, group_id: i64, mute_until: i64) {
        self.self_mute.lock().unwrap().insert(group_id, (mute_until, unix_now()));
    }
}

/// 启动 QQ 监听（阻塞直到进程结束）。
pub async fn run(paths: GqyPaths, args: crate::cli::QqArgs) -> Result<()> {
    let config = AppConfig::load_or_default(&paths)?;
    if !config.qq.enabled {
        bail!(
            "QQ 未启用：先设置 config qq.enabled true 与 qq.access_token（NapCat 侧同一 token），再运行 gqy qq"
        );
    }
    let port = args.port.unwrap_or(config.qq.reverse_ws_port);
    // 本进程会话通道固定为 qq（与 conversation.db 的 channel 列隔离）
    if std::env::var_os("GQY_CHANNEL").is_none() {
        unsafe { std::env::set_var("GQY_CHANNEL", "qq") };
    }
    let state_store = StateStore::new(&paths)?;
    state_store.init_files()?;
    let app_state = QqServerState {
        paths: paths.clone(),
        config: Arc::new(Mutex::new(config.clone())),
        limiter: Arc::new(std::sync::Mutex::new(RateLimiter::new(
            config.qq.rate_limit_max,
            config.qq.rate_limit_window,
        ))),
        outbox: Arc::new(std::sync::Mutex::new(Vec::new())),
        sessions: Arc::new(std::sync::Mutex::new(std::collections::HashMap::new())),
        names: Arc::new(std::sync::Mutex::new(std::collections::HashMap::new())),
        group_names: Arc::new(std::sync::Mutex::new(std::collections::HashMap::new())),
        group_name_cache: Arc::new(std::sync::Mutex::new(std::collections::HashMap::new())),
        cancelled: Arc::new(std::sync::Mutex::new(std::collections::HashSet::new())),
        connections: Arc::new(std::sync::Mutex::new(std::collections::HashMap::new())),
        self_mute: Arc::new(std::sync::Mutex::new(std::collections::HashMap::new())),
    };
    let app = Router::new()
        .route("/", get(onebot_ws))
        .with_state(app_state.clone());
    let listener = tokio::net::TcpListener::bind(SocketAddr::from(([0, 0, 0, 0], port)))
        .await
        .with_context(|| format!("绑定 QQ 反向 WebSocket 端口 {port}"))?;
    println!("顾清影 QQ 监听已启动: ws://0.0.0.0:{port}（等待 NapCat 连接，token {}）",
        if config.qq.access_token.is_empty() { "未设置（仅回环）" } else { "已设置" });
    // 重要事件转告：后台轮询事件源（watch/disk），命中打扰门槛且投递成功的事件
    // 转发给主人 QQ（开关 forward_events）。与 WebUI 入队并行不冲突。
    let forward_state = app_state.clone();
    let forward_enabled = config.qq.forward_events;
    if forward_enabled {
        tokio::spawn(async move {
            let mut interval = tokio::time::interval(std::time::Duration::from_secs(60));
            interval.tick().await; // 首轮立即执行
            loop {
                interval.tick().await;
                let delivered: Vec<(String, String)> = crate::events::poll_all(&forward_state.paths)
                    .await
                    .unwrap_or_default()
                    .into_iter()
                    .filter(|(_, outcome)| *outcome == crate::events::DeliveryOutcome::Delivered)
                    .map(|(source, _)| (source.to_string(), String::new()))
                    .collect();
                for (source, _) in delivered {
                    tracing::info!(target: "gqy::qq", %source, "forwarding system event to owner QQ");
                    // 事件原文由 poll_all 入队，这里把提醒写入 outbox，连接建立时私信主人
                    let message = format!("【系统提醒·{source}】顾清影检测到一条主动事件，请在 WebUI 里查看。");
                    let outbox_path = forward_state.paths.state_dir.join("qq-outbox.jsonl");
                    if let Some(parent) = outbox_path.parent() {
                        let _ = std::fs::create_dir_all(parent);
                    }
                    use std::io::Write;
                    if let Ok(mut file) = std::fs::OpenOptions::new().create(true).append(true).open(&outbox_path) {
                        let _ = writeln!(file, "{}", serde_json::json!({ "ts": unix_now(), "message": message }));
                    }
                }
            }
        });
    }
    axum::serve(
        listener,
        app.into_make_service_with_connect_info::<SocketAddr>(),
    )
    .await?;
    let _ = state_store;
    Ok(())
}

/// 反向 WS 握手入口。
async fn onebot_ws(
    State(state): State<QqServerState>,
    ConnectInfo(peer): ConnectInfo<SocketAddr>,
    headers: HeaderMap,
    ws: WebSocketUpgrade,
) -> impl IntoResponse {
    let config = state.config.lock().await.clone();
    if !config.qq.enabled {
        return axum::http::StatusCode::NOT_FOUND.into_response();
    }
    if !connection_authorized(&headers, &config.qq.access_token, peer) {
        tracing::warn!(target: "gqy::qq", %peer, "OneBot 客户端鉴权失败");
        return axum::http::StatusCode::UNAUTHORIZED.into_response();
    }
    let self_id = headers
        .get("x-self-id")
        .and_then(|value| value.to_str().ok())
        .and_then(|value| value.trim().parse::<i64>().ok())
        .unwrap_or(0);
    ws.on_upgrade(move |socket| connection_loop(state, socket, self_id))
}

/// 与 NapCat 的 Bearer token 比对（常量时间，防时序侧信道）。
fn connection_authorized(headers: &HeaderMap, expected: &str, peer: SocketAddr) -> bool {
    let expected = expected.trim();
    if expected.is_empty() {
        return peer.ip().is_loopback();
    }
    let supplied = headers
        .get(axum::http::header::AUTHORIZATION)
        .and_then(|value| value.to_str().ok())
        .map(str::trim)
        .and_then(|value| {
            value
                .strip_prefix("Bearer ")
                .or_else(|| value.strip_prefix("Token "))
                .or(Some(value))
        })
        .map(str::trim);
    let Some(supplied) = supplied else { return false; };
    Sha256::digest(supplied.as_bytes()) == Sha256::digest(expected.as_bytes())
}

/// 连接主循环：收事件 → 处理 → 发回复。
async fn connection_loop(state: QqServerState, ws: WebSocket, self_id: i64) {
    // 拆分收发：读循环保持读，写半连接共享给并行任务 ——
    // 这样 call_api 等待应答时，读循环仍能收到响应帧并喂回，不会死锁。
    let (sender, mut receiver) = ws.split();
    let conn = Conn {
        sender: Arc::new(tokio::sync::Mutex::new(sender)),
        self_id,
        pending: Arc::new(std::sync::Mutex::new(std::collections::HashMap::new())),
    };
    // 注册活跃连接（多账号可见）
    if self_id > 0 {
        state.connections.lock().unwrap().insert(self_id, unix_now());
    }
    // 连接建立后先排空转告 outbox（agent 工具写的主人来信等）
    drain_outbox(&conn, &state).await;
    // 周期性排空：请求类事件（好友申请等）写入的转告不必等下一次连接
    let periodic_conn = conn.clone();
    let periodic_state = state.clone();
    let drain_task = tokio::spawn(async move {
        let mut interval = tokio::time::interval(std::time::Duration::from_secs(5));
        loop {
            interval.tick().await;
            drain_outbox(&periodic_conn, &periodic_state).await;
        }
    });
    if let Err(err) = run_connection(conn, &state, &mut receiver).await {
        tracing::warn!(target: "gqy::qq", error = %err, "QQ 连接结束: {err:#}");
    }
    drain_task.abort();
    // 注销连接
    if self_id > 0 {
        state.connections.lock().unwrap().remove(&self_id);
    }
}

/// 把 outbox 里积压的转告消息发给主人 QQ（私信），发完清空。
async fn drain_outbox(conn: &Conn, state: &QqServerState) {
    let owner = state.owner_qq().await;
    if owner <= 0 {
        return;
    }
    // 文件 outbox（工具写入）+ 内存兜底
    let mut messages = drain_file_outbox(&state.paths);
    messages.extend(std::mem::take(&mut *state.outbox.lock().unwrap()));
    for message in messages {
        if message.trim().is_empty() {
            continue;
        }
        if let Err(err) = send_api_call(conn, "send_private_msg", serde_json::json!({ "user_id": owner, "message": message }), "gqy-outbox").await {
            tracing::warn!(target: "gqy::qq", error = %err, "outbox send failed");
        }
    }
}

/// 把当前会话待发的图片（qq_send_image 写入的 media outbox）发给对方。
/// 读整个文件，取本 conversation 的条目，按 base64:// 段发送，然后重写文件去掉已发条目。
async fn drain_media_outbox(conn: &Conn, state: &QqServerState, inbound: &InboundText, conversation_id: &str) {
    let media_path = state.paths.state_dir.join("qq-media-outbox.jsonl");
    if !media_path.exists() {
        return;
    }
    let Ok(raw) = std::fs::read_to_string(&media_path) else { return };
    let entries: Vec<serde_json::Value> = raw
        .lines()
        .filter_map(|line| serde_json::from_str::<serde_json::Value>(line).ok())
        .collect();
    let mine: Vec<&serde_json::Value> = entries
        .iter()
        .filter(|entry| entry.get("conversation_id").and_then(serde_json::Value::as_str) == Some(conversation_id))
        .collect();
    if mine.is_empty() {
        return;
    }
    for entry in mine {
        let kind = entry.get("kind").and_then(serde_json::Value::as_str).unwrap_or("image");
        let path_key = if kind == "file" { "file_path" } else { "image_path" };
        let Some(path) = entry.get(path_key).and_then(serde_json::Value::as_str) else { continue };
        let path = std::path::Path::new(path);
        if !path.is_file() {
            continue;
        }
        let Ok(bytes) = std::fs::read(path) else { continue };
        // 图片 20MB / 文件 50MB 上限
        let cap = if kind == "file" { 50 * 1024 * 1024 } else { 20 * 1024 * 1024 };
        if bytes.len() > cap {
            tracing::warn!(target: "gqy::qq", path = path.display().to_string(), "outbound media too large");
            continue;
        }
        let encoded = base64::Engine::encode(&base64::engine::general_purpose::STANDARD, &bytes);
        let segment = if kind == "file" {
            let name = entry.get("name").and_then(serde_json::Value::as_str).unwrap_or("file");
            serde_json::json!({
                "type": "file",
                "data": { "name": name, "file": format!("base64://{encoded}") }
            })
        } else {
            serde_json::json!({ "type": "image", "data": { "file": format!("base64://{encoded}") } })
        };
        let message = serde_json::json!([segment]);
        let (action, params) = if inbound.message_type == "group" {
            let group_id = inbound.group_id.unwrap_or(0);
            ("send_group_msg", serde_json::json!({ "group_id": group_id, "message": message }))
        } else {
            ("send_private_msg", serde_json::json!({ "user_id": inbound.user_id, "message": message }))
        };
        if let Err(err) = send_api_call(conn, action, params, "gqy-media").await {
            tracing::warn!(target: "gqy::qq", error = %err, "media send failed");
        }
    }
    // 已发送的条目从文件移除
    let keep: Vec<String> = entries
        .iter()
        .filter(|entry| entry.get("conversation_id").and_then(serde_json::Value::as_str) != Some(conversation_id))
        .filter_map(|entry| serde_json::to_string(entry).ok())
        .collect();
    let _ = std::fs::write(&media_path, keep.join("\n") + if keep.is_empty() { "" } else { "\n" });
}

/// 下载入站文件并注入上下文。返回一段提示文本（含保存路径）。
/// 段带直连 url 直接用；只有 file_id 时走 get_group_file_url / get_private_file_url 解析。
/// 文件存到 data_dir/files/incoming/（上限 50MB，与 Miyu MAX_INBOUND_FILE_BYTES 一致）。
async fn fetch_inbound_files(state: &QqServerState, conn: &Conn, inbound: &InboundText) -> String {
    if inbound.files.is_empty() && inbound.videos.is_empty() {
        return String::new();
    }
    let mut saved: Vec<String> = Vec::new();
    let incoming_dir = state.paths.data_dir.join("files").join("incoming");
    let _ = std::fs::create_dir_all(&incoming_dir);
    let mut video_refs: Vec<(String, String, Option<String>)> = Vec::new();
    for (url, file) in &inbound.videos {
        video_refs.push(("video".to_string(), url.clone(), if file.is_empty() { None } else { Some(file.clone()) }));
    }
    for (name, url, file_id) in inbound.files.iter().cloned().chain(video_refs) {
        let url = if !url.is_empty() {
            Some(url.clone())
        } else if let Some(file_id) = file_id {
            let api = if inbound.message_type == "group" {
                let group_id = inbound.group_id.unwrap_or(0);
                call_api(conn, "get_group_file_url", serde_json::json!({ "group_id": group_id, "file_id": file_id })).await
            } else {
                call_api(conn, "get_private_file_url", serde_json::json!({ "file_id": file_id })).await
            };
            match api {
                Ok(info) => info.get("url").and_then(serde_json::Value::as_str).map(str::to_string),
                Err(err) => {
                    tracing::debug!(target: "gqy::qq", file_id = %file_id, error = %err, "file url lookup failed");
                    None
                }
            }
        } else {
            None
        };
        let Some(url) = url else { continue };
        // 下载（60s 超时，50MB 上限）
        let bytes = match reqwest::Client::new()
            .get(&url)
            .timeout(std::time::Duration::from_secs(60))
            .send()
            .await
        {
            Ok(resp) => match resp.bytes().await {
                Ok(bytes) => bytes.to_vec(),
                Err(_) => continue,
            },
            Err(_) => continue,
        };
        if bytes.len() > 50 * 1024 * 1024 {
            tracing::warn!(target: "gqy::qq", %name, "inbound file too large");
            continue;
        }
        // 安全文件名：去路径分隔符
        let safe_name = name
            .chars()
            .map(|c| if c.is_ascii_alphanumeric() || c == '.' || c == '-' || c == '_' { c } else { '_' })
            .collect::<String>();
        let safe_name = if safe_name.trim().is_empty() { "file.bin".to_string() } else { safe_name };
        let path = incoming_dir.join(format!("{}_{safe_name}", unix_now()));
        if std::fs::write(&path, &bytes).is_ok() {
            saved.push(path.display().to_string());
        }
    }
    if saved.is_empty() {
        String::new()
    } else {
        let paths = saved.join(" ");
        format!("
[对方发来了文件，可读取查看：{paths}]")
    }
}

/// 引用上下文：回复某条消息时，用 get_msg 取回被引用消息的文本，注入给 agent。
/// 失败（消息被撤回/无权限）就静默跳过。
async fn fetch_quoted_context(conn: &Conn, inbound: &InboundText) -> String {
    let Some(message_id) = inbound.reply_to_message_id else {
        return String::new();
    };
    if message_id <= 0 || message_id == inbound.message_id {
        return String::new();
    }
    let info = match call_api(conn, "get_msg", serde_json::json!({ "message_id": message_id })).await {
        Ok(info) => info,
        Err(_) => return String::new(),
    };
    let data = info.get("data");
    // 被引用消息的文本（段数组或字符串）
    let quoted_text = match data.and_then(|d| d.get("message")) {
        Some(serde_json::Value::Array(segments)) => segments
            .iter()
            .filter_map(|seg| {
                if seg.get("type").and_then(serde_json::Value::as_str) == Some("text") {
                    seg.get("data").and_then(|d| d.get("text")).and_then(serde_json::Value::as_str)
                } else {
                    None
                }
            })
            .collect::<Vec<_>>()
            .join(" "),
        Some(serde_json::Value::String(raw)) => raw.clone(),
        _ => String::new(),
    };
    // 被引用消息里的图片 URL（Miyu merge_quoted_message_images 思路）
    let mut quoted_images: Vec<String> = Vec::new();
    if let Some(serde_json::Value::Array(segments)) = data.and_then(|d| d.get("message")) {
        for seg in segments {
            if seg.get("type").and_then(serde_json::Value::as_str) == Some("image") {
                if let Some(url) = seg.get("data").and_then(|d| d.get("url")).and_then(serde_json::Value::as_str) {
                    if !url.is_empty() {
                        quoted_images.push(url.to_string());
                    }
                }
            }
        }
    }
    let sender = data
        .and_then(|d| d.get("user_id"))
        .and_then(serde_json::Value::as_i64)
        .map(|id| id.to_string())
        .unwrap_or_else(|| "对方".to_string());
    let mut parts = Vec::new();
    if !quoted_text.trim().is_empty() {
        parts.push(format!("[引用了 {} 的消息：{quoted_text}]", sender));
    }
    for url in quoted_images {
        parts.push(format!("[引用消息里有一张图片，可调用「分析图片」查看：{url}]"));
    }
    if parts.is_empty() {
        String::new()
    } else {
        format!("
{}", parts.join("
"))
    }
}

/// 加好友/加群申请：主人直接通过；其他人转告主人待审批（写入 outbox，连接时发私信）。
/// 请求带 flag，通过时用 set_friend_add_request / set_group_add_request 批准。
/// 追加一条转告 outbox 条目（文件，连接时发给主人私信）。
fn append_outbox_entry(state: &QqServerState, message: &str) {
    let text = message.trim().to_string();
    if text.is_empty() {
        return;
    }
    let outbox_path = state.paths.state_dir.join("qq-outbox.jsonl");
    if let Some(parent) = outbox_path.parent() {
        let _ = std::fs::create_dir_all(parent);
    }
    use std::io::Write;
    if let Ok(mut file) = std::fs::OpenOptions::new().create(true).append(true).open(&outbox_path) {
        let _ = writeln!(file, "{}", serde_json::json!({ "ts": unix_now(), "message": text }));
    }
}

async fn handle_add_request(conn: &Conn, state: &QqServerState, value: &serde_json::Value) -> Result<()> {
    let request_type = value.get("request_type").and_then(serde_json::Value::as_str).unwrap_or("");
    let user_id = value.get("user_id").and_then(serde_json::Value::as_i64).unwrap_or(0);
    let flag = value
        .get("flag")
        .and_then(serde_json::Value::as_str)
        .map(str::trim)
        .filter(|s| !s.is_empty())
        .unwrap_or("");
    let comment = value.get("comment").and_then(serde_json::Value::as_str).unwrap_or("");
    if user_id == 0 || flag.is_empty() {
        return Ok(());
    }
    let is_owner = state.is_admin(user_id).await;
    match request_type {
        "friend" => {
            if is_owner {
                // 主人加自己：直接通过
                let _ = call_api(conn, "set_friend_add_request", serde_json::json!({ "flag": flag, "approve": true, "remark": "主人" })).await;
                tracing::info!(target: "gqy::qq", user = user_id, "owner friend request auto-approved");
            } else {
                // 其他人：转告主人
                let message = format!(
                    "【QQ 好友申请】QQ {} 请求加好友{}，待你处理（NapCat 侧审批）。",
                    user_id,
                    if comment.is_empty() { String::new() } else { format!("（备注：{comment}）") }
                );
                append_outbox_entry(state, &message);
            }
        }
        "group" => {
            let group_id = value.get("group_id").and_then(serde_json::Value::as_i64).unwrap_or(0);
            if is_owner {
                let _ = call_api(conn, "set_group_add_request", serde_json::json!({ "flag": flag, "sub_type": "invite", "approve": true })).await;
                tracing::info!(target: "gqy::qq", user = user_id, group = group_id, "owner group invite auto-approved");
            } else {
                let message = format!(
                    "【QQ 加群申请】QQ {} 申请加入群 {}（群号 {}）{}，待你处理。",
                    user_id,
                    value.get("group_name").and_then(serde_json::Value::as_str).unwrap_or(""),
                    group_id,
                    if comment.is_empty() { String::new() } else { format!("（备注：{comment}）") }
                );
                append_outbox_entry(state, &message);
            }
        }
        _ => {}
    }
    Ok(())
}

/// 语音转写：下载 record 段（直连 url 或 get_record 解析），用 macOS 原生
/// speech 转写（speech::transcribe，离线免费），文本注入上下文。
async fn fetch_voice_context(state: &QqServerState, conn: &Conn, inbound: &InboundText) -> String {
    if inbound.voices.is_empty() {
        return String::new();
    }
    let mut transcripts: Vec<String> = Vec::new();
    let incoming_dir = state.paths.data_dir.join("voices").join("incoming");
    let _ = std::fs::create_dir_all(&incoming_dir);
    for (url, file) in &inbound.voices {
        // 解析音频 URL
        let url = if !url.is_empty() {
            Some(url.clone())
        } else if !file.is_empty() {
            match call_api(conn, "get_record", serde_json::json!({ "file": file })).await {
                Ok(info) => info.get("file").and_then(serde_json::Value::as_str).map(str::to_string),
                Err(err) => {
                    tracing::debug!(target: "gqy::qq", voice_file = %file, error = %err, "get_record failed");
                    None
                }
            }
        } else {
            None
        };
        let Some(url) = url else { continue };
        // 下载（30s 超时，20MB 上限）
        let bytes = match reqwest::Client::new()
            .get(&url)
            .timeout(std::time::Duration::from_secs(30))
            .send()
            .await
        {
            Ok(resp) => match resp.bytes().await {
                Ok(bytes) => bytes.to_vec(),
                Err(_) => continue,
            },
            Err(_) => continue,
        };
        if bytes.len() > 20 * 1024 * 1024 {
            continue;
        }
        let path = incoming_dir.join(format!("voice_{}.silk", unix_now()));
        if std::fs::write(&path, &bytes).is_err() {
            continue;
        }
        // 转写是阻塞 swift 调用，放独立线程 + 超时，避免卡住 async 处理器
        let paths_clone = state.paths.clone();
        let audio = path.display().to_string();
        let (tx, rx) = std::sync::mpsc::channel::<Option<String>>();
        std::thread::spawn(move || {
            let _ = tx.send(crate::speech::transcribe(&paths_clone, &audio, None).ok());
        });
        let result = rx.recv_timeout(std::time::Duration::from_secs(20));
        match result {
            Ok(Some(text)) if !text.trim().is_empty() => {
                transcripts.push(format!("[对方发来语音：{text}]"));
            }
            _ => transcripts.push("[对方发来一条语音，暂无法转写。]".to_string()),
        }
    }
    if transcripts.is_empty() {
        String::new()
    } else {
        format!("\n{}", transcripts.join("\n"))
    }
}

/// 群聊唤醒关键词匹配（config qq.trigger_keywords，任一词命中即回应）。
fn matches_trigger_keywords(state: &QqServerState, text: &str) -> bool {
    let keywords = match state.config.try_lock() {
        Ok(guard) => guard.qq.trigger_keywords.clone(),
        Err(_) => return false,
    };
    keywords.iter().any(|kw| !kw.is_empty() && text.contains(kw.as_str()))
}

async fn run_connection(
    conn: Conn,
    state: &QqServerState,
    receiver: &mut SplitStream<WebSocket>,
) -> Result<()> {
    while let Some(frame) = receiver.next().await {
        let frame = match frame {
            Ok(frame) => frame,
            Err(_) => break,
        };
        match frame {
            Message::Text(text) => {
                let value: serde_json::Value = match serde_json::from_str(&text) {
                    Ok(value) => value,
                    Err(_) => continue,
                };
                // API 响应（带 echo 且无 post_type）：立即路由，喂回等待中的 call_api
                if value.get("post_type").is_none() && value.get("echo").is_some() {
                    route_api_response(&conn, &value);
                    continue;
                }
                // 事件处理（消息 → agent 回合）放独立任务：不阻塞读循环，
                // 回合中发起的 API 调用（如 get_group_info）才能拿到响应。
                let task_conn = conn.clone();
                let task_state = state.clone();
                tokio::spawn(async move {
                    if let Err(err) = handle_frame(&task_conn, &task_state, value).await {
                        tracing::debug!(target: "gqy::qq", error = %err, "QQ frame handling failed");
                    }
                });
            }
            Message::Ping(payload) => {
                let _ = conn.sender.lock().await.send(Message::Pong(payload)).await;
            }
            Message::Close(_) => break,
            _ => {}
        }
    }
    Ok(())
}

/// 处理一帧（onebot v11 事件或 API 响应）。
async fn handle_frame(conn: &Conn, state: &QqServerState, value: serde_json::Value) -> Result<()> {
    let post_type = value.get("post_type").and_then(serde_json::Value::as_str).unwrap_or("");
    match post_type {
        "message" => {
            if let Some(inbound) = parse_inbound(&value, conn.self_id) {
                handle_message(conn, state, inbound).await?;
            }
        }
        "meta_event" => {
            // heartbeat 等：回 pong 保持连接
            let action = value.get("meta_event_type").and_then(serde_json::Value::as_str).unwrap_or("");
            if action == "heartbeat" {
                send_api_call(conn, "get_status", serde_json::json!({}), "gqy-heartbeat").await?;
            }
        }
        // 加好友/加群申请
        "request" => {
            handle_add_request(conn, state, &value).await?;
        }
        // 通知类事件：消息撤回 / 群成员变动 / 群聊精华等
        "notice" => {
            handle_notice(conn, state, &value).await?;
        }
        _ => {}
    }
    Ok(())
}

/// 通知类事件（notice）：群成员进群欢迎、退群告别、消息撤回提示等。
async fn handle_notice(conn: &Conn, state: &QqServerState, value: &serde_json::Value) -> Result<()> {
    let notice_type = value.get("notice_type").and_then(serde_json::Value::as_str).unwrap_or("");
    let group_id = value.get("group_id").and_then(serde_json::Value::as_i64).unwrap_or(0);
    let user_id = value.get("user_id").and_then(serde_json::Value::as_i64).unwrap_or(0);
    match notice_type {
        // 群成员增加：简单欢迎（只欢迎新人，不刷屏）
        "group_increase" => {
            if group_id == 0 || user_id == 0 {
                return Ok(());
            }
            let welcome = "欢迎新群友！我是顾清影，需要帮忙 @ 我。";
            let message = serde_json::json!([{ "type": "text", "data": { "text": welcome } }]);
            let _ = send_api_call(
                conn,
                "send_group_msg",
                serde_json::json!({ "group_id": group_id, "message": message }),
                "gqy-notice",
            )
            .await;
        }
        // 消息撤回：转告主人（谁撤了、哪个群/私聊），主人不在 QQ 侧也能知道
        "group_recall" | "friend_recall" => {
            let who = if let Some(name) = state.cached_nickname(user_id) {
                format!("{}（QQ {}）", name, user_id)
            } else {
                format!("QQ {}", user_id)
            };
            let scope = if notice_type == "group_recall" {
                format!("群 {}（群号 {}）", state.cached_group_name(group_id).unwrap_or_default(), group_id)
            } else {
                "私聊".to_string()
            };
            let msg = format!("【消息撤回】{} 在 {} 撤了一条消息。", who, scope);
            append_outbox_entry(state, &msg);
        }
        _ => {}
    }
    Ok(())
}

/// 从 message 事件提取文本与 @ 提及（仅 text/at 段，CQ 图片等暂跳过）。
fn parse_inbound(value: &serde_json::Value, self_id: i64) -> Option<InboundText> {
    let user_id = value.get("user_id").and_then(serde_json::Value::as_i64)?;
    let message_type = value.get("message_type").and_then(serde_json::Value::as_str)?.to_string();
    let group_id = value.get("group_id").and_then(serde_json::Value::as_i64);
    let message_id = value.get("message_id").and_then(serde_json::Value::as_i64).unwrap_or(0);
    let mut mentioned_bot = false;
    let mut image_urls: Vec<String> = Vec::new();
    let mut image_files: Vec<String> = Vec::new();
    let mut files: Vec<(String, String, Option<String>)> = Vec::new();
    let mut reply_to_message_id: Option<i64> = None;
    let mut voices: Vec<(String, String)> = Vec::new();
    let mut videos: Vec<(String, String)> = Vec::new();
    // message 可能是字符串（CQ 码）或段数组；段数组里取 text + at + image + file + reply + record + video
    let text = match value.get("message") {
        Some(serde_json::Value::String(raw)) => {
            // CQ 码字符串：提取 [CQ:image,url=...] 里的图片直链（部分客户端走字符串形态）
            let mut cursor = 0;
            let mut cleaned = raw.clone();
            while let Some(start) = cleaned[cursor..].find("[CQ:image,") {
                let abs = cursor + start;
                if let Some(end) = cleaned[abs..].find("]") {
                    let inner = &cleaned[abs + 10..abs + end]; // 跳过 [CQ:image, 到 ]
                    if let Some(url_part) = inner.split(',').find(|p| p.starts_with("url=")) {
                        let url = url_part[4..].to_string();
                        if !url.is_empty() {
                            image_urls.push(url);
                        }
                    }
                    cleaned.replace_range(abs..abs + end + 1, "");
                    cursor = abs;
                } else {
                    break;
                }
            }
            cleaned
        }
        Some(serde_json::Value::Array(segments)) => {
            let mut parts = Vec::new();
            for segment in segments {
                match segment.get("type").and_then(serde_json::Value::as_str) {
                    Some("text") => {
                        if let Some(data) = segment.get("data") {
                            if let Some(text) = data.get("text").and_then(serde_json::Value::as_str) {
                                parts.push(text.to_string());
                            }
                        }
                    }
                    Some("at") => {
                        // at 段带 qq 字段："all" = @全体（也视为点名，需回应），数字 = 具体 QQ
                        let qq_value = segment.get("data").and_then(|data| data.get("qq")).and_then(serde_json::Value::as_str).unwrap_or("").to_string();
                        if qq_value == "all" {
                            mentioned_bot = true;
                            parts.push("[全体]" .to_string());
                        } else if let Ok(target) = qq_value.parse::<i64>() {
                            if target == self_id {
                                mentioned_bot = true;
                            }
                        }
                    }
                    Some("face") => {
                        // 系统表情：转成占位文本，避免纯表情消息被丢弃
                        let id = segment.get("data").and_then(|d| d.get("id")).and_then(serde_json::Value::as_u64).unwrap_or(0);
                        parts.push(format!("[表情{id}]"));
                    }
                    Some("emoji") => {
                        // 自定义 emoji 段：取 q 字段（QQ 表情码）或 text
                        if let Some(data) = segment.get("data") {
                            if let Some(q) = data.get("q").and_then(serde_json::Value::as_str) {
                                parts.push(q.to_string());
                            } else if let Some(t) = data.get("text").and_then(serde_json::Value::as_str) {
                                parts.push(t.to_string());
                            }
                        }
                    }
                    Some("reply") => {
                        // 引用段：data.id = 被引用的原消息 id
                        if let Some(data) = segment.get("data") {
                            reply_to_message_id = data.get("id").and_then(serde_json::Value::as_i64);
                        }
                    }
                    Some("image") => {
                        // 图片段：NapCat 等常直接带可访问 url；没有 url 时 file 是缓存文件名，
                        // 需要 get_image 解析成文件路径/url。
                        if let Some(data) = segment.get("data") {
                            if let Some(url) = data.get("url").and_then(serde_json::Value::as_str) {
                                if !url.is_empty() {
                                    image_urls.push(url.to_string());
                                }
                            } else if let Some(file) = data.get("file").and_then(serde_json::Value::as_str) {
                                if !file.is_empty() {
                                    image_files.push(file.to_string());
                                }
                            }
                        }
                    }
                    Some("video") => {
                        // 视频段：url 直连或 file 缓存名
                        if let Some(data) = segment.get("data") {
                            let url = data.get("url").and_then(serde_json::Value::as_str).unwrap_or("").to_string();
                            let file = data.get("file").and_then(serde_json::Value::as_str).unwrap_or("").to_string();
                            videos.push((url, file));
                        }
                    }
                    Some("record") => {
                        // 语音段：url 直连或 file 缓存名（需 get_record 解析）
                        if let Some(data) = segment.get("data") {
                            let url = data.get("url").and_then(serde_json::Value::as_str).unwrap_or("").to_string();
                            let file = data.get("file").and_then(serde_json::Value::as_str).unwrap_or("").to_string();
                            voices.push((url, file));
                        }
                    }
                    Some("file") => {
                        // 文件段：name + url（直连）或 file_id（需 get_*_file_url 解析）
                        if let Some(data) = segment.get("data") {
                            let name = data.get("name").and_then(serde_json::Value::as_str).unwrap_or("").to_string();
                            let url = data.get("url").and_then(serde_json::Value::as_str).unwrap_or("").to_string();
                            let file_id = data.get("file_id").and_then(serde_json::Value::as_str).map(str::to_string);
                            files.push((name, url, file_id));
                        }
                    }
                    _ => {}
                }
            }
            parts.join(" ")
        }
        _ => return None,
    };
    let text = text.trim().to_string();
    if text.is_empty()
        && !mentioned_bot
        && image_urls.is_empty()
        && image_files.is_empty()
        && files.is_empty()
        && voices.is_empty()
        && videos.is_empty()
    {
        return None;
    }
    Some(InboundText { user_id, message_type, group_id, text, mentioned_bot, message_id, image_urls, image_files, files, reply_to_message_id, voices, videos, raw: value.clone() })
}

/// 处理一条文本消息：跑一轮 agent，回复原会话。
async fn handle_message(conn: &Conn, state: &QqServerState, inbound: InboundText) -> Result<()> {
    // 管理指令（管理员）：/status /memory 等（主人恒为管理员）
    if inbound.text.starts_with('/') && state.is_admin(inbound.user_id).await {
        // /stop：取消当前会话进行中的回合（结束时不发回复）
        if inbound.text.trim() == "/stop" {
            let conv = if inbound.message_type == "group" {
                format!("qq-g-{}", inbound.group_id.unwrap_or(0))
            } else {
                format!("qq-p-{}", inbound.user_id)
            };
            state.cancelled.lock().unwrap().insert(conv);
            let notice = "已停止当前回合，下一条消息重新开始。";
            let _ = send_reply(conn, &inbound, notice).await;
            return Ok(());
        }
        // /connections：列出已连入的机器人账号
        if inbound.text.trim() == "/connections" {
            let accounts: Vec<String> = state
                .connections
                .lock()
                .unwrap()
                .iter()
                .map(|(id, ts)| {
                    let age = unix_now() - ts;
                    format!("QQ {}（{} 秒前活跃）", id, age.max(0))
                })
                .collect();
            let reply = if accounts.is_empty() {
                "当前没有 NapCat 连接。".to_string()
            } else {
                format!("已连接账号：
{}", accounts.join("
"))
            };
            let _ = send_reply(conn, &inbound, &reply).await;
            return Ok(());
        }
        if let Some(reply) = handle_admin_command(conn, &inbound.text, &state.paths, &inbound).await {
            send_reply(conn, &inbound, &reply).await?;
            return Ok(());
        }
    }
    // 群聊只响应 @ 了机器人、呼名「清影」、或命中唤醒关键词的消息，避免打扰
    if inbound.message_type == "group"
        && !inbound.mentioned_bot
        && !inbound.text.contains("清影")
        && !matches_trigger_keywords(state, &inbound.text)
    {
        return Ok(());
    }
    // 自身被禁言的群：不浪费 LLM 回合，直接跳过（缓存 30s，查不到按未禁言处理）
    if inbound.message_type == "group" {
        if let Some(group_id) = inbound.group_id {
            let muted = if let Some(m) = state.bot_muted(group_id) {
                m
            } else {
                let mute_until = match call_api(
                    conn,
                    "get_group_member_info",
                    serde_json::json!({ "group_id": group_id, "user_id": conn.self_id, "no_cache": false }),
                )
                .await
                {
                    Ok(info) => info
                        .get("data")
                        .and_then(|d| d.get("shut_up_timestamp"))
                        .and_then(|v| v.as_i64().or_else(|| v.as_u64().and_then(|u| i64::try_from(u).ok())))
                        .unwrap_or(0),
                    Err(_) => 0,
                };
                state.cache_bot_mute(group_id, mute_until);
                mute_until > unix_now()
            };
            if muted {
                tracing::debug!(target: "gqy::qq", group = group_id, "bot muted in group, skipping turn");
                return Ok(());
            }
        }
    }
    // 限流：管理员/主人不限，其余每人按配置窗口限流
    let is_admin = state.is_admin(inbound.user_id).await;
    if !is_admin {
        let mut limiter = state.limiter.lock().unwrap();
        if !limiter.allow(inbound.user_id, unix_now()) {
            tracing::debug!(target: "gqy::qq", user = inbound.user_id, "QQ rate limit exceeded");
            return Ok(());
        }
    }

    // 构造 agent 并跑一轮。与 WebUI actor 相同：agent 的 future 因 PastedImage
    // 的 OnceCell 非 Send，不能直接跨线程 await —— 放到独立线程 + current_thread
    // runtime 里 block_on，主循环只 await oneshot 结果。
    let config = state.config.lock().await.clone();
    let paths = state.paths.clone();
    let text = inbound.text.clone();
    let conversation_id = if inbound.message_type == "group" {
        format!("qq-g-{}", inbound.group_id.unwrap_or(0))
    } else {
        format!("qq-p-{}", inbound.user_id)
    };
    // 会话并发闸：同一会话已有回合在跑时，非管理员的新消息直接提示稍候（防打爆）
    let session_full = {
        let mut sessions = state.sessions.lock().unwrap();
        let running = sessions.entry(conversation_id.clone()).or_insert(0);
        if *running >= 1 && !is_admin {
            true
        } else {
            *running += 1;
            false
        }
    };
    if session_full {
        let notice = "当前会话正在处理上一条消息，稍等一下再发哦。";
        let _ = send_reply(conn, &inbound, notice).await;
        return Ok(());
    }
    // 回合后发图用（线程 move 一份进闭包，这份留在本协程）
    let media_conv_id = conversation_id.clone();
    // 发送者识别（Miyu user_identification）+ 群名（Miyu show_group_name）：
    // 群聊查群名 + 群成员名片；私聊查陌生人昵称；失败退化为 QQ 号。
    let sender_context = if inbound.message_type == "group" {
        let group_id = inbound.group_id.unwrap_or(0);
        // 群名 + 群公告/简介（Miyu group_context）：一次 get_group_info 拿全
        let (group_name, group_announce) = if group_id > 0 {
            if let Some(cached) = state.cached_group_name(group_id) {
                (cached, String::new())
            } else {
                match call_api(conn, "get_group_info", serde_json::json!({ "group_id": group_id })).await {
                    Ok(info) => {
                        let data = info.get("data");
                        let name = data
                            .and_then(|d| d.get("group_name"))
                            .and_then(serde_json::Value::as_str)
                            .unwrap_or("")
                            .to_string();
                        let announce = data
                            .and_then(|d| d.get("announcement").or_else(|| d.get("intro")))
                            .and_then(serde_json::Value::as_str)
                            .unwrap_or("")
                            .trim()
                            .to_string();
                        state.cache_group_name(group_id, &name);
                        (name, announce)
                    }
                    Err(err) => {
                        tracing::debug!(target: "gqy::qq", group = group_id, error = %err, "get_group_info failed");
                        (String::new(), String::new())
                    }
                }
            }
        } else {
            (String::new(), String::new())
        };
        // 群成员名片：card 优先，其次 nickname；带 TTL 缓存（命中则不查 API）
        let sender_name = if group_id > 0 {
            if let Some(cached) = state.cached_group_member(group_id, inbound.user_id) {
                cached
            } else {
                let name = match call_api(conn, "get_group_member_info", serde_json::json!({ "group_id": group_id, "user_id": inbound.user_id })).await {
                    Ok(info) => {
                        let data = info.get("data");
                        data.and_then(|d| d.get("card")).and_then(serde_json::Value::as_str)
                            .filter(|s| !s.is_empty())
                            .or_else(|| data.and_then(|d| d.get("nickname")).and_then(serde_json::Value::as_str))
                            .unwrap_or("")
                            .to_string()
                    }
                    Err(_) => String::new(),
                };
                state.cache_group_member(group_id, inbound.user_id, &name);
                name
            }
        } else {
            String::new()
        };
        let who = if sender_name.is_empty() {
            format!("QQ {}", inbound.user_id)
        } else {
            format!("{}（QQ {}）", sender_name, inbound.user_id)
        };
        let mut ctx = if group_name.is_empty() {
            format!("[QQ 群聊] 群号 {}，发消息的人 {}。", group_id, who)
        } else {
            format!("[QQ 群聊] 群「{}」（群号 {}），发消息的人 {}。", group_name, group_id, who)
        };
        // 群公告/简介注入（截断到 200 字，避免刷屏）
        if !group_announce.is_empty() {
            let truncated: String = group_announce.chars().take(200).collect();
            ctx.push_str(&format!("
[本群公告：{truncated}]"));
        }
        ctx
    } else {
        // 私聊：查昵称（带 TTL 缓存）
        let sender_name = if let Some(cached) = state.cached_nickname(inbound.user_id) {
            cached
        } else {
            let name = match call_api(conn, "get_stranger_info", serde_json::json!({ "user_id": inbound.user_id, "no_cache": true })).await {
                Ok(info) => info
                    .get("data")
                    .and_then(|d| d.get("nickname"))
                    .and_then(serde_json::Value::as_str)
                    .unwrap_or("")
                    .to_string(),
                Err(_) => String::new(),
            };
            state.cache_nickname(inbound.user_id, &name);
            name
        };
        if sender_name.is_empty() {
            format!("[QQ 私聊] 发消息的人 QQ {}。", inbound.user_id)
        } else {
            format!("[QQ 私聊] 发消息的人 {}（QQ {}）。", sender_name, inbound.user_id)
        }
    };
    // 图片注入：URL 直连 + 无 URL 的 file 用 get_image 解析成文件路径，
    // 让 agent 可用 vision_analyze 分析。解析失败就只提示有图。
    let mut image_sources: Vec<String> = inbound.image_urls.clone();
    for file in &inbound.image_files {
        match call_api(conn, "get_image", serde_json::json!({ "file": file })).await {
            Ok(info) => {
                if let Some(path) = info
                    .get("data")
                    .and_then(|d| d.get("file"))
                    .and_then(serde_json::Value::as_str)
                {
                    if !path.is_empty() {
                        image_sources.push(path.to_string());
                    }
                }
            }
            Err(err) => {
                tracing::debug!(target: "gqy::qq", image_file = %file, error = %err, "get_image failed");
            }
        }
    }
    let image_context = if image_sources.is_empty() {
        String::new()
    } else {
        let sources = image_sources.join(" ");
        format!(
            "\n[对方发来了图片，可调用「分析图片」工具查看：{sources}]"
        )
    };
    // 文件注入：解析下载 URL（直连 url 或 file_id 走 get_*_file_url），
    // 下载到 data_dir/files/incoming/ 并注入路径，agent 可用 read_file 读取。
    let file_context = fetch_inbound_files(state, conn, &inbound).await;
    // 引用上下文：回复某条消息时，把被引用消息的内容取出来给 agent
    let quote_context = fetch_quoted_context(conn, &inbound).await;
    let voice_context = fetch_voice_context(state, conn, &inbound).await;
    let (reply_tx, reply_rx) = tokio::sync::oneshot::channel::<String>();
    let mut reply_rx = reply_rx;
    // 中间消息通道：回合进行中，攒够一段就发给对方（Miyu intermediate_messages）
    let (interim_tx, interim_rx) = tokio::sync::mpsc::unbounded_channel::<String>();
    std::thread::Builder::new()
        .name("gqy-qq-turn".to_string())
        .spawn(move || {
            let runtime = match tokio::runtime::Builder::new_current_thread()
                .enable_all()
                .build()
            {
                Ok(runtime) => runtime,
                Err(error) => {
                    let _ = reply_tx.send(format!("抱歉，运行时启动失败：{error:#}"));
                    return;
                }
            };
            let outcome = runtime.block_on(async move {
                // 按会话模型路由：命中 qq.conversations 路由表则覆盖 供应商+模型
                let mut turn_config = config.clone();
                for route in &turn_config.qq.conversations {
                    if !route.prefix.is_empty() && conversation_id.starts_with(&route.prefix) {
                        if !route.provider_id.is_empty() {
                            turn_config.active_provider = route.provider_id.clone();
                        }
                        if !route.model.is_empty() {
                            let models = crate::config::ActiveProviderModelConfig {
                                provider_id: route.provider_id.clone(),
                                model: route.model.clone(),
                            };
                            turn_config.active_provider_models = Some(vec![models]);
                        }
                        break;
                    }
                }
                let client = match LlmClient::from_config(&turn_config, &paths) {
                    Ok(client) => client,
                    Err(error) => return Err(anyhow::anyhow!("LLM 客户端初始化失败：{error:#}")),
                };
                let config = turn_config;
                let mut registry = match build_tool_registry(&config, &paths, crate::agent::AgentMode::Normal, false) {
                    Ok(registry) => registry,
                    Err(error) => return Err(anyhow::anyhow!("工具注册失败：{error:#}")),
                };
                // QQ 专属工具：转告主人 + 给当前会话发图（outbox 解耦，回合后发送）
                register_qq_tools(&mut registry, &paths, &conversation_id);
                let state_store = StateStore::new(&paths)?;
                state_store.init_files()?;
                // 会话隔离：切到该 QQ 会话的 conversation_id，历史/记忆独立
                state_store.set_active_conversation(Some(conversation_id));
                let mut agent = Agent::new(
                    config,
                    &paths,
                    state_store,
                    client,
                    registry,
                    crate::agent::AgentMode::Normal,
                )?;
                let input = format!("{sender_context}{image_context}{file_context}{quote_context}{voice_context}\n{text}");
                let mut accumulated = String::new();
                let mut last_flush = String::new();
                let result = agent
                    .chat_stream(&input, |event| {
                        use crate::agent::AgentEvent;
                        if let AgentEvent::Chunk(chunk) = event {
                            if chunk.kind == crate::llm::ChatStreamKind::Content {
                                accumulated.push_str(&chunk.text);
                                // 每攒够 60 字发一条中间消息（避免刷屏）
                                if accumulated.len() - last_flush.len() >= 60 {
                                    let interim = accumulated[last_flush.len()..].to_string();
                                    last_flush = accumulated.clone();
                                    let _ = interim_tx.send(interim);
                                }
                            }
                        }
                        Ok(())
                    })
                    .await?;
                Ok::<_, anyhow::Error>(result.content)
            });
            match outcome {
                Ok(content) => {
                    let _ = reply_tx.send(content);
                }
                Err(error) => {
                    let _ = reply_tx.send(format!("抱歉，这次没处理好：{error:#}"));
                }
            }
        })
        .context("starting QQ turn thread")?;
    // 中间消息 + 最终回复并发等待：回合流式中攒够的分段实时发给对方
    let interim_enabled = state.config.lock().await.qq.intermediate_messages;
    let reply = if interim_enabled {
        let mut interim_rx = interim_rx;
        let mut reply = None;
        loop {
            tokio::select! {
                result = &mut reply_rx => {
                    reply = Some(result.unwrap_or_else(|_| "抱歉，内部通道断开。".to_string()));
                    break;
                }
                interim = interim_rx.recv() => {
                    let Some(interim) = interim else { continue };
                    if !interim.trim().is_empty() {
                        let _ = send_interim(conn, &inbound, &interim).await;
                    }
                }
            }
        }
        reply.unwrap_or_else(|| "抱歉，内部通道断开。".to_string())
    } else {
        reply_rx
            .await
            .unwrap_or_else(|_| "抱歉，内部通道断开。".to_string())
    };
    // 回合期间被 /stop 取消：不发回复
    if state.cancelled.lock().unwrap().contains(&media_conv_id) {
        state.cancelled.lock().unwrap().remove(&media_conv_id);
        // 仍走 outbox/media 排空与闸释放，只是不回复文本
    } else {
        send_reply(conn, &inbound, &reply).await?;
    }
    // 顺带排空转告 outbox（工具可能刚写入主人的转告）
    drain_outbox(conn, state).await;
    // 发图：本会话的 qq_send_image 队列
    drain_media_outbox(conn, state, &inbound, &media_conv_id).await;
    // 释放会话并发闸
    {
        let mut sessions = state.sessions.lock().unwrap();
        if let Some(running) = sessions.get_mut(&media_conv_id) {
            *running = running.saturating_sub(1);
            if *running == 0 {
                sessions.remove(&media_conv_id);
            }
        }
    }
    Ok(())
}

/// 管理员指令：状态/好感度/群管等。返回要回复的文本；None = 非指令或未处理。
async fn handle_admin_command(conn: &Conn, text: &str, paths: &GqyPaths, inbound: &InboundText) -> Option<String> {
    let trimmed = text.trim();
    match trimmed {
        "/status" => Some("顾清影 QQ 在线。".to_string()),
        "/help" => Some(
            "可用管理指令：/status 在线状态 · /affection 好感度 · /mute <QQ> <分钟> 禁言群成员 · /unmute <QQ> 解除禁言 · /kick <QQ> 移出群 · /quit 退出本群 · /conversations 列出 QQ 会话".to_string(),
        ),
        "/affection" => {
            let profile = crate::affection::load_profile(paths);
            Some(format!(
                "好感度 {}（{}）· 累计 {} 轮 · 今日 +{} / -{}",
                profile.score.round() as i64,
                crate::affection::level_label(profile.score),
                profile.message_count,
                (profile.daily_gain * 10.0).round() / 10.0,
                (profile.daily_loss * 10.0).round() / 10.0,
            ))
        }
        "/conversations" => {
            let summaries = StateStore::new(paths)
                .ok()
                .and_then(|s| s.conversation_summaries_for_channel("qq").ok())
                .unwrap_or_default();
            if summaries.is_empty() {
                return Some("QQ 平台暂无会话记录。".to_string());
            }
            let mut lines = Vec::new();
            lines.push(format!("QQ 平台共 {} 个会话：", summaries.len()));
            for s in summaries.iter().take(15) {
                let title = if s.conversation_id.starts_with("qq-g-") {
                    format!("群聊 {}", s.conversation_id.trim_start_matches("qq-g-"))
                } else if s.conversation_id.starts_with("qq-p-") {
                    format!("私聊 QQ {}", s.conversation_id.trim_start_matches("qq-p-"))
                } else {
                    s.conversation_id.clone()
                };
                lines.push(format!("- {title}（id: {}，{} 轮）", s.conversation_id, s.turn_count));
            }
            Some(lines.join("\n"))
        }
        // 群管指令（仅群聊 + 管理员）：/mute <QQ> <分钟> /unmute <QQ> /kick <QQ> /quit
        _ if inbound.message_type == "group" => {
            let group_id = inbound.group_id.unwrap_or(0);
            if group_id == 0 {
                return None;
            }
            let parts: Vec<&str> = trimmed.split_whitespace().collect();
            let cmd = parts.first().copied().unwrap_or("");
            match cmd {
                "/mute" if parts.len() >= 2 => {
                    let target: i64 = parts[1].parse().ok()?;
                    let duration = parts.get(2).and_then(|v| v.parse::<u64>().ok()).unwrap_or(60).min(30 * 24 * 60);
                    match call_api(conn, "set_group_ban", serde_json::json!({ "group_id": group_id, "user_id": target, "duration": duration * 60 })).await {
                        Ok(_) => Some(format!("已禁言 QQ {target} {} 分钟。", duration)),
                        Err(err) => Some(format!("禁言失败：{err:#}")),
                    }
                }
                "/unmute" if parts.len() >= 2 => {
                    let target: i64 = parts[1].parse().ok()?;
                    match call_api(conn, "set_group_ban", serde_json::json!({ "group_id": group_id, "user_id": target, "duration": 0 })).await {
                        Ok(_) => Some(format!("已解除 QQ {target} 的禁言。")),
                        Err(err) => Some(format!("解除禁言失败：{err:#}")),
                    }
                }
                "/kick" if parts.len() >= 2 => {
                    let target: i64 = parts[1].parse().ok()?;
                    match call_api(conn, "set_group_kick", serde_json::json!({ "group_id": group_id, "user_id": target })).await {
                        Ok(_) => Some(format!("已将 QQ {target} 移出本群。")),
                        Err(err) => Some(format!("移出失败：{err:#}")),
                    }
                }
                "/quit" => match call_api(conn, "set_group_leave", serde_json::json!({ "group_id": group_id })).await {
                    Ok(_) => Some("已退出本群。".to_string()),
                    Err(err) => Some(format!("退群失败：{err:#}")),
                },
                _ => None,
            }
        }
        _ => None,
    }
}

/// 发一条中间消息（不带引用，独立消息；Miyu intermediate_messages）。
async fn send_interim(conn: &Conn, inbound: &InboundText, text: &str) -> Result<()> {
    if text.trim().is_empty() {
        return Ok(());
    }
    let message = serde_json::json!([{ "type": "text", "data": { "text": text } }]);
    let (action, params) = if inbound.message_type == "group" {
        let group_id = inbound.group_id.unwrap_or(0);
        ("send_group_msg", serde_json::json!({ "group_id": group_id, "message": message }))
    } else {
        ("send_private_msg", serde_json::json!({ "user_id": inbound.user_id, "message": message }))
    };
    send_api_call(conn, action, params, "gqy-interim").await?;
    Ok(())
}

/// 把回复发回原会话（私聊 send_private_msg，群聊 send_group_msg）。
async fn send_reply(conn: &Conn, inbound: &InboundText, reply: &str) -> Result<()> {
    if reply.trim().is_empty() {
        return Ok(());
    }
    let chunks = split_reply(reply, 3000);
    for (index, chunk) in chunks.into_iter().enumerate() {
        // 首条带 reply 引用原消息；后续拆分条不重复引用
        let message = if index == 0 && inbound.message_id > 0 {
            serde_json::json!([
                { "type": "reply", "data": { "id": inbound.message_id } },
                { "type": "text", "data": { "text": chunk } }
            ])
        } else {
            serde_json::json!([{ "type": "text", "data": { "text": chunk } }])
        };
        let (action, params) = if inbound.message_type == "group" {
            let group_id = inbound.group_id.unwrap_or(0);
            ("send_group_msg", serde_json::json!({ "group_id": group_id, "message": message }))
        } else {
            ("send_private_msg", serde_json::json!({ "user_id": inbound.user_id, "message": message }))
        };
        send_api_call(conn, action, params, "gqy-reply").await?;
    }
    Ok(())
}

/// 按字符数拆分长回复（Miyu max_reply_chars 思路）。
fn split_reply(reply: &str, max_chars: usize) -> Vec<String> {
    if max_chars == 0 || reply.chars().count() <= max_chars {
        return vec![reply.to_string()];
    }
    let mut chunks = Vec::new();
    let mut current = String::new();
    for ch in reply.chars() {
        current.push(ch);
        if current.chars().count() >= max_chars {
            chunks.push(std::mem::take(&mut current));
        }
    }
    if !current.is_empty() {
        chunks.push(current);
    }
    chunks
}

/// 发一条 onebot API 调用（action + params + echo），不等待响应。
async fn send_api_call(conn: &Conn, action: &str, params: serde_json::Value, echo: &str) -> Result<()> {
    let frame = serde_json::json!({
        "action": action,
        "params": params,
        "echo": echo,
    });
    conn.send_text(&frame.to_string()).await?;
    Ok(())
}

/// 带应答的 API 调用（Miyu call_api）：发出 {action, params, echo} 后挂起，
/// 等对应 echo 的响应帧回来（route_api_response 喂回），超时返回 Err。
async fn call_api(conn: &Conn, action: &str, params: serde_json::Value) -> Result<serde_json::Value> {
    let echo = format!("gqy-{}-{}", action, unix_now());
    let (tx, rx) = tokio::sync::oneshot::channel();
    conn.pending.lock().unwrap().insert(echo.clone(), tx);
    let frame = serde_json::json!({
        "action": action,
        "params": params,
        "echo": echo,
    });
    if let Err(error) = conn.send_text(&frame.to_string()).await {
        conn.pending.lock().unwrap().remove(&echo);
        return Err(error);
    }
    match tokio::time::timeout(std::time::Duration::from_secs(10), rx).await {
        Ok(Ok(value)) => {
            conn.pending.lock().unwrap().remove(&echo);
            Ok(value)
        }
        Ok(Err(_)) => {
            conn.pending.lock().unwrap().remove(&echo);
            bail!("QQ API {action} 应答通道断开")
        }
        Err(_) => {
            conn.pending.lock().unwrap().remove(&echo);
            bail!("QQ API {action} 响应超时（10s）")
        }
    }
}

/// QQ 专属工具注册：notify_owner（转告主人）+ qq_send_image（给当前会话发图）。
/// 用文件 outbox 解耦：工具在 agent 线程里写文件，QQ worker 连接/回合后读取发送。
/// 这样工具不需要持有 WS 连接；conversation_id 在 turn 线程里注入，图片发回原会话。
fn register_qq_tools(registry: &mut crate::tools::ToolRegistry, paths: &GqyPaths, conversation_id: &str) {
    let outbox_path = paths.state_dir.join("qq-outbox.jsonl");
    let media_outbox_path = paths.state_dir.join("qq-media-outbox.jsonl");
    let conversation_id = conversation_id.to_string();
    registry.register(crate::tools::ToolSpec::new(
        "notify_owner",
        "把一段话转告给你的主人（通过 QQ 私信发给主人本人）。当对方让你带话、或你认为重要的事需要主人知道时使用。参数 message 是要转告的内容。",
        serde_json::json!({
            "type": "object",
            "properties": {
                "message": { "type": "string", "description": "要转告给主人的内容" }
            },
            "required": ["message"],
            "additionalProperties": false
        }),
        move |args| {
            let outbox_path = outbox_path.clone();
            Box::pin(async move {
                let message = args
                    .get("message")
                    .and_then(serde_json::Value::as_str)
                    .unwrap_or("")
                    .trim()
                    .to_string();
                if message.is_empty() {
                    return Ok("转告内容为空，没有发送。".to_string());
                }
                if let Some(parent) = outbox_path.parent() {
                    std::fs::create_dir_all(parent)?;
                }
                let mut file = std::fs::OpenOptions::new()
                    .create(true)
                    .append(true)
                    .open(&outbox_path)?;
                use std::io::Write;
                writeln!(file, "{}", serde_json::json!({ "ts": unix_now(), "message": message }))?;
                Ok(format!("已把内容写入转告队列，稍后私信发给主人。内容：{message}"))
            })
        },
    ));
    let conv_id = conversation_id.clone();
    let media_outbox_path_clone = media_outbox_path.clone();
    registry.register(crate::tools::ToolSpec::new(
        "qq_send_image",
        "把一张图片发给当前 QQ 对话的人（私聊直接发，群聊发到群里）。当你生成了图片、或需要把本机某张图片给对方看时使用。参数 image_path 是图片的本地路径（必须真实存在）。",
        serde_json::json!({
            "type": "object",
            "properties": {
                "image_path": { "type": "string", "description": "图片的本地绝对路径" }
            },
            "required": ["image_path"],
            "additionalProperties": false
        }),
        move |args| {
            let conv_id = conv_id.clone();
            let media_outbox_path = media_outbox_path_clone.clone();
            Box::pin(async move {
                let image_path = args
                    .get("image_path")
                    .and_then(serde_json::Value::as_str)
                    .unwrap_or("")
                    .trim()
                    .to_string();
                if image_path.is_empty() {
                    return Ok("图片路径为空，没有发送。".to_string());
                }
                let path = std::path::Path::new(&image_path);
                if !path.is_file() {
                    return Ok(format!("图片不存在：{image_path}（请先用真实存在的路径）。"));
                }
                if let Some(parent) = media_outbox_path.parent() {
                    std::fs::create_dir_all(parent)?;
                }
                let mut file = std::fs::OpenOptions::new()
                    .create(true)
                    .append(true)
                    .open(&media_outbox_path)?;
                use std::io::Write;
                writeln!(
                    file,
                    "{}",
                    serde_json::json!({ "ts": unix_now(), "conversation_id": conv_id, "image_path": image_path })
                )?;
                Ok(format!("已把图片排入发送队列，稍后发给对方。路径：{image_path}"))
            })
        },
    ));
    let conv_id_file = conversation_id.clone();
    let media_outbox_path_clone_file = media_outbox_path.clone();
    registry.register(crate::tools::ToolSpec::new(
        "qq_send_file",
        "把一个文件发给当前 QQ 对话的人（私聊直接发，群聊发到群里）。当你生成了文件（脚本/文档/下载物）、或需要把本机某个文件给对方时使用。参数 file_path 是文件的本地路径（必须真实存在），name 可选的文件名（默认取路径末尾）。",
        serde_json::json!({
            "type": "object",
            "properties": {
                "file_path": { "type": "string", "description": "文件的本地绝对路径" },
                "name": { "type": "string", "description": "可选：发给对方时显示的文件名" }
            },
            "required": ["file_path"],
            "additionalProperties": false
        }),
        move |args| {
            let conv_id = conv_id_file.clone();
            let media_outbox_path = media_outbox_path_clone_file.clone();
            Box::pin(async move {
                let file_path = args
                    .get("file_path")
                    .and_then(serde_json::Value::as_str)
                    .unwrap_or("")
                    .trim()
                    .to_string();
                if file_path.is_empty() {
                    return Ok("文件路径为空，没有发送。".to_string());
                }
                let path = std::path::Path::new(&file_path);
                if !path.is_file() {
                    return Ok(format!("文件不存在：{file_path}（请先用真实存在的路径）。"));
                }
                let name = args
                    .get("name")
                    .and_then(serde_json::Value::as_str)
                    .filter(|s| !s.trim().is_empty())
                    .map(str::trim)
                    .unwrap_or(path.file_name().and_then(|n| n.to_str()).unwrap_or("file"));
                if let Some(parent) = media_outbox_path.parent() {
                    std::fs::create_dir_all(parent)?;
                }
                let mut file = std::fs::OpenOptions::new()
                    .create(true)
                    .append(true)
                    .open(&media_outbox_path)?;
                use std::io::Write;
                writeln!(
                    file,
                    "{}",
                    serde_json::json!({ "ts": unix_now(), "conversation_id": conv_id, "kind": "file", "file_path": file_path, "name": name })
                )?;
                Ok(format!("已把文件排入发送队列，稍后发给对方。路径：{file_path}"))
            })
        },
    ));
}

/// 读并清空文件 outbox，返回待发消息（按时间序）。
fn drain_file_outbox(paths: &GqyPaths) -> Vec<String> {
    let outbox_path = paths.state_dir.join("qq-outbox.jsonl");
    if !outbox_path.exists() {
        return Vec::new();
    }
    let messages: Vec<String> = std::fs::read_to_string(&outbox_path)
        .unwrap_or_default()
        .lines()
        .filter_map(|line| {
            serde_json::from_str::<serde_json::Value>(line)
                .ok()
                .and_then(|v| v.get("message").and_then(serde_json::Value::as_str).map(str::to_string))
        })
        .filter(|m| !m.trim().is_empty())
        .collect();
    // 发过的清空
    let _ = std::fs::remove_file(&outbox_path);
    messages
}

/// 把 API 响应帧路由到等待中的 call_api（按 echo 匹配）。
fn route_api_response(conn: &Conn, value: &serde_json::Value) {
    let echo = value.get("echo").and_then(serde_json::Value::as_str);
    let Some(echo) = echo else { return };
    let Some(waiter) = conn.pending.lock().unwrap().remove(echo) else {
        tracing::debug!(target: "gqy::qq", %echo, "unmatched API response");
        return;
    };
    tracing::debug!(target: "gqy::qq", %echo, "API response routed");
    let _ = waiter.send(value.clone());
}

#[cfg(test)]
mod media_tests {
    use super::*;

    #[test]
    fn drain_file_outbox_reads_and_clears() {
        let temp = tempfile::tempdir().unwrap();
        let paths = crate::paths::GqyPaths::new().unwrap();
        // 用临时 state_dir 替代：构造一个 GqyPaths 太麻烦，直接测文件读写逻辑
        let dir = temp.path();
        let outbox = dir.join("qq-outbox.jsonl");
        std::fs::write(
            &outbox,
            "{\"ts\":1,\"message\":\"第一条\"}\n{\"ts\":2,\"message\":\"第二条\"}\n",
        )
        .unwrap();
        let messages = drain_file_outbox_custom(&outbox);
        assert_eq!(messages, vec!["第一条", "第二条"]);
        assert!(!outbox.exists(), "发完应清空文件");
    }

    #[test]
    fn drain_file_outbox_skips_corrupt_lines() {
        let temp = tempfile::tempdir().unwrap();
        let outbox = temp.path().join("qq-outbox.jsonl");
        std::fs::write(&outbox, "{\"ts\":1,\"message\":\"好的\"}\n不是json\n").unwrap();
        let messages = drain_file_outbox_custom(&outbox);
        assert_eq!(messages, vec!["好的"]);
    }

    /// 供测试的文件 outbox 读取（与生产 drain_file_outbox 同逻辑）。
    fn drain_file_outbox_custom(outbox_path: &std::path::Path) -> Vec<String> {
        if !outbox_path.exists() {
            return Vec::new();
        }
        let messages: Vec<String> = std::fs::read_to_string(outbox_path)
            .unwrap_or_default()
            .lines()
            .filter_map(|line| {
                serde_json::from_str::<serde_json::Value>(line)
                    .ok()
                    .and_then(|v| v.get("message").and_then(serde_json::Value::as_str).map(str::to_string))
            })
            .filter(|m| !m.trim().is_empty())
            .collect();
        let _ = std::fs::remove_file(outbox_path);
        messages
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parse_inbound_extracts_text_segments() {
        let value = serde_json::json!({
            "post_type": "message",
            "message_type": "group",
            "user_id": 10001,
            "group_id": 20002,
            "message": [
                { "type": "at", "data": { "qq": "123456" } },
                { "type": "text", "data": { "text": "你好 " } },
                { "type": "image", "data": { "file": "a.png" } },
                { "type": "text", "data": { "text": "清影" } }
            ]
        });
        let inbound = parse_inbound(&value, 123456).unwrap();
        assert_eq!(inbound.user_id, 10001);
        assert_eq!(inbound.message_type, "group");
        assert_eq!(inbound.group_id, Some(20002));
        assert_eq!(inbound.text, "你好  清影");
        // at 段指向 self_id → mentioned_bot
        assert!(inbound.mentioned_bot);
    }

    #[test]
    fn parse_inbound_accepts_plain_string_and_skips_empty() {
        let value = serde_json::json!({
            "message_type": "private",
            "user_id": 1,
            "message": "直接文本"
        });
        let inbound = parse_inbound(&value, 0).unwrap();
        assert_eq!(inbound.text, "直接文本");
        assert!(!inbound.mentioned_bot);
        // 空文本且无 @ → None
        let empty = serde_json::json!({
            "message_type": "private",
            "user_id": 1,
            "message": [{ "type": "image", "data": {} }]
        });
        assert!(parse_inbound(&empty, 0).is_none());
        // 纯 @（无文本）→ 提及也算消息
        let only_at = serde_json::json!({
            "message_type": "group",
            "user_id": 1,
            "message": [{ "type": "at", "data": { "qq": "9" } }]
        });
        let inbound = parse_inbound(&only_at, 9).unwrap();
        assert!(inbound.mentioned_bot);
        assert_eq!(inbound.text, "");
    }

    #[test]
    fn parse_inbound_collects_image_urls() {
        let value = serde_json::json!({
            "message_type": "private",
            "user_id": 7,
            "message": [
                { "type": "text", "data": { "text": "看看这张图" } },
                { "type": "image", "data": { "file": "abc.jpg", "url": "https://example.com/a.jpg" } },
                { "type": "image", "data": { "file": "def.jpg" } }
            ]
        });
        let inbound = parse_inbound(&value, 0).unwrap();
        assert_eq!(inbound.text, "看看这张图");
        assert_eq!(inbound.image_urls, vec!["https://example.com/a.jpg"]);
        assert_eq!(inbound.image_files, vec!["def.jpg"]);
        // 纯图片（无文本）也接收
        let only_image = serde_json::json!({
            "message_type": "private",
            "user_id": 7,
            "message": [{ "type": "image", "data": { "url": "https://example.com/b.jpg" } }]
        });
        let inbound = parse_inbound(&only_image, 0).unwrap();
        assert_eq!(inbound.text, "");
        assert_eq!(inbound.image_urls.len(), 1);
    }

    #[test]
    fn parse_inbound_collects_files() {
        let value = serde_json::json!({
            "message_type": "group",
            "user_id": 7,
            "group_id": 8,
            "message": [
                { "type": "text", "data": { "text": "文件在这" } },
                { "type": "file", "data": { "name": "报告.pdf", "url": "https://example.com/r.pdf", "file_id": "abc123" } },
                { "type": "file", "data": { "name": "无url文件.bin", "file_id": "xyz789" } }
            ]
        });
        let inbound = parse_inbound(&value, 0).unwrap();
        assert_eq!(inbound.files.len(), 2);
        assert_eq!(inbound.files[0], ("报告.pdf".to_string(), "https://example.com/r.pdf".to_string(), Some("abc123".to_string())));
        assert_eq!(inbound.files[1].0, "无url文件.bin");
        assert_eq!(inbound.files[1].2, Some("xyz789".to_string()));
        assert_eq!(inbound.text, "文件在这");
    }

    #[test]
    fn parse_inbound_captures_reply_to() {
        let value = serde_json::json!({
            "message_type": "group",
            "user_id": 7,
            "group_id": 8,
            "message": [
                { "type": "reply", "data": { "id": 424242 } },
                { "type": "text", "data": { "text": "对，就是这样" } }
            ]
        });
        let inbound = parse_inbound(&value, 0).unwrap();
        assert_eq!(inbound.reply_to_message_id, Some(424242));
        assert_eq!(inbound.text, "对，就是这样");
    }

    #[test]
    fn parse_inbound_handles_face_and_emoji() {
        let value = serde_json::json!({
            "message_type": "private",
            "user_id": 7,
            "message": [
                { "type": "face", "data": { "id": 21 } },
                { "type": "emoji", "data": { "q": "😄" } },
                { "type": "text", "data": { "text": " 哈哈" } }
            ]
        });
        let inbound = parse_inbound(&value, 0).unwrap();
        assert_eq!(inbound.text, "[表情21] 😄  哈哈");
    }

    #[test]
    fn parse_inbound_collects_videos() {
        let value = serde_json::json!({
            "message_type": "group",
            "user_id": 7,
            "group_id": 8,
            "message": [
                { "type": "video", "data": { "file": "v.mp4", "url": "https://example.com/v.mp4" } },
                { "type": "video", "data": { "file": "v2.mp4" } }
            ]
        });
        let inbound = parse_inbound(&value, 0).unwrap();
        assert_eq!(inbound.videos.len(), 2);
        assert_eq!(inbound.videos[0], ("https://example.com/v.mp4".to_string(), "v.mp4".to_string()));
        assert_eq!(inbound.videos[1].1, "v2.mp4");
    }

    #[test]
    fn parse_inbound_extracts_cq_image_urls() {
        let value = serde_json::json!({
            "message_type": "private",
            "user_id": 7,
            "message": "看这个 [CQ:image,file=a.jpg,url=https://example.com/a.png] 怎么样",
        });
        let inbound = parse_inbound(&value, 0).unwrap();
        assert_eq!(inbound.image_urls, vec!["https://example.com/a.png"]);
        assert_eq!(inbound.text, "看这个  怎么样");
    }

    #[test]
    fn parse_inbound_handles_at_all() {
        let value = serde_json::json!({
            "message_type": "group",
            "user_id": 7,
            "group_id": 8,
            "message": [
                { "type": "at", "data": { "qq": "all" } },
                { "type": "text", "data": { "text": " 通知" } }
            ]
        });
        let inbound = parse_inbound(&value, 0).unwrap();
        assert!(inbound.mentioned_bot, "@all should count as mention");
        assert_eq!(inbound.text, "[全体]  通知");
    }

    #[test]
    fn split_reply_obeys_max_chars() {
        let long = "字".repeat(100);
        let chunks = split_reply(&long, 30);
        assert_eq!(chunks.len(), 4);
        assert!(chunks.iter().all(|c| c.chars().count() <= 30));
        // 0 = 不拆分
        assert_eq!(split_reply(&long, 0).len(), 1);
        // 短文本不拆
        assert_eq!(split_reply("你好", 30), vec!["你好".to_string()]);
    }

    #[test]
    fn rate_limiter_enforces_window() {
        let mut limiter = RateLimiter::new(2, 600);
        // 头两条放行
        assert!(limiter.allow(42, 1000));
        assert!(limiter.allow(42, 1005));
        // 窗口内第三条拒绝
        assert!(!limiter.allow(42, 1010));
        // 其他人不受影响
        assert!(limiter.allow(7, 1010));
        // 窗口过期后恢复
        assert!(limiter.allow(42, 1700));
        // max=0 不限
        let mut unlimited = RateLimiter::new(0, 60);
        for _ in 0..100 {
            assert!(unlimited.allow(1, 1000));
        }
    }

    #[test]
    fn connection_authorized_checks_loopback_without_token() {
        // 空 token + 回环 → 通过
        let headers = HeaderMap::new();
        let loopback: SocketAddr = "127.0.0.1:9000".parse().unwrap();
        assert!(connection_authorized(&headers, "", loopback));
        // 空 token + 非回环 → 拒绝
        let remote: SocketAddr = "8.8.8.8:9000".parse().unwrap();
        assert!(!connection_authorized(&headers, "", remote));
        // 有 token：Bearer 匹配
        let mut with_token = HeaderMap::new();
        with_token.insert("authorization", "Bearer sekrit".parse().unwrap());
        assert!(connection_authorized(&with_token, "sekrit", loopback));
        assert!(!connection_authorized(&with_token, "other", loopback));
    }
}
