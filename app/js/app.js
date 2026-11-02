import { api, AuthError, ApiError, events } from "./api.js";
import { PREF, loadPref, savePref, state } from "./store.js";
import { applyReduceMotion, applyTheme, THEMES, findTheme } from "./themes.js";
import { initChat, updateSendState } from "./chat.js";
import { initSessions, loadSessions, openSession } from "./sessions.js";
import { initSettings, openSettings } from "./settings.js";
import { initContext } from "./context.js";
import { initBoard } from "./board.js";
import { initOnboarding, openOnboarding } from "./onboarding.js";
import { initPicker, renderPicker } from "./picker.js";
import { initPalette, openPalette } from "./palette.js";
import {
  closeOverlay,
  closeTopOverlay,
  on,
  openOverlay,
  toast,
} from "./ui.js";
import { renderMessages, setHeader, $ } from "./render.js";

let bindingDone = false;

function initTheme() {
  const params = new URLSearchParams(location.search);
  if (params.get("onboarded") === "1") savePref(PREF.onboarded, "1");
  const requested = params.get("theme");
  const saved = loadPref(PREF.theme, "paper");
  const theme = requested && THEMES.some((item) => item.id === requested) ? requested : saved;
  applyTheme(theme, true);

  const motionPref = loadPref(PREF.reduceMotion, "");
  const prefersReduced = window.matchMedia?.("(prefers-reduced-motion: reduce)")?.matches || false;
  const noAnim = params.get("noanim") === "1";
  applyReduceMotion(noAnim || (motionPref === "" ? prefersReduced : motionPref === "1"), false);
}

function bindGlobal() {
  if (bindingDone) return;
  bindingDone = true;

  for (const button of document.querySelectorAll("[data-close]")) {
    button.addEventListener("click", () => closeOverlay(button.dataset.close));
  }

  for (const overlay of document.querySelectorAll(".overlay")) {
    overlay.addEventListener("click", (event) => {
      if (event.target !== overlay) return;
      if (overlay.id === "loginOverlay") return;
      closeOverlay(overlay.id);
    });
  }

  document.addEventListener("keydown", (event) => {
    if (event.key !== "Escape") return;
    if ($("#loginOverlay")?.hidden === false) return;
    if (document.body.classList.contains("modal-open")) {
      event.preventDefault();
      closeTopOverlay();
      return;
    }
    if ($("#sidebar")?.classList.contains("open")) {
      closeSidebar();
    }
  });

  const appShell = $("#app");
  if (loadPref(PREF.sidebarCollapsed, "") === "1") appShell?.classList.add("sidebar-collapsed");
  $("#collapseSidebar")?.addEventListener("click", () => {
    appShell?.classList.add("sidebar-collapsed");
    savePref(PREF.sidebarCollapsed, "1");
  });
  $("#toggleSidebar")?.addEventListener("click", () => {
    if (appShell?.classList.contains("sidebar-collapsed")) {
      appShell.classList.remove("sidebar-collapsed");
      savePref(PREF.sidebarCollapsed, "0");
      return;
    }
    const sidebar = $("#sidebar");
    const scrim = $("#sidebarScrim");
    if (!sidebar) return;
    const open = !sidebar.classList.contains("open");
    sidebar.classList.toggle("open", open);
    if (scrim) scrim.hidden = !open;
  });
  $("#sidebarScrim")?.addEventListener("click", closeSidebar);

  $("#loginForm")?.addEventListener("submit", async (event) => {
    event.preventDefault();
    const input = $("#loginToken");
    const token = input?.value.trim() || "";
    if (!token) return;
    const button = event.target.querySelector("button[type=submit]");
    if (button) button.disabled = true;
    try {
      await api.login(token);
      if (input) input.value = "";
      closeOverlay("loginOverlay");
      toast("已登录");
      await bootstrap();
    } catch (error) {
      toast(error instanceof ApiError ? error.message : "登录失败", "bad");
    } finally {
      if (button) button.disabled = false;
    }
  });

  on("auth-required", openLogin);
  on("persona-changed", () => {
    applyPersonaChrome();
    renderMessages();
  });
  on("lightbox", (event) => {
    const img = $("#lightboxImg");
    if (img) img.src = event.detail;
    openOverlay("lightbox");
  });
  on("onboarding-open", (event) => {
    openOnboarding({ force: Boolean(event.detail?.force) });
  });
}

function applyPersonaChrome() {
  const persona = state.persona || {};
  const name = persona.name || "GQY";
  const mark = document.querySelector(".brand-mark");
  if (mark) {
    mark.innerHTML = "";
    if (persona.avatar) {
      const img = document.createElement("img");
      img.src = persona.avatar;
      img.alt = "";
      mark.append(img);
    } else {
      mark.textContent = Array.from(name)[0] || "G";
    }
  }
  const meta = document.querySelector("#brandMeta");
  if (meta) meta.textContent = persona.tagline || "本地网关";
  document.title = name && name !== "GQY" ? `${name} · GQY` : "GQY";
}

