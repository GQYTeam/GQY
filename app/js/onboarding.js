/* 顾清影 · 首次引导：工作区、默认模型、动效 */
(() => {
  'use strict';
  const G = window.GQY;
  const app = G.app;
  const el = app.el;

  function open() {
    if (!el.onboarding) return;
    const ws = app.activeWs();
    el.onboardWs.value = (ws && ws.name) || 'gqy';
    el.onboardModel.value = app.state.model || '';
    el.onboardMotion.checked = !!app.state.reduceMotion;
    el.onboarding.hidden = false;
    setTimeout(() => el.onboardWs.focus({ preventScroll: true }), 30);
  }

  function close() {
    if (!el.onboarding) return;
    el.onboarding.hidden = true;
  }

  function complete(skip) {
    if (!skip) {
      const name = el.onboardWs.value.trim();
      if (!name) { el.onboardWs.focus(); return; }
      const ws = app.activeWs();
      if (ws) ws.name = name.slice(0, 40);
      app.state.model = el.onboardModel.value.trim();
      app.state.reduceMotion = !!el.onboardMotion.checked;
      app.applyReduceMotion();
    }
    app.state.onboarded = true;
    app.save();
    app.renderAll();
    close();
    app.toast(skip ? '已跳过引导，设置里可以重新打开' : '欢迎使用顾清影');
  }

  if (el.onboardStart) el.onboardStart.addEventListener('click', () => complete(false));
  if (el.onboardSkip) el.onboardSkip.addEventListener('click', () => complete(true));
  if (el.onboardWs) el.onboardWs.addEventListener('keydown', (e) => {
    if (e.key === 'Enter') { e.preventDefault(); complete(false); }
  });
  if (el.onboardModel) el.onboardModel.addEventListener('keydown', (e) => {
    if (e.key === 'Enter') { e.preventDefault(); complete(false); }
  });

  G.onboarding = { open, close, complete };
  app.onboarding = G.onboarding;

  if (!app.state.onboarded) open();
})();
