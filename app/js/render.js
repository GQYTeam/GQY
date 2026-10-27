/* 顾清影 · 渲染层：消息 HTML、侧栏、线程、上下文用量浮层 */
(() => {
  'use strict';
  const G = window.GQY;
  const app = G.app;
  const { uid, now, esc, timeAgo, stamp, estimateTokens, fmtTok, formatBytes, formatCount, renderMarkdown } = G;
  const el = app.el;

  /* ---------------- 上下文用量 ---------------- */

  function currentModelMeta() {
    if (!app.modelCatalog.length) return null;
    return app.modelCatalog.find((m) => m.id === app.activeModel()) || null;
  }

  function contextSnapshot() {
    const meta = currentModelMeta();
    const total = meta && meta.context ? meta.context : 128000;
    // 真实上下文占用 = 当前会话最近一次请求的 prompt_tokens（含完整历史），
    // 不是服务端累计值。取最后一条带 usage 的 assistant 消息。
    let used = 0;
    const conv = app.activeConv();
    if (conv) {
      for (let i = conv.messages.length - 1; i >= 0; i--) {
        const m = conv.messages[i];
        if (m.usage && m.usage.prompt_tokens != null) {
          used = m.usage.prompt_tokens;
          break;
        }
      }
      if (!used) used = estimateTokens(conv.messages.map((m) => m.text || '').join(''));
    }
    const pct = Math.max(0, Math.min(100, Math.round((used / total) * 100)));
    return { used: used, total: total, pct: pct, model: meta };
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
    el.usageBtn.title = '上下文已用 ' + snap.pct + '% · ' + fmtTok(snap.used) + ' / ' + fmtTok(snap.total);
  }

  function contextPanelHTML() {
    const snap = contextSnapshot();
    const meta = snap.model;
    const modes = meta && meta.inputModes && meta.inputModes.length ? meta.inputModes.join(' · ') : '未知';
    const cap = meta
      ? (meta.reasoning ? '推理' : '非推理') + ' · ' + (meta.toolCall ? '工具调用' : '无工具') + ' · ' + (meta.attachment ? '支持附件' : '仅文本')
      : '（models.dev 无该模型元数据）';
    return '<div class="usage-head"><span>上下文已用</span><b>' + snap.pct + '%</b>' +
      '<span>' + fmtTok(snap.used) + ' / ' + fmtTok(snap.total) + '</span></div>' +
      '<div class="ctx-bar"><i style="width:' + snap.pct + '%;background:#7e9a83"></i></div>' +
      '<div class="usage-sec"><h4>当前模型</h4><div class="usage-grid">' +
      '<div><small>上下文窗口</small><b>' + fmtTok(snap.total) + ' tok</b></div>' +
      '<div><small>输入模态</small><b>' + esc(modes) + '</b></div>' +
      '<div><small>能力</small><b>' + esc(cap) + '</b></div>' +
      '</div></div>' +
      '<div class="usage-sec"><h4>Token 用量</h4><div class="usage-grid">' +
      '<div><small>输入</small><b>' + fmtTok(app.usageData && app.usageData.promptTokens || 0) + ' tok</b></div>' +
      '<div><small>输出</small><b>' + fmtTok(app.usageData && app.usageData.completionTokens || 0) + ' tok</b></div>' +
      '<div><small>合计</small><b>' + fmtTok(app.usageData && app.usageData.totalTokens || 0) + ' tok</b></div>' +
      '</div></div>' +
      '<p class="usage-foot">上下文窗口来自 models.dev；token 用量来自服务端聚合</p>';
  }

  function closeContextPanel() {
    if (!app.contextNode) return;
    app.contextNode.remove();
    app.contextNode = null;
    el.usageBtn.setAttribute('aria-expanded', 'false');
    document.removeEventListener('pointerdown', onContextPointer, true);
  }

  function onContextPointer(e) {
    if (!app.contextNode) return;
    if (app.contextNode.contains(e.target) || el.usageBtn.contains(e.target)) return;
    closeContextPanel();
  }

  function openContextPanel() {
    if (app.contextNode) { closeContextPanel(); return; }
    app.closeMenu(false);
    const node = document.createElement('div');
    node.className = 'usage-pop';
    node.setAttribute('role', 'dialog');
    node.setAttribute('aria-label', '上下文用量');
    node.innerHTML = contextPanelHTML();
    document.body.appendChild(node);
    app.contextNode = node;
    el.usageBtn.setAttribute('aria-expanded', 'true');
    app.positionMenu(node, el.usageBtn, 'right', 'top');
    document.addEventListener('pointerdown', onContextPointer, true);
  }

  /* ---------------- 消息 HTML ---------------- */

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
      '<svg class="ico step-chev" aria-hidden="true"><use href="#i-chevron"/></svg>' +
      '</summary>' +
      '<div class="step-body">' + argsHTML(t, running) + '</div>' +
      '</details></div>';
  }

  function toolsHTML(tools, total) {
    const list = (tools || []).filter((t) => t && t.name);
    const running = list.some((t) => t.status === 'running');
    return '<div class="trace">' +
      '<div class="trace-head"><span>' + (running ? '正在执行' : '已执行') + ' ' + list.length + (total > list.length ? ' / ' + total : '') + ' 步</span>' +
      '<svg class="ico trace-chev" aria-hidden="true"><use href="#i-chevron"/></svg></div>' +
      '<div class="trace-body">' + list.map(stepHTML).join('') + '</div></div>';
  }

  function errorHTML(m) {
    return '<div class="msg-error"><span class="err-ico" aria-hidden="true">!</span>' +
      '<span class="err-copy"><b>' + esc((m.error && m.error.message) || '请求失败') + '</b>' +
      '<small>' + esc((m.error && m.error.code) || 'chat_failed') + '</small></span></div>';
  }

  function artifactHTML(m) {
    const a = m.artifact;
    const files = Array.isArray(a.files) ? a.files : [];
    const first = files[0] || null;
    const content = first ? first.content : (a.content || '');
    const preview = content.split('\n').slice(0, 4).join('\n');
    const meta = files.length
      ? files.length + ' 个文件 · ' + files.slice(0, 3).map((f) => esc(f.path)).join(' · ')
      : (esc(a.lang || 'code') + ' · ' + a.lines + ' 行 · 生成的产物');
    return '<button class="artifact" type="button" data-artifact="' + m.id + '" aria-label="打开产物 ' + esc(a.title) + '">' +
      '<span class="art-thumb" aria-hidden="true"><code>' + esc(preview) + '</code></span>' +
      '<span class="art-body">' +
      '<span class="art-title">' + esc(a.title) + '</span>' +
      '<span class="art-meta">' + meta + '</span>' +
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
    const mdHTML = m.text ? renderMarkdown(m.text) : '';
    body += '<div class="md">' + (m.text ? (G.glossary ? G.glossary.decorate(mdHTML) : mdHTML) : (empty && !live ? thinkingHTML() : '')) + '</div>';
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

  /* ---------------- 侧栏 / 线程 ---------------- */

  function pinMark(c) {
    if (c.pinned) {
      return '<svg class="ico conv-pin" aria-hidden="true"><use href="#i-pin"/></svg>' +
        '<span class="sr-only">已置顶</span>';
    }
    return '<span class="conv-dot" aria-hidden="true"></span>';
  }

  function renderSidebar() {
    const q = app.searchQuery.trim().toLowerCase();
    let html = '';
    let shown = 0;

    app.state.workspaces.forEach((ws) => {
      const nameMatch = !!q && ws.name.toLowerCase().indexOf(q) >= 0;
      const convs = ws.convs.filter((c) => !q || nameMatch || String(c.title || '').toLowerCase().indexOf(q) >= 0);
      if (q && !nameMatch && convs.length === 0) return;
      shown++;

      html += '<section class="ws-group">';
      html += '<button type="button" class="ws-name" data-ws="' + esc(ws.id) + '">' + esc(ws.name) + '</button>';
      convs.forEach((c) => {
        const active = c.id === app.state.activeConvId;
        const title = c.title || '新会话';
        html += '<div class="conv-wrap">';
        if (app.manageMode) {
          html += '<label class="conv-row conv-row--manage' + (active ? ' active' : '') + (c.pinned ? ' is-pinned' : '') + '">';
          html += '<input type="checkbox" class="conv-check" data-check="' + esc(c.id) + '"' +
            (app.manageSelected.has(c.id) ? ' checked' : '') +
            ' aria-label="选择会话 ' + esc(title) + '">';
          if (c.pinned) html += '<svg class="ico conv-pin" aria-hidden="true"><use href="#i-pin"/></svg>';
          html += '<span class="conv-title">' + esc(title) + '</span>';
          html += '<span class="conv-time">' + esc(timeAgo(c.updatedAt || now())) + '</span>';
          html += '</label>';
        } else if (app.renamingId === c.id) {
          html += '<div class="conv-row conv-row--edit">';
          html += '<span class="conv-dot" aria-hidden="true"></span>';
          html += '<input class="conv-rename" type="text" maxlength="80" value="' + esc(title) + '"' +
            ' aria-label="重命名会话" data-rename-input="' + esc(c.id) + '" autocomplete="off" spellcheck="false">';
          html += '</div>';
        } else {
          html += '<button type="button" class="conv-row' + (active ? ' active' : '') + (c.pinned ? ' is-pinned' : '') + '" data-conv="' + esc(c.id) + '"' + (active ? ' aria-current="true"' : '') + '>';
          html += pinMark(c);
          html += '<span class="conv-title">' + esc(title) + '</span>';
          html += '<span class="conv-time">' + esc(timeAgo(c.updatedAt || now())) + '</span>';
          html += '</button>';
          html += '<button type="button" class="conv-more" data-more="' + esc(c.id) + '" title="会话操作" aria-label="会话操作 ' + esc(title) + '" aria-haspopup="menu" aria-expanded="false">';
          html += '<svg class="ico" aria-hidden="true"><use href="#i-more"/></svg></button>';
          html += '<button type="button" class="conv-del" data-del="' + esc(c.id) + '" title="删除对话" aria-label="删除对话 ' + esc(title) + '">';
          html += '<svg class="ico" aria-hidden="true"><use href="#i-trash"/></svg></button>';
        }
        html += '</div>';
      });
      html += '</section>';
    });

    if (q && shown === 0) html = '<div id="noResults">没有找到匹配的对话</div>';
    el.wsList.innerHTML = html;

    if (app.renamingId) {
      const input = el.wsList.querySelector('.conv-rename');
      if (input) { input.focus({ preventScroll: true }); input.select(); }
    }
    if (app.manageMode && app.refreshManageBar) app.refreshManageBar();
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

  function renderThread() {
    const conv = app.activeConv();
    const empty = !conv || !conv.messages || conv.messages.length === 0;
    const nearBottom = el.scroller.scrollHeight - el.scroller.scrollTop - el.scroller.clientHeight < 240;
    el.body.classList.toggle('is-empty', empty);
    el.thread.hidden = empty;
    const inChat = app.view === 'chat';
    el.topbarTitle.hidden = empty || !inChat;
    el.topbarTitle.textContent = (empty || !inChat) ? '' : (conv.title || '新会话');
    if (empty) {
      el.thread.innerHTML = '';
      el.scroller.scrollTop = 0;
      updateScrollBtn();
      return;
    }
    const total = conv.messages.length;
    const start = Math.max(0, total - (app.renderLimit || 80));
    const earlier = start > 0
      ? '<button class="load-earlier" type="button" data-load-earlier="1">加载更早的 ' + start + ' 条消息</button>'
      : '';
    el.thread.innerHTML = earlier + conv.messages.slice(start).map(messageHTML).join('');
    requestAnimationFrame(() => {
      if (nearBottom) el.scroller.scrollTop = el.scroller.scrollHeight;
      updateScrollBtn();
    });
  }

  function activeModel() {
    if (app.state.model) return app.state.model;
    const info = G.api && G.api.info ? G.api.info() : null;
    return (info && info.model) || '默认模型';
  }

  function updateComposer() {
    el.modelName.textContent = app.activeModel();
    el.modelEffort.textContent = app.state.effort;
    el.motionSwitch.setAttribute('aria-checked', String(app.state.reduceMotion));
    el.sideSwitch.setAttribute('aria-checked', String(app.state.sidebarCollapsed));
  }

  function renderAll() {
    renderSidebar();
    renderThread();
    updateComposer();
    app.updateSendState();
    app.updateContextChip();
  }

  function updateSendState() {
    const busy = !!app.stream;
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
    const min = app.mqMobile.matches ? 44 : 50;
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

  Object.assign(app, {
    currentModelMeta, contextSnapshot, contextPie, updateContextChip,
    contextPanelHTML, closeContextPanel, openContextPanel,
    messageHTML, renderSidebar, announce, updateScrollBtn,
    renderThread, activeModel, updateComposer, renderAll,
    updateSendState, autoGrow, scrollToBottom
  });
})();
