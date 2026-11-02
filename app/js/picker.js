import { api } from "./api.js";
import { PREF, loadPref, savePref, state } from "./store.js";
import { setHeader, $ } from "./render.js";
import { updateSendState } from "./chat.js";
import { esc } from "./markdown.js";
import { toast } from "./ui.js";

const RECENT_LIMIT = 5;
const CAPS = [
  ["tool_call", "工具"],
  ["vision", "图片"],
  ["thinking", "思考"],
];

let panelOpen = false;
let activeIndex = 0;
let activeFilters = new Set();

export function initPicker() {
  document.getElementById("setGlobalModel")?.addEventListener("click", async () => {
    if (!state.model) {
      toast("先选一个模型");
      return;
    }
    try {
      const result = await api.setActiveModel(state.model, true);
      state.activeModel = { model: state.model, source: "runtime", default: state.model };
      toast(result.persisted ? "已设为全局默认并持久化" : "已设为全局默认（未持久化）");
      renderGlobalHint();
    } catch (error) {
      toast(error?.message || "设置失败", "bad");
    }
  });

  const btn = $("#modelPickerBtn");
  const panel = $("#modelPickerPanel");
  const search = $("#modelPickerSearch");
  const vendor = $("#pickerVendor");
  btn?.addEventListener("click", (event) => {
    event.stopPropagation();
    togglePanel();
  });

  document.addEventListener("click", (event) => {
    if (panelOpen && !event.target.closest("#modelPickerPanel") && !event.target.closest("#modelPickerBtn")) {
      closePanel();
    }
  });

  search?.addEventListener("input", () => {
    activeIndex = 0;
    renderPicker();
  });

  vendor?.addEventListener("change", () => {
    activeIndex = 0;
    renderPicker();
  });

  document.addEventListener("keydown", (event) => {
    if (!panelOpen) return;
    const items = $$("[data-picker-id]", $("#modelPickerPanel"));
    if (event.key === "Escape") {
      event.preventDefault();
      closePanel();
      return;
    }
    if (event.key === "ArrowDown") {
      event.preventDefault();
      activeIndex = Math.min(activeIndex + 1, items.length - 1);
      focusActive(items);
      return;
    }
    if (event.key === "ArrowUp") {
      event.preventDefault();
      activeIndex = Math.max(activeIndex - 1, 0);
      focusActive(items);
      return;
    }
    if (event.key === "Enter") {
      const item = items[activeIndex];
      if (item) {
        event.preventDefault();
        selectModel(item.dataset.pickerId);
      }
    }
  });
}

// 更新全局默认模型提示（HR5）。
function renderGlobalHint() {
  const hint = document.getElementById("globalModelHint");
  if (!hint) return;
  const active = state.activeModel || {};
  if (active.source === "runtime" && active.model) hint.textContent = `全局默认：${active.model}（运行时）`;
  else if (active.default) hint.textContent = `全局默认：${active.default}（配置）`;
  else hint.textContent = "未设置全局默认";
}

export function renderPicker() {
  const btn = $("#modelPickerLabel");
  if (btn) btn.textContent = labelFor(state.model) || "未配置模型";

  renderGlobalHint();
  const list = $("#pickerList");
  const vendor = $("#pickerVendor");
  const capsBox = $("#pickerCaps");
  if (!list) return;

  renderCaps(capsBox);
  renderVendors(vendor);

  const query = ($("#modelPickerSearch")?.value || "").trim().toLowerCase();
  const capActive = CAPS.some(([id]) => activeFilters.has(id));
  const vendorFilter = vendor?.value || "";
  const models = state.models || [];

  const visible = models.filter((entry) => matches(entry, query, capActive, vendorFilter));

  const recents = loadRecents().filter((id) => models.some((m) => m.id === id));
  const recentEntries = recents
    .map((id) => models.find((m) => m.id === id))
    .filter((entry) => entry && visible.includes(entry))
    .slice(0, RECENT_LIMIT);

  const sections = [];
  if (recentEntries.length) {
    sections.push(renderSection("最近使用", recentEntries));
  }
  const groupEntries = visible.filter((entry) => entry.owned_by === "group");
  const modelEntries = visible.filter((entry) => entry.owned_by !== "group");
  if (groupEntries.length) sections.push(renderSection("路由组", groupEntries));
  if (modelEntries.length) sections.push(renderSection("模型", modelEntries));

  list.innerHTML = sections.join("") || `<div class="picker-empty">没有匹配的模型</div>`;

  const items = $$("[data-picker-id]", list);
  activeIndex = Math.min(activeIndex, Math.max(items.length - 1, 0));
  for (const item of list.querySelectorAll("[data-picker-id]")) {
    item.addEventListener("click", () => selectModel(item.dataset.pickerId));
  }
}

export function applyModel(id) {
  state.model = id || "";
  savePref(PREF.model, state.model);
  renderPicker();
  setHeader(state.sessionTitle, state.model || "");
  updateSendState();
}

function selectModel(id) {
  if (!id) return;
  applyModel(id);
  pushRecent(id);
  closePanel();
}

