//! 语音能力（本地、零 API 成本）：
//! - TTS：macOS 自带 `say` 命令直接播放/生成音频文件（零依赖）
//! - STT：speech-tool.swift（SFSpeechRecognizer 本地离线识别）
//!
//! 工具：`speak`（读一段文字）、`listen_audio`（识别音频文件）
//! CLI：`gqy tts "文字"`、`gqy stt 音频文件`

use crate::paths::GqyPaths;
use anyhow::{bail, Context, Result};
use std::process::Command;

/// 顾清影克隆音色朗读（Qwen3-TTS 本地服务，见 src/scripts/tts-server.py）：
/// text → 8091 服务合成 → afplay 播放（终端直接播，不开 App）。
/// 服务未启动时按需拉起（运行环境运行时解析；首次运行需下载模型）。
/// HTTP 走 macOS 自带 curl：避免在 tokio runtime 里用 blocking reqwest（会 panic）。
pub fn speak_clone(text: &str, tts_url: Option<&str>) -> Result<()> {
    let text = text.trim();
    if text.is_empty() {
        bail!("text is required");
    }
    let base = tts_url.unwrap_or("http://127.0.0.1:8091");
    // URL 编码（中文等非 ASCII → %XX）
    let mut encoded = String::new();
    for c in text.chars() {
        if c.is_ascii_alphanumeric() || matches!(c, '-' | '_' | '.' | '~') {
            encoded.push(c);
        } else if c == ' ' {
            encoded.push('+');
        } else {
            for b in c.to_string().as_bytes() {
                encoded.push_str(&format!("%{b:02X}"));
            }
        }
    }
    let url = format!("{base}/tts?text={encoded}");
    let tmp = std::env::temp_dir().join(format!("gqy-tts-{}.wav", std::process::id()));
    let mut code = http_get_to_file(&url, &tmp)?;
    if code != 200 {
        // 服务未起：按需拉起（首次要下模型，给足时间）
        ensure_tts_server(std::time::Duration::from_secs(120))?;
        code = http_get_to_file(&url, &tmp)
            .with_context(|| format!("TTS 服务不可达（{base}）：拉起后仍无响应"))?;
    }
    if code != 200 {
        bail!("TTS 服务返回 HTTP {code}");
    }
    let bytes = std::fs::read(&tmp).context("读取 TTS 音频失败")?;
    if bytes.len() < 100 {
        let _ = std::fs::remove_file(&tmp);
        bail!("TTS 返回内容过短，可能合成失败");
    }
    let status = Command::new("afplay")
        .arg(&tmp)
        .status()
        .with_context(|| "failed to run afplay")?;
    let _ = std::fs::remove_file(&tmp);
    if !status.success() {
        bail!("afplay exited with status {status}");
    }
    Ok(())
}

/// curl GET 到文件，返回 HTTP 状态码。连接失败返回 Ok(0)。
fn http_get_to_file(url: &str, out: &std::path::Path) -> Result<u16> {
    let output = Command::new("curl")
        .args(["-s", "-o"])
        .arg(out)
        .args(["-w", "%{http_code}", url])
        .output()
        .with_context(|| "failed to run curl")?;
    let code = String::from_utf8_lossy(&output.stdout).trim().to_string();
    Ok(code.parse().unwrap_or(0))
}

/// 文字转语音：默认直接播放，可指定输出文件（.aiff/.m4a）。
/// 用 macOS 内置 `say`，零依赖、零 API 成本。
pub fn speak(text: &str, voice: Option<&str>, output: Option<&str>) -> Result<()> {
    let text = text.trim();
    if text.is_empty() {
        bail!("text is required");
    }
    let mut command = Command::new("say");
    if let Some(voice) = voice.filter(|v| !v.trim().is_empty()) {
        command.arg("-v").arg(voice);
    }
    if let Some(output) = output {
        command.arg("-o").arg(output);
    }
    let status = command
        .arg(text)
        .status()
        .with_context(|| "failed to run `say`; TTS requires macOS")?;
    if !status.success() {
        bail!("say exited with status {status}");
    }
    Ok(())
}

// ──── 克隆音色 TTS（可选组件）：运行环境解析 + 按需拉起 ────
//
// tts-server.py 是独立的 Python 服务（Qwen3-TTS + mlx-audio，Apple Silicon）。
// 它不在编译产物里：脚本随 share 资源分发（App bundle / brew / 源码树），
// venv 是运行时依赖，需按 tts-setup.sh 安装。本模块负责在运行时解析
// 两者的位置（不再用编译期硬编码路径——那在 App 打包后必然指向构建机）。

/// 克隆音色 TTS 的运行环境：python 可执行 + tts-server.py 脚本。
#[derive(Debug, Clone)]
pub struct TtsRuntime {
    pub python: std::path::PathBuf,
    pub script: std::path::PathBuf,
}

