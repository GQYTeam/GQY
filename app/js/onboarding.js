import { PREF, loadPref, savePref, state } from "./store.js";
import { THEMES, applyTheme, themeCardHTML } from "./themes.js";
import { closeOverlay, openOverlay, toast } from "./ui.js";
import { $ } from "./render.js";

let bound = false;

export function initOnboarding() {
  if (bound) return;
  bound = true;
  $("#finishOnboarding")?.addEventListener("click", finish);
  $("#skipOnboarding")?.addEventListener("click", finish);
  $("#onboardingThemes")?.addEventListener("click", (event) => {
    const card = event.target.closest("[data-theme-choice]");
    if (card) applyTheme(card.dataset.themeChoice);
  });
}

export function openOnboarding({ force = false } = {}) {
  if (!force && loadPref(PREF.onboarded, "") === "1") return;
  const box = $("#onboardingThemes");
  if (box) {
    box.innerHTML = THEMES.map((theme) => themeCardHTML(theme, theme.id === state.theme)).join("");
  }
  openOverlay("onboardingOverlay");
}

function finish() {
  savePref(PREF.onboarded, "1");
  closeOverlay("onboardingOverlay");
  $("#input")?.focus();
  toast("随时可以在设置里换主题");
}
