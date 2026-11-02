let toastTimer = 0;
const overlayStack = [];
let confirmResolve = null;
let confirmBound = false;

export const bus = new EventTarget();

export function emit(name, detail) {
  bus.dispatchEvent(new CustomEvent(name, { detail }));
}

export function on(name, handler) {
  bus.addEventListener(name, handler);
  return () => bus.removeEventListener(name, handler);
}

export function openOverlay(id, { focus = true } = {}) {
  const overlay = document.getElementById(id);
  if (!overlay) return;
  if (!overlayStack.includes(id)) overlayStack.push(id);
  overlay.hidden = false;
  const app = document.getElementById("app");
  if (app) app.inert = true;
  document.body.classList.add("modal-open");
  if (focus) {
    requestAnimationFrame(() => {
      const target = overlay.querySelector("input, textarea, select, button, a[href]");
      target?.focus();
    });
  }
}

export function closeOverlay(id) {
  const overlay = document.getElementById(id);
  if (!overlay) return;
  overlay.hidden = true;
  if (id === "confirmOverlay" && confirmResolve) {
    const resolve = confirmResolve;
    confirmResolve = null;
    resolve(false);
  }
  const index = overlayStack.lastIndexOf(id);
  if (index !== -1) overlayStack.splice(index, 1);
  if (!overlayStack.length) {
    const app = document.getElementById("app");
    if (app) app.inert = false;
    document.body.classList.remove("modal-open");
  }
}

export function closeTopOverlay() {
  const id = overlayStack[overlayStack.length - 1];
  if (id) closeOverlay(id);
  return id || "";
}

export function toast(message, kind = "") {
  const el = document.getElementById("toast");
  if (!el) return;
  el.textContent = message;
  el.classList.toggle("bad", kind === "bad");
  el.classList.add("show");
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => el.classList.remove("show"), 2800);
}

export function confirmDialog({
  title = "确认",
  message = "",
  confirmText = "确认",
  cancelText = "取消",
} = {}) {
  const overlay = document.getElementById("confirmOverlay");
  if (!overlay) return Promise.resolve(window.confirm(message || title));
  bindConfirm();
  const titleEl = document.getElementById("confirmTitle");
  const messageEl = document.getElementById("confirmMessage");
  const okEl = document.getElementById("confirmOk");
  const cancelEl = document.getElementById("confirmCancel");
  if (titleEl) titleEl.textContent = title;
  if (messageEl) messageEl.textContent = message;
  if (okEl) okEl.textContent = confirmText;
  if (cancelEl) cancelEl.textContent = cancelText;
  openOverlay("confirmOverlay", { focus: false });
  cancelEl?.focus();
  return new Promise((resolve) => {
    confirmResolve = resolve;
  });
}

function bindConfirm() {
  if (confirmBound) return;
  confirmBound = true;
  document.getElementById("confirmOk")?.addEventListener("click", () => {
    const resolve = confirmResolve;
    confirmResolve = null;
    closeOverlay("confirmOverlay");
    resolve?.(true);
  });
  document.getElementById("confirmCancel")?.addEventListener("click", () => {
    const resolve = confirmResolve;
    confirmResolve = null;
    closeOverlay("confirmOverlay");
    resolve?.(false);
  });
}
