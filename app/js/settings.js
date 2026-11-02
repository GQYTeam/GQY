import { api, AuthError, ApiError } from "./api.js";
import { state, formatTokens } from "./store.js";
import { esc } from "./markdown.js";
import { THEMES, applyReduceMotion, applyTheme, themeCardHTML } from "./themes.js";
import { emit, openOverlay, toast } from "./ui.js";
import { $ } from "./render.js";
import { applyModel } from "./picker.js";

let bound = false;
let activeTab = "usage";

const TABS = [
  ["usage", "用量", "本会话与账号配额"],
  ["models", "模型", "当前目录里可用的 provider 与路由组"],
  ["providers", "供应商", "配置的 provider、账号与密钥状态"],
  ["gateway", "网关", "账号健康、冷却与配置重载"],
  ["plugins", "插件", "本机插件目录与运行时状态"],
  ["tools", "工具", "核心工具与外部工具的契约"],
  ["appearance", "外观", "主题、动效与视觉 Demo"],
  ["about", "关于", "构建、端点与边界"],
];

export function initSettings() {
  if (bound) return;
  bound = true;
  $("#openSettings")?.addEventListener("click", () => openConsole("appearance"));
  $("#openUsage")?.addEventListener("click", () => openConsole("usage"));
}

export function openSettings(tab = "appearance") {
  openConsole(tab);
}

export function openUsage() {
  openConsole("usage");
}

function openConsole(tab) {
  activeTab = tab;
  renderConsole();
  openOverlay("consoleOverlay", { focus: false });
  requestAnimationFrame(() => $("#consoleNav .active")?.focus({ preventScroll: true }));
  ensureStatus().catch(() => {});
  if (tab === "plugins") ensurePlugins().catch(() => {});
  if (tab === "tools") ensureTools().catch(() => {});
}

async function ensureStatus() {
  try {
    state.status = await api.status();
    state.config = await api.config();
    if (!$("#consoleOverlay")?.hidden && (activeTab === "gateway" || activeTab === "usage")) {
      renderConsole();
    }
  } catch (error) {
    if (error instanceof AuthError) emit("auth-required");
  }
}

async function ensureTools(force = false) {
  if (!force && state.tools.length) return;
  try {
    state.tools = await api.tools();
  } catch (error) {
    if (error instanceof AuthError) {
      emit("auth-required");
      return;
    }
    state.tools = [];
  }
  if (!$("#consoleOverlay")?.hidden && activeTab === "tools") renderConsole();
}

async function ensurePlugins(force = false) {
  if (!force && state.plugins.length) return;
  try {
    state.plugins = await api.plugins();
  } catch (error) {
    if (error instanceof AuthError) {
      emit("auth-required");
      return;
    }
    state.plugins = [];
  }
  if (!$("#consoleOverlay")?.hidden && activeTab === "plugins") renderConsole();
}

function renderConsole() {
  const nav = $("#consoleNav");
  const body = $("#consoleBody");
  const title = $("#consoleTitle");
  const sub = $("#consoleSub");
  if (!nav || !body) return;

  nav.innerHTML = TABS.map(
    ([id, label]) =>
      `<button type="button" class="${id === activeTab ? "active" : ""}" data-console-tab="${id}">${label}</button>`,
  ).join("");
  for (const button of nav.querySelectorAll("[data-console-tab]")) {
    button.addEventListener("click", () => {
      activeTab = button.dataset.consoleTab;
      renderConsole();
      if (activeTab === "plugins") ensurePlugins().catch(() => {});
      if (activeTab === "tools") ensureTools().catch(() => {});
      if (activeTab === "gateway" || activeTab === "usage") ensureStatus().catch(() => {});
    });
  }

  const meta = TABS.find(([id]) => id === activeTab) || TABS[0];
  if (title) title.textContent = meta[1];
  if (sub) sub.textContent = meta[2];

  if (activeTab === "usage") body.innerHTML = usageHTML();
  else if (activeTab === "models") body.innerHTML = modelsHTML();
  else if (activeTab === "providers") body.innerHTML = providersHTML();
  else if (activeTab === "gateway") body.innerHTML = gatewayHTML();
  else if (activeTab === "plugins") body.innerHTML = pluginsHTML();
  else if (activeTab === "tools") body.innerHTML = toolsHTML();
  else if (activeTab === "appearance") body.innerHTML = appearanceHTML();
  else body.innerHTML = aboutHTML();

  bindConsoleBody();
}

