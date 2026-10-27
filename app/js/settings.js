/* 顾清影 · 设置/用量/数据 service：设置窗口、用量聚合、导出/导入、服务端探测 */
(() => {
  'use strict';
  const G = window.GQY;
  const app = G.app;
  const { uid, now, esc, formatBytes } = G;
  const el = app.el;
  const $$ = app.$$;

  function syncSettingsUI() {
    el.motionSwitch.setAttribute('aria-checked', String(app.state.reduceMotion));
    if (el.themeSwitch) el.themeSwitch.setAttribute('aria-checked', String(!!app.state.darkMode));
    if (el.localeSelect) el.localeSelect.value = app.state.locale === 'en' ? 'en' : 'zh';
    el.sideSwitch.setAttribute('aria-checked', String(app.state.sidebarCollapsed));
    el.thinkSwitch.setAttribute('aria-checked', String(app.state.thinkOpen));
    if (el.voiceSwitch) el.voiceSwitch.setAttribute('aria-checked', String(!!app.state.voiceEnabled));
    if (el.micBtn) {
      el.micBtn.disabled = !app.state.voiceEnabled;
      el.micBtn.title = app.state.voiceEnabled ? '语音输入' : '语音输入已关闭（设置 → 语音）';
    }
    if (el.voiceNote) {
      el.voiceNote.textContent = app.state.voiceEnabled
        ? '语音输入已开启；不支持 SpeechRecognition 的浏览器会提示不可用。'
        : '语音输入已关闭；打开开关后输入框的麦克风才可用。';
    }
    $$('button', el.fontSeg).forEach((b) => b.setAttribute('aria-pressed', String(b.getAttribute('data-font') === app.state.fontSize)));
    el.aboutModels.forEach((node) => { node.textContent = app.activeModel(); });
    if (el.aboutModelInline) el.aboutModelInline.textContent = app.activeModel();
    if (el.aboutEffortInline) el.aboutEffortInline.textContent = app.state.effort;
    if (el.aboutModelInline2) el.aboutModelInline2.textContent = app.activeModel();
    if (el.aboutEffortInline2) el.aboutEffortInline2.textContent = app.state.effort;
    renderServerStatus();
    refreshUsage();
  }

  function applyFontSize() {
    document.documentElement.style.setProperty('--msg-size', app.FONT_SIZES[app.state.fontSize] || app.FONT_SIZES.normal);
  }

  function applyTheme() {
    document.documentElement.setAttribute('data-theme', app.state.darkMode ? 'dark' : 'light');
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
    if (app.settingsTab === 'usage') renderUsagePanel();
  }

  function openSettingsTab(name, focusTab) {
    const valid = ['general', 'models', 'personas', 'memory', 'knowledge', 'voice', 'packages', 'tools', 'usage', 'accounts'];
    app.settingsTab = valid.indexOf(name) >= 0 ? name : 'general';
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
    if (app.settingsTab === 'models') {
      loadProviders();
      renderGatewayPanel();
    }
    if (app.settingsTab === 'personas') renderPersonasPanel();
    if (app.settingsTab === 'memory') renderMemoryPanel();
    if (app.settingsTab === 'knowledge') renderKnowledgePanel();
    if (app.settingsTab === 'voice') syncSettingsUI();
    if (app.settingsTab === 'packages') refreshPlugins();
    if (app.settingsTab === 'tools') renderToolsPanel();
    if (app.settingsTab === 'usage') renderUsagePanel();
    if (app.settingsTab === 'accounts') renderAccountsPanel();
  }

  async function loadProviders() {
    if (!el.providerList || !G.api || !G.api.providers) return;
    try {
      const [list, status] = await Promise.all([
        G.api.providers(),
        G.api.gatewayStatus ? G.api.gatewayStatus().catch(() => null) : Promise.resolve(null)
      ]);
      app.providerCache = list;
      if (status) app.gatewayCache = status;
      if (el.providerCount) el.providerCount.textContent = String(list.length);
      el.providerList.innerHTML = list.length ? list.map((p) =>
        '<div class="provider-row"><span class="provider-id">' + esc(p.id || '') + '</span>' +
        '<span class="provider-name">' + esc(p.name || p.id || '') + '</span>' +
        '<span class="provider-base">' + esc(p.baseUrl || '') + '</span>' +
        '<span class="feature-tag ' + (p.keySet ? 'ok' : 'warn') + '">' + (p.keySet ? '有密钥' : '无密钥') + '</span></div>'
      ).join('') : '<p class="feature-empty">还没有 provider。填写下面的表单，或设置 GQY_API_KEY。</p>';
      renderProviderEditor(list, status);
    } catch (err) {
      if (el.providerCount) el.providerCount.textContent = '0';
      if (el.providerList) el.providerList.innerHTML = '<p class="feature-empty">provider 列表不可用。</p>';
    }
  }

  function renderProviderEditor(list, status) {
    if (!el.providerEditor) return;
    const editing = app.editingProvider ? (list || []).find((p) => p.id === app.editingProvider) : null;
    const groups = (status && status.groups) || [];
    const def = (status && status.default) || {};
    const providerRows = (list || []).map((p) =>
      '<div class="feature-row"><div class="feature-main"><b>' + esc(p.name || p.id) +
      ' <span class="feature-tag">' + esc(p.id) + '</span></b><small>' +
      esc(p.baseUrl || '') + ' · ' + esc(p.defaultModel || '') + ' · ' + (p.models || []).length + ' 个模型 · ' +
      ((p.accounts || []).length || (p.keySet ? 1 : 0)) + ' 个账号 · ' +
      (p.calls || 0) + ' 调用 / ' + (p.failures || 0) + ' 失败' +
      (p.cooldownUntil ? ' · 冷却至 ' + esc(p.cooldownUntil) : '') + '</small></div>' +
      '<div class="feature-actions">' +
      (p.id === 'env'
        ? '<span class="feature-tag">由环境变量配置</span>'
        : '<button class="btn" type="button" data-provider-edit="' + esc(p.id) + '">编辑</button>' +
          '<button class="btn btn-danger" type="button" data-provider-del="' + esc(p.id) + '">删除</button>') +
      '</div></div>'
    ).join('');
    const accounts = editing && Array.isArray(editing.accounts) ? editing.accounts : [];
    const accountRows = accounts.length ? accounts.map((a) =>
      '<div class="feature-row"><div class="feature-main"><b>' + esc(a.name || a.id) +
      ' <span class="feature-tag">' + esc(a.id) + '</span></b><small>' +
      (a.keySet ? '有密钥' : '无密钥') + ' · 今日 ' + (a.usedTokens || 0) +
      (a.dailyTokenLimit ? ' / ' + a.dailyTokenLimit : ' / 不限') + ' token · ' +
      (a.calls || 0) + ' 调用 / ' + (a.failures || 0) + ' 失败' +
      (a.cooldownUntil ? ' · 冷却至 ' + esc(a.cooldownUntil) : '') + '</small></div>' +
      '<div class="feature-actions"><button class="btn btn-danger" type="button" data-account-del="' + esc(a.id) + '" data-account-provider="' + esc(editing.id) + '">删除</button></div></div>'
    ).join('') : '<p class="feature-empty">未单独配置账号；使用 provider 的 api_key。</p>';
    const accountSection = editing
      ? '<div class="panel-list-row" style="margin-top:14px"><span>账号 / 配额</span><b>' + accounts.length + ' 个</b></div>' +
        '<form class="feature-form" data-account-form="' + esc(editing.id) + '"><div class="feature-form-row">' +
        '<input name="id" type="text" maxlength="32" placeholder="账号 id">' +
        '<input name="name" type="text" maxlength="60" placeholder="名称">' +
        '<input name="daily_token_limit" type="number" min="0" placeholder="日 token 上限（0 不限）"></div>' +
        '<input name="api_key" type="password" maxlength="300" placeholder="账号 API key" autocomplete="off">' +
        '<div class="feature-form-row"><button class="btn" type="submit">添加 / 更新账号</button></div></form>' +
        '<div class="feature-list">' + accountRows + '</div>'
      : '';
    const groupRows = groups.length ? groups.map((g) =>
      '<div class="feature-row"><div class="feature-main"><b>' + esc(g.id) + ' <span class="feature-tag">' + esc(g.strategy) + '</span></b><small>' +
      esc((g.members || []).join(', ')) + '</small></div>' +
      '<div class="feature-actions"><button class="btn btn-danger" type="button" data-group-del="' + esc(g.id) + '">删除</button></div></div>'
    ).join('') : '<p class="feature-empty">没有路由组。</p>';
    el.providerEditor.innerHTML =
      '<div class="panel-list-row"><span>' + (editing ? '编辑 provider' : '新增 provider') + '</span><b>' + esc(editing ? editing.id : '') + '</b></div>' +
      '<form class="feature-form" data-provider-form>' +
      '<div class="feature-form-row"><input name="id" type="text" maxlength="32" placeholder="id（小写，如 deepseek）" value="' + esc(editing ? editing.id : '') + '"' + (editing ? ' readonly' : '') + '>' +
      '<input name="name" type="text" maxlength="60" placeholder="显示名称" value="' + esc(editing ? editing.name : '') + '"></div>' +
      '<input name="base_url" type="text" maxlength="300" placeholder="https://api.example.com/v1" value="' + esc(editing ? editing.baseUrl : '') + '">' +
      '<div class="feature-form-row"><input name="default_model" type="text" maxlength="120" placeholder="默认模型" value="' + esc(editing ? editing.defaultModel : '') + '">' +
      '<input name="max_concurrency" type="number" min="1" max="64" placeholder="并发" value="' + (editing ? (editing.maxConcurrency || 2) : 2) + '"></div>' +
      '<input name="api_key" type="password" maxlength="300" placeholder="' + (editing && editing.keySet ? '留空保留原密钥' : 'API key') + '" autocomplete="off">' +
      '<div class="feature-form-row"><button class="btn btn-primary" type="submit">' + (editing ? '保存修改' : '新增 provider') + '</button>' +
      (editing ? '<button class="btn" type="button" data-provider-cancel>取消编辑</button>' : '') + '</div></form>' +
      accountSection +
      '<div class="feature-list">' + (providerRows || '<p class="feature-empty">没有 provider。</p>') + '</div>' +
      '<div class="panel-list-row" style="margin-top:14px"><span>默认模型</span><b>' + esc((def.provider || '—') + '/' + (def.model || '—')) + '</b></div>' +
      '<form class="feature-form" data-default-form><div class="feature-form-row">' +
      '<input name="provider" type="text" maxlength="32" placeholder="provider id" value="' + esc(def.provider || '') + '">' +
      '<input name="model" type="text" maxlength="120" placeholder="model" value="' + esc(def.model || '') + '">' +
      '<button class="btn" type="submit">设为默认</button></div></form>' +
      '<div class="panel-list-row" style="margin-top:14px"><span>路由组</span><b>' + groups.length + ' 个</b></div>' +
      '<form class="feature-form" data-group-form><div class="feature-form-row">' +
      '<input name="id" type="text" maxlength="32" placeholder="组 id">' +
      '<select name="strategy"><option value="order">order</option><option value="rotate">rotate</option><option value="usage">usage</option></select></div>' +
      '<input name="members" type="text" maxlength="600" placeholder="成员，逗号分隔：deepseek/deepseek-chat,moonshot/kimi">' +
      '<div class="feature-form-row"><button class="btn" type="submit">保存路由组</button></div></form>' +
      '<div class="feature-list">' + groupRows + '</div>';
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

  // 搜索过滤：按设置项文本高亮/过滤 panel，跳转到匹配的第一个 tab
  function filterSettings(query) {
    const q = String(query || '').trim().toLowerCase();
    if (!el.settingsTabs || !el.settingsPanels) return;
    if (!q) {
      // 清空搜索：显示所有 tab
      el.settingsTabs.forEach((tab) => { tab.hidden = false; });
      return;
    }
    // 对每个 panel 判断是否包含关键词，隐藏不匹配的 tab 和 panel
    el.settingsTabs.forEach((tab) => {
      const name = tab.getAttribute('data-settings-tab');
      const panel = el.settingsPanels.find((p) => p.getAttribute('data-settings-panel') === name);
      const text = ((tab.textContent || '') + ' ' + (panel ? panel.textContent : '')).toLowerCase();
      tab.hidden = text.indexOf(q) < 0;
    });
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
    refreshGithub();
    refreshPlugins();
  }

  async function refreshGithub() {
    if (!G.api || !G.api.githubMe) return;
    try {
      const data = await G.api.githubMe();
      if (el.githubStatus) {
        el.githubStatus.textContent = data.connected
          ? ('已连接 · @' + (data.account && data.account.login || ''))
          : '未连接';
      }
      if (el.githubConnectBtn) el.githubConnectBtn.hidden = !!data.connected;
      if (el.githubDisconnectBtn) el.githubDisconnectBtn.hidden = !data.connected;
    } catch (err) {
      if (el.githubStatus) el.githubStatus.textContent = '未连接';
    }
  }

  async function connectGithub() {
    if (!G.api || !G.api.githubAuth) return;
    try {
      const data = await G.api.githubAuth();
      if (data && data.url) window.location.href = data.url;
    } catch (err) {
      app.toast('GitHub 未配置：请在 .env 填 GITHUB_CLIENT_ID / SECRET');
    }
  }

  async function disconnectGithub() {
    if (!G.api || !G.api.githubDisconnect) return;
    await G.api.githubDisconnect();
    refreshGithub();
    app.toast('已断开 GitHub');
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
    if (next === 'board') {
      app.view = 'board';
      if (el.mainRow) el.mainRow.hidden = true;
      if (el.boardView) el.boardView.hidden = false;
      if (el.topbarTitle) { el.topbarTitle.hidden = false; el.topbarTitle.textContent = '看板'; }
      app.renderBoard();
      app.closeDrawer();
      return;
    }
    app.view = 'chat';
    el.body.hidden = false;
    if (el.mainRow) el.mainRow.hidden = false;
    if (el.boardView) el.boardView.hidden = true;
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
      applyTheme();
      if (G.i18n) G.i18n.apply();
      el.app.setAttribute('data-sidebar', app.state.sidebarCollapsed ? 'collapsed' : 'open');
      app.applyReduceMotion();
      renderServerStatus();
      app.renderAll();
      app.restoreDraft();
      syncSettingsUI();
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
      reduceMotion: app.state.reduceMotion, darkMode: app.state.darkMode, locale: app.state.locale,
      sidebarCollapsed: app.state.sidebarCollapsed,
      onboarded: app.state.onboarded, voiceEnabled: app.state.voiceEnabled,
      activePersonaId: app.state.activePersonaId,
      personas: JSON.parse(JSON.stringify(app.state.personas || [])),
      memories: JSON.parse(JSON.stringify(app.state.memories || [])),
      knowledge: JSON.parse(JSON.stringify(app.state.knowledge || [])),
      glossary: JSON.parse(JSON.stringify(app.state.glossary || []))
    };
    app.resetState();
    Object.assign(app.state, settings);
    applyFontSize();
    applyTheme();
    if (G.i18n) G.i18n.apply();
    el.app.setAttribute('data-sidebar', app.state.sidebarCollapsed ? 'collapsed' : 'open');
    applyReduceMotion();
    app.save();
    app.renderAll();
    syncSettingsUI();
    closeSettings();
    setView('chat');
    app.toast('已清空全部对话');
  }

  /* ---------------- 人设 / 记忆 / 知识库 ---------------- */

  function renderPersonasPanel() {
    if (!el.personasPanel) return;
    const editing = app.editingPersonaId ? app.state.personas.find((p) => p.id === app.editingPersonaId) : null;
    const rows = app.state.personas.map((p) => {
      const active = p.id === app.state.activePersonaId;
      return '<div class="feature-row"><div class="feature-main"><b>' + esc(p.name) +
        (active ? ' <span class="feature-tag ok">当前</span>' : '') +
        (p.id === 'default' ? ' <span class="feature-tag">内置</span>' : '') +
        '</b><small>' + esc(p.system || '（空 system，只使用工具系统提示）') + '</small></div>' +
        '<div class="feature-actions">' +
        '<button class="btn" type="button" data-persona-use="' + esc(p.id) + '">使用</button>' +
        '<button class="btn" type="button" data-persona-edit="' + esc(p.id) + '">编辑</button>' +
        (p.id === 'default' ? '' : '<button class="btn btn-danger" type="button" data-persona-del="' + esc(p.id) + '">删除</button>') +
        '</div></div>';
    }).join('');
    el.personasPanel.innerHTML =
      '<form class="feature-form" data-persona-form>' +
      '<input name="name" type="text" maxlength="40" placeholder="人设名称" value="' + esc(editing ? editing.name : '') + '">' +
      '<textarea name="system" maxlength="8000" placeholder="system prompt，例如：你是严谨的 Go 工程师，先给结论再给依据。">' + esc(editing ? editing.system : '') + '</textarea>' +
      '<div class="feature-form-row"><button class="btn btn-primary" type="submit">' + (editing ? '保存修改' : '新增人设') + '</button>' +
      (editing ? '<button class="btn" type="button" data-persona-cancel>取消编辑</button>' : '') + '</div>' +
      '</form><div class="feature-list">' + rows + '</div>';
  }

  function renderMemoryPanel() {
    if (!el.memoryPanel) return;
    const rows = app.state.memories.length ? app.state.memories.map((m) =>
      '<div class="feature-row"><div class="feature-main"><small>' + esc(m.text) + '</small></div>' +
      '<div class="feature-actions"><label class="feature-tag"><input type="checkbox" data-memory-toggle="' + esc(m.id) + '"' + (m.enabled ? ' checked' : '') + '> 启用</label>' +
      '<button class="btn btn-danger" type="button" data-memory-del="' + esc(m.id) + '">删除</button></div></div>'
    ).join('') : '<p class="feature-empty">还没有记忆。记忆不会自动抽取，只发送你手动添加并启用的条目。</p>';
    el.memoryPanel.innerHTML =
      '<form class="feature-form" data-memory-form>' +
      '<textarea name="text" maxlength="4000" placeholder="例如：我偏好 Go 标准库，不引入第三方依赖。"></textarea>' +
      '<div class="feature-form-row"><button class="btn btn-primary" type="submit">添加记忆</button></div>' +
      '</form><div class="feature-list">' + rows + '</div>';
  }

  function renderKnowledgePanel() {
    if (!el.knowledgePanel) return;
    const terms = app.state.glossary.length ? app.state.glossary.map((g) =>
      '<div class="feature-row"><div class="feature-main"><b>' + esc(g.term) + '</b><small>' + esc(g.definition) + '</small></div>' +
      '<div class="feature-actions"><button class="btn btn-danger" type="button" data-glossary-del="' + esc(g.term) + '">删除</button></div></div>'
    ).join('') : '<p class="feature-empty">没有自定义术语；内置术语对所有人可用。</p>';
    const docs = app.state.knowledge.length ? app.state.knowledge.map((k) =>
      '<div class="feature-row"><div class="feature-main"><b>' + esc(k.title) + '</b><small>' + esc((k.content || '').slice(0, 240)) + '</small></div>' +
      '<div class="feature-actions"><label class="feature-tag"><input type="checkbox" data-doc-toggle="' + esc(k.id) + '"' + (k.enabled ? ' checked' : '') + '> 启用</label>' +
      '<button class="btn btn-danger" type="button" data-doc-del="' + esc(k.id) + '">删除</button></div></div>'
    ).join('') : '<p class="feature-empty">没有知识条目。发送时按关键词匹配，最多注入 3 条。</p>';
    el.knowledgePanel.innerHTML =
      '<div class="panel-list-row"><span>自定义术语</span><b>' + app.state.glossary.length + ' 条</b></div>' +
      '<form class="feature-form" data-glossary-form>' +
      '<div class="feature-form-row"><input name="term" type="text" maxlength="40" placeholder="术语"><input name="definition" type="text" maxlength="400" placeholder="一句话定义"></div>' +
      '<div class="feature-form-row"><button class="btn btn-primary" type="submit">添加术语</button></div></form>' +
      '<div class="feature-list">' + terms + '</div>' +
      '<div class="panel-list-row" style="margin-top:14px"><span>知识文档</span><b>' + app.state.knowledge.length + ' 条</b></div>' +
      '<form class="feature-form" data-doc-form>' +
      '<input name="title" type="text" maxlength="80" placeholder="标题">' +
      '<textarea name="content" maxlength="20000" placeholder="内容（纯文本；发送时按关键词匹配）"></textarea>' +
      '<div class="feature-form-row"><button class="btn btn-primary" type="submit">添加文档</button></div></form>' +
      '<div class="feature-list">' + docs + '</div>';
  }

  /* ---------------- 插件 / 工具 ---------------- */

  let pluginCache = [];

  function pluginRowHTML(p) {
    const state = p.state || 'discovered';
    const tag = state === 'ready' ? 'ok' : state === 'failed' ? 'warn' : '';
    const actions = state === 'ready'
      ? '<button class="btn" type="button" data-plugin-unload="' + esc(p.id) + '">卸载</button>'
      : '<button class="btn" type="button" data-plugin-load="' + esc(p.id) + '">加载</button>';
    const actionNames = (p.actions || []).map((a) => a.name).join(' · ') || '无动作';
    return '<div class="ext-plugin"><div class="ext-plugin-main"><b>' + esc(p.name || p.id) +
      ' <span class="feature-tag ' + tag + '">' + esc(state) + '</span></b><small>' +
      esc(p.protocol || '') + ' · v' + esc(p.version || '?') + ' · ' + esc(actionNames) +
      (p.lastError ? ' · 错误：' + esc(p.lastError) : '') + '</small></div>' + actions + '</div>';
  }

  function renderPluginList(list) {
    pluginCache = list;
    const html = list.length ? list.map(pluginRowHTML).join('') : '<p class="feature-empty">没有发现插件。把插件放到 $GQY_HOME/plugins/&lt;id&gt;/manifest.json。</p>';
    if (el.extensionsPlugins) el.extensionsPlugins.innerHTML = html;
    if (el.packagesPanel) el.packagesPanel.innerHTML = '<div class="ext-plugins">' + html + '</div>';
  }

  async function refreshPlugins() {
    if (!G.api || !G.api.plugins) { renderPluginList([]); return; }
    try {
      renderPluginList(await G.api.plugins());
    } catch (err) {
      renderPluginList([]);
    }
  }

  async function renderToolsPanel() {
    if (!el.toolsPanel) return;
    el.toolsPanel.innerHTML = '<p class="feature-empty">读取中…</p>';
    if (!G.api || !G.api.tools) {
      el.toolsPanel.innerHTML = '<p class="feature-empty">未连接服务端；工具由后端管理。</p>';
      return;
    }
    try {
      const data = await G.api.tools();
      const core = (data.tools || []).filter((t) => t.kind === 'core');
      const ext = (data.tools || []).filter((t) => t.kind !== 'core');
      const rows = (list) => list.map((t) =>
        '<div class="feature-row"><div class="feature-main"><b>' + esc(t.name) + '</b><small>' + esc(t.description) + '</small></div></div>'
      ).join('');
      el.toolsPanel.innerHTML =
        '<div class="panel-list-row"><span>核心工具</span><b>' + core.length + ' 个</b></div>' +
        '<div class="feature-list">' + (rows(core) || '<p class="feature-empty">无</p>') + '</div>' +
        '<div class="panel-list-row" style="margin-top:12px"><span>外部工具</span><b>' + ext.length + ' 个</b></div>' +
        '<div class="setting-row"><div class="setting-copy"><b>注入外部工具</b><small>provider_usage、已加载插件动作</small></div>' +
        '<button class="switch" type="button" role="switch" data-external-toggle aria-checked="' + String(!!data.external) + '"></button></div>' +
        '<div class="feature-list">' + (rows(ext) || '<p class="feature-empty">无</p>') + '</div>';
    } catch (err) {
      el.toolsPanel.innerHTML = '<p class="feature-empty">工具列表不可用：' + esc(err.message) + '</p>';
    }
  }

  /* ---------------- 用量 / 账户 / 网关 ---------------- */

  function renderUsagePanel() {
    if (!el.usagePanel) return;
    const u = usageSnapshot();
    const s = app.usageData || {};
    const days = Array.isArray(s.days) ? s.days : u.days;
    const maxDay = Math.max.apply(null, days.concat([1]));
    const bars = days.map((n, i) => '<i class="' + (i === 6 ? 'is-today' : '') + '" style="height:' + (n ? Math.max(8, Math.round(n / maxDay * 100)) : 4) + '%" title="' + n + ' 条"></i>').join('');
    const wk = ['日', '一', '二', '三', '四', '五', '六'];
    const axis = days.map((n, i) => '<span>' + (i === 6 ? '今天' : '周' + wk[new Date(Date.now() - (6 - i) * 86400000).getDay()]) + '</span>').join('');
    el.usagePanel.innerHTML =
      '<div class="usage-tiles">' +
      '<div class="usage-tile"><span>本地存储</span><b>' + esc(formatBytes(u.bytes)) + '</b></div>' +
      '<div class="usage-tile"><span>会话</span><b>' + (s.sessions != null ? s.sessions : u.convs) + '</b></div>' +
      '<div class="usage-tile"><span>消息</span><b>' + (s.messages != null ? s.messages : u.messages) + '</b></div>' +
      '<div class="usage-tile"><span>产物 / 附件</span><b>' + (s.artifacts != null ? s.artifacts : u.assets) + '</b></div>' +
      '</div>' +
      '<div class="panel-list-row"><span>Token</span><b>' + (s.totalTokens != null ? s.totalTokens : 0) + '</b></div>' +
      '<div class="panel-list-row"><span>输入 / 输出</span><b>' + (s.promptTokens || 0) + ' / ' + (s.completionTokens || 0) + '</b></div>' +
      '<div class="usage-panel-bars">' + bars + '</div><div class="usage-panel-axis">' + axis + '</div>' +
      '<p class="panel-note">近 7 天共 ' + days.reduce((a, b) => a + b, 0) + ' 条消息。服务端数据来自落盘会话聚合。</p>';
  }

  async function renderAccountsPanel() {
    if (!el.accountsPanel) return;
    const info = G.api && G.api.info ? G.api.info() : null;
    const connected = info ? (info.provider === 'mock' ? '模拟流' : '已连接') : '未连接';
    let github = '未连接';
    if (G.api && G.api.githubMe) {
      try {
        const data = await G.api.githubMe();
        if (data.connected) github = '@' + ((data.account && data.account.login) || '');
      } catch (err) {}
    }
    el.accountsPanel.innerHTML =
      '<div class="panel-list-row"><span>模式</span><b>本机单用户</b></div>' +
      '<div class="panel-list-row"><span>服务端</span><b>' + esc(connected) + (info && info.model ? ' · ' + esc(info.model) : '') + '</b></div>' +
      '<div class="panel-list-row"><span>数据目录</span><b>' + esc((info && info.home) || '—') + '</b></div>' +
      '<div class="panel-list-row"><span>GitHub</span><b>' + esc(github) + '</b></div>' +
      '<div class="panel-list-row"><span>版本</span><b>1.2.0</b></div>' +
      '<p class="panel-note">没有账号体系与权限分组；公网部署前先加反向代理鉴权。</p>';
  }

  async function renderGatewayPanel() {
    if (!el.gatewayPanel) return;
    if (!G.api || !G.api.gatewayStatus) {
      el.gatewayPanel.innerHTML = '<p class="feature-empty">未连接服务端。</p>';
      return;
    }
    try {
      const st = await G.api.gatewayStatus();
      const providers = (st.providers || []).map((p) =>
        '<div class="gateway-row"><div><b>' + esc(p.name || p.id) + '</b> <span class="feature-tag ' + (p.keySet ? 'ok' : 'warn') + '">' + (p.keySet ? '有密钥' : '无密钥') + '</span><br>' +
        '<small>' + esc(p.id) + ' · ' + (p.inflight || 0) + ' 在途 · ' + (p.calls || 0) + ' 调用 · ' + (p.failures || 0) + ' 失败' +
        (p.cooldownUntil ? ' · 冷却至 ' + esc(p.cooldownUntil) : '') + (p.lastError ? ' · ' + esc(p.lastError) : '') + '</small></div></div>'
      ).join('') || '<p class="feature-empty">没有 provider。</p>';
      const groups = (st.groups || []).map((g) =>
        '<div class="gateway-row"><small><b>' + esc(g.id) + '</b> · ' + esc(g.strategy) + ' · ' + esc((g.members || []).join(', ')) + '</small></div>'
      ).join('') || '<p class="feature-empty">没有路由组。</p>';
      el.gatewayPanel.innerHTML = '<div class="panel-list-row"><span>默认</span><b>' +
        esc((st.default && st.default.provider || '—') + '/' + (st.default && st.default.model || '—')) + '</b></div>' +
        '<div class="panel-list-row"><span>Provider</span><b>' + (st.providers || []).length + ' 个</b></div>' +
        providers + '<div class="panel-list-row" style="margin-top:10px"><span>路由组</span><b>' + (st.groups || []).length + ' 个</b></div>' + groups;
    } catch (err) {
      el.gatewayPanel.innerHTML = '<p class="feature-empty">网关状态不可用。</p>';
    }
  }

  /* ---------------- 设置内事件 ---------------- */

  if (el.voiceSwitch) el.voiceSwitch.addEventListener('click', () => {
    app.state.voiceEnabled = !app.state.voiceEnabled;
    app.save();
    syncSettingsUI();
    app.toast(app.state.voiceEnabled ? '语音输入已开启' : '语音输入已关闭');
  });

  if (el.settingsContent) {
    el.settingsContent.addEventListener('submit', (e) => {
      const form = e.target;
      if (form.matches('[data-persona-form]')) {
        e.preventDefault();
        const name = form.querySelector('[name=name]').value.trim();
        const system = form.querySelector('[name=system]').value.trim();
        if (!name) { form.querySelector('[name=name]').focus(); return; }
        if (app.editingPersonaId) {
          const p = app.state.personas.find((x) => x.id === app.editingPersonaId);
          if (p) { p.name = name; p.system = system; }
          app.editingPersonaId = null;
        } else {
          const id = uid();
          app.state.personas.push({ id: id, name: name, system: system, enabled: true });
          app.state.activePersonaId = id;
        }
        app.save();
        renderPersonasPanel();
        app.toast('人设已保存');
        return;
      }
      if (form.matches('[data-memory-form]')) {
        e.preventDefault();
        const text = form.querySelector('[name=text]').value.trim();
        if (!text) return;
        app.state.memories.unshift({ id: uid(), text: text, enabled: true, createdAt: now() });
        app.save();
        renderMemoryPanel();
        app.toast('记忆已添加');
        return;
      }
      if (form.matches('[data-glossary-form]')) {
        e.preventDefault();
        const term = form.querySelector('[name=term]').value.trim();
        const definition = form.querySelector('[name=definition]').value.trim();
        if (!term || !definition) return;
        app.state.glossary = app.state.glossary.filter((g) => g.term.toLowerCase() !== term.toLowerCase());
        app.state.glossary.push({ term: term, definition: definition });
        app.save();
        renderKnowledgePanel();
        app.toast('术语已添加');
        return;
      }
      if (form.matches('[data-doc-form]')) {
        e.preventDefault();
        const title = form.querySelector('[name=title]').value.trim();
        const content = form.querySelector('[name=content]').value.trim();
        if (!title || !content) return;
        app.state.knowledge.unshift({ id: uid(), title: title, content: content, enabled: true, updatedAt: now() });
        app.save();
        renderKnowledgePanel();
        app.toast('文档已添加');
        return;
      }
      if (form.matches('[data-provider-form]')) {
        e.preventDefault();
        const p = {
          id: form.querySelector('[name=id]').value.trim(),
          display_name: form.querySelector('[name=name]').value.trim(),
          base_url: form.querySelector('[name=base_url]').value.trim(),
          default_model: form.querySelector('[name=default_model]').value.trim(),
          max_concurrency: Number(form.querySelector('[name=max_concurrency]').value) || 2,
          api_key: form.querySelector('[name=api_key]').value,
          protocol: 'openai-chat'
        };
        if (!p.id || !p.base_url || !p.default_model) { app.toast('id / base_url / 默认模型都要填'); return; }
        const existing = (app.providerCache || []).find((x) => x.id === p.id);
        p.models = existing && existing.models && existing.models.length ? existing.models : [p.default_model];
        if (p.models.indexOf(p.default_model) < 0) p.models.push(p.default_model);
        const old = app.editingProvider;
        G.api.saveProvider(p).then(async () => {
          if (old && old !== p.id) await G.api.deleteProvider(old).catch(() => {});
          app.editingProvider = null;
          await loadProviders();
          renderGatewayPanel();
          app.toast('provider 已保存');
        }).catch((err) => app.toast(err.message));
        return;
      }
      if (form.matches('[data-account-form]')) {
        e.preventDefault();
        const providerId = form.getAttribute('data-account-form');
        const account = {
          id: form.querySelector('[name=id]').value.trim(),
          name: form.querySelector('[name=name]').value.trim(),
          api_key: form.querySelector('[name=api_key]').value,
          daily_token_limit: Number(form.querySelector('[name=daily_token_limit]').value) || 0
        };
        if (!account.id) { app.toast('账号 id 不能为空'); return; }
        G.api.saveAccount(providerId, account).then(() => { loadProviders(); app.toast('账号已保存'); })
          .catch((err) => app.toast(err.message));
        return;
      }
      if (form.matches('[data-default-form]')) {
        e.preventDefault();
        const provider = form.querySelector('[name=provider]').value.trim();
        const model = form.querySelector('[name=model]').value.trim();
        G.api.setDefault(provider, model).then(() => { loadProviders(); renderGatewayPanel(); app.toast('默认模型已更新'); })
          .catch((err) => app.toast(err.message));
        return;
      }
      if (form.matches('[data-group-form]')) {
        e.preventDefault();
        const id = form.querySelector('[name=id]').value.trim();
        const strategy = form.querySelector('[name=strategy]').value;
        const members = form.querySelector('[name=members]').value.split(/[,，\s]+/).filter(Boolean);
        if (!id || !members.length) { app.toast('组 id 和成员都要填'); return; }
        G.api.saveGroup({ id: id, strategy: strategy, members: members }).then(() => { loadProviders(); renderGatewayPanel(); app.toast('路由组已保存'); })
          .catch((err) => app.toast(err.message));
      }
    });

    el.settingsContent.addEventListener('click', (e) => {
      const use = e.target.closest('[data-persona-use]');
      if (use) {
        app.state.activePersonaId = use.getAttribute('data-persona-use');
        app.save(); renderPersonasPanel(); app.toast('已切换人设');
        return;
      }
      const edit = e.target.closest('[data-persona-edit]');
      if (edit) {
        app.editingPersonaId = edit.getAttribute('data-persona-edit');
        renderPersonasPanel();
        return;
      }
      const cancel = e.target.closest('[data-persona-cancel]');
      if (cancel) { app.editingPersonaId = null; renderPersonasPanel(); return; }
      const delP = e.target.closest('[data-persona-del]');
      if (delP) {
        const id = delP.getAttribute('data-persona-del');
        app.state.personas = app.state.personas.filter((p) => p.id !== id);
        if (app.state.activePersonaId === id) app.state.activePersonaId = 'default';
        app.save(); renderPersonasPanel(); return;
      }
      const delM = e.target.closest('[data-memory-del]');
      if (delM) {
        app.state.memories = app.state.memories.filter((m) => m.id !== delM.getAttribute('data-memory-del'));
        app.save(); renderMemoryPanel(); return;
      }
      const delG = e.target.closest('[data-glossary-del]');
      if (delG) {
        const term = delG.getAttribute('data-glossary-del');
        app.state.glossary = app.state.glossary.filter((g) => g.term !== term);
        app.save(); renderKnowledgePanel(); return;
      }
      const delD = e.target.closest('[data-doc-del]');
      if (delD) {
        app.state.knowledge = app.state.knowledge.filter((k) => k.id !== delD.getAttribute('data-doc-del'));
        app.save(); renderKnowledgePanel(); return;
      }
      const ext = e.target.closest('[data-external-toggle]');
      if (ext) {
        if (!G.api || !G.api.setExternal) return;
        const next = ext.getAttribute('aria-checked') !== 'true';
        G.api.setExternal(next).then(() => { renderToolsPanel(); app.toast(next ? '外部工具已注入' : '外部工具已关闭'); })
          .catch((err) => app.toast(err.message));
        return;
      }
      const load = e.target.closest('[data-plugin-load]');
      if (load) {
        const id = load.getAttribute('data-plugin-load');
        G.api.pluginLoad(id).then(() => { refreshPlugins(); app.toast('插件已加载'); })
          .catch((err) => app.toast(err.message));
        return;
      }
      const unload = e.target.closest('[data-plugin-unload]');
      if (unload) {
        const id = unload.getAttribute('data-plugin-unload');
        G.api.pluginUnload(id).then(() => { refreshPlugins(); app.toast('插件已卸载'); })
          .catch((err) => app.toast(err.message));
        return;
      }
      const editP = e.target.closest('[data-provider-edit]');
      if (editP) {
        app.editingProvider = editP.getAttribute('data-provider-edit');
        renderProviderEditor(app.providerCache || [], app.gatewayCache || null);
        return;
      }
      const cancelP = e.target.closest('[data-provider-cancel]');
      if (cancelP) {
        app.editingProvider = null;
        renderProviderEditor(app.providerCache || [], app.gatewayCache || null);
        return;
      }
      const delProv = e.target.closest('[data-provider-del]');
      if (delProv) {
        const id = delProv.getAttribute('data-provider-del');
        if (!window.confirm('删除 provider「' + id + '」？')) return;
        G.api.deleteProvider(id).then(() => { loadProviders(); renderGatewayPanel(); app.toast('provider 已删除'); })
          .catch((err) => app.toast(err.message));
        return;
      }
      const delAcc = e.target.closest('[data-account-del]');
      if (delAcc) {
        const id = delAcc.getAttribute('data-account-del');
        const provider = delAcc.getAttribute('data-account-provider');
        G.api.deleteAccount(provider, id).then(() => { loadProviders(); app.toast('账号已删除'); })
          .catch((err) => app.toast(err.message));
        return;
      }
      const delGroup = e.target.closest('[data-group-del]');
      if (delGroup) {
        const id = delGroup.getAttribute('data-group-del');
        G.api.deleteGroup(id).then(() => { loadProviders(); renderGatewayPanel(); app.toast('路由组已删除'); })
          .catch((err) => app.toast(err.message));
      }
    });

    el.settingsContent.addEventListener('change', (e) => {
      const mem = e.target.closest('[data-memory-toggle]');
      if (mem) {
        const item = app.state.memories.find((m) => m.id === mem.getAttribute('data-memory-toggle'));
        if (item) { item.enabled = mem.checked; app.save(); }
        return;
      }
      const doc = e.target.closest('[data-doc-toggle]');
      if (doc) {
        const item = app.state.knowledge.find((k) => k.id === doc.getAttribute('data-doc-toggle'));
        if (item) { item.enabled = doc.checked; app.save(); }
      }
    });
  }

  Object.assign(app, {
    syncSettingsUI, applyFontSize, applyTheme, usageSnapshot, renderServerStatus, refreshUsage,
    openSettingsTab, openSettings, closeSettings, filterSettings, loadProviders, openUsage, closeUsage,
    renderPersonasPanel, renderMemoryPanel, renderKnowledgePanel, renderToolsPanel,
    renderUsagePanel, renderAccountsPanel, renderGatewayPanel, refreshPlugins,
    openExtensions, closeExtensions, refreshGithub, connectGithub, disconnectGithub,
    trapTab, setView,
    exportData, importData, setupDetectedApi, openWsDialog, applyReduceMotion, clearEverything
  });
})();
