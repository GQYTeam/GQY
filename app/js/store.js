/* 顾清影 · 共享 store：状态、DOM 引用、常量与跨模块基础函数
 *
 * 拆分原则：所有可变状态与共享函数挂在 window.GQY.app 上，
 * 各 service 文件（chat/composer/menu/panels/settings）从这里读写，
 * 跨模块调用走 app.xxx()，模块内直接调用局部函数。
 */
(() => {
  'use strict';
  const G = (window.GQY = window.GQY || {});
  const { HOUR, DAY, uid, now, esc, timeAgo, titleFrom, stamp, estimateTokens, fmtTok, fmtDur, formatBytes, formatCount, renderMarkdown, highlightHTML } = G;

  const app = (G.app = {});

  app.$ = (sel, root) => (root || document).querySelector(sel);
  app.$$ = (sel, root) => Array.prototype.slice.call((root || document).querySelectorAll(sel));
  const $ = app.$;
  const $$ = app.$$;

  app.STORE_KEY = 'claude-webchat:v7';
  app.FONT_SIZES = { small: '14.2px', normal: '15.3px', large: '16.6px' };
  app.EFFORTS = ['Low', 'Medium', 'High'];
  app.EFFORT_DESC = {
    Low: '更快，回答简短，适合简单问题',
    Medium: '速度与详细程度之间取平衡',
    High: '先想清楚再答，更慢但更完整'
  };
  app.FILES_WIDTH_KEY = 'gqy:files-width';
  app.MAX_ATTACH = 10;

  app.el = {
    app: $('#app'), sidebar: $('#sidebar'), collapseBtn: $('#collapseBtn'), openSideBtn: $('#openSideBtn'),
    newChatBtn: $('#newChatBtn'),
    searchBtn: $('#searchBtn'), newWsBtn: $('#newWsBtn'), searchWrap: $('#searchWrap'), searchInput: $('#searchInput'),
    manageBtn: $('#manageBtn'), manageBar: $('#manageBar'), manageCount: $('#manageCount'),
    manageAll: $('#manageAll'), manageNone: $('#manageNone'), manageDelete: $('#manageDelete'), manageCancel: $('#manageCancel'),
    wsList: $('#wsList'), accountBtn: $('#accountBtn'), scrim: $('#scrim'),
    body: $('#body'), scroller: $('#scroller'), thread: $('#thread'), hero: $('#hero'),
    topbarTitle: $('#topbarTitle'), fullBtn: $('#fullBtn'),
    form: $('#composerForm'), input: $('#composerInput'), plusBtn: $('#plusBtn'),
    modelBtn: $('#modelBtn'), modelName: $('#modelName'), modelEffort: $('#modelEffort'),
    micBtn: $('#micBtn'), sendBtn: $('#sendBtn'), attachRow: $('#attachRow'),
    toast: $('#toast'),
    wsDialog: $('#wsDialog'), wsForm: $('#wsForm'), wsName: $('#wsName'),
    mainRow: $('#mainRow'),
    settingsLayer: $('#settingsLayer'), settingsWindow: $('#settingsWindow'),
    settingsContent: $('#settingsContent'), settingsTabs: $$('.settings-tab'), settingsPanels: $$('.settings-panel'),
    miniUsageText: $('#miniUsageText'), miniUsageBar: $('#miniUsageBar'),
    usageRing: $('#usageRing'), usageStorageText: $('#usageStorageText'), usageStoragePercent: $('#usageStoragePercent'),
    usageWorkspaces: $('#usageWorkspaces'), usageConvs: $('#usageConvs'), usageMessages: $('#usageMessages'),
    usageAssets: $('#usageAssets'), usageBars: $('#usageBars'), usageAxis: $('#usageAxis'),
    usageWeekTotal: $('#usageWeekTotal'),
    aboutModels: $$('.about-model'), aboutModelInline: $('#aboutModelInline'), aboutEffortInline: $('#aboutEffortInline'),
    aboutModelInline2: $('#aboutModelInline2'), aboutEffortInline2: $('#aboutEffortInline2'),
    providerCount: $('#providerCount'), providerList: $('#providerList'),
    motionSwitch: $('#motionSwitch'), sideSwitch: $('#sideSwitch'),
    fontSeg: $('#fontSeg'), thinkSwitch: $('#thinkSwitch'), voiceSwitch: $('#voiceSwitch'),
    settingsSearch: $('#settingsSearch'),
    exportData: $('#exportData'),
    lightbox: $('#lightbox'), lbTitle: $('#lbTitle'), lbCode: $('#lbCode'), lbCopy: $('#lbCopy'),
    lbPath: $('#lbPath'), lbLang: $('#lbLang'), lbLines: $('#lbLines'), lbAdded: $('#lbAdded'),
    lbBarName: $('#lbBarName'), lbFootLang: $('#lbFootLang'),
    usageBtn: $('#usageBtn'), usagePct: $('#usagePct'), usageWedge: $('#usageWedge'),
    clawd: $('#clawd'),
    filesPanel: $('#filesPanel'), filesTree: $('#filesTree'), filesRoot: $('#filesRoot'), filesCodePane: $('#filesCodePane'),
    filesName: $('#filesName'), filesStat: $('#filesStat'), filesGutter: $('#filesGutter'), filesCode: $('#filesCode'),
    filesCopy: $('#filesCopy'), filesClose: $('#filesClose'), filesExpand: $('#filesExpand'), filesTreeBtn: $('#filesTreeBtn'),
    filesResize: $('#filesResize'), filesTreeFoot: $('#filesTreeFoot'),
    imgbox: $('#imgbox'), imgboxImg: $('#imgboxImg'), imgboxTitle: $('#imgboxTitle'), imgboxMeta: $('#imgboxMeta'),
    imgboxCount: $('#imgboxCount'), imgboxPrev: $('#imgboxPrev'), imgboxNext: $('#imgboxNext'), imgboxSource: $('#imgboxSource'),
    dropzone: $('#dropzone'),
    clearAll: $('#clearAll'),
    fileInput: $('#fileInput'), importInput: $('#importInput'), importData: $('#importData'),
    scrollBtn: $('#scrollBtn'), liveStatus: $('#liveStatus'), serverStatus: $('#serverStatus'),
    usageTopBtn: $('#usageTopBtn'), usageLayer: $('#usageLayer'),
    extensionsBtn: $('#extensionsBtn'), extensionsLayer: $('#extensionsLayer'),
    githubStatus: $('#githubStatus'), githubConnectBtn: $('#githubConnectBtn'), githubDisconnectBtn: $('#githubDisconnectBtn')
  };

  app.prefersReducedMotion = () => {
    try { return window.matchMedia('(prefers-reduced-motion: reduce)').matches; } catch (err) { return false; }
  };
  app.mqMobile = window.matchMedia('(max-width: 880px)');

  /* ---------------- 可变状态 ---------------- */
  app.view = 'chat';
  app.stream = null;
  app.attached = [];
  app.attachUrls = new Map();
  app.dragDepth = 0;
  app.searchOpen = false;
  app.searchQuery = '';
  app.manageMode = false;
  app.manageSelected = new Set();
  app.renamingId = null;
  app.menu = null;
  app.menuAnchor = null;
  app.toastTimer = 0;
  app.recog = null;
  app.settingsTab = 'appearance';
  app.settingsTrigger = null;
  app.imgboxItems = [];
  app.imgboxIndex = 0;
  app.imgboxTrigger = null;
  app.lbTrigger = null;
  app.modelCatalog = [];
  app.modelCatalogLoading = false;
  app.usageData = null;
  app.contextNode = null;
  app.draftTimer = 0;
  app.paintTimer = 0;
  app.paintLast = 0;
  app.filesState = { root: '~/项目', files: [], current: null, tree: [], treeView: false };
  app.filesDrag = null;

  app.dropBlobUrls = (key, value) =>
    (key === 'url' && typeof value === 'string' && value.indexOf('blob:') === 0) ? undefined : value;

  /* ---------------- state 载入 ---------------- */
  function seedState() {
    const wsId = uid();
    const convId = uid();
    return {
      workspaces: [
        {
          id: wsId,
          name: 'gqy',
          convs: [{ id: convId, title: '新会话', pinned: false, messages: [], updatedAt: now() }]
        }
      ],
      activeWorkspaceId: wsId,
      activeConvId: convId,
      model: '',
      effort: 'High',
      fontSize: 'normal',
      thinkOpen: false,
      sidebarCollapsed: false,
      reduceMotion: app.prefersReducedMotion(),
      drafts: {}
    };
  }

  /* 置顶会话稳定排在同工作区未置顶会话之前；其余保持原有先后。 */
  function sortConvs(ws) {
    if (!ws || !Array.isArray(ws.convs)) return;
    const pinned = [];
    const rest = [];
    ws.convs.forEach((c) => { (c && c.pinned ? pinned : rest).push(c); });
    ws.convs.splice(0, ws.convs.length, ...pinned, ...rest);
  }

  /* 旧数据迁移：补齐/校验 pinned、title、updatedAt，不丢历史会话。 */
  function normalizeConv(c) {
    if (!c || typeof c !== 'object') return false;
    if (!Array.isArray(c.messages)) c.messages = [];
    if (typeof c.title !== 'string' || !c.title.trim()) {
      c.title = '新会话';
    } else {
      const title = c.title.trim().slice(0, 80);
      c.title = title || '新会话';
    }
    c.pinned = c.pinned === true;
    if (typeof c.updatedAt !== 'number' || !isFinite(c.updatedAt)) c.updatedAt = now();
    return true;
  }

  function loadState() {
    try {
      const raw = localStorage.getItem(app.STORE_KEY);
      if (!raw) return seedState();
      const s = JSON.parse(raw);
      if (!s || !Array.isArray(s.workspaces) || !s.workspaces.length) return seedState();
      s.workspaces.forEach((ws) => {
        if (!Array.isArray(ws.convs)) ws.convs = [];
        ws.convs = ws.convs.filter(normalizeConv);
        sortConvs(ws);
      });
      s.effort = app.EFFORTS.indexOf(s.effort) >= 0 ? s.effort : 'High';
      s.fontSize = app.FONT_SIZES[s.fontSize] ? s.fontSize : 'normal';
      s.thinkOpen = !!s.thinkOpen;
      s.sidebarCollapsed = !!s.sidebarCollapsed;
      s.reduceMotion = typeof s.reduceMotion === 'boolean' ? s.reduceMotion : app.prefersReducedMotion();
      s.drafts = (s.drafts && typeof s.drafts === 'object' && !Array.isArray(s.drafts)) ? s.drafts : {};
      Object.keys(s.drafts).forEach((k) => { if (typeof s.drafts[k] !== 'string') delete s.drafts[k]; });
      s.model = typeof s.model === 'string' ? s.model : '';
      let ws = s.workspaces.find((w) => w.id === s.activeWorkspaceId) || s.workspaces[0];
      s.activeWorkspaceId = ws.id;
      let conv = ws.convs.find((c) => c.id === s.activeConvId);
      if (!conv) conv = ws.convs[0];
      if (!conv) {
        conv = { id: uid(), title: '新会话', pinned: false, messages: [], updatedAt: now() };
        ws.convs.unshift(conv);
      }
      s.activeConvId = conv.id;
      return s;
    } catch (err) {
      return seedState();
    }
  }

  app.state = loadState();

  // reloadState：从 localStorage 重新读取（导入后）；resetState：回到全新种子状态。
  app.reloadState = () => { app.state = loadState(); };
  app.resetState = () => { app.state = seedState(); };
  app.sortConvs = sortConvs;

  /* ---------------- 基础函数 ---------------- */
  app.activeWs = () => app.state.workspaces.find((w) => w.id === app.state.activeWorkspaceId) || app.state.workspaces[0];

  app.activeConv = () => {
    const ws = app.activeWs();
    if (!ws) return null;
    return ws.convs.find((c) => c.id === app.state.activeConvId) || null;
  };

  app.findConv = (id) => {
    for (let i = 0; i < app.state.workspaces.length; i++) {
      const ws = app.state.workspaces[i];
      for (let j = 0; j < ws.convs.length; j++) {
        if (ws.convs[j].id === id) return { ws: ws, conv: ws.convs[j] };
      }
    }
    return null;
  };

  app.findMessage = (id) => {
    for (let i = 0; i < app.state.workspaces.length; i += 1) {
      const convs = app.state.workspaces[i].convs;
      for (let j = 0; j < convs.length; j += 1) {
        const msg = convs[j].messages.find((m) => m.id === id);
        if (msg) return msg;
      }
    }
    return null;
  };

  app.hideToast = () => { app.el.toast.classList.remove('show'); };

  app.toast = (message, actionLabel, action) => {
    app.el.toast.innerHTML = '';
    const span = document.createElement('span');
    span.textContent = message;
    app.el.toast.appendChild(span);
    if (actionLabel) {
      const btn = document.createElement('button');
      btn.type = 'button';
      btn.className = 'toast-action';
      btn.textContent = actionLabel;
      btn.addEventListener('click', () => { app.hideToast(); if (action) action(); });
      app.el.toast.appendChild(btn);
    }
    app.el.toast.classList.add('show');
    clearTimeout(app.toastTimer);
    app.toastTimer = setTimeout(app.hideToast, actionLabel ? 6000 : 2400);
  };

  /* save：写 localStorage + 刷新用量 + 镜像会话。
   * refreshUsage / mirrorSession 由后续 service 文件挂到 app 上，
   * 这里在调用时才解析（首次调用发生在 boot 之后）。 */
  app.save = () => {
    try {
      localStorage.setItem(app.STORE_KEY, JSON.stringify(app.state, app.dropBlobUrls));
    } catch (err) {
      const quota = err && (err.name === 'QuotaExceededError' || /quota/i.test(String(err.message)));
      app.toast(quota ? '本地存储已满：改动只留在内存里，先导出备份再清理' : '本地保存失败，刷新会丢失最近的改动');
    }
    if (app.refreshUsage) app.refreshUsage();
    if (app.mirrorSession) app.mirrorSession();
  };

  // 供各 service 解构用的工具（含 core 里已导入的）
  app.u = { uid, now, esc, timeAgo, titleFrom, stamp, estimateTokens, fmtTok, fmtDur, formatBytes, formatCount, renderMarkdown, highlightHTML };
})();