function usageHTML() {
  let prompt = 0;
  let completion = 0;
  let turns = 0;
  const rows = [];
  let maxTotal = 1;
  state.messages.forEach((message) => {
    if (message.role !== "assistant" || !message.usage) return;
    const p = message.usage.prompt_tokens || 0;
    const c = message.usage.completion_tokens || 0;
    prompt += p;
    completion += c;
    turns += 1;
    maxTotal = Math.max(maxTotal, p + c);
    rows.push({ message, total: p + c });
  });

  const bars = rows
    .map(
      ({ message, total }, i) => `<div class="usage-bar-row">
        <span class="usage-bar-index">${String(i + 1).padStart(2, "0")}</span>
        <span class="usage-bar"><i style="width:${Math.max(4, (total / maxTotal) * 100)}%"></i></span>
        <span class="usage-bar-value">${formatTokens(total)}</span>
        <span class="usage-bar-note">${esc(message.model || state.model || "")}</span>
      </div>`,
    )
    .join("");

  const accounts = state.status?.accounts || [];
  const accountRows = accounts
    .map(
      (account) => `<div class="account-row">
        <div class="account-main">
          <div class="account-name">${esc(account.provider)} / ${esc(account.id)}</div>
          <div class="account-meta">${account.calls || 0} 次调用 · ${account.failures || 0} 次失败${account.cooldown_remaining_s ? ` · 冷却 ${account.cooldown_remaining_s}s` : ""}${account.token_limit ? ` · 配额 ${formatTokens(account.token_limit)}` : ""}</div>
        </div>
        <span class="chip ${account.healthy ? "ok" : "warn"}">${formatTokens(account.token_used_today)}</span>
      </div>`,
    )
    .join("");

  return `<div class="usage-grid">
      <div class="stat"><div class="stat-value">${formatTokens(prompt + completion)}</div><div class="stat-label">本会话 tokens</div></div>
      <div class="stat"><div class="stat-value">${turns}</div><div class="stat-label">已完成回合</div></div>
      <div class="stat"><div class="stat-value">${state.sessions.length}</div><div class="stat-label">本机会话</div></div>
    </div>
    <div class="section-label" style="padding:0 0 8px">每回合用量</div>
    ${rows.length ? `<div class="usage-bars">${bars}</div>` : `<div class="empty-mini">还没有完成的回复。发一条消息后会在这里看到每回合的 prompt 与 completion。</div>`}
    <div class="section-label" style="padding:20px 0 8px">账号配额</div>
    ${accountRows ? `<div class="account-list">${accountRows}</div>` : `<div class="empty-mini">没有账号状态。配置 provider 后刷新。</div>`}`;
}

function modelsHTML() {
  const models = state.models || [];
  if (!models.length) {
    return `<div class="empty-mini">还没有模型。检查 <code>$GQY_HOME/providers.json</code>，或在 <code>.env</code> 里设置 <code>GQY_API_KEY</code> 后重启。</div>`;
  }
  const rows = models
    .map(
      (model) => `<div class="model-row">
        <div class="model-row-main">
          <div class="model-id">${esc(model.id)}${model.default ? ' <span class="chip ok">default</span>' : ""}${model.members ? ' <span class="chip">group</span>' : ""}</div>
          <div class="model-meta">${esc(model.owned_by || "")}${model.members ? ` · ${model.members.map((m) => esc(m)).join(" · ")}` : ""}</div>
        </div>
        <button class="btn${model.id === state.model ? " primary" : ""}" type="button" data-use-model="${esc(model.id)}">
          ${model.id === state.model ? "当前" : "使用"}
        </button>
      </div>`,
    )
    .join("");
  return `<div class="model-list">${rows}</div>
    <div class="settings-actions">
      <a class="btn ghost" href="demos.html" target="_blank" rel="noopener">看看视觉 Demo</a>
    </div>`;
}

function providersHTML() {
  const providers = state.config?.providers || [];
  if (!providers.length) {
    return `<div class="empty-mini">还没有 provider。编辑 <code>$GQY_HOME/providers.json</code>，或设置 <code>GQY_API_KEY</code> 后重启。</div>`;
  }
  const rows = providers
    .map((provider) => {
      const accounts = (provider.accounts || [])
        .map(
          (account) => `<span class="chip ${account.api_key_set ? "ok" : "warn"}">${esc(account.id)} · ${account.api_key_set ? "key set" : "no key"}${account.daily_token_limit ? ` · ${formatTokens(account.daily_token_limit)}` : ""}</span>`,
        )
        .join("");
      return `<div class="model-row" style="align-items:flex-start">
        <div class="model-row-main">
          <div class="model-id">${esc(provider.display_name || provider.id)} <span class="chip">${esc(provider.protocol || "openai-chat")}</span></div>
          <div class="model-meta">${esc(provider.id)} · ${esc(provider.base_url)} · 并发 ${provider.max_concurrency || 0} · 默认 ${esc(provider.default_model || "—")}</div>
          <div class="drawer-chip-list" style="margin-top:6px">${accounts}</div>
        </div>
      </div>`;
    })
    .join("");
  return `<div class="model-list">${rows}</div>
    <p class="field-hint" style="margin-top:14px">只读视图。密钥只显示是否已配置；编辑 <code>$GQY_HOME/providers.json</code> 后在网关页重载配置。</p>`;
}

