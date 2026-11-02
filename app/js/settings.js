import { api, AuthError, ApiError } from "./api.js";
import { state, formatTokens } from "./store.js";
import { esc } from "./markdown.js";
import { THEMES, applyReduceMotion, applyTheme, themeCardHTML } from "./themes.js";
import { confirmDialog, emit, on, openOverlay, toast } from "./ui.js";
import { $ } from "./render.js";
import { applyModel } from "./picker.js";

let bound = false;
let activeTab = "usage";
let personaDraft = null;

const TABS = [
  ["usage", "用量", "本会话与账号配额"],
  ["persona", "人格", "名称、头像与系统提示词"],
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
  // 插件集合变化（热加载/卸载/删除）时，如果控制台开着插件页就刷新（HR2/HR7）。
  on("plugins-changed", () => {
    if (!$("#consoleOverlay")?.hidden && activeTab === "plugins") {
      ensurePlugins(true).catch(() => {});
    }
  });
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
  if (tab === "persona") personaDraft = { ...(state.persona || {}) };
  renderConsole();
  openOverlay("consoleOverlay", { focus: false });
  requestAnimationFrame(() => $("#consoleBody")?.focus({ preventScroll: true }));
  ensureStatus().catch(() => {});
  if (tab === "plugins") {
    ensurePlugins()
      .then(() => {
        const want = new URLSearchParams(location.search).get("pluginSettings");
        if (want) togglePluginSettings(want);
      })
      .catch(() => {});
  }
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
  else if (activeTab === "persona") body.innerHTML = personaHTML();
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

function personaHTML() {
  const draft = personaDraft || state.persona || {};
  const initial = Array.from(draft.name || "GQY")[0] || "G";
  const preview = draft.avatar
    ? `<img src="${esc(draft.avatar)}" alt="头像预览">`
    : esc(initial);
  return `<form class="persona-form" id="personaForm">
      <div class="persona-avatar-row">
        <div class="persona-avatar-preview" id="personaAvatarPreview">${preview}</div>
        <div class="persona-actions">
          <button class="btn" type="button" data-persona-pick>选择图片</button>
          ${draft.avatar ? '<button class="btn ghost" type="button" data-persona-remove-avatar>移除头像</button>' : ""}
          <input id="personaAvatarInput" type="file" accept="image/*" hidden>
        </div>
      </div>
      <div class="persona-field">
        <label for="personaName">名称</label>
        <input id="personaName" type="text" maxlength="40" value="${esc(draft.name || "GQY")}" placeholder="助手叫什么">
      </div>
      <div class="persona-field">
        <label for="personaTagline">一句话介绍</label>
        <input id="personaTagline" type="text" maxlength="120" value="${esc(draft.tagline || "")}" placeholder="显示在侧栏与标题下方">
      </div>
      <div class="persona-field">
        <label for="personaSystemPrompt">系统提示词</label>
        <textarea id="personaSystemPrompt" maxlength="8000" placeholder="例如：回答简短、先给结论。每次对话会作为 system 消息放在最前面。">${esc(draft.system_prompt || "")}</textarea>
      </div>
      <div class="persona-actions">
        <button class="btn primary" type="submit">保存人格</button>
        <button class="btn ghost" type="button" data-persona-reset>恢复已保存</button>
        <span class="field-hint">头像只存本机；建议 512×512 以内，最大 768 KB。</span>
      </div>
    </form>`;
}

function bindPersonaForm() {
  const form = $("#personaForm");
  if (!form) return;
  if (!personaDraft) personaDraft = { ...(state.persona || {}) };
  const name = $("#personaName");
  const tagline = $("#personaTagline");
  const prompt = $("#personaSystemPrompt");
  const file = $("#personaAvatarInput");
  const preview = $("#personaAvatarPreview");

  const sync = () => {
    personaDraft.name = name.value.trim();
    personaDraft.tagline = tagline.value.trim();
    personaDraft.system_prompt = prompt.value;
  };
  name?.addEventListener("input", sync);
  tagline?.addEventListener("input", sync);
  prompt?.addEventListener("input", sync);

  form.querySelector("[data-persona-pick]")?.addEventListener("click", () => file?.click());
  file?.addEventListener("change", async () => {
    const picked = file.files?.[0];
    if (!picked) return;
    if (picked.size > 768 * 1024) {
      toast("头像超过 768 KB", "bad");
      file.value = "";
      return;
    }
    personaDraft.avatar = await readDataURL(picked);
    if (preview) {
      preview.innerHTML = "";
      const img = document.createElement("img");
      img.src = personaDraft.avatar;
      img.alt = "头像预览";
      preview.append(img);
    }
  });
  form.querySelector("[data-persona-remove-avatar]")?.addEventListener("click", () => {
    personaDraft.avatar = "";
    if (preview) preview.textContent = Array.from(personaDraft.name || "GQY")[0] || "G";
  });
  form.querySelector("[data-persona-reset]")?.addEventListener("click", () => {
    personaDraft = { ...(state.persona || {}) };
    renderConsole();
  });
  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    sync();
    try {
      const result = await api.savePersona(personaDraft);
      state.persona = result.persona;
      personaDraft = { ...result.persona };
      emit("persona-changed");
      renderConsole();
      toast("人格已保存");
    } catch (error) {
      toast(error instanceof ApiError ? error.message : "保存失败", "bad");
    }
  });
}