function closeSidebar() {
  $("#sidebar")?.classList.remove("open");
  const scrim = $("#sidebarScrim");
  if (scrim) scrim.hidden = true;
}

function openLogin() {
  openOverlay("loginOverlay");
  $("#loginToken")?.focus();
}

function showBootError(message) {
  const title = $(".empty-title");
  const copy = $(".empty-copy");
  if (title) title.textContent = "连不上网关。";
  if (copy) copy.textContent = message;
  setHeader("新对话", "网关未就绪");
}

// initLiveUpdates 建立一条持久连接（SSE），服务端配置/插件/全局模型变化即时生效（HR7）。
function initLiveUpdates() {
  if (state.liveStream) return;
  state.liveStream = events(async (event) => {
    try {
      if (event.type === "config") {
        state.config = await api.config();
        state.models = await api.models();
    state.activeModel = await api.activeModel().catch(() => null);
        ensureModelSelection();
        renderPicker();
        state.status = await api.status();
        renderGatewayPill();
      } else if (event.type === "model") {
        state.models = await api.models();
    state.activeModel = await api.activeModel().catch(() => null);
        ensureModelSelection();
        renderPicker();
      } else if (event.type === "plugins") {
        emit("plugins-changed");
      }
    } catch {
      /* 断线由 EventSource 自动重连；单次刷新失败忽略 */
    }
  });
}

async function bootstrap() {
  try {
    state.health = await api.health();
    state.config = await api.config();
    state.persona = await api.persona();
    applyPersonaChrome();
    state.models = await api.models();
    state.activeModel = await api.activeModel().catch(() => null);
    ensureModelSelection();
    renderPicker();
    await loadSessions();
    const requestedSession = new URLSearchParams(location.search).get("session");
    if (requestedSession) await openSession(requestedSession);
    state.status = await api.status();
    renderGatewayPill();
    setHeader(state.sessionTitle, state.model || "");
    updateSendState();
    state.booted = true;
    initLiveUpdates();
    if (loadPref(PREF.onboarded, "") !== "1") {
      openOnboarding();
    }
  } catch (error) {
    if (error instanceof AuthError) {
      openLogin();
      return;
    }
    const message = error instanceof ApiError ? error.message : "请求失败";
    showBootError(message);
    toast(message, "bad");
  }
}

function ensureModelSelection() {
  const saved = loadPref(PREF.model, "");
  const available = state.models.some((model) => model.id === saved);
  state.model = available
    ? saved
    : state.models.find((model) => model.default)?.id || state.models[0]?.id || "";
  if (state.model) savePref(PREF.model, state.model);
}

function renderGatewayPill() {
  const pill = $("#gatewayPill");
  const text = $("#gatewayText");
  if (!pill || !text) return;
  const accounts = state.status?.accounts || [];
  pill.classList.remove("ok", "warn", "bad");
  if (!accounts.length) {
    text.textContent = "未配置";
    return;
  }
  const healthy = accounts.filter((account) => account.healthy).length;
  const cooling = accounts.filter((account) => account.cooldown_remaining_s > 0).length;
  if (healthy === accounts.length) {
    pill.classList.add("ok");
    text.textContent = `${healthy}/${accounts.length} 账号健康`;
  } else if (healthy > 0) {
    pill.classList.add("warn");
    text.textContent = `${healthy}/${accounts.length} 健康${cooling ? ` · ${cooling} 冷却` : ""}`;
  } else {
    pill.classList.add("bad");
    text.textContent = "全部不可用";
  }
  pill.title = accounts
    .map((account) => `${account.provider}/${account.id}: ${account.healthy ? "健康" : "暂停"}${account.cooldown_remaining_s ? `，冷却 ${account.cooldown_remaining_s}s` : ""}`)
    .join("\n");
}

async function main() {
  initTheme();
  initChat();
  initSessions();
  initSettings();
  initContext();
  initBoard();
  initOnboarding();
  initPicker();
  initPalette();
  bindGlobal();

  const requested = new URLSearchParams(location.search).get("theme");
  if (requested && THEMES.some((item) => item.id === requested)) {
    const theme = findTheme(requested);
    document.documentElement.dataset.theme = theme.id;
    state.theme = theme.id;
  }

  await bootstrap();
  const params = new URLSearchParams(location.search);
  const consoleTab = params.get("console");
  if (consoleTab) openSettings(consoleTab);
  if (params.get("context") === "1") $("#toggleContext")?.click();
  if (params.get("palette") === "1") openPalette();
}

main().catch((error) => {
  if (error instanceof AuthError) {
    openLogin();
    return;
  }
  showBootError(error instanceof Error ? error.message : "启动失败");
});