function renderCaps(box) {
  if (!box) return;
  box.innerHTML = CAPS.map(
    ([id, label]) =>
      `<button type="button" class="picker-chip${activeFilters.has(id) ? " active" : ""}" data-picker-cap="${id}">${label}</button>`,
  ).join("");
  for (const chip of box.querySelectorAll("[data-picker-cap]")) {
    chip.addEventListener("click", () => {
      const id = chip.dataset.pickerCap;
      if (activeFilters.has(id)) activeFilters.delete(id);
      else activeFilters.add(id);
      activeIndex = 0;
      renderPicker();
    });
  }
}

function renderVendors(vendor) {
  if (!vendor) return;
  const models = state.models || [];
  const vendors = Array.from(new Set(models.map((m) => m.owned_by || "").filter(Boolean))).sort();
  const current = vendor.value;
  vendor.innerHTML = [`<option value="">全部供应商</option>`]
    .concat(vendors.map((v) => `<option value="${esc(v)}">${esc(v === "group" ? "路由组" : v)}</option>`))
    .join("");
  if (vendors.includes(current)) vendor.value = current;
}

function matches(entry, query, capActive, vendorFilter) {
  if (vendorFilter && entry.owned_by !== vendorFilter) return false;
  if (capActive) {
    if (entry.owned_by === "group" || !entry.capabilities) return false;
    for (const [id] of CAPS) {
      if (activeFilters.has(id) && !entry.capabilities[id]) return false;
    }
  }
  if (!query) return true;
  const hay = [entry.id, entry.display_name, entry.owned_by, ...(entry.members || [])]
    .filter(Boolean)
    .join(" ")
    .toLowerCase();
  return hay.includes(query);
}

function renderSection(title, entries) {
  const rows = entries
    .map((entry, index) => {
      const caps = entry.capabilities ? capBadges(entry.capabilities) : "";
      const badges = [];
      if (entry.default) badges.push(`<span class="picker-badge default">默认</span>`);
      if (entry.owned_by === "group") badges.push(`<span class="picker-badge">${esc(entry.members?.length || 0)} 个成员</span>`);
      const meta = [entry.owned_by !== "group" ? esc(entry.owned_by || "") : "", caps].filter(Boolean).join(" · ");
      return `<button type="button" class="picker-item${entry.id === state.model ? " current" : ""}" data-picker-id="${esc(entry.id)}" role="option" aria-selected="${entry.id === state.model}">
        <span class="picker-item-main">
          <span class="picker-item-id">${esc(entry.display_name || entry.id)}${badges.join("")}</span>
          ${meta ? `<span class="picker-item-meta">${meta}</span>` : ""}
        </span>
        ${entry.id === state.model ? `<span class="picker-check">✓</span>` : ""}
      </button>`;
    })
    .join("");
  return `<div class="picker-section"><div class="picker-section-title">${esc(title)}</div>${rows}</div>`;
}

function capBadges(caps) {
  const parts = [];
  if (caps.tool_call) parts.push("工具");
  if (caps.vision) parts.push("图片");
  if (caps.thinking) parts.push("思考");
  if (Array.isArray(caps.reasoning_effort) && caps.reasoning_effort.length) parts.push(`思考力度 ${caps.reasoning_effort.join("/")}`);
  if (caps.context_window) parts.push(`上下文 ${caps.context_window}`);
  if (caps.max_output) parts.push(`输出 ${caps.max_output}`);
  return parts.length ? `能力 ${parts.join(" · ")}` : "能力未知";
}

function labelFor(id) {
  const model = (state.models || []).find((m) => m.id === id);
  if (!model) return "";
  return model.display_name || model.id;
}

function loadRecents() {
  try {
    const raw = loadPref(PREF.recentModels, "[]");
    const parsed = JSON.parse(raw);
    return Array.isArray(parsed) ? parsed.filter((item) => typeof item === "string") : [];
  } catch {
    return [];
  }
}

function pushRecent(id) {
  const recents = loadRecents().filter((item) => item !== id);
  recents.unshift(id);
  savePref(PREF.recentModels, JSON.stringify(recents.slice(0, RECENT_LIMIT)));
}

function togglePanel() {
  if (panelOpen) closePanel();
  else openPanel();
}

function openPanel() {
  const panel = $("#modelPickerPanel");
  const btn = $("#modelPickerBtn");
  if (!panel) return;
  panelOpen = true;
  panel.hidden = false;
  btn?.setAttribute("aria-expanded", "true");
  activeIndex = 0;
  renderPicker();
  $("#modelPickerSearch")?.focus();
}

function closePanel() {
  const panel = $("#modelPickerPanel");
  const btn = $("#modelPickerBtn");
  panelOpen = false;
  if (panel) panel.hidden = true;
  btn?.setAttribute("aria-expanded", "false");
}

function focusActive(items) {
  items[activeIndex]?.focus();
}

function $$(selector, root = document) {
  return Array.from((root || document).querySelectorAll(selector));
}