/// 解析克隆音色 TTS 运行环境，按优先级：
/// 1. GQY_TTS_PYTHON / GQY_TTS_SCRIPT 环境变量（完全自定义）
/// 2. GQY_HOME/scripts/tts-server.py + GQY_HOME/venv/bin/python（用户自托管，随备份迁移）
/// 3. 可执行文件旁 share/gqy/scripts/tts-server.py（App bundle / brew 内嵌）
/// 4. 源码树 src/scripts/tts-server.py（开发模式）
/// python 找不到时退回 PATH 里的 python3；脚本找不到时给安装指引。
pub fn resolve_tts_runtime() -> Result<TtsRuntime> {
    let mut candidates: Vec<(std::path::PathBuf, Option<std::path::PathBuf>)> = Vec::new();

    // 1. 环境变量完全自定义
    if let Ok(script) = std::env::var("GQY_TTS_SCRIPT") {
        let python = std::env::var("GQY_TTS_PYTHON").map(std::path::PathBuf::from).ok();
        candidates.push((std::path::PathBuf::from(script), python));
    }

    // 2. GQY_HOME（隔离布局）或兼容目录（非隔离布局）
    let home = std::env::var("GQY_HOME")
        .map(std::path::PathBuf::from)
        .ok()
        .or_else(|| {
            directories::BaseDirs::new()
                .map(|dirs| dirs.home_dir().join("Library/Application Support/gqy"))
        });
    if let Some(home) = &home {
        candidates.push((
            home.join("scripts/tts-server.py"),
            Some(home.join("venv/bin/python")),
        ));
    }

    // 3. 可执行文件旁 share（App bundle / brew 安装的内嵌资源）
    if let Ok(exe) = std::env::current_exe() {
        if let Some(dir) = exe.parent() {
            candidates.push((
                dir.join("share/gqy/scripts/tts-server.py"),
                Some(dir.join("venv/bin/python")),
            ));
        }
    }

    // 4. 源码树（开发模式；venv 由 tts-setup.sh 建在仓库 gqy/venv）
    candidates.push((
        std::path::PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("src/scripts/tts-server.py"),
        Some(
            std::path::PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("venv/bin/python"),
        ),
    ));
    // 历史位置：旧代码引用过 CARGO_MANIFEST_DIR/scripts，兼容一次
    candidates.push((
        std::path::PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("scripts/tts-server.py"),
        Some(
            std::path::PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("venv/bin/python"),
        ),
    ));

    for (script, python) in candidates {
        if !script.is_file() {
            continue;
        }
        let python = python
            .filter(|path| path.is_file())
            .unwrap_or_else(|| std::path::PathBuf::from("python3"));
        return Ok(TtsRuntime { python, script });
    }

    bail!(
        "未找到克隆音色 TTS 服务（tts-server.py）。安装方式：\n\
          · 源码/开发：运行 src/scripts/tts-setup.sh\n\
          · App/CLI：把 tts-server.py 放到 GQY_HOME/scripts/，并建 GQY_HOME/venv（pip install mlx-audio）\n\
          · 或设置 GQY_TTS_SCRIPT / GQY_TTS_PYTHON 指定位置"
    )
}

const TTS_PORT: u16 = 8091;

/// 健康检查：TTS 服务是否已在响应（curl，快进快出）。
fn tts_health() -> bool {
    let url = format!("http://127.0.0.1:{TTS_PORT}/health");
    Command::new("curl")
        .args(["-s", "-o", "/dev/null", "-w", "%{http_code}", &url])
        .output()
        .map(|output| String::from_utf8_lossy(&output.stdout).trim() == "200")
        .unwrap_or(false)
}

/// 拉起 TTS 服务（子进程；空闲自动退出由脚本负责）。
/// 后台线程 wait 回收子进程，避免常驻进程积累僵尸。
pub fn spawn_tts_server() -> Result<()> {
    let runtime = resolve_tts_runtime()?;
    let mut child = Command::new(&runtime.python)
        .arg(&runtime.script)
        .arg(TTS_PORT.to_string())
        .stdout(std::process::Stdio::null())
        .stderr(std::process::Stdio::null())
        .spawn()
        .with_context(|| {
            format!(
                "TTS 服务拉起失败：{} {}",
                runtime.python.display(),
                runtime.script.display()
            )
        })?;
    std::thread::spawn(move || {
        let _ = child.wait();
    });
    Ok(())
}

/// 确保 TTS 服务在跑：健康则复用；否则拉起并轮询就绪。
/// timeout 要覆盖首次运行：模型（约 1GB）在首次合成时下载。
/// 可用 GQY_TTS_START_TIMEOUT 秒数覆盖（调试/测试用）。
pub fn ensure_tts_server(timeout: std::time::Duration) -> Result<()> {
    let timeout = std::env::var("GQY_TTS_START_TIMEOUT")
        .ok()
        .and_then(|value| value.parse::<u64>().ok())
        .map(std::time::Duration::from_secs)
        .unwrap_or(timeout);
    if tts_health() {
        return Ok(());
    }
    spawn_tts_server()?;
    let deadline = std::time::Instant::now() + timeout;
    while std::time::Instant::now() < deadline {
        if tts_health() {
            return Ok(());
        }
        std::thread::sleep(std::time::Duration::from_secs(2));
    }
    bail!(
        "TTS 服务启动超时（{}s）。首次运行需下载模型，可稍后重试",
        timeout.as_secs()
    )
}

