export const PREF = {
  theme: "gqy.theme",
  model: "gqy.model",
  recentModels: "gqy.recentModels",
  reduceMotion: "gqy.reduceMotion",
  onboarded: "gqy.onboarded",
  sidebarCollapsed: "gqy.sidebarCollapsed",
};

export const state = {
  config: null,
  health: null,
  status: null,
  models: [],
  plugins: [],
  tools: [],
  sessions: [],
  sessionQuery: "",
  sessionId: null,
  sessionTitle: "新对话",
  messages: [],
  attachments: [],
  model: "",
  streaming: false,
  abort: null,
  expanded: new Set(),
  theme: "paper",
  reduceMotion: false,
  panels: [],
  booted: false,
};

export function loadPref(key, fallback = "") {
  try {
    const value = localStorage.getItem(key);
    return value == null ? fallback : value;
  } catch {
    return fallback;
  }
}

export function savePref(key, value) {
  try {
    localStorage.setItem(key, String(value));
  } catch {
    /* private window or blocked storage; the UI works without it */
  }
}

export function removePref(key) {
  try {
    localStorage.removeItem(key);
  } catch {
    /* ignore */
  }
}

export function uid(prefix = "id") {
  if (globalThis.crypto?.randomUUID) return `${prefix}-${crypto.randomUUID()}`;
  return `${prefix}-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`;
}

export function shortId(id = "") {
  return id.length > 10 ? id.slice(0, 8) : id;
}

export function formatTime(iso) {
  if (!iso) return "";
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return "";
  const now = new Date();
  const sameDay = date.toDateString() === now.toDateString();
  if (sameDay) {
    return date.toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit" });
  }
  return date.toLocaleDateString("zh-CN", { month: "numeric", day: "numeric" });
}

export function formatTokens(n) {
  if (n == null) return "—";
  if (n < 1000) return String(n);
  if (n < 1000000) return `${(n / 1000).toFixed(1)}k`;
  return `${(n / 1000000).toFixed(2)}m`;
}
