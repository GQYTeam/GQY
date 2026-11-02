import { api, AuthError, ApiError } from "./api.js";
import { state } from "./store.js";
import { renderSessions, setHeader, $ } from "./render.js";
import { confirmDialog, emit, on, toast } from "./ui.js";
import {
  loadSessionMessages,
  resetChatInput,
  stopStreaming,
  updateSendState,
} from "./chat.js";

let bound = false;

export function initSessions() {
  if (bound) return;
  bound = true;

  $("#newChat")?.addEventListener("click", () => newChat());

  $("#sessionSearch")?.addEventListener("input", (event) => {
    state.sessionQuery = event.target.value;
    renderSessions();
  });

  const list = $("#sessionList");
  list?.addEventListener("click", (event) => {
    const remove = event.target.closest("[data-delete-session]");
    if (remove) {
      event.stopPropagation();
      deleteSession(remove.dataset.deleteSession);
      return;
    }
    const item = event.target.closest("[data-session-id]");
    if (item) openSession(item.dataset.sessionId);
  });

  list?.addEventListener("keydown", (event) => {
    if (event.key !== "Enter" && event.key !== " ") return;
    const item = event.target.closest("[data-session-id]");
    if (!item) return;
    event.preventDefault();
    openSession(item.dataset.sessionId);
  });

  on("sessions-changed", () => {
    loadSessions().catch(() => {});
  });
}

export async function loadSessions() {
  try {
    const sessions = await api.sessions();
    state.sessions = sessions;
    const current = sessions.find((session) => session.session_id === state.sessionId);
    if (current?.title) {
      state.sessionTitle = current.title;
      setHeader(state.sessionTitle, $("#sessionSub")?.textContent || "");
    }
    renderSessions();
  } catch (error) {
    if (error instanceof AuthError) {
      emit("auth-required");
      return;
    }
    if (error instanceof ApiError) {
      toast(error.message, "bad");
    }
  }
}

export async function newChat({ focus = true } = {}) {
  if (state.streaming) stopStreaming();
  await saveCurrentDraft($("#input")?.value || "");
  state.sessionId = null;
  state.sessionTitle = "新对话";
  state.messages = [];
  setSessionParam("");
  resetChatInput();
  setHeader("新对话", state.model || "本地优先 · 供应商由配置决定");
  renderSessions();
  emit("conversation-changed");
  closeSidebar();
  if (focus) $("#input")?.focus();
}

export async function openSession(id) {
  if (!id || id === state.sessionId) {
    closeSidebar();
    return;
  }
  if (state.streaming) stopStreaming();
  await saveCurrentDraft($("#input")?.value || "");

  try {
    const session = await api.session(id);
    state.sessionId = id;
    state.sessionTitle = session.manifest?.title || `会话 ${id.slice(0, 8)}`;
    loadSessionMessages(session);
    const draft = await api.draft(id);
    const input = $("#input");
    if (input) {
      input.value = draft || "";
      input.dispatchEvent(new Event("input", { bubbles: true }));
      if (draft) $("#draftStatus").textContent = "已恢复草稿";
    }
    setHeader(state.sessionTitle, state.model || "");
    setSessionParam(id);
    renderSessions();
    updateSendState();
    emit("conversation-changed");
    closeSidebar();
    if (!draft) input?.focus();
  } catch (error) {
    if (error instanceof AuthError) {
      emit("auth-required");
      return;
    }
    toast(error instanceof ApiError ? error.message : "打开会话失败", "bad");
    await loadSessions();
  }
}

async function deleteSession(id) {
  if (!id) return;
  const ok = await confirmDialog({
    title: "删除会话",
    message: "会话目录与事件流会被移除，无法撤销。",
    confirmText: "删除",
  });
  if (!ok) return;
  try {
    await api.deleteSession(id);
    if (state.sessionId === id) {
      state.sessionId = null;
      state.messages = [];
      state.sessionTitle = "新对话";
      setSessionParam("");
      resetChatInput();
      setHeader("新对话", state.model || "");
    }
    await loadSessions();
    toast("会话已删除");
  } catch (error) {
    if (error instanceof AuthError) {
      emit("auth-required");
      return;
    }
    toast(error instanceof ApiError ? error.message : "删除失败", "bad");
  }
}

async function saveCurrentDraft(text) {
  if (!state.sessionId) return;
  try {
    await api.saveDraft(state.sessionId, text || "");
  } catch {
    /* drafts are a convenience; do not block navigation */
  }
}

function closeSidebar() {
  $("#sidebar")?.classList.remove("open");
  const scrim = $("#sidebarScrim");
  if (scrim) scrim.hidden = true;
}

function setSessionParam(id) {
  const url = new URL(location.href);
  if (id) url.searchParams.set("session", id);
  else url.searchParams.delete("session");
  history.replaceState(null, "", url);
}