function readDataURL(file) {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result || ""));
    reader.onerror = () => reject(reader.error);
    reader.readAsDataURL(file);
  });
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

function pluginModeChip(plugin) {
  const installed = plugin.source === "installed";
  const label = installed ? "已安装" : "本地自建";
  const tip = installed ? "在线安装的插件本体只读，可改设置" : "本地开发模式，可随时修改插件本体";
  return `<span class="chip ${installed ? "warn" : "ok"}" title="${esc(tip)}">${label}</span>`;
}

function settingHint(setting) {
  return setting.description ? `<span class="plugin-setting-desc">${esc(setting.description)}</span>` : "";
}

// numberAttrs 把 schema 的上下界/步长映射到 number input；缺省 step=any，
// 免得 0–1 置信度这类小数被整数步长拦掉。
function numberAttrs(setting) {
  const attrs = [];
  const min = setting.minimum ?? setting.min;
  const max = setting.maximum ?? setting.max;
  const step = setting.step ?? setting.multipleOf;
  if (min !== undefined && min !== null && Number.isFinite(Number(min))) attrs.push(`min="${esc(min)}"`);
  if (max !== undefined && max !== null && Number.isFinite(Number(max))) attrs.push(`max="${esc(max)}"`);
  const stepValue =
    step !== undefined && step !== null && Number.isFinite(Number(step)) && Number(step) > 0 ? Number(step) : "any";
  attrs.push(`step="${esc(stepValue)}"`);
  attrs.push('inputmode="decimal"');
  return attrs.join(" ");
}

function settingInput(setting, value) {
  const v = value === undefined || value === null ? setting.default : value;
  const key = esc(setting.key);
  const title = esc(setting.title || setting.key);
  const hint = settingHint(setting);
  if (setting.type === "boolean") {
    return `<label class="plugin-setting boolean"><input type="checkbox" data-setting-key="${key}" ${v ? "checked" : ""} /><span class="plugin-setting-title">${title}</span>${hint}</label>`;
  }
  if (setting.type === "select") {
    const opts = (setting.options || [])
      .map((opt) => `<option value="${esc(opt)}" ${String(v) === String(opt) ? "selected" : ""}>${esc(opt)}</option>`)
      .join("");
    return `<label class="plugin-setting"><span>${title}</span><select data-setting-key="${key}">${opts}</select>${hint}</label>`;
  }
  if (setting.type === "number") {
    return `<label class="plugin-setting"><span>${title}</span><input type="number" data-setting-key="${key}" value="${esc(v ?? "")}" ${numberAttrs(setting)} />${hint}</label>`;
  }
  return `<label class="plugin-setting"><span>${title}</span><input type="text" data-setting-key="${key}" value="${esc(v ?? "")}" />${hint}</label>`;
}

