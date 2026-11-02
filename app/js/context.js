import { state, formatTokens } from "./store.js";
import { esc } from "./markdown.js";
import { on } from "./ui.js";
import { $ } from "./render.js";

let bound = false;

export function initContext() {
  if (bound) return;
  bound = true;
  $("#toggleContext")?.addEventListener("click", toggleContext);
  $("#closeContext")?.addEventListener("click", closeContext);
  $("#contextScrim")?.addEventListener("click", closeContext);
  document.addEventListener("keydown", (event) => {
    if (event.key !== "Escape" || $("#contextDrawer")?.hidden !== false) return;
    if (document.body.classList.contains("modal-open")) return;
    closeContext();
  });
  on("sessions-changed", () => {
    if ($("#contextDrawer")?.hidden === false) renderContext();
  });
  on("conversation-changed", () => {
    if ($("#contextDrawer")?.hidden === false) renderContext();
  });
}

function toggleContext() {
  const drawer = $("#contextDrawer");
  if (!drawer) return;
  if (drawer.hidden) openContext();
  else closeContext();
}

function openContext() {
  renderContext();
  const drawer = $("#contextDrawer");
  const scrim = $("#contextScrim");
  if (drawer) drawer.hidden = false;
  if (scrim) scrim.hidden = window.innerWidth > 900;
  $("#toggleContext")?.setAttribute("aria-pressed", "true");
}

function closeContext() {
  const drawer = $("#contextDrawer");
  const scrim = $("#contextScrim");
  if (drawer) drawer.hidden = true;
  if (scrim) scrim.hidden = true;
  $("#toggleContext")?.setAttribute("aria-pressed", "false");
}

function renderContext() {
  const body = $("#contextBody");
  if (!body) return;
  const session = state.sessions.find((item) => item.session_id === state.sessionId) || null;
  const chars = state.messages.reduce(
    (sum, message) => sum + (message.content?.length || 0) + (message.thinking?.length || 0),
    0,
  );
  const tokens = state.messages.reduce(
    (sum, message) =>
      sum + (message.usage ? (message.usage.prompt_tokens || 0) + (message.usage.completion_tokens || 0) : 0),
    0,
  );
  const tools = [];
  for (const message of state.messages) {
    for (const tool of message.tools || []) tools.push(tool);
  }
  const artifacts = session?.artifacts || [];

  const toolChips = tools.length
    ? `<div class="drawer-chip-list">${tools
        .map(
          (tool) =>
            `<span class="chip ${tool.status && tool.status !== "ok" ? "bad" : "ok"}">${esc(tool.name)}${tool.duration_ms != null ? ` · ${tool.duration_ms}ms` : ""}</span>`,
        )
        .join("")}</div>`
    : `<div class="empty-mini">这一轮还没有调用工具。</div>`;

  const artifactRows = artifacts.length
    ? artifacts
        .map(
          (artifact) => `<div class="drawer-artifact">
            <span>${esc(artifact.path)}</span>
            <span class="chip">${esc(artifact.kind || "final")}</span>
          </div>`,
        )
        .join("")
    : `<div class="empty-mini">还没有产物索引。模型生成的交付文件会出现在这里。</div>`;

  body.innerHTML = `<section class="drawer-section">
      <div class="section-label" style="padding:0 0 6px">会话</div>
      <div class="about-grid">
        <div class="about-row"><span>标题</span><code>${esc(session?.title || state.sessionTitle || "新对话")}</code></div>
        <div class="about-row"><span>会话 ID</span><code>${esc(state.sessionId || "尚未创建")}</code></div>
        <div class="about-row"><span>模型</span><code>${esc(state.model || "未选择")}</code></div>
        <div class="about-row"><span>消息</span><code>${state.messages.length} 条</code></div>
        <div class="about-row"><span>上下文</span><code>约 ${chars} 字</code></div>
        <div class="about-row"><span>已用</span><code>${tokens ? formatTokens(tokens) + " tokens" : "—"}</code></div>
      </div>
    </section>
    <section class="drawer-section">
      <div class="section-label" style="padding:0 0 6px">工具轨迹</div>
      ${toolChips}
    </section>
    <section class="drawer-section">
      <div class="section-label" style="padding:0 0 6px">产物</div>
      ${artifactRows}
    </section>
    <section class="drawer-section">
      <p class="field-hint" style="margin:0">面板只读。会话、事件流、草稿和产物都落在 <code>$GQY_HOME/sessions</code>；这里只读当前内存与清单索引。</p>
    </section>`;
}
