import { state, formatTime, shortId } from "./store.js";
import { esc } from "./markdown.js";
import { closeOverlay, emit, openOverlay, toast } from "./ui.js";
import { $, $$ } from "./render.js";
import { newChat, openSession } from "./sessions.js";
import { openSettings } from "./settings.js";

let bound = false;
let items = [];
let active = 0;

const CONSOLE_ACTIONS = [
  ["usage", "用量", "本会话 tokens 与账号配额"],
  ["models", "模型", "当前模型与路由组"],
  ["providers", "供应商", "provider、账号与密钥状态"],
  ["gateway", "网关", "健康、冷却与配置重载"],
  ["plugins", "插件", "本机插件运行时"],
  ["tools", "工具", "核心工具契约"],
  ["appearance", "外观", "主题与动效"],
  ["about", "关于", "构建与端点"],
];

export function initPalette() {
  if (bound) return;
  bound = true;

  $("#openPalette")?.addEventListener("click", openPalette);
  $("#paletteInput")?.addEventListener("input", () => {
    active = 0;
    render();
  });
  $("#paletteList")?.addEventListener("click", (event) => {
    const item = event.target.closest("[data-palette-index]");
    if (!item) return;
    run(Number(item.dataset.paletteIndex));
  });
  $("#openShortcuts")?.addEventListener("click", openShortcuts);

  document.addEventListener("keydown", (event) => {
    const mod = event.metaKey || event.ctrlKey;
    if (mod && event.key.toLowerCase() === "k") {
      event.preventDefault();
      openPalette();
      return;
    }
    if (mod && event.key === ",") {
      event.preventDefault();
      openSettings("appearance");
      return;
    }
    if (mod && event.key === "/") {
      event.preventDefault();
      openShortcuts();
      return;
    }
    if (event.altKey && event.key.toLowerCase() === "n") {
      event.preventDefault();
      newChat();
      return;
    }
    if (event.key === "?" && !isTyping(event.target)) {
      event.preventDefault();
      openShortcuts();
      return;
    }
    if ($("#paletteOverlay")?.hidden !== false) return;
    const list = visibleItems();
    if (event.key === "ArrowDown") {
      event.preventDefault();
      active = Math.min(active + 1, list.length - 1);
      paintActive();
      return;
    }
    if (event.key === "ArrowUp") {
      event.preventDefault();
      active = Math.max(active - 1, 0);
      paintActive();
      return;
    }
    if (event.key === "Enter") {
      event.preventDefault();
      run(active);
    }
  });

  document.addEventListener("click", (event) => {
    if (event.target.closest("#openShortcuts")) {
      openShortcuts();
    }
  });
}

export function openPalette() {
  const input = $("#paletteInput");
  if (input) input.value = "";
  active = 0;
  render();
  openOverlay("paletteOverlay");
  input?.focus();
}

function openShortcuts() {
  openOverlay("shortcutsOverlay");
}

function isTyping(target) {
  if (!target) return false;
  const tag = target.tagName;
  return tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT" || target.isContentEditable;
}

function commands() {
  const list = [
    { title: "新对话", sub: "清空当前视图，开始一轮新会话", key: "Alt N", run: () => newChat() },
    { title: "聚焦输入框", sub: "回到消息输入", key: "", run: () => $("#input")?.focus() },
    { title: "会话信息", sub: "打开右侧上下文与产物抽屉", key: "", run: () => $("#toggleContext")?.click() },
    { title: "视觉 Demo", sub: "六套主题对照页", key: "", run: () => window.open("demos.html", "_blank", "noopener") },
    { title: "键盘快捷键", sub: "查看全部快捷键", key: "?", run: () => openShortcuts() },
    { title: "重播引导", sub: "重新选择主题", key: "", run: () => emit("onboarding-open", { force: true }) },
  ];
  for (const [tab, label, sub] of CONSOLE_ACTIONS) {
    list.push({ title: `控制台 · ${label}`, sub, key: "", run: () => openSettings(tab) });
  }
  return list;
}

function visibleItems() {
  const query = ($("#paletteInput")?.value || "").trim().toLowerCase();
  const matchedCommands = commands().filter((item) => {
    if (!query) return true;
    return item.title.toLowerCase().includes(query) || item.sub.toLowerCase().includes(query);
  });
  const matchedSessions = state.sessions
    .filter((session) => {
      if (!query) return true;
      const title = (session.title || session.session_id).toLowerCase();
      return title.includes(query);
    })
    .slice(0, query ? 8 : 4)
    .map((session) => ({
      title: session.title || `会话 ${shortId(session.session_id)}`,
      sub: `${formatTime(session.created_at)} · ${session.session_id.slice(0, 8)}`,
      key: "会话",
      run: () => openSession(session.session_id),
    }));

  return [...matchedCommands, ...matchedSessions];
}

function render() {
  const list = $("#paletteList");
  if (!list) return;
  items = visibleItems();
  active = Math.min(active, Math.max(items.length - 1, 0));
  if (!items.length) {
    list.innerHTML = `<div class="palette-empty">没有匹配的会话或命令</div>`;
    return;
  }
  const commandsPart = items.filter((item) => item.key !== "会话");
  const sessionsPart = items.filter((item) => item.key === "会话");
  const section = (title, entries) => {
    if (!entries.length) return "";
    const start = items.indexOf(entries[0]);
    return `<div class="palette-group">${esc(title)}</div>${entries
      .map((item, i) => {
        const index = start + i;
        return `<button class="palette-item${index === active ? " active" : ""}" type="button" data-palette-index="${index}" role="option" aria-selected="${index === active}">
          <span class="palette-item-main"><span class="palette-item-title">${esc(item.title)}</span><span class="palette-item-sub">${esc(item.sub)}</span></span>
          ${item.key ? `<span class="palette-item-key">${esc(item.key)}</span>` : ""}
        </button>`;
      })
      .join("")}`;
  };
  list.innerHTML = section("命令", commandsPart) + section("会话", sessionsPart);
  paintActive();
}

function paintActive() {
  const nodes = $$("[data-palette-index]", $("#paletteList"));
  nodes.forEach((node) => {
    const isActive = Number(node.dataset.paletteIndex) === active;
    node.classList.toggle("active", isActive);
    node.setAttribute("aria-selected", isActive ? "true" : "false");
    if (isActive) node.scrollIntoView({ block: "nearest" });
  });
}

function run(index) {
  const item = items[index];
  if (!item) return;
  closeOverlay("paletteOverlay");
  try {
    item.run();
  } catch (error) {
    toast(error instanceof Error ? error.message : "命令执行失败", "bad");
  }
}
