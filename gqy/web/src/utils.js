// 纯工具函数（从 main.js 提取，无 DOM 依赖）。
// 新增纯函数放这里；改完跑 npm run build 重新生成 app.js。

export function asFiniteNumber(value, fallback = 0) {
  const number = Number(value);
  return Number.isFinite(number) ? number : fallback;
}

export function formatInteger(value) {
  const number = Math.max(0, asFiniteNumber(value));
  try {
    return new Intl.NumberFormat("zh-CN", { maximumFractionDigits: 0 }).format(number);
  } catch (_) {
    return String(Math.round(number));
  }
}

export function formatTokens(value) {
  const number = Math.max(0, asFiniteNumber(value));
  if (number < 1000) return formatInteger(number);
  const useMillions = number >= 1_000_000;
  const amount = number / (useMillions ? 1_000_000 : 1000);
  const digits = amount >= 100 ? 0 : amount >= 10 ? 1 : 1;
  const suffix = useMillions ? "M" : "k";
  try {
    return `${new Intl.NumberFormat("zh-CN", { maximumFractionDigits: digits }).format(amount)}${suffix}`;
  } catch (_) {
    return `${amount.toFixed(digits)}${suffix}`;
  }
}

export function parseDate(value) {
  if (value == null || value === "") return null;
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? null : date;
}

export function formatTime(value) {
  const date = parseDate(value);
  if (!date) return "";
  try {
    return new Intl.DateTimeFormat("zh-CN", { hour: "2-digit", minute: "2-digit", hour12: false }).format(date);
  } catch (_) {
    return date.toLocaleTimeString?.() || "";
  }
}

export function formatDateTime(value) {
  const date = parseDate(value);
  if (!date) return "";
  try {
    return new Intl.DateTimeFormat("zh-CN", {
      year: "numeric",
      month: "2-digit",
      day: "2-digit",
      hour: "2-digit",
      minute: "2-digit",
      hour12: false
    }).format(date);
  } catch (_) {
    return date.toLocaleString?.() || "";
  }
}

export function formatRelativeTime(value) {
  const date = parseDate(value);
  if (!date) return "";
  const difference = Date.now() - date.getTime();
  if (difference >= 0 && difference < 60_000) return "刚刚";
  if (difference >= 0 && difference < 3_600_000) return `${Math.max(1, Math.floor(difference / 60_000))} 分钟前`;
  const now = new Date();
  if (date.toDateString() === now.toDateString()) return formatTime(date);
  try {
    return new Intl.DateTimeFormat("zh-CN", { month: "numeric", day: "numeric" }).format(date);
  } catch (_) {
    return date.toLocaleDateString?.() || "";
  }
}

export function dayKey(value) {
  const date = parseDate(value);
  if (!date) return "unknown";
  return `${date.getFullYear()}-${date.getMonth()}-${date.getDate()}`;
}

export function formatDayLabel(value) {
  const date = parseDate(value);
  if (!date) return "较早";
  const today = new Date();
  const yesterday = new Date(today);
  yesterday.setDate(today.getDate() - 1);
  if (date.toDateString() === today.toDateString()) return "今天";
  if (date.toDateString() === yesterday.toDateString()) return "昨天";
  try {
    return new Intl.DateTimeFormat("zh-CN", { year: "numeric", month: "long", day: "numeric" }).format(date);
  } catch (_) {
    return date.toLocaleDateString?.() || "较早";
  }
}

export function firstLine(value) {
  return String(value || "").split(/\r?\n/, 1)[0].trim();
}

export function modelMark(model) {
  const source = String(model?.provider_name || model?.provider_id || model?.model || "").trim();
  if (!source) return "--";
  const words = source.split(/[\s._/-]+/).filter(Boolean);
  const mark = words.length > 1 ? `${words[0][0] || ""}${words[1][0] || ""}` : source.slice(0, 2);
  return mark.toLocaleUpperCase("en-US");
}