/// 列出可用的系统语音（`say -v '?'` 解析）。
pub fn list_voices() -> Result<Vec<String>> {
    let output = Command::new("say")
        .args(["-v", "?"])
        .output()
        .with_context(|| "failed to list voices")?;
    let text = String::from_utf8_lossy(&output.stdout);
    let voices = text
        .lines()
        .filter_map(|line| line.split_whitespace().next())
        .map(str::to_string)
        .collect();
    Ok(voices)
}

/// 语音转文字：把本地音频文件交给 speech-tool.swift 识别（离线）。
pub fn transcribe(paths: &GqyPaths, audio_path: &str, locale: Option<&str>) -> Result<String> {
    let tool = speech_tool_path(paths);
    if !tool.is_file() {
        bail!("speech-tool.swift not found at {}", tool.display());
    }
    let locale = locale.unwrap_or("zh-Hans");
    let output = Command::new("swift")
        .arg(&tool)
        .arg(audio_path)
        .arg(locale)
        .output()
        .with_context(|| "failed to run speech-tool.swift (requires macOS + swift)")?;
    if !output.status.success() {
        bail!(
            "speech-tool failed: {}",
            String::from_utf8_lossy(&output.stderr).trim()
        );
    }
    let stdout = String::from_utf8_lossy(&output.stdout);
    let parsed: serde_json::Value = serde_json::from_str(stdout.trim())
        .with_context(|| format!("speech-tool returned invalid JSON: {stdout}"))?;
    if parsed.get("ok").and_then(serde_json::Value::as_bool) != Some(true) {
        let error = parsed
            .get("error")
            .and_then(serde_json::Value::as_str)
            .unwrap_or("unknown error");
        bail!("speech recognition failed: {error}");
    }
    parsed
        .get("text")
        .and_then(serde_json::Value::as_str)
        .map(str::to_string)
        .ok_or_else(|| anyhow::anyhow!("speech-tool returned no text"))
}

/// 定位 speech-tool.swift：brew 装 = share/gqy/scripts；源码 = <repo>/src/scripts。
fn speech_tool_path(paths: &GqyPaths) -> std::path::PathBuf {
    let candidates = [
        paths.share_dir.join("scripts/speech-tool.swift"),
        paths.share_dir.join("src/scripts/speech-tool.swift"),
    ];
    candidates
        .into_iter()
        .find(|path| path.is_file())
        .unwrap_or_else(|| paths.share_dir.join("scripts/speech-tool.swift"))
}

#[cfg(test)]
mod tests {
    use super::*;

    // 依赖 macOS 自带 `say`；Linux 上无此命令，跳过
    #[cfg(target_os = "macos")]
    #[test]
    fn tts_generates_audio_file() {
        let out = std::env::temp_dir().join(format!("gqy-tts-test-{}.aiff", std::process::id()));
        let _ = std::fs::remove_file(&out);
        speak("test", None, Some(out.to_str().unwrap())).unwrap();
        assert!(out.is_file(), "say should produce an audio file");
        let size = std::fs::metadata(&out).map(|m| m.len()).unwrap_or(0);
        assert!(size > 1000, "audio file should have content, got {size} bytes");
        let _ = std::fs::remove_file(&out);
    }

    #[cfg(target_os = "macos")]
    #[test]
    fn lists_system_voices() {
        let voices = list_voices().unwrap();
        assert!(!voices.is_empty(), "macOS should have voices");
    }

    #[test]
    fn rejects_empty_text() {
        assert!(speak("", None, None).is_err());
    }

    /// 源码树里必须能解析出 tts-server.py（restructure 后脚本曾丢失，回归保护）。
    #[test]
    fn resolves_tts_runtime_in_source_tree() {
        let runtime = resolve_tts_runtime().unwrap();
        assert!(runtime.script.is_file(), "脚本不存在：{}", runtime.script.display());
        assert_eq!(
            runtime.script.file_name().and_then(|name| name.to_str()),
            Some("tts-server.py")
        );
        // python 未装 venv 时回退 PATH python3（不会 panic）
        assert!(!runtime.python.as_os_str().is_empty());
    }

    /// 自定义环境变量优先；指向不存在的脚本时回退到源码树。
    #[test]
    fn tts_runtime_env_override_falls_back_gracefully() {
        unsafe {
            std::env::set_var("GQY_TTS_SCRIPT", "/nonexistent/tts-server.py");
            std::env::set_var("GQY_TTS_PYTHON", "/nonexistent/python");
        }
        let runtime = resolve_tts_runtime().unwrap();
        assert!(runtime.script.is_file());
        unsafe {
            std::env::remove_var("GQY_TTS_SCRIPT");
            std::env::remove_var("GQY_TTS_PYTHON");
        }
    }
}
