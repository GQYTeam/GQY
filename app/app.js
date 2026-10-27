/* 顾清影 · 对话窗口逻辑（生产版） */

(() => {
  'use strict';

  /* 共享模块（见 js/ 目录，core/markdown/clawd 先于本文件加载） */
  const G = window.GQY || {};
  const {
    HOUR, DAY, uid, now, esc, timeAgo, titleFrom, stamp,
    estimateTokens, fmtTok, fmtDur, formatBytes, formatCount,
    renderMarkdown, highlightHTML, codeHTML
  } = G;

  const STORE_KEY = 'claude-webchat:v7';
  const FONT_SIZES = { small: '14.2px', normal: '15.3px', large: '16.6px' };
  const EFFORTS = ['Low', 'Medium', 'High'];
  const EFFORT_DESC = {
    Low: '更快，回答简短，适合简单问题',
    Medium: '速度与详细程度之间取平衡',
    High: '先想清楚再答，更慢但更完整'
  };

  const $ = (sel, root) => (root || document).querySelector(sel);
  const $$ = (sel, root) => Array.prototype.slice.call((root || document).querySelectorAll(sel));

  const el = {
    app: $('#app'), sidebar: $('#sidebar'), collapseBtn: $('#collapseBtn'), openSideBtn: $('#openSideBtn'),
    newChatBtn: $('#newChatBtn'),
    searchBtn: $('#searchBtn'), newWsBtn: $('#newWsBtn'), searchWrap: $('#searchWrap'), searchInput: $('#searchInput'),
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
    usageBadge: $('#usageBadge'), miniUsageText: $('#miniUsageText'), miniUsageBar: $('#miniUsageBar'),
    usageRing: $('#usageRing'), usageStorageText: $('#usageStorageText'), usageStoragePercent: $('#usageStoragePercent'),
    usageWorkspaces: $('#usageWorkspaces'), usageConvs: $('#usageConvs'), usageMessages: $('#usageMessages'),
    usageAssets: $('#usageAssets'), usageBars: $('#usageBars'), usageAxis: $('#usageAxis'),
    usageWeekTotal: $('#usageWeekTotal'),
    aboutModels: $$('.about-model'), aboutModelInline: $('#aboutModelInline'), aboutEffortInline: $('#aboutEffortInline'),
    motionSwitch: $('#motionSwitch'), sideSwitch: $('#sideSwitch'),
    fontSeg: $('#fontSeg'), thinkSwitch: $('#thinkSwitch'),
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
    scrollBtn: $('#scrollBtn'), liveStatus: $('#liveStatus'), serverStatus: $('#serverStatus')
  };

  const prefersReducedMotion = () => {
    try { return window.matchMedia('(prefers-reduced-motion: reduce)').matches; } catch (err) { return false; }
  };

  const mqMobile = window.matchMedia('(max-width: 880px)');

  /* ---------------- state ---------------- */

  function seedState() {
    const wsId = uid();
    const convId = uid();
    return {
      workspaces: [
        {
          id: wsId,
          name: 'gqy',
          convs: [{ id: convId, title: '新会话', messages: [], updatedAt: now() }]
        }
      ],
      activeWorkspaceId: wsId,
      activeConvId: convId,
      model: '',
      effort: 'High',
      fontSize: 'normal',
      thinkOpen: false,
      sidebarCollapsed: false,
      reduceMotion: prefersReducedMotion(),
      drafts: {}
    };
  }

  function loadState() {
    try {
      const raw = localStorage.getItem(STORE_KEY);
      if (!raw) return seedState();
      const s = JSON.parse(raw);
      if (!s || !Array.isArray(s.workspaces) || !s.workspaces.length) return seedState();
      s.workspaces.forEach((ws) => {
        if (!Array.isArray(ws.convs)) ws.convs = [];
        ws.convs.forEach((c) => { if (!Array.isArray(c.messages)) c.messages = []; });
      });
      s.effort = EFFORTS.indexOf(s.effort) >= 0 ? s.effort : 'High';
      s.fontSize = FONT_SIZES[s.fontSize] ? s.fontSize : 'normal';
      s.thinkOpen = !!s.thinkOpen;
      s.sidebarCollapsed = !!s.sidebarCollapsed;
      s.reduceMotion = typeof s.reduceMotion === 'boolean' ? s.reduceMotion : prefersReducedMotion();
      s.drafts = (s.drafts && typeof s.drafts === 'object' && !Array.isArray(s.drafts)) ? s.drafts : {};
      Object.keys(s.drafts).forEach((k) => { if (typeof s.drafts[k] !== 'string') delete s.drafts[k]; });
      // model 允许为空字符串（未选择时回落到服务端默认）
      s.model = typeof s.model === 'string' ? s.model : '';
      let ws = s.workspaces.find((w) => w.id === s.activeWorkspaceId) || s.workspaces[0];
      s.activeWorkspaceId = ws.id;
      let conv = ws.convs.find((c) => c.id === s.activeConvId);
      if (!conv) conv = ws.convs[0];
      if (!conv) {
        conv = { id: uid(), title: '新会话', messages: [], updatedAt: now() };
        ws.convs.unshift(conv);
      }
      s.activeConvId = conv.id;
      return s;
    } catch (err) {
      return seedState();
    }
  }

  let state = loadState();
  let view = 'chat';
  let stream = null;
  let attached = [];
  const MAX_ATTACH = 10;
  const attachUrls = new Map();
  let dragDepth = 0;
  let searchOpen = false;
  let searchQuery = '';
  let menu = null;
  let menuAnchor = null;
  let toastTimer = 0;
  let recog = null;
  let settingsTab = 'appearance';
  let settingsTrigger = null;
  let imgboxItems = [];
  let imgboxIndex = 0;
  let imgboxTrigger = null;
  let lbTrigger = null;

  /* 模型目录：由服务端从 models.dev 拉取，前端缓存并搜索 */
  let modelCatalog = [];
  let modelCatalogLoading = false;

  const dropBlobUrls = (key, value) =>
    (key === 'url' && typeof value === 'string' && value.indexOf('blob:') === 0) ? undefined : value;

  function save() {
    try {
      localStorage.setItem(STORE_KEY, JSON.stringify(state, dropBlobUrls));
    } catch (err) {
      const quota = err && (err.name === 'QuotaExceededError' || /quota/i.test(String(err.message)));
      toast(quota ? '本地存储已满：改动只留在内存里，先导出备份再清理' : '本地保存失败，刷新会丢失最近的改动');
    }
    refreshUsage();
    mirrorSession();
  }

  /* 服务端可用时把当前会话镜像到 $GQY_HOME/sessions/<id>/（防抖在 api 层） */
  function mirrorSession() {
    if (!G.api || !G.api.available()) return;
    const ws = activeWs();
    const conv = activeConv();
    if (!ws || !conv) return;
    G.api.syncSession(conv.id, {
      id: conv.id,
      title: conv.title || '',
      workspace: ws.name,
      updatedAt: conv.updatedAt || now(),
      messages: conv.messages.map((m) => ({
        id: m.id, role: m.role, ts: m.ts, text: m.text,
        thinking: m.thinking, tools: m.tools, artifact: m.artifact
      }))
    });
  }

  function activeWs() {
    return state.workspaces.find((w) => w.id === state.activeWorkspaceId) || state.workspaces[0];
  }

  function activeConv() {
    const ws = activeWs();
    if (!ws) return null;
    return ws.convs.find((c) => c.id === state.activeConvId) || null;
  }

  function findConv(id) {
    for (let i = 0; i < state.workspaces.length; i++) {
      const ws = state.workspaces[i];
      for (let j = 0; j < ws.convs.length; j++) {
        if (ws.convs[j].id === id) return { ws: ws, conv: ws.convs[j] };
      }
    }
    return null;
  }



  /* ---------------- markdown ---------------- */

  const USAGE_TOTAL = 1000000;
  const ctxUsage = {
    system: 24400,
    tools: 628,
    history: 366000,
    modelMs: 17 * 60000 + 43000,
    toolMs: 2 * 60000 + 42000,
    firstMs: 1700,
    firstCount: 1,
    outTokens: 263015,
    outMs: 900000,
    uncached: 150174,
    cacheRead: 22071168,
    cacheHit: 99
  };
  let contextNode = null;




  function contextSnapshot() {
    const conv = activeConv();
    let chars = 0;
    if (conv) {
      conv.messages.forEach((m) => {
        chars += String(m.text || '').length;
        chars += String(m.thinking || '').length;
        (m.tools || []).forEach((t) => { chars += String(t.detail || '').length + String(t.output || '').length; });
      });
    }
    const messages = ctxUsage.history + Math.round(chars * 0.62);
    const used = ctxUsage.system + ctxUsage.tools + messages;
    return {
      used: used,
      total: USAGE_TOTAL,
      pct: Math.max(1, Math.min(100, Math.round((used / USAGE_TOTAL) * 100))),
      system: ctxUsage.system,
      tools: ctxUsage.tools,
      messages: messages
    };
  }

  function contextPie(pct) {
    const r = 6.4;
    const a = (Math.min(pct, 99.9) / 100) * Math.PI * 2 - Math.PI / 2;
    const x = (8 + r * Math.cos(a)).toFixed(2);
    const y = (8 + r * Math.sin(a)).toFixed(2);
    const large = pct > 50 ? 1 : 0;
    return 'M8 8 L8 ' + (8 - r) + ' A' + r + ' ' + r + ' 0 ' + large + ' 1 ' + x + ' ' + y + ' Z';
  }

  function updateContextChip() {
    const snap = contextSnapshot();
    el.usagePct.textContent = snap.pct + '%';
    el.usageWedge.setAttribute('d', contextPie(snap.pct));
    el.usageBtn.title = '上下文已用 ' + snap.pct + '% · ' + fmtTok(snap.used) + ' / 1M';
  }

  function contextPanelHTML() {
    const snap = contextSnapshot();
    const seg = [
      { label: '系统提示词', value: snap.system, color: '#b9b9b3' },
      { label: '工具定义', value: snap.tools, color: '#dfb28c' },
      { label: '对话消息', value: snap.messages, color: '#7e9a83' }
    ];
    const sum = seg.reduce((a, b) => a + b.value, 0);
    const tps = Math.round(ctxUsage.outTokens / Math.max(1, ctxUsage.outMs / 1000));
    const firstAvg = ctxUsage.firstMs / Math.max(1, ctxUsage.firstCount);
    return '<div class="usage-head"><span>上下文已用</span><b>' + snap.pct + '%</b>' +
      '<span>' + fmtTok(snap.used) + ' / ' + (snap.total / 1000000) + 'M</span></div>' +
      '<div class="ctx-bar">' + seg.map((x) => '<i style="width:' + (x.value / sum * 100).toFixed(2) + '%;background:' + x.color + '"></i>').join('') + '</div>' +
      '<div class="usage-legend">' + seg.map((x) => '<div class="usage-row"><i style="background:' + x.color + '"></i>' + x.label + '<b>' + fmtTok(x.value) + '</b></div>').join('') + '</div>' +
      '<div class="usage-sec"><h4>会话统计</h4><div class="usage-grid">' +
      '<div><small>模型用时</small><b>' + fmtDur(ctxUsage.modelMs) + '</b></div>' +
      '<div><small>工具调用用时</small><b>' + fmtDur(ctxUsage.toolMs) + '</b></div>' +
      '<div><small>首 token 平均</small><b>' + (firstAvg / 1000).toFixed(1) + ' 秒</b></div>' +
      '<div><small>输出速度</small><b>' + tps + ' tok/s</b></div>' +
      '</div></div>' +
      '<div class="usage-sec"><h4>Token 用量</h4><div class="usage-grid">' +
      '<div><small>缓存命中</small><b>' + ctxUsage.cacheHit + '%</b></div>' +
      '<div><small>未缓存输入</small><b>' + ctxUsage.uncached.toLocaleString('en-US') + ' tok</b></div>' +
      '<div><small>缓存读取</small><b>' + ctxUsage.cacheRead.toLocaleString('en-US') + ' tok</b></div>' +
      '<div><small>输出</small><b>' + ctxUsage.outTokens.toLocaleString('en-US') + ' tok</b></div>' +
      '</div></div>' +
      '<p class="usage-foot">本机模拟数据 · 按字符估算 token，不联网</p>';
  }

  function closeContextPanel() {
    if (!contextNode) return;
    contextNode.remove();
    contextNode = null;
    el.usageBtn.setAttribute('aria-expanded', 'false');
    document.removeEventListener('pointerdown', onContextPointer, true);
  }

  function onContextPointer(e) {
    if (!contextNode) return;
    if (contextNode.contains(e.target) || el.usageBtn.contains(e.target)) return;
    closeContextPanel();
  }

  function openContextPanel() {
    if (contextNode) { closeContextPanel(); return; }
    closeMenu(false);
    const node = document.createElement('div');
    node.className = 'usage-pop';
    node.setAttribute('role', 'dialog');
    node.setAttribute('aria-label', '上下文用量');
    node.innerHTML = contextPanelHTML();
    document.body.appendChild(node);
    contextNode = node;
    el.usageBtn.setAttribute('aria-expanded', 'true');
    positionMenu(node, el.usageBtn, 'right', 'top');
    document.addEventListener('pointerdown', onContextPointer, true);
  }

  function findMessage(id) {
    for (let i = 0; i < state.workspaces.length; i += 1) {
      const convs = state.workspaces[i].convs;
      for (let j = 0; j < convs.length; j += 1) {
        const msg = convs[j].messages.find((m) => m.id === id);
        if (msg) return msg;
      }
    }
    return null;
  }


  function thinkingHTML() {
    return '<div class="thinking"><span>思考中</span><span class="dots" aria-hidden="true"><i></i><i></i><i></i></span></div>';
  }

  function thinkHTML(m, live) {
    const running = !!live && live.stage === 'thinking';
    return '<details class="think"' + (running ? ' open' : '') + '>' +
      '<summary class="think-head">' +
      '<svg class="ico think-i" aria-hidden="true"><use href="#i-spark"/></svg>' +
      '<span class="think-label">' + (running ? '思考中' : '已思考 ' + (m.thinkMs || 6) + ' 秒') + '</span>' +
      (running ? '<span class="dots" aria-hidden="true"><i></i><i></i><i></i></span>' : '') +
      '<svg class="ico think-chev" aria-hidden="true"><use href="#i-chevron"/></svg>' +
      '</summary>' +
      '<div class="think-body">' + esc(m.thinking) + (running ? '<span class="stream-caret"></span>' : '') + '</div>' +
      '</details>';
  }

  function argsHTML(t, running) {
    let html = '';
    if (t.args && t.args.length) {
      html += '<div class="trace-kv"><span>输入</span><pre>' +
        t.args.map((pair) => esc(pair[0]) + ': ' + esc(pair[1])).join('\n') + '</pre></div>';
    }
    if (t.output && !running) {
      html += '<div class="trace-kv"><span>输出</span><pre>' + esc(t.output) + '</pre></div>';
    }
    return html;
  }

  function stepHTML(t) {
    const running = t.status === 'running';
    const hasBody = !!((t.args && t.args.length) || t.output);
    const secs = t.ms ? (t.ms / 1000).toFixed(1) + 's' : '';
    return '<div class="step' + (running ? ' is-running' : '') + '">' +
      '<span class="step-rail" aria-hidden="true"><i class="step-dot"></i></span>' +
      '<details class="step-box">' +
      '<summary class="step-head">' +
      '<svg class="ico step-ico" aria-hidden="true"><use href="#' + (t.icon || 'i-check') + '"/></svg>' +
      '<span class="step-main">' +
      '<span class="step-line"><b>' + esc(t.name) + '</b><code>' + esc(t.detail) + '</code>' +
      '<span class="step-state">' + (running ? '<span class="spin"></span>运行中' : secs) + '</span></span>' +
      (!running && t.output ? '<span class="step-out">' + esc(t.output.split('\n')[0]) + '</span>' : '') +
      '</span>' +
      (hasBody ? '<svg class="ico step-chev" aria-hidden="true"><use href="#i-chevron"/></svg>' : '') +
      '</summary>' +
      (hasBody ? '<div class="step-detail">' + argsHTML(t, running) + '</div>' : '') +
      '</details></div>';
  }

  function toolsHTML(tools, total) {
    const running = tools.filter((t) => t.status === 'running')[0];
    const done = tools.filter((t) => t.status === 'done');
    const ms = done.reduce((sum, t) => sum + (t.ms || 0), 0);
    const meta = running
      ? '第 ' + (tools.indexOf(running) + 1) + ' / ' + (total || tools.length) + ' 步'
      : tools.length + ' 步' + (ms ? ' · ' + (ms / 1000).toFixed(1) + 's' : '');
    return '<div class="trace">' +
      '<div class="trace-head">' +
      '<svg class="ico trace-ico" aria-hidden="true"><use href="#i-terminal"/></svg>' +
      '<span>工具调用</span>' +
      '<span class="trace-meta">' + meta + '</span>' +
      '</div>' +
      tools.map(stepHTML).join('') +
      '</div>';
  }

  function errorHTML(m) {
    const err = m.error || {};
    return '<div class="msg-error" role="alert">' +
      '<svg class="ico" aria-hidden="true"><use href="#i-stop-circle"/></svg>' +
      '<div><b>没有拿到完整回复</b><small>' + esc(err.message || '请求失败') + '</small></div>' +
      '<button class="btn btn-sm" type="button" data-retry="' + m.id + '">重试</button>' +
      '</div>';
  }

  function artifactHTML(m) {
    const a = m.artifact;
    const preview = a.content.split('\n').slice(0, 4).join('\n');
    return '<button class="artifact" type="button" data-artifact="' + m.id + '" aria-label="打开产物 ' + esc(a.title) + '">' +
      '<span class="art-thumb" aria-hidden="true"><code>' + esc(preview) + '</code></span>' +
      '<span class="art-body">' +
      '<span class="art-title">' + esc(a.title) + '</span>' +
      '<span class="art-meta">' + esc(a.lang || 'code') + ' · ' + a.lines + ' 行 · 生成的产物</span>' +
      '</span><span class="art-open">查看</span></button>';
  }

  function askHTML(m) {
    const q = m.question;
    const opts = q.options.map((o) => (typeof o === 'string' ? { label: o, desc: '' } : o));
    const ans = q.answer && typeof q.answer === 'object' ? q.answer : null;
    const picked = ans ? (ans.picks || []) : [];
    const done = !!ans;
    const multi = !!q.multiSelect;
    return '<div class="ask' + (done ? ' is-done' : '') + '" data-askcard="' + m.id + '"' + (multi ? ' data-multi="1"' : '') + '>' +
      '<div class="ask-head">' +
      '<span class="ask-badge"><svg class="ico" aria-hidden="true"><use href="#i-help"/></svg>AskUserQuestion</span>' +
      '<span class="ask-note">' + (done ? '已回答' : (multi ? '可多选' : '单选')) + '</span>' +
      '</div>' +
      (q.header ? '<p class="ask-title">' + esc(q.header) + '</p>' : '') +
      '<p class="ask-text">' + esc(q.text) + '</p>' +
      '<div class="ask-opts">' +
      opts.map((o, i) => '<button class="ask-opt' + (picked.indexOf(i) >= 0 ? ' is-picked' : '') + '" type="button" data-ask="' + m.id + '" data-opt="' + i + '"' + (done ? ' disabled' : '') + '>' +
        '<span class="ask-key" aria-hidden="true">' + (i + 1) + '</span>' +
        '<span class="ask-copy"><b>' + esc(o.label) + '</b>' + (o.desc ? '<small>' + esc(o.desc) + '</small>' : '') + '</span>' +
        '<span class="ask-mark" aria-hidden="true"></span>' +
        '</button>').join('') +
      '</div>' +
      (done
        ? (ans.custom ? '<p class="ask-answer">你的回答：' + esc(ans.custom) + '</p>' : '')
        : '<div class="ask-foot">' +
            (multi ? '<button class="ask-submit" type="button" data-ask-submit="' + m.id + '">提交选择</button>' : '<span class="ask-hint">按 1–' + opts.length + ' 选择</span>') +
            '<button class="ask-other" type="button" data-ask-other="' + m.id + '">其他…</button>' +
          '</div>') +
      '</div>';
  }

  function userAttachmentsHTML(m) {
    const list = Array.isArray(m.attachments) ? m.attachments : [];
    if (!list.length) return '';
    const images = list.filter((a) => a && a.url && /^image\//.test(a.type || ''));
    return '<div class="msg-atts">' + list.map((a) => {
      const isImage = a && /^image\//.test(a.type || '');
      if (isImage && a.url) {
        const idx = images.indexOf(a);
        return '<button class="msg-att-img" type="button" data-att-msg="' + m.id + '" data-att-img="' + idx + '" aria-label="预览图片 ' + esc(a.name) + '">' +
          '<img src="' + esc(a.url) + '" alt="' + esc(a.name) + '" loading="lazy"></button>';
      }
      return '<span class="msg-att-file"><svg class="ico" aria-hidden="true"><use href="#' + (isImage ? 'i-image' : 'i-file') + '"/></svg>' +
        '<span>' + esc((a && a.name) || '附件') + '</span></span>';
    }).join('') + '</div>';
  }

  function messageHTML(m, live) {
    if (m.role === 'user') {
      return '<article class="msg msg--user" data-mid="' + m.id + '"><div class="msg-body">' +
        '<div class="bubble">' + esc(m.text) + '</div>' +
        userAttachmentsHTML(m) +
        '<div class="msg-tools"><button class="msg-tool" type="button" data-copy="' + m.id + '" title="复制" aria-label="复制这条消息">' +
        '<svg class="ico" aria-hidden="true"><use href="#i-copy"/></svg></button><span class="msg-time">' + stamp(m.ts) + '</span></div></div></article>';
    }

    let body = '';
    if (m.thinking) body += thinkHTML(m, live);
    if (m.tools && m.tools.length) {
      body += toolsHTML(m.tools, m.toolsTotal);
    }
    const empty = !m.text && !m.thinking && !(m.tools && m.tools.length);
    body += '<div class="md">' + (m.text ? renderMarkdown(m.text) : (empty && !live ? thinkingHTML() : '')) + '</div>';
    if (m.error) body += errorHTML(m);
    if (m.artifact) body += artifactHTML(m);
    if (m.question) body += askHTML(m);

    return '<article class="msg msg--ai" data-mid="' + m.id + '">' +
      '<div class="ai-body">' + body +
      '<div class="msg-tools">' +
      '<button class="msg-tool" type="button" data-copy="' + m.id + '" title="复制" aria-label="复制这条回复">' +
      '<svg class="ico" aria-hidden="true"><use href="#i-copy"/></svg></button>' +
      '<button class="msg-tool" type="button" data-retry="' + m.id + '" title="重新生成" aria-label="重新生成这条回复">' +
      '<svg class="ico" aria-hidden="true"><use href="#i-retry"/></svg></button>' +
      (m.usage && (m.usage.total_tokens || m.usage.completion_tokens)
        ? '<span class="msg-usage" title="输入 ' + Number(m.usage.prompt_tokens || 0) + ' · 输出 ' + Number(m.usage.completion_tokens || 0) + '">' +
          formatCount(m.usage.total_tokens || m.usage.completion_tokens) + ' tok</span>'
        : '') +
      '<span class="msg-time">' + stamp(m.ts) + '</span>' +
      '</div></div></article>';
  }

  /* ---------------- rendering ---------------- */

  function renderSidebar() {
    const q = searchQuery.trim().toLowerCase();
    let html = '';
    let shown = 0;

    state.workspaces.forEach((ws) => {
      const nameMatch = !!q && ws.name.toLowerCase().indexOf(q) >= 0;
      const convs = ws.convs.filter((c) => !q || nameMatch || String(c.title || '').toLowerCase().indexOf(q) >= 0);
      if (q && !nameMatch && convs.length === 0) return;
      shown++;

      html += '<section class="ws-group">';
      html += '<button type="button" class="ws-name" data-ws="' + ws.id + '">' + esc(ws.name) + '</button>';
      convs.forEach((c) => {
        const active = c.id === state.activeConvId;
        html += '<div class="conv-wrap">';
        html += '<button type="button" class="conv-row' + (active ? ' active' : '') + '" data-conv="' + c.id + '"' + (active ? ' aria-current="true"' : '') + '>';
        html += '<span class="conv-dot" aria-hidden="true"></span>';
        html += '<span class="conv-title">' + esc(c.title || '新会话') + '</span>';
        html += '<span class="conv-time">' + esc(timeAgo(c.updatedAt || now())) + '</span>';
        html += '</button>';
        html += '<button type="button" class="conv-del" data-del="' + c.id + '" title="删除对话" aria-label="删除对话 ' + esc(c.title || '新会话') + '">';
        html += '<svg class="ico" aria-hidden="true"><use href="#i-trash"/></svg></button>';
        html += '</div>';
      });
      html += '</section>';
    });

    if (q && shown === 0) html = '<div id="noResults">没有找到匹配的对话</div>';
    el.wsList.innerHTML = html;
  }

  function announce(text) {
    if (!el.liveStatus) return;
    el.liveStatus.textContent = text;
  }

  function updateScrollBtn() {
    if (!el.scrollBtn) return;
    const gap = el.scroller.scrollHeight - el.scroller.scrollTop - el.scroller.clientHeight;
    el.scrollBtn.hidden = gap < 220;
  }

  /* ---------- 草稿：按会话保存，切换不丢 ---------- */
  let draftTimer = 0;

  function saveDraft() {
    const conv = activeConv();
    if (!conv) return;
    const value = el.input.value;
    if (value) state.drafts[conv.id] = value;
    else delete state.drafts[conv.id];
  }

  function restoreDraft() {
    const conv = activeConv();
    const value = conv && state.drafts ? state.drafts[conv.id] || '' : '';
    el.input.value = value;
    autoGrow();
    updateSendState();
  }

  function renderThread() {
    const conv = activeConv();
    const empty = !conv || !conv.messages || conv.messages.length === 0;
    const nearBottom = el.scroller.scrollHeight - el.scroller.scrollTop - el.scroller.clientHeight < 240;
    el.body.classList.toggle('is-empty', empty);
    el.thread.hidden = empty;
    const inChat = view === 'chat';
    el.topbarTitle.hidden = empty || !inChat;
    el.topbarTitle.textContent = (empty || !inChat) ? '' : (conv.title || '新会话');
    if (empty) {
      el.thread.innerHTML = '';
      el.scroller.scrollTop = 0;
      updateScrollBtn();
      return;
    }
    el.thread.innerHTML = conv.messages.map(messageHTML).join('');
    requestAnimationFrame(() => {
      if (nearBottom) el.scroller.scrollTop = el.scroller.scrollHeight;
      updateScrollBtn();
    });
  }

  function activeModel() {
    if (state.model) return state.model;
    const info = G.api && G.api.info ? G.api.info() : null;
    return (info && info.model) || '默认模型';
  }

  function updateComposer() {
    el.modelName.textContent = activeModel();
    el.modelEffort.textContent = state.effort;
    el.motionSwitch.setAttribute('aria-checked', String(state.reduceMotion));
    el.sideSwitch.setAttribute('aria-checked', String(state.sidebarCollapsed));
  }

  function renderAll() {
    renderSidebar();
    renderThread();
    updateComposer();
    updateSendState();
    updateContextChip();
  }

  function updateSendState() {
    const busy = !!stream;
    const hasText = el.input.value.trim().length > 0;
    el.sendBtn.disabled = busy ? false : !hasText;
    const wasBusy = el.sendBtn.getAttribute('data-busy') === '1';
    el.sendBtn.classList.toggle('is-stop', busy);
    if (wasBusy !== busy) {
      el.sendBtn.setAttribute('data-busy', busy ? '1' : '0');
      el.sendBtn.innerHTML = '<svg class="ico" aria-hidden="true"><use href="#' + (busy ? 'i-stop' : 'i-arrow-up') + '"/></svg>';
    }
    el.sendBtn.setAttribute('aria-label', busy ? '停止生成' : '发送');
    el.sendBtn.title = busy ? '停止生成' : '发送';
  }

  function autoGrow() {
    const min = mqMobile.matches ? 44 : 50;
    el.input.style.height = 'auto';
    const h = Math.min(el.input.scrollHeight, 210);
    el.input.style.height = Math.max(min, h) + 'px';
  }

  function scrollToBottom(force) {
    const sc = el.scroller;
    if (!sc) return;
    const near = sc.scrollHeight - sc.scrollTop - sc.clientHeight < 180;
    if (force || near) sc.scrollTop = sc.scrollHeight;
  }

  /* ---------------- sending / streaming ---------------- */

  function handleSend() {
    if (stream) { stopStreaming(); return; }
    const text = el.input.value.trim();
    if (!text) return;

    let conv = activeConv();
    if (!conv) {
      const ws = activeWs();
      conv = { id: uid(), title: '新会话', messages: [], updatedAt: now() };
      ws.convs.unshift(conv);
      state.activeConvId = conv.id;
    }

    const sending = attached.slice();
    const attachments = sending.map((f) => ({
      name: f.name,
      size: f.size || 0,
      type: f.type || '',
      url: urlForFile(f)
    }));
    attached = [];
    attachUrls.clear();
    renderAttachments();
    updateSendState();

    conv.messages.push({ id: uid(), role: 'user', text: text, ts: now(), attachments: attachments });
    if (!conv.title || conv.title === '新会话') conv.title = titleFrom(text);
    conv.updatedAt = now();
    moveConvToTop(conv);

    el.input.value = '';
    delete state.drafts[conv.id];
    autoGrow();
    save();
    renderAll();
    scrollToBottom(true);
    respond(conv, text, sending.map((f) => f.name));
  }

  function moveConvToTop(conv) {
    const found = findConv(conv.id);
    if (!found) return;
    const list = found.ws.convs;
    const idx = list.indexOf(conv);
    if (idx > 0) { list.splice(idx, 1); list.unshift(conv); }
  }

  function respond(conv, prompt, files) {
    if (stream) stopStreaming();
    const msg = { id: uid(), role: 'assistant', text: '', ts: now() };
    conv.messages.push(msg);
    conv.updatedAt = now();
    moveConvToTop(conv);

    renderThread();
    renderSidebar();
    renderAttachments();
    updateComposer();
    scrollToBottom(true);

    if (!G.api || !G.api.available()) {
      msg.error = { message: '未连接服务端，请先启动后端（npm start）', code: 'no_provider' };
      finishStream();
      return;
    }
    if (files && files.length) {
      toast('附件暂未接入上传通道，这条回复不含文件内容');
    }
    startApiStream(conv, msg);
  }

  /* ---------- 真实后端流式（SSE） ---------- */

  function apiMessages(conv) {
    return conv.messages
      .filter((m) => m && m.text && (m.role === 'user' || m.role === 'assistant') && !m.error)
      .slice(-20)
      .map((m) => ({ role: m.role, content: String(m.text).slice(0, 20000) }));
  }

  function upsertTool(list, tool) {
    if (!tool) return;
    const key = String(tool.name || '') + '|' + String(tool.detail || '');
    const found = list.find((t) => String(t.name || '') + '|' + String(t.detail || '') === key);
    const next = Object.assign({ icon: 'i-check' }, tool);
    if (found) Object.assign(found, next);
    else list.push(next);
  }

  function startApiStream(conv, msg) {
    const controller = new AbortController();
    stream = {
      mode: 'api', convId: conv.id, msgId: msg.id, demo: false,
      thinking: '', thinkingShown: 0, tools: [], toolIndex: 0,
      full: '', shown: 0, stage: 'thinking', started: now(), timer: 0,
      controller: controller, usage: null, error: null
    };
    updateSendState();
    G.clawd.set('thinking');
    paintStream();

    G.api.chat({
      messages: apiMessages(conv),
      model: activeModel(),
      effort: state.effort,
      signal: controller.signal,
      onEvent: (event) => {
        if (!stream || stream.controller !== controller) return;
        const s = stream;
        if (event.type === 'thinking') {
          s.thinking += event.delta || '';
          s.thinkingShown = s.thinking.length;
        } else if (event.type === 'text') {
          if (s.stage !== 'text') { s.stage = 'text'; s.thinkingShown = s.thinking.length; G.clawd.set('typing'); }
          s.full += event.delta || '';
          s.shown = s.full.length;
        } else if (event.type === 'tool') {
          upsertTool(s.tools, event.tool);
        } else if (event.type === 'done') {
          s.usage = event.usage || null;
        }
        scheduleStreamPaint();
      }
    }).then(() => {
      if (stream && stream.controller === controller) finishStream();
    }).catch((err) => {
      if (!stream || stream.controller !== controller) return;
      if (err && err.name === 'AbortError') return;
      stream.error = { message: err.message || '请求失败', code: err.code || 'chat_failed' };
      finishStream();
    });
  }

  function liveMsg() {
    const s = stream;
    return {
      id: s.msgId, role: 'assistant', ts: now(),
      thinking: s.thinking.slice(0, s.thinkingShown),
      thinkMs: Math.max(1, Math.round((now() - s.started) / 1000)),
      tools: s.tools.filter((t) => t.status !== 'idle'),
      toolsTotal: s.tools.length,
      text: s.full.slice(0, s.shown)
    };
  }

  let paintTimer = 0;
  let paintLast = 0;
  /* 流式重绘节流：约 18fps 足够像打字，同时避免每 16ms 重解析整段 Markdown */
  function scheduleStreamPaint() {
    if (paintTimer) return;
    const wait = Math.max(0, 55 - (now() - paintLast));
    paintTimer = setTimeout(() => {
      paintTimer = 0;
      paintLast = now();
      paintStream();
      scrollToBottom(false);
      updateScrollBtn();
    }, wait);
  }

  function cancelStreamPaint() {
    if (paintTimer) { clearTimeout(paintTimer); paintTimer = 0; }
  }

  function paintStream() {
    if (!stream) return;
    if (!stream.firstAt) stream.firstAt = now();
    const article = el.thread.querySelector('.msg--ai[data-mid="' + stream.msgId + '"]');
    if (!article) return;
    article.outerHTML = messageHTML(liveMsg(), { stage: stream.stage });
    if (stream.stage === 'text') {
      const md = el.thread.querySelector('.msg--ai[data-mid="' + stream.msgId + '"] .md');
      if (!md) return;
      const caret = document.createElement('span');
      caret.className = 'stream-caret';
      caret.setAttribute('aria-hidden', 'true');
      const blocks = md.querySelectorAll('p, li, h1, h2, h3, blockquote, td');
      (blocks.length ? blocks[blocks.length - 1] : md).appendChild(caret);
    }
  }

  function stopStreaming() {
    if (!stream) return;
    clearTimeout(stream.timer);
    if (stream.controller) { try { stream.controller.abort(); } catch (err) {} }
    finishStream();
  }

  function finishStream() {
    if (!stream) return;
    const s = stream;
    clearTimeout(s.timer);
    cancelStreamPaint();
    stream = null;
    const found = findConv(s.convId);
    if (found) {
      const msg = found.conv.messages.find((m) => m.id === s.msgId);
      if (msg) {
        const text = s.full.slice(0, s.shown).trim();
        msg.text = text || (s.error ? '' : '（已停止生成）');
        msg.thinking = s.thinking.slice(0, s.thinkingShown).trim();
        msg.thinkMs = Math.max(1, Math.round((now() - s.started) / 1000));
        msg.tools = s.tools.filter((t) => t.status !== 'idle').map((t) => Object.assign({}, t, {
          status: 'done',
          ms: t.ms || (t.t0 ? Math.max(80, now() - t.t0) : 0)
        }));
        if (s.usage) msg.usage = s.usage;
        if (s.error) msg.error = s.error;
        msg.ts = now();
      }
      found.conv.updatedAt = now();
    }
    const dur = now() - s.started;
    ctxUsage.modelMs += Math.max(0, dur);
    ctxUsage.toolMs += s.tools.reduce((sum, t) => sum + (t.ms || 0), 0);
    const produced = estimateTokens(s.full.slice(0, s.shown));
    ctxUsage.outTokens += produced;
    if (s.firstAt) {
      ctxUsage.firstMs += s.firstAt - s.started;
      ctxUsage.firstCount += 1;
      ctxUsage.outMs += Math.max(1, dur - (s.firstAt - s.started));
    }
    ctxUsage.uncached += Math.round(estimateTokens(activeConv() ? activeConv().messages.map((m) => m.text || '').join('') : '') * 0.12);
    updateContextChip();
    if (s.error) {
      G.clawd.idle();
      announce('回复失败：' + s.error.message);
      toast(s.error.message || '请求失败');
    } else {
      G.clawd.happy();
      announce('回复完成');
    }
    save();
    renderThread();
    renderSidebar();
    updateSendState();
    updateScrollBtn();
    if (!mqMobile.matches && !s.error) el.input.focus({ preventScroll: true });
  }

  /* ---------------- reply generator ---------------- */

  /* ---------------- interactions ---------------- */

  function focusInput() {
    if (mqMobile.matches) return;
    el.input.focus({ preventScroll: true });
  }

  function closeDrawer() {
    el.app.classList.remove('drawer-open');
  }

  function setSidebarCollapsed(collapsed) {
    state.sidebarCollapsed = !!collapsed;
    el.app.setAttribute('data-sidebar', state.sidebarCollapsed ? 'collapsed' : 'open');
    el.sideSwitch.setAttribute('aria-checked', String(state.sidebarCollapsed));
    save();
  }

  function selectConv(id) {
    closeFilesPanel();
    if (stream) stopStreaming();
    const found = findConv(id);
    if (!found) return;
    if (state.activeConvId !== id) saveDraft();
    state.activeWorkspaceId = found.ws.id;
    state.activeConvId = id;
    save();
    setView('chat');
    renderAll();
    restoreDraft();
    el.scroller.scrollTop = el.scroller.scrollHeight;
    closeDrawer();
    if (!found.conv.messages.length) focusInput();
  }

  function selectWorkspace(wsId) {
    if (stream) stopStreaming();
    saveDraft();
    const ws = state.workspaces.find((w) => w.id === wsId);
    if (!ws) return;
    let conv = ws.convs[0];
    if (!conv) {
      conv = { id: uid(), title: '新会话', messages: [], updatedAt: now() };
      ws.convs.unshift(conv);
    }
    state.activeWorkspaceId = ws.id;
    state.activeConvId = conv.id;
    save();
    setView('chat');
    renderAll();
    restoreDraft();
    el.scroller.scrollTop = el.scroller.scrollHeight;
    closeDrawer();
    focusInput();
  }

  function newChat() {
    closeFilesPanel();
    if (stream) stopStreaming();
    saveDraft();
    const ws = activeWs();
    let conv = activeConv();
    if (!conv || conv.messages.length > 0) {
      conv = { id: uid(), title: '新会话', messages: [], updatedAt: now() };
      ws.convs.unshift(conv);
    }
    state.activeWorkspaceId = ws.id;
    state.activeConvId = conv.id;
    save();
    setView('chat');
    renderAll();
    restoreDraft();
    closeDrawer();
    focusInput();
    el.scroller.scrollTop = 0;
  }

  function deleteConv(id) {
    if (stream && stream.convId === id) stopStreaming();
    const found = findConv(id);
    if (!found) return;
    const ws = found.ws;
    const index = ws.convs.findIndex((c) => c.id === id);
    const removed = ws.convs.splice(index, 1)[0];
    if (!removed) return;

    if (state.activeConvId === id) {
      const next = ws.convs[Math.max(0, index - 1)] || ws.convs[0];
      if (next) {
        state.activeConvId = next.id;
      } else {
        const fresh = { id: uid(), title: '新会话', messages: [], updatedAt: now() };
        ws.convs.unshift(fresh);
        state.activeConvId = fresh.id;
      }
      state.activeWorkspaceId = ws.id;
    }
    save();
    renderAll();
    restoreDraft();
    toast('已删除对话', '撤销', () => {
      const target = state.workspaces.find((w) => w.id === ws.id);
      if (!target) return;
      target.convs.splice(Math.min(index, target.convs.length), 0, removed);
      state.activeWorkspaceId = target.id;
      state.activeConvId = removed.id;
      save();
      renderAll();
      toast('已恢复对话');
    });
  }

  function retryMessage(mid) {
    if (stream) stopStreaming();
    const conv = activeConv();
    if (!conv) return;
    const idx = conv.messages.findIndex((m) => m.id === mid);
    if (idx < 1) return;
    let u = idx - 1;
    while (u >= 0 && conv.messages[u].role !== 'user') u--;
    if (u < 0) return;
    const prompt = conv.messages[u].text;
    conv.messages = conv.messages.slice(0, u + 1);
    conv.updatedAt = now();
    save();
    respond(conv, prompt, []);
  }

  function copyText(text) {
    if (!text) return;
    const done = () => toast('已复制');
    const fallback = () => {
      const ta = document.createElement('textarea');
      ta.value = text;
      ta.setAttribute('readonly', '');
      ta.style.position = 'fixed';
      ta.style.top = '-1000px';
      ta.style.opacity = '0';
      document.body.appendChild(ta);
      ta.select();
      try { document.execCommand('copy'); done(); } catch (err) { toast('复制失败，请手动选择文本'); }
      ta.remove();
    };
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(text).then(done).catch(fallback);
    } else {
      fallback();
    }
  }

  function syncInert() {
    el.app.inert = !el.settingsLayer.hidden || !el.lightbox.hidden || !el.imgbox.hidden;
  }

  function openImgbox(items, index, trigger) {
    if (!items || !items.length) return;
    imgboxItems = items.slice();
    imgboxIndex = Math.max(0, Math.min(Number(index) || 0, imgboxItems.length - 1));
    imgboxTrigger = trigger || document.activeElement;
    el.imgbox.hidden = false;
    document.body.classList.add('imgbox-open');
    syncInert();
    paintImgbox();
    const panel = el.imgbox.querySelector('.imgbox-panel');
    if (panel) panel.focus({ preventScroll: true });
  }

  function paintImgbox() {
    if (!imgboxItems.length) return;
    const item = imgboxItems[imgboxIndex];
    el.imgboxTitle.textContent = item.name || '图片预览';
    el.imgboxMeta.textContent = formatBytes(item.size || 0);
    el.imgboxCount.textContent = (imgboxIndex + 1) + ' / ' + imgboxItems.length;
    el.imgboxPrev.disabled = imgboxItems.length < 2;
    el.imgboxNext.disabled = imgboxItems.length < 2;
    el.imgboxSource.href = item.url || '#';
    el.imgboxSource.style.display = item.url ? '' : 'none';
    el.imgboxImg.onload = function () {
      if (!el.imgboxImg.naturalWidth) return;
      el.imgboxMeta.textContent = formatBytes(item.size || 0) + ' · ' + el.imgboxImg.naturalWidth + ' × ' + el.imgboxImg.naturalHeight;
    };
    el.imgboxImg.src = item.url;
    el.imgboxImg.alt = item.name || '图片预览';
  }

  function stepImgbox(delta) {
    if (!imgboxItems.length || imgboxItems.length < 2) return;
    imgboxIndex = (imgboxIndex + delta + imgboxItems.length) % imgboxItems.length;
    paintImgbox();
  }

  function closeImgbox() {
    if (el.imgbox.hidden) return;
    el.imgbox.hidden = true;
    document.body.classList.remove('imgbox-open');
    el.imgboxImg.removeAttribute('src');
    syncInert();
    const trigger = imgboxTrigger;
    imgboxTrigger = null;
    imgboxItems = [];
    if (trigger && trigger.isConnected && typeof trigger.focus === 'function') trigger.focus({ preventScroll: true });
  }

  function openComposerImage(index, trigger) {
    const file = attached[index];
    if (!file) return;
    const items = imageItemsFromFiles(attached);
    let pos = -1;
    items.forEach((item, i) => { if (item.name === file.name && item.size === (file.size || 0)) pos = i; });
    openImgbox(items, pos >= 0 ? pos : 0, trigger);
  }

  function openMessageImage(mid, imgIndex, trigger) {
    const conv = activeConv();
    if (!conv) return;
    const msg = conv.messages.find((m) => m.id === mid);
    if (!msg) return;
    const items = (msg.attachments || []).filter((a) => a && a.url && /^image\//.test(a.type || '')).map((a) => ({
      name: a.name || '图片',
      size: a.size || 0,
      type: a.type || '',
      url: a.url
    }));
    if (!items.length) return;
    openImgbox(items, Number(imgIndex) || 0, trigger);
  }

  const filesState = { root: '~/项目', files: [], current: null, tree: [], treeView: false };

  function buildFiles(messages) {
    const arts = [];
    (messages || []).forEach((m) => {
      const a = m.artifact;
      if (a && !arts.some((x) => x.title === a.title)) arts.push(a);
    });
    const first = arts[0];
    const root = first && first.path ? first.path.replace(/\/[^/]+$/, '') : '~/项目';
    const files = [];
    arts.forEach((a, i) => {
      files.push({
        id: 'art-' + i, name: a.title, path: a.path || root + '/' + a.title,
        lang: a.lang, content: a.content, lines: a.lines, artifact: true
      });
    });
    return { root, files };
  }

  function buildTree(files) {
    return [
      {
        kind: 'folder', key: 'data', name: 'data', children: [
          {
            kind: 'folder', key: 'data/train', name: 'train', children: [
              { kind: 'folder', key: 'data/train/cats', name: 'cats' },
              { kind: 'folder', key: 'data/train/dogs', name: 'dogs' },
              { kind: 'folder', key: 'data/train/birds', name: 'birds' }
            ]
          }
        ]
      }
    ].concat(files.map((f) => ({ kind: 'file', key: f.id, file: f })));
  }

  function isFolderOpen(key) {
    return filesState.open[key] === true;
  }

  function treeIndent(depth) {
    let out = '';
    for (let i = 0; i < depth; i += 1) out += '<span class="fp-guide"></span>';
    return out;
  }

  function renderFilesTree() {
    const rows = [];
    const rootName = filesState.root.split('/').filter(Boolean).pop() || 'project';
    const folderRow = (node, depth) => {
      const open = isFolderOpen(node.key);
      return '<div class="fp-row is-folder" role="treeitem" tabindex="-1" data-key="' + esc(node.key) + '" data-kind="folder"' +
        ' aria-level="' + (depth + 1) + '" aria-expanded="' + open + '">' +
        treeIndent(depth) +
        '<svg class="ico fp-chev" aria-hidden="true"><use href="#i-chevron"/></svg>' +
        '<svg class="ico fp-ico" aria-hidden="true"><use href="#i-folder"/></svg>' +
        '<span class="fp-name">' + esc(node.name) + '</span></div>';
    };
    const fileRow = (file, depth) => {
      const active = filesState.current && filesState.current.id === file.id;
      const ext = String(file.name.split('.').pop() || '').toLowerCase();
      return '<div class="fp-row' + (active ? ' is-active' : '') + '" role="treeitem" tabindex="-1" data-key="' + esc(file.id) + '"' +
        ' data-kind="file" data-ext="' + esc(ext) + '" aria-level="' + (depth + 1) + '" aria-selected="' + (!!active) + '">' +
        treeIndent(depth) +
        '<svg class="ico fp-ico" aria-hidden="true"><use href="#i-file"/></svg>' +
        '<span class="fp-name">' + esc(file.name) + '</span>' +
        (file.artifact ? '<span class="fp-mark" title="由顾清影生成"></span>' : '<span class="fp-lines">' + file.lines + '</span>') +
        '</div>';
    };
    const walk = (nodes, depth) => {
      nodes.forEach((node) => {
        if (node.kind === 'folder') {
          rows.push(folderRow(node, depth));
          if (isFolderOpen(node.key)) walk(node.children || [], depth + 1);
        } else {
          rows.push(fileRow(node.file, depth));
        }
      });
    };
    rows.push('<div class="fp-row is-folder is-root" role="treeitem" tabindex="-1" data-key="" data-kind="folder" aria-level="1"' +
      ' aria-expanded="' + isFolderOpen('') + '">' +
      '<svg class="ico fp-chev" aria-hidden="true"><use href="#i-chevron"/></svg>' +
      '<svg class="ico fp-ico" aria-hidden="true"><use href="#i-folder"/></svg>' +
      '<span class="fp-name">' + esc(rootName) + '</span></div>');
    if (isFolderOpen('')) walk(filesState.tree, 1);
    el.filesTree.innerHTML = rows.join('');
    const active = filesState.current && el.filesTree.querySelector('[data-key="' + filesState.current.id + '"]');
    const focusTarget = active || el.filesTree.querySelector('[role="treeitem"]');
    if (focusTarget) focusTarget.tabIndex = 0;
    if (el.filesTreeFoot) {
      const arts = filesState.files.filter((f) => f.artifact).length;
      el.filesTreeFoot.textContent = filesState.files.length + ' 个文件 · ' + arts + ' 个产物';
    }
  }

  function treeRows() {
    return Array.prototype.slice.call(el.filesTree.querySelectorAll('[role="treeitem"]'));
  }

  function focusTreeRow(key) {
    const row = el.filesTree.querySelector('[role="treeitem"][data-key="' + key + '"]');
    if (!row) return;
    treeRows().forEach((r) => { r.tabIndex = -1; });
    row.tabIndex = 0;
    row.focus({ preventScroll: true });
  }

  function toggleFolder(key) {
    filesState.open[key] = !isFolderOpen(key);
    renderFilesTree();
    focusTreeRow(key);
  }

  function selectFile(id, opts) {
    const file = filesState.files.find((f) => f.id === id);
    if (!file) return;
    filesState.current = file;
    filesState.treeView = false;
    el.filesPanel.setAttribute('data-view', 'code');
    el.filesTree.hidden = true;
    el.filesCodePane.hidden = false;
    el.filesTreeBtn.setAttribute('aria-pressed', 'false');
    const dirs = file.path.replace(/^~\//, '').split('/');
    const tail = dirs.slice(-2);
    el.filesName.innerHTML = tail.map((part, i) => (i ? '<span class="fp-sep">›</span>' : '') +
      '<span' + (i === tail.length - 1 ? ' class="fp-last"' : '') + '>' + esc(part) + '</span>').join('');
    el.filesStat.textContent = '+' + file.lines;
    el.filesGutter.textContent = Array.from({ length: file.lines }, (_, i) => i + 1).join('\n');
    el.filesCode.innerHTML = file.lang === 'md' ? esc(file.content) : highlightHTML(file.content, file.lang);
    el.filesCopy.onclick = () => copyText(file.content);
    document.querySelector('.fp-scroll').scrollTop = 0;
    renderFilesTree();
    if (opts && opts.focus) focusTreeRow(file.id);
  }

  function showFilesTree() {
    filesState.treeView = true;
    el.filesPanel.setAttribute('data-view', 'tree');
    el.filesTree.hidden = false;
    el.filesCodePane.hidden = true;
    el.filesTreeBtn.setAttribute('aria-pressed', 'true');
    renderFilesTree();
    const row = el.filesTree.querySelector('[role="treeitem"][tabindex="0"]');
    if (row) row.focus({ preventScroll: true });
  }

  function openFilesPanel(mid) {
    const m = findMessage(mid);
    if (!m || !m.artifact) return;
    const conv = activeConv();
    const data = buildFiles(conv ? conv.messages : []);
    filesState.root = data.root;
    filesState.files = data.files;
    filesState.tree = buildTree(data.files);
    filesState.open = { '': true, data: true, 'data/train': false };
    filesState.current = null;
    el.filesRoot.textContent = data.root;
    el.filesPanel.classList.add('is-open');
    el.filesPanel.setAttribute('aria-hidden', 'false');
    el.filesPanel.inert = false;
    if (el.filesResize) el.filesResize.hidden = false;
    applyStoredFilesWidth();
    const target = data.files.find((f) => f.artifact && f.name === m.artifact.title) || data.files[0];
    if (target) selectFile(target.id);
  }

  function closeFilesPanel() {
    if (!el.filesPanel || !el.filesPanel.classList.contains('is-open')) return;
    el.filesPanel.classList.remove('is-open');
    el.filesPanel.setAttribute('aria-hidden', 'true');
    el.filesPanel.inert = true;
    if (el.filesResize) el.filesResize.hidden = true;
  }

  function toggleFilesTree() {
    if (!filesState.current || el.filesTree.hidden) showFilesTree();
    else selectFile(filesState.current.id);
  }

  /* ---------- 面板宽度拖动（持久化） ---------- */
  const FILES_WIDTH_KEY = 'gqy:files-width';
  let filesDrag = null;

  function clampFilesWidth(width) {
    const row = el.mainRow ? el.mainRow.getBoundingClientRect().width : 960;
    const max = Math.max(340, row - 380);
    return Math.max(320, Math.min(width, Math.min(720, max)));
  }

  function applyStoredFilesWidth() {
    let width = 0;
    try { width = Number(localStorage.getItem(FILES_WIDTH_KEY)) || 0; } catch (err) {}
    if (width > 0) el.filesPanel.style.flexBasis = clampFilesWidth(width) + 'px';
  }

  function initFilesResize() {
    if (!el.filesResize) return;
    el.filesResize.addEventListener('pointerdown', (e) => {
      if (e.button !== 0) return;
      filesDrag = { x: e.clientX, width: el.filesPanel.getBoundingClientRect().width };
      el.filesResize.classList.add('is-active');
      try { el.filesResize.setPointerCapture(e.pointerId); } catch (err) {}
      e.preventDefault();
    });
    el.filesResize.addEventListener('pointermove', (e) => {
      if (!filesDrag) return;
      const width = clampFilesWidth(filesDrag.width - (e.clientX - filesDrag.x));
      el.filesPanel.style.flexBasis = width + 'px';
    });
    const end = (e) => {
      if (!filesDrag) return;
      filesDrag = null;
      el.filesResize.classList.remove('is-active');
      try { el.filesResize.releasePointerCapture(e.pointerId); } catch (err) {}
      try { localStorage.setItem(FILES_WIDTH_KEY, String(Math.round(el.filesPanel.getBoundingClientRect().width))); } catch (err) {}
    };
    el.filesResize.addEventListener('pointerup', end);
    el.filesResize.addEventListener('pointercancel', end);
    el.filesResize.addEventListener('dblclick', () => {
      el.filesPanel.style.flexBasis = '';
      try { localStorage.removeItem(FILES_WIDTH_KEY); } catch (err) {}
    });
  }

  function openViewer(file, trigger) {
    if (!file) return;
    lbTrigger = trigger || document.activeElement;
    const language = file.lang || 'code';
    const path = file.path || '~/项目/' + file.name;
    const gutter = el.lightbox.querySelector('.lb-gutter');
    const src = el.lightbox.querySelector('.lb-src code');
    el.lbTitle.textContent = file.name;
    el.lbPath.textContent = path;
    el.lbBarName.textContent = path;
    el.lbLang.textContent = language;
    el.lbLines.textContent = file.lines + ' 行';
    el.lbAdded.textContent = file.lines;
    el.lbFootLang.textContent = language.charAt(0).toUpperCase() + language.slice(1);
    gutter.textContent = Array.from({ length: file.lines }, (_, i) => i + 1).join('\n');
    src.innerHTML = language === 'md' ? esc(file.content) : highlightHTML(file.content, language);
    el.lightbox.hidden = false;
    document.body.classList.add('lb-open');
    syncInert();
    el.lightbox.querySelector('.lb-code').scrollTop = 0;
    el.lightbox.querySelector('.lb-code').scrollLeft = 0;
    el.lbCopy.onclick = () => copyText(file.content);
    const panel = el.lightbox.querySelector('.lb-panel');
    if (panel) panel.focus({ preventScroll: true });
  }

  function openArtifact(mid, trigger) {
    const m = findMessage(mid);
    if (!m || !m.artifact) return;
    const a = m.artifact;
    openViewer({ name: a.title, path: a.path, lang: a.lang, content: a.content, lines: a.lines }, trigger);
  }

  function closeLightbox() {
    if (el.lightbox.hidden) return;
    el.lightbox.hidden = true;
    document.body.classList.remove('lb-open');
    syncInert();
    const trigger = lbTrigger;
    lbTrigger = null;
    if (trigger && trigger.isConnected && typeof trigger.focus === 'function') trigger.focus({ preventScroll: true });
  }

  function answerQuestion(mid, picks, custom) {
    const msg = findMessage(mid);
    if (!msg || !msg.question || msg.question.answer) return;
    const opts = msg.question.options.map((o) => (typeof o === 'string' ? { label: o } : o));
    const chosen = picks || [];
    const labels = custom ? [custom] : chosen.map((i) => (opts[i] ? opts[i].label : '')).filter(Boolean);
    if (!labels.length) return;
    msg.question.answer = { picks: chosen, custom: custom || '' };
    const conv = activeConv();
    if (!conv || !conv.messages.some((m) => m.id === msg.id)) {
      save();
      renderThread();
      return;
    }
    conv.messages.push({ id: uid(), role: 'user', text: labels.join('、'), ts: now() });
    conv.updatedAt = now();
    moveConvToTop(conv);
    save();
    renderThread();
    scrollToBottom(true);
    respond(conv, labels.join('、'), []);
  }

  function openAskCustom(mid) {
    const card = el.thread.querySelector('.ask[data-askcard="' + mid + '"]');
    if (!card || card.querySelector('.ask-other-form')) return;
    const foot = card.querySelector('.ask-foot');
    if (!foot) return;
    const form = document.createElement('form');
    form.className = 'ask-other-form';
    form.innerHTML = '<input type="text" maxlength="120" placeholder="输入你自己的答案…" aria-label="自定义答案"><button type="submit">发送</button>';
    foot.innerHTML = '';
    foot.appendChild(form);
    const input = form.querySelector('input');
    form.addEventListener('submit', (e) => {
      e.preventDefault();
      const value = input.value.trim();
      if (value) answerQuestion(mid, [], value);
    });
    input.focus({ preventScroll: true });
  }

  function copyMessage(mid) {
    const conv = activeConv();
    if (!conv) return;
    const msg = conv.messages.find((m) => m.id === mid);
    if (msg) copyText(msg.text);
  }

  /* ---------------- toast ---------------- */

  function hideToast() {
    el.toast.classList.remove('show');
  }

  function toast(message, actionLabel, action) {
    el.toast.innerHTML = '';
    const span = document.createElement('span');
    span.textContent = message;
    el.toast.appendChild(span);
    if (actionLabel) {
      const btn = document.createElement('button');
      btn.type = 'button';
      btn.className = 'toast-action';
      btn.textContent = actionLabel;
      btn.addEventListener('click', () => { hideToast(); if (action) action(); });
      el.toast.appendChild(btn);
    }
    el.toast.classList.add('show');
    clearTimeout(toastTimer);
    toastTimer = setTimeout(hideToast, actionLabel ? 6000 : 2400);
  }

  /* ---------------- menus ---------------- */

  function closeMenu(restoreFocus) {
    if (!menu) return;
    menu.remove();
    menu = null;
    const anchor = menuAnchor;
    menuAnchor = null;
    if (anchor) {
      anchor.setAttribute('aria-expanded', 'false');
      if (restoreFocus) anchor.focus({ preventScroll: true });
    }
    document.removeEventListener('pointerdown', onDocPointer, true);
    document.removeEventListener('keydown', onMenuKey, true);
  }

  function onDocPointer(e) {
    if (!menu) return;
    if (menu.contains(e.target)) return;
    if (menuAnchor && menuAnchor.contains(e.target)) return;
    closeMenu();
  }

  function onMenuKey(e) {
    if (!menu) return;
    if (e.key === 'Escape') { e.preventDefault(); closeMenu(true); return; }
    if (e.target && e.target.tagName === 'INPUT') return;
    if (e.target && e.target.closest && e.target.closest('.effort-track')) return;
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault();
      const items = $$('button', menu).filter((b) => !b.disabled);
      if (!items.length) return;
      const idx = items.indexOf(document.activeElement);
      const dir = e.key === 'ArrowDown' ? 1 : -1;
      const next = idx === -1 ? 0 : (idx + dir + items.length) % items.length;
      items[next].focus({ preventScroll: true });
    }
  }

  function openMenu(anchor, build, opts) {
    opts = opts || {};
    const sameAnchor = !!menu && menuAnchor === anchor;
    closeMenu(false);
    if (sameAnchor) return;
    menuAnchor = anchor;
    anchor.setAttribute('aria-expanded', 'true');
    const node = document.createElement('div');
    node.className = 'menu';
    node.setAttribute('role', 'menu');
    build(node);
    document.body.appendChild(node);
    menu = node;
    positionMenu(node, anchor, opts.align || 'left', opts.place || 'auto');
    document.addEventListener('pointerdown', onDocPointer, true);
    document.addEventListener('keydown', onMenuKey, true);
    const first = $('button:not([disabled])', node);
    if (first) first.focus({ preventScroll: true });
  }

  function positionMenu(node, anchor, align, place) {
    const r = anchor.getBoundingClientRect();
    const w = node.offsetWidth;
    const h = node.offsetHeight;
    const vw = document.documentElement.clientWidth;
    const vh = document.documentElement.clientHeight;
    let left = align === 'right' ? r.right - w : r.left;
    left = Math.max(8, Math.min(left, vw - w - 8));
    let top;
    if (place === 'top' || (r.bottom + h + 8 > vh && r.top - h - 8 > 8)) top = r.top - h - 8;
    else top = r.bottom + 8;
    top = Math.max(8, Math.min(top, vh - h - 8));
    node.style.left = Math.round(left) + 'px';
    node.style.top = Math.round(top) + 'px';
  }

  function menuItem(label, opts) {
    opts = opts || {};
    const btn = document.createElement('button');
    btn.type = 'button';
    btn.className = 'menu-item';
    btn.setAttribute('role', 'menuitem');
    let inner = '';
    if (opts.icon) inner += '<svg class="ico" aria-hidden="true"><use href="#' + opts.icon + '"/></svg>';
    if (opts.desc) inner += '<span class="menu-copy"><b>' + esc(label) + '</b><small>' + esc(opts.desc) + '</small></span>';
    else inner += '<span>' + esc(label) + '</span>';
    if (opts.hint) inner += '<span class="menu-hint">' + esc(opts.hint) + '</span>';
    if (opts.checked) inner += '<svg class="ico menu-check" aria-hidden="true"><use href="#i-check"/></svg>';
    btn.innerHTML = inner;
    if (opts.desc) btn.classList.add('has-desc');
    if (opts.disabled) btn.disabled = true;
    btn.addEventListener('click', () => {
      if (opts.keepOpen) { if (opts.onSelect) opts.onSelect(); }
      else { closeMenu(); if (opts.onSelect) opts.onSelect(); }
    });
    return btn;
  }

  function speechSupported() {
    return !!(window.SpeechRecognition || window.webkitSpeechRecognition);
  }

  function pickFile(imagesOnly) {
    el.fileInput.accept = imagesOnly ? 'image/*' : '';
    el.fileInput.click();
  }

  function openPlusMenu() {
    openMenu(el.plusBtn, (node) => {
      node.appendChild(menuItem('上传文件', { icon: 'i-paperclip', onSelect: () => pickFile(false) }));
      node.appendChild(menuItem('上传图片', { icon: 'i-image', onSelect: () => pickFile(true) }));
      node.appendChild(menuItem('语音输入', { icon: 'i-mic', hint: speechSupported() ? '' : '不可用', disabled: !speechSupported(), onSelect: toggleMic }));
    }, { align: 'left', place: 'top' });
  }

  function mountEffort(card) {
    const steps = EFFORTS;
    const track = $('.effort-track', card);
    const knob = $('.effort-knob', card);
    const fill = $('.effort-fill', card);
    const ticks = $('.effort-ticks', card);
    const matrix = $('.effort-matrix', card);
    const valueEl = $('.effort-value', card);
    const ghost = $('.effort-ghost', card);
    const descEl = $('.effort-desc', card);
    let selected = Math.max(0, steps.indexOf(state.effort));
    let live = selected;
    let pressed = false;
    let dragging = false;
    let matrixWidth = 0;

    function span() { return Math.max(0, track.clientWidth - 16); }
    function xFor(i) {
      return steps.length < 2 ? span() : (i / (steps.length - 1)) * span();
    }

    function paintTicks() {
      if (!track.clientWidth) return;
      ticks.innerHTML = '';
      steps.forEach((_, i) => {
        const dot = document.createElement('i');
        dot.className = 'effort-tick';
        dot.style.left = (xFor(i) + 8) + 'px';
        ticks.appendChild(dot);
      });
    }

    function buildMatrix() {
      const w = track.clientWidth;
      if (!w || w === matrixWidth) return;
      matrixWidth = w;
      const block = 4;
      const gap = 1;
      const cols = Math.max(1, Math.floor((w + gap) / (block + gap)));
      matrix.style.gridTemplateColumns = 'repeat(' + cols + ', ' + block + 'px)';
      matrix.style.gridAutoRows = block + 'px';
      matrix.style.gap = gap + 'px';
      let html = '';
      for (let r = 0; r < 5; r += 1) {
        for (let c = 0; c < cols; c += 1) {
          const fx = cols < 2 ? 1 : c / (cols - 1);
          let fade = 1;
          if (fx <= 0.05) fade = 0;
          else if (fx < 0.78) { const t = (fx - 0.05) / 0.73; fade = t * t * (3 - 2 * t); }
          const delay = (-Math.random() * 2.6).toFixed(2);
          html += '<i class="effort-cell" style="opacity:' + fade.toFixed(2) + ';animation-delay:' + delay + 's"></i>';
        }
      }
      matrix.innerHTML = html;
    }

    function restart(el2) {
      el2.style.animation = 'none';
      void el2.offsetWidth;
      el2.style.animation = '';
    }

    function setValueText(name) {
      if (valueEl.textContent === name) return;
      const prev = valueEl.textContent;
      if (prev) {
        ghost.textContent = prev;
        ghost.style.left = valueEl.offsetLeft + 'px';
        restart(ghost);
      }
      valueEl.textContent = name;
      restart(valueEl);
    }

    function paint(animate) {
      const at = pressed ? live : selected;
      const x = xFor(at);
      const apex = steps.length > 1 && at === steps.length - 1;
      if (apex) buildMatrix();
      card.classList.toggle('is-apex', apex);
      const off = !animate && !dragging;
      if (off) {
        knob.style.transition = 'none';
        fill.style.transition = 'none';
      }
      knob.style.translate = Math.round(x) + 'px';
      fill.style.width = Math.round(x + 8) + 'px';
      if (off) {
        void knob.offsetWidth;
        knob.style.transition = '';
        fill.style.transition = '';
      }
      setValueText(steps[at]);
      descEl.textContent = EFFORT_DESC[steps[at]] || '';
      track.setAttribute('aria-valuenow', String(at));
      track.setAttribute('aria-valuetext', steps[at]);
    }

    function commit(index) {
      selected = Math.max(0, Math.min(steps.length - 1, index));
      live = selected;
      const next = steps[selected];
      if (next !== state.effort) {
        state.effort = next;
        save();
        updateComposer();
        el.modelEffort.classList.remove('pop');
        void el.modelEffort.offsetWidth;
        el.modelEffort.classList.add('pop');
      }
      paint(true);
    }

    function travelFor(clientX) {
      const rect = track.getBoundingClientRect();
      const raw = Math.max(0, Math.min(span(), clientX - rect.left - 8));
      if (steps.length < 2) return raw;
      const seg = span() / (steps.length - 1);
      if (!seg) return raw;
      const i = Math.min(steps.length - 2, Math.floor(raw / seg));
      const t = (raw - i * seg) / seg;
      return (i + t - 0.8 * Math.sin(2 * Math.PI * t) / (2 * Math.PI)) * seg;
    }

    function nearest(x) {
      const s = span();
      if (steps.length < 2 || !s) return 0;
      return Math.max(0, Math.min(steps.length - 1, Math.round((x / s) * (steps.length - 1))));
    }

    track.addEventListener('pointerdown', (e) => {
      if (e.button !== 0) return;
      pressed = true;
      dragging = false;
      live = nearest(travelFor(e.clientX));
      try { track.setPointerCapture(e.pointerId); } catch (err) {}
      paint(true);
    });
    track.addEventListener('pointermove', (e) => {
      if (!pressed) return;
      dragging = true;
      track.setAttribute('data-dragging', '');
      live = nearest(travelFor(e.clientX));
      paint(false);
    });
    function release(e) {
      if (!pressed) return;
      pressed = false;
      dragging = false;
      track.removeAttribute('data-dragging');
      try { track.releasePointerCapture(e.pointerId); } catch (err) {}
      commit(live);
    }
    track.addEventListener('pointerup', release);
    track.addEventListener('pointercancel', release);
    track.addEventListener('keydown', (e) => {
      if (e.key === 'ArrowLeft' || e.key === 'ArrowDown') { e.preventDefault(); commit(selected - 1); }
      else if (e.key === 'ArrowRight' || e.key === 'ArrowUp') { e.preventDefault(); commit(selected + 1); }
      else if (e.key === 'Home') { e.preventDefault(); commit(0); }
      else if (e.key === 'End') { e.preventDefault(); commit(steps.length - 1); }
    });

    setTimeout(() => {
      paintTicks();
      if (steps.length > 1 && selected === steps.length - 1) buildMatrix();
      paint(false);
    }, 0);
    window.addEventListener('resize', paintTicks);
  }

  function modelLabel(m) {
    if (!m) return activeModel();
    return m.name || m.id || '';
  }

  function selectModel(m) {
    // m 可能为 null（回到服务端默认），也可能为目录里的模型条目
    state.model = m ? m.id : '';
    save();
    updateComposer();
    if (el.aboutModelInline) el.aboutModelInline.textContent = activeModel();
  }

  async function ensureModelCatalog() {
    if (modelCatalog.length || modelCatalogLoading) return modelCatalog;
    if (!G.api || !G.api.models || !G.api.available()) return modelCatalog;
    modelCatalogLoading = true;
    try {
      modelCatalog = await G.api.models();
    } catch (err) {
      modelCatalog = [];
    } finally {
      modelCatalogLoading = false;
    }
    return modelCatalog;
  }

  function openModelMenu() {
    openMenu(el.modelBtn, (node) => {
      node.classList.add('model-menu');

      const search = document.createElement('div');
      search.className = 'model-search';
      search.innerHTML = '<svg class="ico" aria-hidden="true"><use href="#i-search"/></svg>' +
        '<input type="search" placeholder="搜索模型…" aria-label="搜索模型" autocomplete="off">';
      node.appendChild(search);

      const info = G.api && G.api.info ? G.api.info() : null;
      const online = G.api && G.api.available() && info && info.provider === 'openai-compatible';

      const list = document.createElement('div');
      list.className = 'model-list';
      node.appendChild(list);

      const applyModels = (models) => {
        const q = search.querySelector('input').value.trim().toLowerCase();
        const matches = models.filter((m) => {
          if (!q) return true;
          const hay = (m.id + ' ' + (m.name || '') + ' ' + m.provider + ' ' + (m.providerName || '')).toLowerCase();
          return hay.indexOf(q) >= 0;
        });
        list.innerHTML = '';
        if (matches.length === 0) {
          const empty = document.createElement('div');
          empty.className = 'model-empty';
          empty.textContent = models.length ? '没有匹配的模型' : (online ? '正在加载模型目录…' : '未连接服务端，无法加载模型目录');
          list.appendChild(empty);
          return;
        }
        matches.slice(0, 100).forEach((m) => {
          list.appendChild(menuItem(m.name || m.id, {
            desc: (m.providerName || m.provider) + ' · ' + m.id,
            hint: m.reasoning ? '推理' : (m.context ? formatCount(m.context) + ' ctx' : ''),
            checked: state.model === m.id,
            onSelect: () => selectModel(m)
          }));
        });
      };

      // 默认模型入口：清空选择，回到服务端 GQY_MODEL
      const info2 = G.api && G.api.info ? G.api.info() : null;
      if (online && info2 && info2.model) {
        list.appendChild(menuItem('默认（' + info2.model + '）', {
          desc: '服务端 GQY_MODEL',
          checked: state.model === '',
          onSelect: () => selectModel(null)
        }));
      }

      search.querySelector('input').addEventListener('input', () => {
        if (modelCatalog.length) applyModels(modelCatalog);
      });

      if (modelCatalog.length) {
        applyModels(modelCatalog);
      } else if (online) {
        ensureModelCatalog().then(applyModels);
      } else {
        applyModels([]);
      }

      // 推理强度
      const sep = document.createElement('div');
      sep.className = 'menu-sep';
      node.appendChild(sep);

      const effort = document.createElement('div');
      effort.className = 'effort';
      effort.innerHTML =
        '<div class="effort-head"><span class="effort-label">推理强度</span>' +
        '<span class="effort-value-wrap"><span class="effort-value">' + esc(state.effort) + '</span>' +
        '<span class="effort-ghost" aria-hidden="true"></span></span></div>' +
        '<div class="effort-ends"><span>更快</span><span>更强</span></div>' +
        '<div class="effort-track" role="slider" tabindex="0" aria-label="推理强度" ' +
        'aria-valuemin="0" aria-valuemax="' + (EFFORTS.length - 1) + '" aria-valuenow="' + Math.max(0, EFFORTS.indexOf(state.effort)) + '">' +
        '<div class="effort-fill"></div><div class="effort-ticks"></div>' +
        '<div class="effort-matrix" aria-hidden="true"></div><div class="effort-knob"></div></div>' +
        '<p class="effort-desc"></p>';
      node.appendChild(effort);
      mountEffort(effort);

      // 聚焦搜索框
      const inp = search.querySelector('input');
      if (inp) setTimeout(() => inp.focus(), 20);
    }, { align: 'right', place: 'top' });
  }

  /* ---------------- attachments / speech ---------------- */

  function isImageFile(file) {
    return !!file && /^image\//.test(file.type || '');
  }



  function urlForFile(file) {
    if (!isImageFile(file)) return '';
    if (attachUrls.has(file)) return attachUrls.get(file);
    const url = URL.createObjectURL(file);
    attachUrls.set(file, url);
    return url;
  }

  function addFiles(fileList) {
    const incoming = Array.prototype.slice.call(fileList || []);
    if (!incoming.length) return;
    let added = 0;
    let skipped = 0;
    incoming.forEach((file) => {
      if (!file || !file.name) return;
      const duplicate = attached.some((f) => f.name === file.name && f.size === file.size && f.lastModified === file.lastModified);
      if (attached.length >= MAX_ATTACH || duplicate) { skipped += 1; return; }
      attached.push(file);
      added += 1;
    });
    renderAttachments();
    updateSendState();
    if (added && skipped) toast('已添加 ' + added + ' 个附件，跳过 ' + skipped + ' 个');
    else if (added) toast('已添加 ' + added + ' 个附件');
    else if (skipped) toast('没有可添加的文件：重复或超过 ' + MAX_ATTACH + ' 个');
  }

  function removeAttached(index) {
    const file = attached[index];
    if (!file) return;
    if (isImageFile(file) && attachUrls.has(file)) {
      URL.revokeObjectURL(attachUrls.get(file));
      attachUrls.delete(file);
    }
    attached.splice(index, 1);
    renderAttachments();
    updateSendState();
  }

  function imageItemsFromFiles(files) {
    return files.filter(isImageFile).map((f) => ({
      name: f.name,
      size: f.size || 0,
      type: f.type || '',
      url: urlForFile(f)
    })).filter((item) => item.url);
  }

  function renderAttachments() {
    if (!attached.length) {
      el.attachRow.hidden = true;
      el.attachRow.innerHTML = '';
      return;
    }
    el.attachRow.hidden = false;
    el.attachRow.innerHTML = attached.map((f, i) => {
      if (isImageFile(f)) {
        return '<div class="attach-item">' +
          '<button class="attach-thumb" type="button" data-preview="' + i + '" aria-label="预览图片 ' + esc(f.name) + '">' +
          '<img src="' + esc(urlForFile(f)) + '" alt="' + esc(f.name) + '"></button>' +
          '<button class="attach-remove" type="button" data-rm="' + i + '" aria-label="移除附件 ' + esc(f.name) + '">' +
          '<svg class="ico" aria-hidden="true"><use href="#i-x"/></svg></button>' +
          '<span class="attach-meta">' + esc(f.name) + '</span></div>';
      }
      return '<span class="attach-file">' +
        '<svg class="ico file-ico" aria-hidden="true"><use href="#i-file"/></svg>' +
        '<span class="name">' + esc(f.name) + '</span>' +
        '<button class="rm" type="button" data-rm="' + i + '" aria-label="移除附件 ' + esc(f.name) + '">' +
        '<svg class="ico" aria-hidden="true"><use href="#i-x"/></svg></button></span>';
    }).join('');
  }

  function toggleMic() {
    const SR = window.SpeechRecognition || window.webkitSpeechRecognition;
    if (!SR) { toast('当前浏览器不支持语音输入'); return; }
    if (recog) {
      try { recog.stop(); } catch (err) {}
      return;
    }
    const r = new SR();
    r.lang = 'zh-CN';
    r.interimResults = true;
    r.continuous = false;
    const base = el.input.value;
    r.onresult = (event) => {
      let transcript = '';
      for (let i = event.resultIndex; i < event.results.length; i++) transcript += event.results[i][0].transcript;
      el.input.value = base ? base + (base.endsWith(' ') ? '' : ' ') + transcript : transcript;
      autoGrow();
      updateSendState();
    };
    r.onend = () => {
      recog = null;
      el.micBtn.classList.remove('recording');
      el.micBtn.setAttribute('aria-pressed', 'false');
    };
    r.onerror = () => { toast('语音输入没有启动成功'); };
    recog = r;
    el.micBtn.classList.add('recording');
    el.micBtn.setAttribute('aria-pressed', 'true');
    try { r.start(); } catch (err) {
      recog = null;
      el.micBtn.classList.remove('recording');
      el.micBtn.setAttribute('aria-pressed', 'false');
      toast('语音输入没有启动成功');
    }
  }

  /* ---------------- settings / dialogs ---------------- */

  function syncSettingsUI() {
    el.motionSwitch.setAttribute('aria-checked', String(state.reduceMotion));
    el.sideSwitch.setAttribute('aria-checked', String(state.sidebarCollapsed));
    el.thinkSwitch.setAttribute('aria-checked', String(state.thinkOpen));
    $$('button', el.fontSeg).forEach((b) => b.setAttribute('aria-pressed', String(b.getAttribute('data-font') === state.fontSize)));
    el.aboutModels.forEach((node) => { node.textContent = activeModel(); });
    if (el.aboutModelInline) el.aboutModelInline.textContent = activeModel();
    if (el.aboutEffortInline) el.aboutEffortInline.textContent = state.effort;
    renderServerStatus();
    refreshUsage();
  }

  function applyFontSize() {
    document.documentElement.style.setProperty('--msg-size', FONT_SIZES[state.fontSize] || FONT_SIZES.normal);
  }

  function usageSnapshot() {
    let raw = '';
    try { raw = localStorage.getItem(STORE_KEY) || ''; } catch (err) {}
    if (!raw) {
      try {
        raw = JSON.stringify(state, (key, value) => {
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
    let chars = 0;
    const days = [0, 0, 0, 0, 0, 0, 0];
    state.workspaces.forEach((ws) => {
      ws.convs.forEach((conv) => {
        convs += 1;
        conv.messages.forEach((msg) => {
          messages += 1;
          chars += (msg.text || '').length;
          if (msg.artifact) assets += 1;
          if (Array.isArray(msg.attachments)) assets += msg.attachments.length;
          const d = new Date(msg.ts || 0);
          d.setHours(0, 0, 0, 0);
          const diff = Math.round((base - d.getTime()) / 86400000);
          if (diff >= 0 && diff < 7) days[6 - diff] += 1;
        });
      });
    });
    return { bytes: bytes, budget: budget, convs: convs, messages: messages, assets: assets, chars: chars, days: days, base: base, tokens: Math.round(chars / 3.5) };
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

  function refreshUsage() {
    if (!el.usageBadge || !el.usageStorageText) return;
    const u = usageSnapshot();
    const pct = Math.min(100, u.bytes / u.budget * 100);
    const pctText = (pct >= 10 ? Math.round(pct) : pct.toFixed(1)) + '%';
    el.usageStorageText.textContent = formatBytes(u.bytes);
    el.usageStoragePercent.textContent = pctText + ' · 5 MB';
    el.usageWorkspaces.textContent = String(state.workspaces.length);
    el.usageConvs.textContent = String(u.convs);
    el.usageMessages.textContent = String(u.messages);
    el.usageAssets.textContent = String(u.assets);
    el.usageBadge.textContent = formatBytes(u.bytes);
    if (el.miniUsageText) el.miniUsageText.textContent = formatBytes(u.bytes);
    if (el.miniUsageBar) el.miniUsageBar.style.width = Math.max(2, pct) + '%';
    if (el.usageRing) {
      const circumference = 301.59;
      el.usageRing.style.strokeDashoffset = String(circumference - circumference * pct / 100);
    }
    const weekday = ['日', '一', '二', '三', '四', '五', '六'];
    const maxDay = Math.max.apply(null, u.days.concat([1]));
    const bars = [];
    const axis = [];
    u.days.forEach((count, i) => {
      const d = new Date(u.base - (6 - i) * 86400000);
      const height = count ? Math.max(8, Math.round(count / maxDay * 100)) : 4;
      bars.push('<div class="usage-bar' + (i === 6 ? ' is-today' : '') + (count ? '' : ' is-empty') + '" title="' + (d.getMonth() + 1) + '/' + d.getDate() + ' · ' + count + ' 条"><i style="height:' + height + '%"></i></div>');
      axis.push('<span>' + (i === 6 ? '今天' : '周' + weekday[d.getDay()]) + '</span>');
    });
    el.usageBars.innerHTML = bars.join('');
    el.usageAxis.innerHTML = axis.join('');
    el.usageWeekTotal.textContent = u.days.reduce((a, b) => a + b, 0) + ' 条';
  }

  function openSettingsTab(name, focusTab) {
    const valid = ['appearance', 'chat', 'data', 'usage', 'about'];
    settingsTab = valid.indexOf(name) >= 0 ? name : 'appearance';
    el.settingsTabs.forEach((tab) => {
      const on = tab.getAttribute('data-settings-tab') === settingsTab;
      tab.classList.toggle('is-active', on);
      tab.setAttribute('aria-selected', String(on));
      tab.tabIndex = on ? 0 : -1;
    });
    el.settingsPanels.forEach((panel) => {
      const on = panel.getAttribute('data-settings-panel') === settingsTab;
      panel.classList.toggle('is-active', on);
      panel.hidden = !on;
    });
    if (settingsTab === 'usage') refreshUsage();
    if (focusTab) {
      const tab = el.settingsTabs.find((node) => node.getAttribute('data-settings-tab') === settingsTab);
      if (tab) tab.focus({ preventScroll: true });
    }
  }

  function openSettings() {
    syncSettingsUI();
    settingsTrigger = el.accountBtn;
    el.settingsLayer.hidden = false;
    document.body.classList.add('settings-open');
    syncInert();
    openSettingsTab(settingsTab, false);
    el.settingsWindow.focus({ preventScroll: true });
    refreshUsage();
  }

  function closeSettings() {
    if (el.settingsLayer.hidden) return;
    el.settingsLayer.hidden = true;
    document.body.classList.remove('settings-open');
    syncInert();
    const trigger = settingsTrigger;
    settingsTrigger = null;
    if (trigger && trigger.isConnected && typeof trigger.focus === 'function') trigger.focus({ preventScroll: true });
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
    view = 'chat';
    el.body.hidden = false;
    if (el.mainRow) el.mainRow.hidden = false;
    renderThread();
    closeDrawer();
  }

  function exportData() {
    try {
      const blob = new Blob([JSON.stringify(state, null, 2)], { type: 'application/json' });
      const a = document.createElement('a');
      a.href = URL.createObjectURL(blob);
      a.download = 'guqingying-chats.json';
      document.body.appendChild(a);
      a.click();
      a.remove();
      setTimeout(() => URL.revokeObjectURL(a.href), 2000);
      toast('已导出对话 JSON');
    } catch (err) {
      toast('导出失败');
    }
  }

  function importData(file) {
    if (!file) return;
    if (file.size > 8 * 1024 * 1024) { toast('文件太大，最多 8 MB'); return; }
    const reader = new FileReader();
    reader.onload = () => {
      let next;
      try { next = JSON.parse(String(reader.result)); }
      catch (err) { toast('不是有效的 JSON'); return; }
      const okShape = next && Array.isArray(next.workspaces) && next.workspaces.length &&
        next.workspaces.every((ws) => ws && typeof ws.name === 'string' && Array.isArray(ws.convs));
      if (!okShape) { toast('结构不匹配，需要导出时生成的文件'); return; }
      if (!window.confirm('导入会覆盖当前的 ' + state.workspaces.length + ' 个工作区，继续？')) return;
      if (stream) stopStreaming();
      try {
        localStorage.setItem(STORE_KEY, JSON.stringify(next, dropBlobUrls));
      } catch (err) {
        toast('导入失败：本地存储空间不足');
        return;
      }
      state = loadState();
      applyFontSize();
      el.app.setAttribute('data-sidebar', state.sidebarCollapsed ? 'collapsed' : 'open');
      applyReduceMotion();
      renderServerStatus();
      renderAll();
      restoreDraft();
      closeSettings();
      setView('chat');
      toast('已导入 ' + state.workspaces.length + ' 个工作区');
    };
    reader.onerror = () => toast('读取文件失败');
    reader.readAsText(file);
  }

  function setupDetectedApi() {
    if (!G.api || !G.api.detect) { renderServerStatus(); return; }
    G.api.detect().then(() => {
      renderServerStatus();
      updateComposer();
      if (el.settingsLayer && !el.settingsLayer.hidden) syncSettingsUI();
      mirrorSession();
    });
  }

  function openWsDialog() {
    if (el.wsDialog.open) return;
    el.wsName.value = '';
    el.wsDialog.showModal();
    setTimeout(() => el.wsName.focus(), 30);
  }

  function applyReduceMotion() {
    document.documentElement.classList.toggle('reduce-motion', state.reduceMotion);
    G.clawd.sync();
    el.motionSwitch.setAttribute('aria-checked', String(state.reduceMotion));
  }

  function clearEverything() {
    if (stream) stopStreaming();
    const settings = {
      model: state.model, effort: state.effort,
      fontSize: state.fontSize, thinkOpen: state.thinkOpen,
      reduceMotion: state.reduceMotion, sidebarCollapsed: state.sidebarCollapsed
    };
    state = seedState();
    state.model = settings.model;
    state.effort = settings.effort;
    state.fontSize = settings.fontSize;
    state.thinkOpen = settings.thinkOpen;
    state.reduceMotion = settings.reduceMotion;
    state.sidebarCollapsed = settings.sidebarCollapsed;
    applyFontSize();
    el.app.setAttribute('data-sidebar', state.sidebarCollapsed ? 'collapsed' : 'open');
    applyReduceMotion();
    save();
    renderAll();
    closeSettings();
    setView('chat');
    toast('已清空全部对话');
  }

  /* ---------------- events ---------------- */

  el.newChatBtn.addEventListener('click', newChat);
  el.collapseBtn.addEventListener('click', () => {
    if (mqMobile.matches) { closeDrawer(); return; }
    setSidebarCollapsed(true);
    el.openSideBtn.focus({ preventScroll: true });
  });
  el.openSideBtn.addEventListener('click', () => {
    if (mqMobile.matches) el.app.classList.add('drawer-open');
    else setSidebarCollapsed(false);
  });
  el.scrim.addEventListener('click', closeDrawer);
  el.accountBtn.addEventListener('click', openSettings);

  function setSearchOpen(open, focus) {
    searchOpen = !!open;
    el.searchWrap.hidden = !searchOpen;
    el.searchBtn.setAttribute('aria-expanded', String(searchOpen));
    if (searchOpen) {
      if (focus !== false) {
        el.searchInput.focus({ preventScroll: true });
        el.searchInput.select();
      }
    } else {
      el.searchInput.value = '';
      searchQuery = '';
      renderSidebar();
    }
  }

  el.searchBtn.addEventListener('click', () => setSearchOpen(!searchOpen));

  el.searchInput.addEventListener('input', () => {
    searchQuery = el.searchInput.value;
    renderSidebar();
  });

  el.searchInput.addEventListener('keydown', (e) => {
    if (e.key === 'Escape') {
      e.stopPropagation();
      el.searchBtn.click();
    }
  });

  el.wsList.addEventListener('click', (e) => {
    const del = e.target.closest('[data-del]');
    if (del) { deleteConv(del.getAttribute('data-del')); return; }
    const conv = e.target.closest('[data-conv]');
    if (conv) { selectConv(conv.getAttribute('data-conv')); return; }
    const ws = e.target.closest('[data-ws]');
    if (ws) { selectWorkspace(ws.getAttribute('data-ws')); }
  });

  el.newWsBtn.addEventListener('click', openWsDialog);
  el.form.addEventListener('submit', (e) => {
    e.preventDefault();
    handleSend();
  });

  el.input.addEventListener('input', () => {
    G.clawd.wake();
    autoGrow();
    updateSendState();
    clearTimeout(draftTimer);
    draftTimer = setTimeout(() => { saveDraft(); save(); }, 500);
  });

  el.input.addEventListener('focus', () => G.clawd.wake());

  el.input.addEventListener('keydown', (e) => {
    if (e.key === 'Enter' && !e.shiftKey && !e.metaKey && !e.ctrlKey && !e.isComposing && e.keyCode !== 229 && !mqMobile.matches) {
      e.preventDefault();
      handleSend();
    }
  });

  el.input.addEventListener('paste', (e) => {
    const files = e.clipboardData && e.clipboardData.files ? e.clipboardData.files : null;
    if (!files || !files.length) return;
    e.preventDefault();
    addFiles(files);
  });

  el.plusBtn.addEventListener('click', () => pickFile(false));
  el.modelBtn.addEventListener('click', openModelMenu);
  el.micBtn.addEventListener('click', toggleMic);
  el.usageBtn.addEventListener('click', openContextPanel);
  el.filesTree.addEventListener('click', (e) => {
    const row = e.target.closest('[role="treeitem"]');
    if (!row) return;
    const key = row.getAttribute('data-key');
    if (row.getAttribute('data-kind') === 'folder') toggleFolder(key);
    else selectFile(key, { focus: true });
  });

  el.filesTree.addEventListener('keydown', (e) => {
    const rows = treeRows();
    if (!rows.length) return;
    const row = e.target.closest('[role="treeitem"]');
    if (!row) return;
    const idx = rows.indexOf(row);
    const key = row.getAttribute('data-key');
    const kind = row.getAttribute('data-kind');
    const expanded = row.getAttribute('aria-expanded') === 'true';
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault();
      const next = rows[Math.max(0, Math.min(rows.length - 1, idx + (e.key === 'ArrowDown' ? 1 : -1)))];
      if (next) { rows.forEach((r) => { r.tabIndex = -1; }); next.tabIndex = 0; next.focus({ preventScroll: true }); }
    } else if (e.key === 'Home' || e.key === 'End') {
      e.preventDefault();
      const next = e.key === 'Home' ? rows[0] : rows[rows.length - 1];
      rows.forEach((r) => { r.tabIndex = -1; });
      next.tabIndex = 0;
      next.focus({ preventScroll: true });
    } else if (e.key === 'ArrowRight') {
      e.preventDefault();
      if (kind === 'folder') {
        if (!expanded) toggleFolder(key);
        else {
          const btn = rows[idx + 1];
          if (btn) { rows.forEach((r) => { r.tabIndex = -1; }); btn.tabIndex = 0; btn.focus({ preventScroll: true }); }
        }
      } else selectFile(key, { focus: true });
    } else if (e.key === 'ArrowLeft') {
      e.preventDefault();
      if (kind === 'folder' && expanded) toggleFolder(key);
      else if (idx > 0) {
        let at = idx - 1;
        const level = Number(row.getAttribute('aria-level')) - 1;
        while (at > 0 && Number(rows[at].getAttribute('aria-level')) >= level + 1) at -= 1;
        const parent = rows[at];
        if (parent) { rows.forEach((r) => { r.tabIndex = -1; }); parent.tabIndex = 0; parent.focus({ preventScroll: true }); }
      }
    } else if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      if (kind === 'folder') toggleFolder(key);
      else selectFile(key, { focus: false });
    }
  });
  el.filesClose.addEventListener('click', closeFilesPanel);
  el.filesTreeBtn.addEventListener('click', toggleFilesTree);
  el.filesExpand.addEventListener('click', () => { if (filesState.current) openViewer(filesState.current, el.filesExpand); });

  el.attachRow.addEventListener('click', (e) => {
    const rm = e.target.closest('[data-rm]');
    if (rm) { removeAttached(Number(rm.getAttribute('data-rm'))); return; }
    const preview = e.target.closest('[data-preview]');
    if (preview) openComposerImage(Number(preview.getAttribute('data-preview')), preview);
  });

  el.fileInput.addEventListener('change', () => {
    addFiles(el.fileInput.files);
    el.fileInput.value = '';
    el.input.focus();
  });

  el.thread.addEventListener('click', (e) => {
    const img = e.target.closest('[data-att-msg]');
    if (img) {
      openMessageImage(img.getAttribute('data-att-msg'), Number(img.getAttribute('data-att-img')) || 0, img);
      return;
    }
    const art = e.target.closest('[data-artifact]');
    if (art) { openFilesPanel(art.getAttribute('data-artifact')); return; }
    const fileRow = e.target.closest('[data-file]');
    if (fileRow) { selectFile(fileRow.getAttribute('data-file')); return; }
    const other = e.target.closest('[data-ask-other]');
    if (other) { openAskCustom(other.getAttribute('data-ask-other')); return; }
    const submit = e.target.closest('[data-ask-submit]');
    if (submit) {
      const card = submit.closest('.ask');
      const picks = Array.prototype.map.call(card.querySelectorAll('.ask-opt.is-picked'), (b) => Number(b.getAttribute('data-opt')));
      if (!picks.length) { toast('先选至少一项'); return; }
      answerQuestion(submit.getAttribute('data-ask-submit'), picks);
      return;
    }
    const ask = e.target.closest('[data-ask]');
    if (ask) {
      const mid = ask.getAttribute('data-ask');
      const card = ask.closest('.ask');
      if (card && card.getAttribute('data-multi') === '1') {
        ask.classList.toggle('is-picked');
        return;
      }
      answerQuestion(mid, [Number(ask.getAttribute('data-opt'))]);
      return;
    }
    const copy = e.target.closest('[data-copy]');
    if (copy) { copyMessage(copy.getAttribute('data-copy')); return; }
    const retry = e.target.closest('[data-retry]');
    if (retry) { retryMessage(retry.getAttribute('data-retry')); return; }
    const wrap = e.target.closest('[data-wrap]');
    if (wrap) {
      const block = wrap.closest('.codeblock');
      if (block) {
        block.classList.toggle('is-wrap');
        wrap.classList.toggle('is-on', block.classList.contains('is-wrap'));
      }
      return;
    }
    const codeBtn = e.target.closest('.code-copy');
    if (codeBtn) {
      const pre = codeBtn.closest('.codeblock').querySelector('pre code');
      copyText(pre ? pre.textContent : '');
      const use = codeBtn.querySelector('use');
      codeBtn.classList.add('is-done');
      codeBtn.setAttribute('aria-label', '已复制');
      if (use) use.setAttribute('href', '#i-check');
      clearTimeout(codeBtn.dataset.timer);
      codeBtn.dataset.timer = setTimeout(() => {
        codeBtn.classList.remove('is-done');
        codeBtn.setAttribute('aria-label', '复制代码');
        if (use) use.setAttribute('href', '#i-copy');
      }, 1400);
    }
  });

  el.thread.addEventListener('keydown', (e) => {
    if (e.target && e.target.tagName === 'INPUT') return;
    if (e.target && e.target.closest && e.target.closest('.effort-track')) return;
    const card = e.target.closest ? e.target.closest('.ask') : null;
    if (!card) return;
    const n = Number(e.key);
    if (!n || n < 1) return;
    const opt = card.querySelector('.ask-opt[data-opt="' + (n - 1) + '"]');
    if (!opt || opt.disabled) return;
    e.preventDefault();
    opt.click();
  });

  el.scroller.addEventListener('scroll', updateScrollBtn, { passive: true });
  el.scrollBtn.addEventListener('click', () => {
    el.scroller.scrollTo({ top: el.scroller.scrollHeight, behavior: state.reduceMotion ? 'auto' : 'smooth' });
    el.input.focus({ preventScroll: true });
  });

  el.importData.addEventListener('click', () => el.importInput.click());
  el.importInput.addEventListener('change', () => {
    importData(el.importInput.files && el.importInput.files[0]);
    el.importInput.value = '';
  });

  el.fullBtn.addEventListener('click', () => {
    if (document.fullscreenElement) {
      document.exitFullscreen().catch(() => {});
    } else if (document.documentElement.requestFullscreen) {
      document.documentElement.requestFullscreen().catch(() => toast('当前浏览器不支持全屏'));
    } else {
      toast('当前浏览器不支持全屏');
    }
  });

  document.addEventListener('fullscreenchange', () => {
    const on = !!document.fullscreenElement;
    el.fullBtn.innerHTML = '<svg class="ico" aria-hidden="true"><use href="#' + (on ? 'i-shrink' : 'i-expand') + '"/></svg>';
    el.fullBtn.setAttribute('aria-label', on ? '退出全屏' : '进入全屏');
    el.fullBtn.title = on ? '退出全屏' : '进入全屏';
  });

  $$('[data-close]').forEach((btn) => {
    btn.addEventListener('click', () => {
      const d = document.getElementById(btn.getAttribute('data-close'));
      if (d) d.close();
    });
  });

  $$('dialog.modal').forEach((d) => {
    d.addEventListener('click', (e) => { if (e.target === d) d.close(); });
  });

  el.wsForm.addEventListener('submit', (e) => {
    e.preventDefault();
    const name = el.wsName.value.trim();
    if (!name) { el.wsName.focus(); return; }
    if (stream) stopStreaming();
    const conv = { id: uid(), title: '新会话', messages: [], updatedAt: now() };
    const ws = { id: uid(), name: name.slice(0, 40), convs: [conv] };
    state.workspaces.unshift(ws);
    state.activeWorkspaceId = ws.id;
    state.activeConvId = conv.id;
    el.wsDialog.close();
    save();
    renderAll();
    focusInput();
    toast('已创建工作区「' + ws.name + '」');
  });

  el.motionSwitch.addEventListener('click', () => {
    state.reduceMotion = !state.reduceMotion;
    applyReduceMotion();
    save();
  });

  el.sideSwitch.addEventListener('click', () => {
    setSidebarCollapsed(!state.sidebarCollapsed);
  });

  el.clearAll.addEventListener('click', clearEverything);
  el.exportData.addEventListener('click', exportData);

  $$('[data-settings-close]').forEach((node) => node.addEventListener('click', closeSettings));
  el.settingsTabs.forEach((tab) => {
    tab.addEventListener('click', () => openSettingsTab(tab.getAttribute('data-settings-tab'), false));
    tab.addEventListener('keydown', (e) => {
      if (e.key !== 'ArrowDown' && e.key !== 'ArrowUp') return;
      e.preventDefault();
      const idx = el.settingsTabs.indexOf(tab);
      const dir = e.key === 'ArrowDown' ? 1 : -1;
      const next = el.settingsTabs[(idx + dir + el.settingsTabs.length) % el.settingsTabs.length];
      openSettingsTab(next.getAttribute('data-settings-tab'), true);
    });
  });

  $$('button', el.fontSeg).forEach((b) => {
    b.addEventListener('click', () => {
      state.fontSize = b.getAttribute('data-font');
      applyFontSize();
      save();
      syncSettingsUI();
    });
  });

  el.thinkSwitch.addEventListener('click', () => {
    state.thinkOpen = !state.thinkOpen;
    save();
    syncSettingsUI();
    renderThread();
  });

  $$('[data-lb-close]', el.lightbox).forEach((n) => n.addEventListener('click', closeLightbox));

  $$('[data-img-close]', el.imgbox).forEach((n) => n.addEventListener('click', closeImgbox));
  el.imgboxPrev.addEventListener('click', () => stepImgbox(-1));
  el.imgboxNext.addEventListener('click', () => stepImgbox(1));

  function hasFileDrag(e) {
    return !!e.dataTransfer && Array.prototype.indexOf.call(e.dataTransfer.types || [], 'Files') >= 0;
  }
  function hideDropzone() {
    dragDepth = 0;
    el.dropzone.hidden = true;
  }
  function overlayOpen() {
    return !el.settingsLayer.hidden || !el.lightbox.hidden || !el.imgbox.hidden;
  }
  window.addEventListener('dragenter', (e) => {
    if (!hasFileDrag(e) || overlayOpen()) return;
    e.preventDefault();
    dragDepth += 1;
    el.dropzone.hidden = false;
  });
  window.addEventListener('dragover', (e) => {
    if (!hasFileDrag(e) || overlayOpen()) return;
    e.preventDefault();
    if (e.dataTransfer) e.dataTransfer.dropEffect = 'copy';
    if (el.dropzone.hidden) el.dropzone.hidden = false;
  });
  window.addEventListener('dragleave', (e) => {
    if (!hasFileDrag(e)) return;
    dragDepth = Math.max(0, dragDepth - 1);
    if (!dragDepth) hideDropzone();
  });
  window.addEventListener('drop', (e) => {
    if (!hasFileDrag(e)) return;
    e.preventDefault();
    hideDropzone();
    const files = e.dataTransfer && e.dataTransfer.files;
    if (files && files.length) addFiles(files);
  });
  window.addEventListener('dragend', hideDropzone);

  document.addEventListener('keydown', (e) => {
    const mod = e.metaKey || e.ctrlKey;
    const typing = e.target && /^(INPUT|TEXTAREA)$/.test(e.target.tagName);
    const overlay = !el.settingsLayer.hidden || !el.lightbox.hidden || !el.imgbox.hidden;

    if (mod && !e.shiftKey && !e.altKey && String(e.key).toLowerCase() === 'k') {
      e.preventDefault();
      if (el.app.classList.contains('drawer-open') || mqMobile.matches) el.app.classList.add('drawer-open');
      setSearchOpen(true);
      return;
    }
    if (e.altKey && !mod && String(e.key).toLowerCase() === 'n') {
      e.preventDefault();
      if (!overlay) newChat();
      return;
    }
    if (mod && e.key === 'Enter') {
      if (overlay || (typing && e.target !== el.input)) return;
      e.preventDefault();
      handleSend();
      return;
    }
    if (e.key === 'Escape') {
      if (!el.imgbox.hidden) { closeImgbox(); return; }
      if (!el.lightbox.hidden) { closeLightbox(); return; }
      if (!el.settingsLayer.hidden) { closeSettings(); return; }
      if (contextNode) { closeContextPanel(); return; }
      if (el.filesPanel.classList.contains('is-open')) { closeFilesPanel(); return; }
      if (menu) { closeMenu(true); return; }
      if (searchOpen) { setSearchOpen(false); el.searchBtn.focus({ preventScroll: true }); return; }
      if (el.app.classList.contains('drawer-open')) { closeDrawer(); return; }
      return;
    }
    if (e.key === 'Tab') {
      if (!el.settingsLayer.hidden) { trapTab(el.settingsWindow, e); return; }
      if (!el.lightbox.hidden) { trapTab(el.lightbox.querySelector('.lb-panel'), e); return; }
      if (!el.imgbox.hidden) { trapTab(el.imgbox.querySelector('.imgbox-panel'), e); return; }
    }
    if (!el.imgbox.hidden && (e.key === 'ArrowLeft' || e.key === 'ArrowRight')) {
      e.preventDefault();
      stepImgbox(e.key === 'ArrowLeft' ? -1 : 1);
    }
  });

  if (mqMobile.addEventListener) {
    mqMobile.addEventListener('change', (e) => {
      if (!e.matches) el.app.classList.remove('drawer-open');
    });
  }

  /* ---------------- boot ---------------- */

  el.app.setAttribute('data-sidebar', state.sidebarCollapsed ? 'collapsed' : 'open');
  applyReduceMotion();
  applyFontSize();
  syncSettingsUI();
  updateContextChip();
  initFilesResize();
  G.clawd.init({ el: el.clawd, reduced: () => state.reduceMotion });
  G.clawd.idle();
  renderAll();
  refreshUsage();
  renderAttachments();
  setupDetectedApi();
  restoreDraft();
  autoGrow();
  updateSendState();
  updateScrollBtn();
  if (!mqMobile.matches) el.input.focus({ preventScroll: true });

})();