function gatewayHTML() {
  const status = state.status;
  const config = status?.config || state.config;
  const accounts = status?.accounts || [];
  const accountRows = accounts.length
    ? accounts
        .map((account) => {
          const notes = [
            account.healthy ? "健康" : "不可用",
            `${account.calls || 0} 次调用`,
            account.cooldown_remaining_s ? `冷却 ${account.cooldown_remaining_s}s` : "",
            account.token_limit
              ? `${formatTokens(account.token_used_today)} / ${formatTokens(account.token_limit)} tokens`
              : `${formatTokens(account.token_used_today)} tokens`,
          ].filter(Boolean);
          return `<div class="account-row">
            <div class="account-main">
              <div class="account-name">${esc(account.provider)} / ${esc(account.id)}</div>
              <div class="account-meta">${esc(notes.join(" · "))}</div>
            </div>
            <span class="chip ${account.healthy ? "ok" : "warn"}">${account.healthy ? "healthy" : "paused"}</span>
          </div>`;
        })
        .join("")
    : `<div class="empty-mini">没有账号状态。配置 provider 后刷新。</div>`;

  const defaultRef = status?.routing?.default;
  return `<div class="about-grid" style="margin-bottom:16px">
      <div class="about-row"><span>配置版本</span><code>v${config?.version ?? "—"}</code></div>
      <div class="about-row"><span>默认模型</span><code>${esc(defaultRef ? `${defaultRef.provider}/${defaultRef.model}` : "未设置")}</code></div>
      <div class="about-row"><span>最近重载错误</span><code>${esc(config?.last_error || "无")}</code></div>
    </div>
    <div class="account-list">${accountRows}</div>
    <div class="settings-actions">
      <button class="btn" type="button" data-refresh-status>刷新状态</button>
      <button class="btn primary" type="button" data-reload-config>重载配置</button>
      <a class="btn ghost" href="demos.html" target="_blank" rel="noopener">视觉 Demo</a>
    </div>`;
}

function pluginsHTML() {
  const plugins = state.plugins || [];
  const rows = plugins
    .map((plugin) => {
      const ready = plugin.state === "ready";
      const meta = [
        plugin.id,
        plugin.protocol || "",
        `${plugin.calls || 0} 次调用`,
        plugin.failures ? `${plugin.failures} 次失败` : "",
        plugin.last_error || "",
      ].filter(Boolean);
      return `<div class="model-row">
        <div class="model-row-main">
          <div class="model-id">${esc(plugin.name || plugin.id)} <span class="chip ${ready ? "ok" : "warn"}">${esc(plugin.state || "discovered")}</span></div>
          <div class="model-meta">${esc(meta.join(" · "))}</div>
        </div>
        <button class="btn" type="button" data-plugin-${ready ? "unload" : "load"}="${esc(plugin.id)}">${ready ? "卸载" : "加载"}</button>
      </div>`;
    })
    .join("");
  return `<div class="settings-actions" style="margin-top:0;margin-bottom:14px">
      <button class="btn" type="button" data-plugin-scan>重新扫描</button>
      <span class="field-hint">插件目录：<code>$GQY_HOME/plugins</code></span>
    </div>
    ${rows ? `<div class="model-list">${rows}</div>` : `<div class="empty-mini">还没有插件。把插件目录放到 <code>$GQY_HOME/plugins</code> 下，或点“重新扫描”。</div>`}`;
}

function toolsHTML() {
  const tools = state.tools || [];
  if (!tools.length) {
    return `<div class="empty-mini">还没有工具清单。agent loop 初始化后，这里会列出核心工具与插件动作。</div>`;
  }
  const rows = tools
    .map(
      (tool) => `<details class="tool-card" style="margin-bottom:6px">
        <summary class="tool-summary">
          <span class="tool-dot"></span>
          <span class="tool-name">${esc(tool.name)}</span>
          <span class="tool-meta">${esc(tool.source || "core")}</span>
        </summary>
        <div class="tool-body">
          <div class="tool-label">说明</div>
          <pre>${esc(tool.description || "")}</pre>
          <div class="tool-label">参数 schema</div>
          <pre>${esc(JSON.stringify(tool.schema || {}, null, 2))}</pre>
        </div>
      </details>`,
    )
    .join("");
  return `<p class="field-hint" style="margin:0 0 12px">核心工具自动注入模型；外部工具默认不注入，需开启 <code>GQY_ENABLE_EXTERNAL</code>。</p>
    <div>${rows}</div>`;
}

