import { PREF, state, savePref } from "./store.js";

export const THEMES = [
  {
    id: "paper",
    name: "暖纸",
    tag: "paper",
    blurb: "暖纸底、赤陶强调、衬线标题。适合长时间阅读与写作。",
    colors: { bg: "#f2ece2", surface: "#fbf7ef", accent: "#b5522f", ink: "#221c17", line: "#d9cfbf" },
  },
  {
    id: "midnight",
    name: "午夜",
    tag: "midnight",
    blurb: "深色玻璃、冷色荧光。夜里盯着网关和长日志时更舒服。",
    colors: { bg: "#080b14", surface: "#0f1524", accent: "#64e6d4", ink: "#e8eefc", line: "#243049" },
  },
  {
    id: "editorial",
    name: "校样",
    tag: "editorial",
    blurb: "白底黑字、钴蓝强调、直角与细线。像一份排好的校样。",
    colors: { bg: "#fbfaf6", surface: "#ffffff", accent: "#1d4ed8", ink: "#151515", line: "#d8d5ca" },
  },
  {
    id: "moss",
    name: "苔原",
    tag: "moss",
    blurb: "灰绿、圆角、柔软留白。更轻的工作台气质。",
    colors: { bg: "#eef1e7", surface: "#f9fbf4", accent: "#4f6f43", ink: "#22281e", line: "#cfd8c2" },
  },
  {
    id: "dawn",
    name: "晨光",
    tag: "dawn",
    blurb: "暖白纸面与紫藤色。向参考 GQY WebUI 的 dawn 主题致意。",
    colors: { bg: "#f8f3ea", surface: "#fffdf8", accent: "#7568b0", ink: "#2b2633", line: "#ded6e6" },
  },
  {
    id: "dusk",
    name: "夜阑",
    tag: "dusk",
    blurb: "暮蓝底色与雾蓝强调。参考 GQY WebUI 的 dusk 主题。",
    colors: { bg: "#141826", surface: "#1b2032", accent: "#aebde8", ink: "#e6e9f5", line: "#303853" },
  },
];

export function findTheme(id) {
  return THEMES.find((theme) => theme.id === id) || THEMES[0];
}

export function applyTheme(id, persist = true) {
  const theme = findTheme(id);
  state.theme = theme.id;
  document.documentElement.dataset.theme = theme.id;
  if (persist) savePref(PREF.theme, theme.id);
  const meta = document.querySelector('meta[name="theme-color"]');
  if (meta) meta.content = theme.colors.accent;
  for (const card of document.querySelectorAll("[data-theme-choice]")) {
    card.classList.toggle("active", card.dataset.themeChoice === theme.id);
    card.setAttribute("aria-pressed", card.dataset.themeChoice === theme.id ? "true" : "false");
  }
  return theme;
}

export function applyReduceMotion(value, persist = true) {
  state.reduceMotion = Boolean(value);
  document.documentElement.dataset.reduceMotion = state.reduceMotion ? "true" : "false";
  if (persist) savePref(PREF.reduceMotion, state.reduceMotion ? "1" : "0");
  for (const toggle of document.querySelectorAll("[data-reduce-motion]")) {
    toggle.setAttribute("aria-pressed", state.reduceMotion ? "true" : "false");
  }
}

export function previewHTML(theme) {
  const c = theme.colors;
  return `<span class="theme-preview" style="background:${c.bg}" aria-hidden="true">
    <span class="pv-side" style="background:${c.surface};border:1px solid ${c.line}"></span>
    <span class="pv-main">
      <span class="pv-line" style="width:48%;background:${c.ink};opacity:.75"></span>
      <span class="pv-line" style="width:86%;background:${c.line}"></span>
      <span class="pv-line" style="width:70%;background:${c.line}"></span>
      <span class="pv-line short" style="background:${c.accent};opacity:.9"></span>
    </span>
  </span>`;
}

export function themeCardHTML(theme, active = false) {
  return `<button class="theme-card${active ? " active" : ""}" type="button" data-theme-choice="${theme.id}" aria-pressed="${active}">
    ${previewHTML(theme)}
    <span class="theme-name"><span>${theme.name}</span><span>${theme.tag}</span></span>
    <span class="theme-blurb">${theme.blurb}</span>
  </button>`;
}