function pluginSettingsForm(plugin, detail) {
  const schema = detail?.schema || [];
  const values = detail?.effective || {};
  if (!schema.length) {
    return `<div class="plugin-settings"><div class="field-hint">该插件没有可配置项。</div></div>`;
  }
  const fields = schema.map((setting) => settingInput(setting, values[setting.key])).join("");
  return `<div class="plugin-settings">
    <div class="plugin-settings-head">设置 <span class="field-hint">${plugin.read_only ? "插件本体只读，设置单独保存" : "本地插件，可随时改本体"}</span></div>
    ${fields}
    <div class="settings-actions">
      <button class="btn primary" type="button" data-plugin-save="${esc(plugin.id)}">保存设置</button>
      <button class="btn ghost" type="button" data-plugin-settings-close="${esc(plugin.id)}">收起</button>
    </div>
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
      const editing = state.pluginEditing === plugin.id;
      const detail = state.pluginDetail?.id === plugin.id ? state.pluginDetail : null;
      const form = editing && detail ? pluginSettingsForm(plugin, detail) : "";
      return `<div class="model-row plugin-row">
        <div class="model-row-main">
          <div class="model-id">${esc(plugin.name || plugin.id)} <span class="chip ${ready ? "ok" : "warn"}">${esc(plugin.state || "discovered")}</span> ${pluginModeChip(plugin)}</div>
          <div class="model-meta">${esc(meta.join(" · "))}</div>
          ${form}
        </div>
        <div class="plugin-actions">
          ${plugin.has_settings || (detail && detail.schema?.length) ? `<button class="btn ghost" type="button" data-plugin-settings="${esc(plugin.id)}">${editing ? "收起" : "设置"}</button>` : ""}
          <button class="btn" type="button" data-plugin-${ready ? "unload" : "load"}="${esc(plugin.id)}">${ready ? "卸载" : "加载"}</button>
          <button class="btn danger" type="button" data-plugin-delete="${esc(plugin.id)}">删除</button>
        </div>
      </div>`;
    })
    .join("");
  const installed = new Set(plugins.map((plugin) => plugin.id));
  const repoButton = (id, source, label) =>
    `<button class="btn" type="button" data-plugin-install="${esc(id)}" data-plugin-source="${esc(source)}"${installed.has(id) ? " disabled" : ""}>${label}</button>`;
  return `<div class="settings-actions" style="margin-top:0;margin-bottom:10px">
      ${repoButton("show-images-default", "https://github.com/GQYTeam/gqy-plugins-ShowImages-default", "安装图片展示")}
      ${repoButton("emote-pack-default", "https://github.com/GQYTeam/gqy-plugins-EmotePack-default", "安装表情包")}
      <button class="btn" type="button" data-plugin-scan>重新扫描</button>
    </div>
    <p class="field-hint" style="margin:0 0 14px">从项目插件仓库安装会用 <code>git clone</code> 下载到 <code>$GQY_HOME/plugins</code>；安装 = 信任插件代码。</p>
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
  if (activeTab === "persona") bindPersonaForm();
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
  for (const button of body.querySelectorAll("[data-plugin-install]")) {
    button.addEventListener("click", () => installPlugin(button));
  }
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
  for (const button of body.querySelectorAll("[data-plugin-settings]")) {
    button.addEventListener("click", () => togglePluginSettings(button.dataset.pluginSettings));
  }
  for (const button of body.querySelectorAll("[data-plugin-settings-close]")) {
    button.addEventListener("click", () => {
      state.pluginEditing = "";
      renderConsole();
    });
  }
  for (const button of body.querySelectorAll("[data-plugin-save]")) {
    button.addEventListener("click", () => savePluginSettings(button));
  }
  for (const button of body.querySelectorAll("[data-plugin-delete]")) {
    button.addEventListener("click", () => deletePlugin(button.dataset.pluginDelete));
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

// togglePluginSettings 展开/收起某个插件的设置表单（PL3）。
async function togglePluginSettings(id) {
  if (!id) return;
  if (state.pluginEditing === id) {
    state.pluginEditing = "";
    state.pluginDetail = null;
    renderConsole();
    return;
  }
  try {
    state.pluginDetail = await api.pluginSettings(id);
    state.pluginDetail.id = state.pluginDetail.id || id;
    state.pluginEditing = id;
    renderConsole();
  } catch (error) {
    toast(error instanceof ApiError ? error.message : "读取设置失败", "bad");
  }
}

// savePluginSettings 读取表单值并按 schema 类型提交（PL3）。
async function savePluginSettings(button) {
  const id = button.dataset.pluginSave;
  const form = button.closest(".plugin-settings");
  if (!id || !form) return;
  const values = {};
  for (const el of form.querySelectorAll("[data-setting-key]")) {
    const key = el.dataset.settingKey;
    if (!key) continue;
    if (el.type === "checkbox") values[key] = el.checked;
    else if (el.type === "number") {
      const n = Number(el.value);
      values[key] = Number.isFinite(n) && el.value !== "" ? n : null;
    } else values[key] = el.value;
  }
  try {
    await api.pluginSettingsSave(id, values);
    const plugin = (state.plugins || []).find((item) => item.id === id);
    let reloaded = false;
    if (plugin?.state === "ready") {
      try {
        await api.pluginUnload(id);
        await api.pluginLoad(id);
        reloaded = true;
      } catch {
        /* 设置已保存；加载失败保留在插件状态里 */
      }
    }
    state.pluginDetail = await api.pluginSettings(id);
    await ensurePlugins(true);
    renderConsole();
    toast(reloaded ? "设置已保存并重新加载" : "设置已保存");
  } catch (error) {
    toast(error instanceof ApiError ? error.message : "保存设置失败", "bad");
  }
}

// installPlugin 从项目插件仓库安装（PL1）：git clone + manifest 校验 + 安装标记。
async function installPlugin(button) {
  const id = button.dataset.pluginInstall;
  const source = button.dataset.pluginSource;
  if (!id || !source) return;
  const ok = await confirmDialog({
    title: "安装插件",
    message: `从 ${source} 安装「${id}」？\n\n安装 = 信任插件代码；它会以你的用户权限运行。`,
    confirmText: "安装",
  });
  if (!ok) return;
  const original = button.textContent;
  button.disabled = true;
  button.textContent = "安装中…";
  try {
    await api.pluginInstall(source, id);
    await ensurePlugins(true);
    renderConsole();
    toast("插件已安装");
  } catch (error) {
    toast(error instanceof ApiError ? error.message : "安装失败", "bad");
  } finally {
    button.disabled = false;
    button.textContent = original;
  }
}

// deletePlugin 删除插件：卸载、移除目录与设置（PL4，二次确认）。
async function deletePlugin(id) {
  if (!id) return;
  const plugin = (state.plugins || []).find((p) => p.id === id);
  const name = plugin?.name || id;
  const ok = await confirmDialog({
    title: `删除插件「${name}」？`,
    message: `会卸载并删除 $GQY_HOME/plugins/${id} 与它的设置，无法撤销。`,
    confirmText: "删除",
  });
  if (!ok) return;
  try {
    await api.pluginDelete(id);
    if (state.pluginEditing === id) {
      state.pluginEditing = "";
      state.pluginDetail = null;
    }
    await ensurePlugins(true);
    toast("插件已删除");
  } catch (error) {
    toast(error instanceof ApiError ? error.message : "删除失败", "bad");
  }
}