function appearanceHTML() {
  return `<div class="theme-grid" id="themeGrid">
      ${THEMES.map((theme) => themeCardHTML(theme, theme.id === state.theme)).join("")}
    </div>
    <div style="margin-top:18px">
      <div class="field">
        <div class="field-copy">
          <div class="field-label">减少动效</div>
          <div class="field-hint">关闭消息进入与打字点动画；系统设置优先。</div>
        </div>
        <button class="toggle" type="button" data-reduce-motion aria-pressed="${state.reduceMotion}" aria-label="减少动效"></button>
      </div>
      <div class="field">
        <div class="field-copy">
          <div class="field-label">视觉 Demo</div>
          <div class="field-hint">六种方向的对照页，带完整的迷你界面预览。</div>
        </div>
        <a class="btn" href="demos.html" target="_blank" rel="noopener">打开</a>
      </div>
      <div class="field">
        <div class="field-copy">
          <div class="field-label">重新看引导</div>
          <div class="field-hint">再选一次主题与第一印象。</div>
        </div>
        <button class="btn" type="button" data-replay-onboarding>重播</button>
      </div>
    </div>`;
}

function aboutHTML() {
  const health = state.health || {};
  return `<div class="about-grid">
      <div class="about-row"><span>构建</span><code>${esc(health.build || "dev")}</code></div>
      <div class="about-row"><span>配置版本</span><code>v${state.config?.version ?? "—"}</code></div>
      <div class="about-row"><span>界面</span><code>零构建 · 原生 ES modules</code></div>
      <div class="about-row"><span>网关</span><code>OpenAI · Anthropic · Gemini</code></div>
      <div class="about-row"><span>会话</span><code>${state.sessions.length} 个 · 本地 JSONL 事件流</code></div>
    </div>
    <div class="settings-actions">
      <a class="btn" href="demos.html" target="_blank" rel="noopener">视觉 Demo 对照页</a>
      <a class="btn ghost" href="/healthz" target="_blank" rel="noopener">健康检查</a>
    </div>
    <p class="field-hint" style="margin-top:18px">GQY 把会话、草稿、附件和请求日志留在本机。密钥只在服务端内存与出站请求里，界面只看到 <code>api_key_set</code>。</p>`;
}

function bindConsoleBody() {
  const body = $("#consoleBody");
  if (!body) return;
  for (const card of body.querySelectorAll("[data-theme-choice]")) {
    card.addEventListener("click", () => {
      applyTheme(card.dataset.themeChoice);
      renderConsole();
      toast(`已换成「${THEMES.find((theme) => theme.id === card.dataset.themeChoice)?.name || ""}」`);
    });
  }
  body.querySelector("[data-reduce-motion]")?.addEventListener("click", () => {
    applyReduceMotion(!state.reduceMotion);
    renderConsole();
  });
  body.querySelector("[data-replay-onboarding]")?.addEventListener("click", () => {
    emit("onboarding-open", { force: true });
  });
  for (const button of body.querySelectorAll("[data-use-model]")) {
    button.addEventListener("click", () => {
      useModel(button.dataset.useModel);
      renderConsole();
    });
  }
  body.querySelector("[data-refresh-status]")?.addEventListener("click", async () => {
    await ensureStatus();
    renderConsole();
    toast("状态已刷新");
  });
  body.querySelector("[data-reload-config]")?.addEventListener("click", async () => {
    try {
      const result = await api.reload();
      state.config = await api.config();
      state.status = await api.status();
      renderConsole();
      toast(`配置已重载到 v${result.version}`);
    } catch (error) {
      toast(error instanceof ApiError ? error.message : "重载失败", "bad");
    }
  });
  body.querySelector("[data-plugin-scan]")?.addEventListener("click", async () => {
    try {
      await api.pluginScan();
      await ensurePlugins(true);
      toast("插件已重新扫描");
    } catch (error) {
      toast(error instanceof ApiError ? error.message : "扫描失败", "bad");
    }
  });
  for (const button of body.querySelectorAll("[data-plugin-load]")) {
    button.addEventListener("click", () => pluginAction("load", button.dataset.pluginLoad));
  }
  for (const button of body.querySelectorAll("[data-plugin-unload]")) {
    button.addEventListener("click", () => pluginAction("unload", button.dataset.pluginUnload));
  }
}

async function pluginAction(action, id) {
  if (!id) return;
  try {
    if (action === "load") await api.pluginLoad(id);
    else await api.pluginUnload(id);
    await ensurePlugins(true);
    toast(action === "load" ? "插件已加载" : "插件已卸载");
  } catch (error) {
    toast(error instanceof ApiError ? error.message : "插件操作失败", "bad");
  }
}

function useModel(id) {
  applyModel(id);
  toast("已切换当前模型");
}
