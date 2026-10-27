/* 顾清影 · 设置/用量/数据 service：设置窗口、用量聚合、导出/导入、服务端探测 */
(() => {
  'use strict';
  const G = window.GQY;
  const app = G.app;
  const { uid, now, formatBytes } = G;
  const el = app.el;
  const $$ = app.$$;

  function syncSettingsUI() {
    el.motionSwitch.setAttribute('aria-checked', String(app.state.reduceMotion));
    el.sideSwitch.setAttribute('aria-checked', String(app.state.sidebarCollapsed));
    el.thinkSwitch.setAttribute('aria-checked', String(app.state.thinkOpen));
    $$('button', el.fontSeg).forEach((b) => b.setAttribute('aria-pressed', String(b.getAttribute('data-font') === app.state.fontSize)));
    el.aboutModels.forEach((node) => { node.textContent = app.activeModel(); });
    if (el.aboutModelInline) el.aboutModelInline.textContent = app.activeModel();
    if (el.aboutEffortInline) el.aboutEffortInline.textContent = app.state.effort;
    renderServerStatus();
    refreshUsage();
  }

  function applyFontSize() {
    document.documentElement.style.setProperty('--msg-size', app.FONT_SIZES[app.state.fontSize] || app.FONT_SIZES.normal);
  }

  function usageSnapshot() {
    let raw = '';
    try { raw = localStorage.getItem(app.STORE_KEY) || ''; } catch (err) {}
    if (!raw) {
      try {
        raw = JSON.stringify(app.state, (key, value) => {
          if (key === 'url' && typeof value === 'string' && value.indexOf('blob:') === 0) return undefined;
          return value;
        });
      } catch (err) { raw = ''; }
    }
    let bytes = 0;
    try { bytes = new Blob([raw]).size; } catch (err) { bytes = raw.length; }
    const budget = 5 * 1024 * 1024;
    const today = new Date();
    today.setHours(0, 0, 0, 0);
    const base = today.getTime();
    let convs = 0;
    let messages = 0;
    let assets = 0;
    const days = [0, 0, 0, 0, 0, 0, 0];
    app.state.workspaces.forEach((ws) => {
      ws.convs.forEach((conv) => {
        convs += 1;
        conv.messages.forEach((msg) => {
          messages += 1;
          if (msg.artifact) assets += 1;
          if (Array.isArray(msg.attachments)) assets += msg.attachments.length;
          const d = new Date(msg.ts || 0);
          d.setHours(0, 0, 0, 0);
          const diff = Math.round((base - d.getTime()) / 86400000);
          if (diff >= 0 && diff < 7) days[6 - diff] += 1;
        });
      });
    });
    return { bytes: bytes, budget: budget, convs: convs, messages: messages, assets: assets, days: days, base: base };
  }

  function renderServerStatus() {
    if (!el.serverStatus) return;
    const info = G.api && G.api.info ? G.api.info() : null;
    if (info && info.provider === 'openai-compatible') {
      el.serverStatus.textContent = '已连接 · ' + info.model;
    } else if (info && info.provider === 'mock') {
      el.serverStatus.textContent = '已连接 · 模拟流';
    } else {
      el.serverStatus.textContent = '未连接服务端';
    }
  }

  async function refreshUsage() {
    if (!el.usageStorageText) return;
    const u = usageSnapshot();
    const pct = Math.min(100, u.bytes / u.budget * 100);
    const pctText = (pct >= 10 ? Math.round(pct) : pct.toFixed(1)) + '%';
    el.usageStorageText.textContent = formatBytes(u.bytes);
    el.usageStoragePercent.textContent = pctText + ' · 5 MB';
    el.usageWorkspaces.textContent = String(app.state.workspaces.length);
    if (el.miniUsageText) el.miniUsageText.textContent = formatBytes(u.bytes);
    if (el.miniUsageBar) el.miniUsageBar.style.width = Math.max(2, pct) + '%';
    if (el.usageRing) {
      const circumference = 301.59;
      el.usageRing.style.strokeDashoffset = String(circumference - circumference * pct / 100);
    }

    if (G.api && G.api.usage && G.api.available()) {
      try {
        app.usageData = await G.api.usage();
      } catch (err) {
        app.usageData = null;
      }
    }
    const s = app.usageData || {};
    el.usageConvs.textContent = String(s.sessions != null ? s.sessions : u.convs);
    el.usageMessages.textContent = String(s.messages != null ? s.messages : u.messages);
    el.usageAssets.textContent = String(s.artifacts != null ? s.artifacts : u.assets);

    const weekday = ['日', '一', '二', '三', '四', '五', '六'];
    const days = Array.isArray(s.days) ? s.days : u.days;
    const maxDay = Math.max.apply(null, days.concat([1]));
    const bars = [];
    const axis = [];
    days.forEach((count, i) => {
      const d = new Date(Date.now() - (6 - i) * 86400000);
      const height = count ? Math.max(8, Math.round(count / maxDay * 100)) : 4;
      bars.push('<div class="usage-bar' + (i === 6 ? ' is-today' : '') + (count ? '' : ' is-empty') + '" title="' + (d.getMonth() + 1) + '/' + d.getDate() + ' · ' + count + ' 条"><i style="height:' + height + '%"></i></div>');
      axis.push('<span>' + (i === 6 ? '今天' : '周' + weekday[d.getDay()]) + '</span>');
    });
    el.usageBars.innerHTML = bars.join('');
    el.usageAxis.innerHTML = axis.join('');
    el.usageWeekTotal.textContent = days.reduce((a, b) => a + b, 0) + ' 条';

    app.updateContextChip();
  }

  function openSettingsTab(name, focusTab) {
    const valid = ['appearance', 'chat', 'data', 'about'];
    app.settingsTab = valid.indexOf(name) >= 0 ? name : 'appearance';
    el.settingsTabs.forEach((tab) => {
      const on = tab.getAttribute('data-settings-tab') === app.settingsTab;
      tab.classList.toggle('is-active', on);
      tab.setAttribute('aria-selected', String(on));
      tab.tabIndex = on ? 0 : -1;
    });
    el.settingsPanels.forEach((panel) => {
      const on = panel.getAttribute('data-settings-panel') === app.settingsTab;
      panel.classList.toggle('is-active', on);
      panel.hidden = !on;
    });
    if (focusTab) {
      const tab = el.settingsTabs.find((node) => node.getAttribute('data-settings-tab') === app.settingsTab);
      if (tab) tab.focus({ preventScroll: true });
    }
  }

  function openSettings() {
    syncSettingsUI();
    app.settingsTrigger = el.accountBtn;
    el.settingsLayer.hidden = false;
    document.body.classList.add('settings-open');
    app.syncInert();
    openSettingsTab(app.settingsTab, false);
    el.settingsWindow.focus({ preventScroll: true });
    refreshUsage();
  }

  function closeSettings() {
    if (el.settingsLayer.hidden) return;
    el.settingsLayer.hidden = true;
    document.body.classList.remove('settings-open');
    app.syncInert();
    const trigger = app.settingsTrigger;
    app.settingsTrigger = null;
    if (trigger && trigger.isConnected && typeof trigger.focus === 'function') trigger.focus({ preventScroll: true });
  }

  function openUsage() {
    if (!el.usageLayer) return;
    refreshUsage();
    el.usageLayer.hidden = false;
    app.syncInert();
    el.usageLayer.querySelector('.usage-window').focus({ preventScroll: true });
  }

  function closeUsage() {
    if (!el.usageLayer || el.usageLayer.hidden) return;
    el.usageLayer.hidden = true;
    app.syncInert();
    if (el.usageTopBtn) el.usageTopBtn.focus({ preventScroll: true });
  }

  function openExtensions() {
    if (!el.extensionsLayer) return;
    el.extensionsLayer.hidden = false;
    app.syncInert();
    el.extensionsLayer.querySelector('#extensionsWindow').focus({ preventScroll: true });
  }

  function closeExtensions() {
    if (!el.extensionsLayer || el.extensionsLayer.hidden) return;
    el.extensionsLayer.hidden = true;
    app.syncInert();
    if (el.extensionsBtn) el.extensionsBtn.focus({ preventScroll: true });
  }

  function trapTab(container, event) {
    if (!container) return;
    const focusables = $$('button:not([disabled]), [href], input:not([disabled]), textarea:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex="-1"])', container)
      .filter((node) => node.offsetParent !== null || node === document.activeElement);
    if (!focusables.length) return;
    const first = focusables[0];
    const last = focusables[focusables.length - 1];
    if (!container.contains(document.activeElement)) {
      event.preventDefault();
      first.focus({ preventScroll: true });
      return;
    }
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault();
      last.focus({ preventScroll: true });
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      first.focus({ preventScroll: true });
    }
  }

  function setView(next) {
    if (next === 'settings') { openSettings(); return; }
    app.view = 'chat';
    el.body.hidden = false;
    if (el.mainRow) el.mainRow.hidden = false;
    app.renderThread();
    app.closeDrawer();
  }

  function exportData() {
    try {
      const blob = new Blob([JSON.stringify(app.state, null, 2)], { type: 'application/json' });
      const a = document.createElement('a');
      a.href = URL.createObjectURL(blob);
      a.download = 'guqingying-chats.json';
      document.body.appendChild(a);
      a.click();
      a.remove();
      setTimeout(() => URL.revokeObjectURL(a.href), 2000);
      app.toast('已导出对话 JSON');
    } catch (err) {
      app.toast('导出失败');
    }
  }

  function importData(file) {
    if (!file) return;
    if (file.size > 8 * 1024 * 1024) { app.toast('文件太大，最多 8 MB'); return; }
    const reader = new FileReader();
    reader.onload = () => {
      let next;
      try { next = JSON.parse(String(reader.result)); }
      catch (err) { app.toast('不是有效的 JSON'); return; }
      const okShape = next && Array.isArray(next.workspaces) && next.workspaces.length &&
        next.workspaces.every((ws) => ws && typeof ws.name === 'string' && Array.isArray(ws.convs));
      if (!okShape) { app.toast('结构不匹配，需要导出时生成的文件'); return; }
      if (!window.confirm('导入会覆盖当前的 ' + app.state.workspaces.length + ' 个工作区，继续？')) return;
      if (app.stream) app.stopStreaming();
      try {
        localStorage.setItem(app.STORE_KEY, JSON.stringify(next, app.dropBlobUrls));
      } catch (err) {
        app.toast('导入失败：本地存储空间不足');
        return;
      }
      // 重新载入 state（store 暴露 reload）
      app.reloadState();
      applyFontSize();
      el.app.setAttribute('data-sidebar', app.state.sidebarCollapsed ? 'collapsed' : 'open');
      app.applyReduceMotion();
      renderServerStatus();
      app.renderAll();
      app.restoreDraft();
      closeSettings();
      setView('chat');
      app.toast('已导入 ' + app.state.workspaces.length + ' 个工作区');
    };
    reader.onerror = () => app.toast('读取文件失败');
    reader.readAsText(file);
  }

  function setupDetectedApi() {
    if (!G.api || !G.api.detect) { renderServerStatus(); return; }
    G.api.detect().then(() => {
      renderServerStatus();
      app.updateComposer();
      if (el.settingsLayer && !el.settingsLayer.hidden) syncSettingsUI();
      app.mirrorSession();
    });
  }

  function openWsDialog() {
    if (el.wsDialog.open) return;
    el.wsName.value = '';
    el.wsDialog.showModal();
    setTimeout(() => el.wsName.focus(), 30);
  }

  function applyReduceMotion() {
    document.documentElement.classList.toggle('reduce-motion', app.state.reduceMotion);
    G.clawd.sync();
    el.motionSwitch.setAttribute('aria-checked', String(app.state.reduceMotion));
  }

  function clearEverything() {
    if (!window.confirm('清空全部对话后不可恢复，确定继续？')) return;
    if (app.stream) app.stopStreaming();
    const settings = {
      model: app.state.model, effort: app.state.effort,
      fontSize: app.state.fontSize, thinkOpen: app.state.thinkOpen,
      reduceMotion: app.state.reduceMotion, sidebarCollapsed: app.state.sidebarCollapsed
    };
    app.resetState();
    app.state.model = settings.model;
    app.state.effort = settings.effort;
    app.state.fontSize = settings.fontSize;
    app.state.thinkOpen = settings.thinkOpen;
    app.state.reduceMotion = settings.reduceMotion;
    app.state.sidebarCollapsed = settings.sidebarCollapsed;
    applyFontSize();
    el.app.setAttribute('data-sidebar', app.state.sidebarCollapsed ? 'collapsed' : 'open');
    applyReduceMotion();
    app.save();
    app.renderAll();
    closeSettings();
    setView('chat');
    app.toast('已清空全部对话');
  }

  Object.assign(app, {
    syncSettingsUI, applyFontSize, usageSnapshot, renderServerStatus, refreshUsage,
    openSettingsTab, openSettings, closeSettings, openUsage, closeUsage,
    openExtensions, closeExtensions, trapTab, setView,
    exportData, importData, setupDetectedApi, openWsDialog, applyReduceMotion, clearEverything
  });
})();
