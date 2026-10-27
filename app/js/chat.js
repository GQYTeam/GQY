/* 顾清影 · 对话 service：发送、流式管线、会话切换与镜像 */
(() => {
  'use strict';
  const G = window.GQY;
  const app = G.app;
  const { uid, now, esc, titleFrom } = G;
  const el = app.el;

  /* 服务端可用时把当前会话镜像到 $GQY_HOME/sessions/<id>/（防抖在 api 层） */
  function mirrorSession() {
    if (!G.api || !G.api.available()) return;
    const ws = app.activeWs();
    const conv = app.activeConv();
    if (!ws || !conv) return;
    G.api.syncSession(conv.id, {
      id: conv.id,
      title: conv.title || '',
      workspace: ws.name,
      updatedAt: conv.updatedAt || now(),
      messages: conv.messages.map((m) => ({
        id: m.id, role: m.role, ts: m.ts, text: m.text,
        thinking: m.thinking, tools: m.tools, artifact: m.artifact, usage: m.usage
      }))
    });
  }

  /* 置顶会话只在置顶组内提前；未置顶会话不会跑到置顶会话前面。 */
  function moveConvToTop(conv) {
    const found = app.findConv(conv.id);
    if (!found) return;
    const list = found.ws.convs;
    const idx = list.indexOf(conv);
    if (idx < 0) return;
    list.splice(idx, 1);
    if (conv.pinned) {
      list.unshift(conv);
      return;
    }
    let at = 0;
    while (at < list.length && list[at] && list[at].pinned) at += 1;
    list.splice(at, 0, conv);
  }

  /* 标题变化只刷新侧栏与顶栏，避免流式期间重建消息区。 */
  function refreshConvChrome(conv) {
    app.renderSidebar();
    if (!conv || conv.id !== app.state.activeConvId) return;
    const empty = !conv.messages || conv.messages.length === 0;
    el.topbarTitle.hidden = empty || app.view !== 'chat';
    el.topbarTitle.textContent = (empty || app.view !== 'chat') ? '' : (conv.title || '新会话');
  }

  function handleSend() {
    if (app.stream) { app.stopStreaming(); return; }
    const text = el.input.value.trim();
    if (!text) return;

    let conv = app.activeConv();
    if (!conv) {
      const ws = app.activeWs();
      conv = { id: uid(), title: '新会话', pinned: false, messages: [], updatedAt: now() };
      ws.convs.unshift(conv);
      app.state.activeConvId = conv.id;
    }

    const sending = app.attached.slice();
    const attachments = sending.map((f) => ({
      name: f.name,
      size: f.size || 0,
      type: f.type || '',
      url: app.urlForFile(f)
    }));
    app.attached = [];
    app.attachUrls.clear();
    app.renderAttachments();
    app.updateSendState();

    conv.messages.push({ id: uid(), role: 'user', text: text, ts: now(), attachments: attachments });
    if (!conv.title || conv.title === '新会话') conv.title = titleFrom(text);
    conv.updatedAt = now();
    moveConvToTop(conv);

    el.input.value = '';
    delete app.state.drafts[conv.id];
    app.autoGrow();
    app.save();
    app.renderAll();
    app.scrollToBottom(true);
    respond(conv, text, sending.map((f) => f.name));
  }

  function respond(conv, prompt, files) {
    if (app.stream) app.stopStreaming();
    const msg = { id: uid(), role: 'assistant', text: '', ts: now() };
    conv.messages.push(msg);
    conv.updatedAt = now();
    moveConvToTop(conv);

    app.renderThread();
    app.renderSidebar();
    app.renderAttachments();
    app.updateComposer();
    app.scrollToBottom(true);

    if (!G.api || !G.api.available()) {
      msg.error = { message: '未连接服务端，请先启动后端（npm start）', code: 'no_provider' };
      app.finishStream();
      return;
    }
    if (files && files.length) {
      app.toast('附件暂未接入上传通道，这条回复不含文件内容');
    }
    startApiStream(conv, msg);
  }

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
    app.stream = {
      mode: 'api', convId: conv.id, msgId: msg.id, demo: false,
      thinking: '', thinkingShown: 0, tools: [], toolIndex: 0,
      full: '', shown: 0, stage: 'thinking', started: now(), timer: 0,
      controller: controller, usage: null, error: null, question: null
    };
    app.updateSendState();
    G.clawd.set('thinking');
    paintStream();

    G.api.chat({
      messages: apiMessages(conv),
      model: app.activeModel(),
      effort: app.state.effort,
      signal: controller.signal,
      onEvent: (event) => {
        if (!app.stream || app.stream.controller !== controller) return;
        const s = app.stream;
        if (event.type === 'thinking') {
          s.thinking += event.delta || '';
          s.thinkingShown = s.thinking.length;
        } else if (event.type === 'text') {
          if (s.stage !== 'text') { s.stage = 'text'; s.thinkingShown = s.thinking.length; G.clawd.set('typing'); }
          s.full += event.delta || '';
          s.shown = s.full.length;
        } else if (event.type === 'tool') {
          upsertTool(s.tools, event.tool);
        } else if (event.type === 'question') {
          s.question = event.question || null;
        } else if (event.type === 'done') {
          s.usage = event.usage || null;
        }
        scheduleStreamPaint();
      }
    }).then(() => {
      if (app.stream && app.stream.controller === controller) app.finishStream();
    }).catch((err) => {
      if (!app.stream || app.stream.controller !== controller) return;
      if (err && err.name === 'AbortError') return;
      app.stream.error = { message: err.message || '请求失败', code: err.code || 'chat_failed' };
      app.finishStream();
    });
  }

  function liveMsg() {
    const s = app.stream;
    return {
      id: s.msgId, role: 'assistant', ts: now(),
      thinking: s.thinking.slice(0, s.thinkingShown),
      thinkMs: Math.max(1, Math.round((now() - s.started) / 1000)),
      tools: s.tools.filter((t) => t.status !== 'idle'),
      toolsTotal: s.tools.length,
      text: s.full.slice(0, s.shown)
    };
  }

  /* 流式重绘节流：约 18fps */
  function scheduleStreamPaint() {
    if (app.paintTimer) return;
    const wait = Math.max(0, 55 - (now() - app.paintLast));
    app.paintTimer = setTimeout(() => {
      app.paintTimer = 0;
      app.paintLast = now();
      paintStream();
      app.scrollToBottom(false);
      app.updateScrollBtn();
    }, wait);
  }

  function cancelStreamPaint() {
    if (app.paintTimer) { clearTimeout(app.paintTimer); app.paintTimer = 0; }
  }

  function paintStream() {
    const stream = app.stream;
    if (!stream) return;
    if (!stream.firstAt) stream.firstAt = now();
    const article = el.thread.querySelector('.msg--ai[data-mid="' + stream.msgId + '"]');
    if (!article) return;
    article.outerHTML = app.messageHTML(liveMsg(), { stage: stream.stage });
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
    if (!app.stream) return;
    clearTimeout(app.stream.timer);
    if (app.stream.controller) { try { app.stream.controller.abort(); } catch (err) {} }
    finishStream();
  }

  function finishStream() {
    if (!app.stream) return;
    const s = app.stream;
    clearTimeout(s.timer);
    cancelStreamPaint();
    app.stream = null;
    const found = app.findConv(s.convId);
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
        // ask_user → question 卡片（复用现有 askHTML）
        if (s.question) {
          msg.question = {
            header: '需要确认',
            text: s.question.text,
            multiSelect: false,
            options: (s.question.options || []).map((o) => ({ label: o, desc: '' })),
            answer: null
          };
        }
        msg.ts = now();
      }
      found.conv.updatedAt = now();
    }
    app.updateContextChip();
    if (s.error) {
      G.clawd.idle();
      app.announce('回复失败：' + s.error.message);
      app.toast(s.error.message || '请求失败');
    } else {
      G.clawd.happy();
      app.announce('回复完成');
    }
    app.save();
    app.renderThread();
    app.renderSidebar();
    app.updateSendState();
    app.updateScrollBtn();
    if (!app.mqMobile.matches && !s.error) el.input.focus({ preventScroll: true });
  }

  /* ---------------- interactions ---------------- */

  function focusInput() {
    if (app.mqMobile.matches) return;
    el.input.focus({ preventScroll: true });
  }

  function closeDrawer() {
    el.app.classList.remove('drawer-open');
  }

  function setSidebarCollapsed(collapsed) {
    app.state.sidebarCollapsed = !!collapsed;
    el.app.setAttribute('data-sidebar', app.state.sidebarCollapsed ? 'collapsed' : 'open');
    el.sideSwitch.setAttribute('aria-checked', String(app.state.sidebarCollapsed));
    app.save();
  }

  function saveDraft() {
    const conv = app.activeConv();
    if (!conv) return;
    const value = el.input.value;
    if (value) app.state.drafts[conv.id] = value;
    else delete app.state.drafts[conv.id];
  }

  function restoreDraft() {
    const conv = app.activeConv();
    const value = conv && app.state.drafts ? app.state.drafts[conv.id] || '' : '';
    el.input.value = value;
    app.autoGrow();
    app.updateSendState();
  }

  function selectConv(id) {
    app.closeFilesPanel();
    if (app.stream) app.stopStreaming();
    const found = app.findConv(id);
    if (!found) return;
    if (app.state.activeConvId !== id) saveDraft();
    app.state.activeWorkspaceId = found.ws.id;
    app.state.activeConvId = id;
    app.save();
    app.setView('chat');
    app.renderAll();
    restoreDraft();
    el.scroller.scrollTop = el.scroller.scrollHeight;
    closeDrawer();
    if (!found.conv.messages.length) focusInput();
  }

  function selectWorkspace(wsId) {
    if (app.stream) app.stopStreaming();
    saveDraft();
    const ws = app.state.workspaces.find((w) => w.id === wsId);
    if (!ws) return;
    let conv = ws.convs[0];
    if (!conv) {
      conv = { id: uid(), title: '新会话', pinned: false, messages: [], updatedAt: now() };
      ws.convs.unshift(conv);
    }
    app.state.activeWorkspaceId = ws.id;
    app.state.activeConvId = conv.id;
    app.save();
    app.setView('chat');
    app.renderAll();
    restoreDraft();
    el.scroller.scrollTop = el.scroller.scrollHeight;
    closeDrawer();
    focusInput();
  }

  function newChat() {
    app.closeFilesPanel();
    if (app.stream) app.stopStreaming();
    saveDraft();
    const ws = app.activeWs();
    let conv = app.activeConv();
    if (!conv || conv.messages.length > 0) {
      conv = { id: uid(), title: '新会话', pinned: false, messages: [], updatedAt: now() };
      ws.convs.unshift(conv);
    }
    app.state.activeWorkspaceId = ws.id;
    app.state.activeConvId = conv.id;
    app.save();
    app.setView('chat');
    app.renderAll();
    restoreDraft();
    closeDrawer();
    focusInput();
    el.scroller.scrollTop = 0;
  }

  /* ---------------- 会话管理：重命名 / 置顶 / 多选删除 / 导出 ---------------- */

  function startRename(id) {
    const found = app.findConv(id);
    if (!found) return;
    if (app.manageMode) exitManageMode();
    if (app.closeMenu) app.closeMenu(false);
    app.renamingId = id;
    app.renderSidebar();
  }

  function commitRename(id, raw, cancelled) {
    if (app.renamingId !== id) return;
    const found = app.findConv(id);
    app.renamingId = null;
    if (!found) { app.renderSidebar(); return; }
    if (!cancelled) {
      const title = String(raw == null ? '' : raw).trim().slice(0, 80);
      if (!title) {
        app.toast('标题不能为空，已保留原名');
      } else if (title !== found.conv.title) {
        found.conv.title = title;
        app.save();
        app.announce('已重命名为 ' + title);
      }
    }
    refreshConvChrome(found.conv);
  }

  function cancelRename(id) {
    commitRename(id, '', true);
  }

  function togglePin(id) {
    const found = app.findConv(id);
    if (!found) return;
    found.conv.pinned = !found.conv.pinned;
    moveConvToTop(found.conv);
    if (app.sortConvs) app.sortConvs(found.ws);
    app.save();
    app.renderSidebar();
    app.toast(found.conv.pinned ? '已置顶会话' : '已取消置顶');
    app.announce(found.conv.pinned ? '已置顶会话' : '已取消置顶');
  }

  function pad2(n) {
    return String(n).padStart(2, '0');
  }

  function fmtDateTime(ts) {
    const d = new Date(ts || now());
    return d.getFullYear() + '-' + pad2(d.getMonth() + 1) + '-' + pad2(d.getDate()) +
      ' ' + pad2(d.getHours()) + ':' + pad2(d.getMinutes());
  }

  /* 文件名只保留安全字符，避免路径分隔符与保留字符。 */
  function safeFilename(title, id, ext) {
    const clean = String(title || '')
      .replace(/[\\/:*?"<>|\u0000-\u001f]+/g, '-')
      .replace(/\s+/g, ' ')
      .replace(/^[. ]+|[. ]+$/g, '')
      .slice(0, 40);
    const fallback = 'conv-' + String(id || '').replace(/[^A-Za-z0-9_-]/g, '').slice(0, 16);
    const d = new Date();
    const stamp = d.getFullYear() + pad2(d.getMonth() + 1) + pad2(d.getDate()) + '-' + pad2(d.getHours()) + pad2(d.getMinutes());
    return 'gqy-' + (clean || fallback || 'session') + '-' + stamp + '.' + ext;
  }

  function downloadText(filename, content, type) {
    try {
      const blob = new Blob([content], { type: type });
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = filename;
      a.rel = 'noopener';
      document.body.appendChild(a);
      a.click();
      a.remove();
      setTimeout(() => URL.revokeObjectURL(url), 2000);
      return true;
    } catch (err) {
      return false;
    }
  }

  function convMarkdown(conv, ws) {
    const lines = [];
    lines.push('# ' + (conv.title || '新会话'));
    lines.push('');
    lines.push('- 会话 ID：' + conv.id);
    if (ws) lines.push('- 工作区：' + ws.name);
    lines.push('- 更新时间：' + fmtDateTime(conv.updatedAt));
    lines.push('- 置顶：' + (conv.pinned ? '是' : '否'));
    lines.push('');
    (conv.messages || []).forEach((m) => {
      lines.push('## ' + (m.role === 'user' ? '用户' : '助手') + ' · ' + fmtDateTime(m.ts));
      lines.push('');
      if (m.text) lines.push(String(m.text).trim());
      else if (!m.thinking && !(m.tools && m.tools.length)) lines.push('（空消息）');
      if (m.tools && m.tools.length) {
        lines.push('');
        lines.push('**工具轨迹**');
        m.tools.forEach((t) => {
          if (!t || !t.name) return;
          const secs = t.ms ? ' · ' + (t.ms / 1000).toFixed(1) + 's' : '';
          lines.push('- ' + t.name + (t.detail ? '：' + t.detail : '') + secs + ' · ' + (t.status || 'done'));
          if (t.output) {
            const first = String(t.output).split('\n')[0].trim().slice(0, 200);
            if (first) lines.push('  - 输出：' + first);
          }
        });
      }
      if (m.error) {
        lines.push('');
        lines.push('**错误**：' + (m.error.message || '请求失败'));
      }
      lines.push('');
      lines.push('---');
      lines.push('');
    });
    return lines.join('\n');
  }

  function exportConv(id, format) {
    const found = app.findConv(id);
    if (!found) return;
    const conv = found.conv;
    const ws = found.ws;
    let text;
    let filename;
    let type;
    if (format === 'md') {
      text = convMarkdown(conv, ws);
      filename = safeFilename(conv.title, conv.id, 'md');
      type = 'text/markdown;charset=utf-8';
    } else {
      text = JSON.stringify({
        id: conv.id,
        title: conv.title || '新会话',
        pinned: !!conv.pinned,
        updatedAt: conv.updatedAt || now(),
        messages: conv.messages || [],
        workspace: ws ? ws.name : '',
        exportedAt: now()
      }, app.dropBlobUrls, 2);
      filename = safeFilename(conv.title, conv.id, 'json');
      type = 'application/json;charset=utf-8';
    }
    if (downloadText(filename, text, type)) app.toast('已导出' + (format === 'md' ? ' Markdown' : ' JSON'));
    else app.toast('导出失败');
  }

  function openConvMenu(id, anchor) {
    const found = app.findConv(id);
    if (!found || !app.openMenu || !app.menuItem) return;
    app.openMenu(anchor, (node) => {
      node.appendChild(app.menuItem('重命名', {
        icon: 'i-pencil',
        onSelect: () => startRename(id)
      }));
      node.appendChild(app.menuItem(found.conv.pinned ? '取消置顶' : '置顶', {
        icon: 'i-pin',
        onSelect: () => togglePin(id)
      }));
      const sep1 = document.createElement('div');
      sep1.className = 'menu-sep';
      node.appendChild(sep1);
      node.appendChild(app.menuItem('导出 JSON', {
        icon: 'i-file',
        onSelect: () => exportConv(id, 'json')
      }));
      node.appendChild(app.menuItem('导出 Markdown', {
        icon: 'i-book',
        onSelect: () => exportConv(id, 'md')
      }));
      const sep2 = document.createElement('div');
      sep2.className = 'menu-sep';
      node.appendChild(sep2);
      const del = app.menuItem('删除', {
        icon: 'i-trash',
        onSelect: () => app.deleteConv(id)
      });
      del.classList.add('is-danger');
      node.appendChild(del);
    }, { align: 'right', place: 'auto' });
  }

  function refreshManageBar() {
    if (!el.manageBar) return;
    el.manageBar.hidden = !app.manageMode;
    if (el.manageBtn) el.manageBtn.setAttribute('aria-pressed', String(!!app.manageMode));
    if (!app.manageMode) return;
    const alive = new Set();
    app.state.workspaces.forEach((ws) => ws.convs.forEach((c) => alive.add(c.id)));
    Array.from(app.manageSelected).forEach((id) => { if (!alive.has(id)) app.manageSelected.delete(id); });
    const count = app.manageSelected.size;
    if (el.manageCount) el.manageCount.textContent = '已选 ' + count;
    if (el.manageDelete) el.manageDelete.disabled = count === 0;
  }

  function enterManageMode() {
    if (app.manageMode) return;
    app.renamingId = null;
    app.manageMode = true;
    app.manageSelected.clear();
    if (app.searchOpen) {
      app.searchOpen = false;
      if (el.searchWrap) el.searchWrap.hidden = true;
      if (el.searchBtn) el.searchBtn.setAttribute('aria-expanded', 'false');
      if (el.searchInput) el.searchInput.value = '';
      app.searchQuery = '';
    }
    if (app.closeMenu) app.closeMenu(false);
    app.renderSidebar();
    refreshManageBar();
    app.announce('已进入会话管理模式');
  }

  function exitManageMode() {
    if (!app.manageMode) return;
    app.manageMode = false;
    app.manageSelected.clear();
    app.renderSidebar();
    refreshManageBar();
    app.announce('已退出会话管理模式');
  }

  function toggleManageMode() {
    if (app.manageMode) exitManageMode();
    else enterManageMode();
  }

  function setManageChecked(id, checked) {
    if (!app.manageMode) return;
    if (checked) app.manageSelected.add(id);
    else app.manageSelected.delete(id);
    refreshManageBar();
  }

  function selectAllConvs() {
    if (!app.manageMode) return;
    app.manageSelected.clear();
    app.state.workspaces.forEach((ws) => ws.convs.forEach((c) => app.manageSelected.add(c.id)));
    el.wsList.querySelectorAll('.conv-check').forEach((cb) => { cb.checked = true; });
    refreshManageBar();
  }

  function clearManageSelection() {
    if (!app.manageMode) return;
    app.manageSelected.clear();
    el.wsList.querySelectorAll('.conv-check').forEach((cb) => { cb.checked = false; });
    refreshManageBar();
  }

  function firstRemainingConv() {
    let ws = app.state.workspaces.find((w) => w.id === app.state.activeWorkspaceId) || app.state.workspaces[0];
    let next = ws && ws.convs[0];
    if (!next) {
      const other = app.state.workspaces.find((w) => w.convs.length);
      if (other) { ws = other; next = other.convs[0]; }
    }
    if (!next) {
      next = { id: uid(), title: '新会话', pinned: false, messages: [], updatedAt: now() };
      if (!ws) {
        ws = { id: uid(), name: 'gqy', convs: [next] };
        app.state.workspaces.push(ws);
      } else {
        ws.convs.unshift(next);
      }
    }
    return { ws: ws, conv: next };
  }

  function deleteSelectedConvs() {
    if (!app.manageMode) return;
    const alive = new Set();
    app.state.workspaces.forEach((ws) => ws.convs.forEach((c) => alive.add(c.id)));
    const ids = Array.from(app.manageSelected).filter((id) => alive.has(id));
    if (!ids.length) { app.toast('先选择要删除的会话'); refreshManageBar(); return; }
    if (app.stream && ids.indexOf(app.stream.convId) >= 0) {
      app.toast('正在流式回复的会话不能删除，请先停止生成');
      return;
    }
    if (!window.confirm('确定删除选中的 ' + ids.length + ' 个会话？删除后不可撤销。')) return;

    const selected = new Set(ids);
    const activeDeleted = selected.has(app.state.activeConvId);
    let removed = 0;
    app.state.workspaces.forEach((ws) => {
      ws.convs = ws.convs.filter((c) => {
        if (!selected.has(c.id)) return true;
        delete app.state.drafts[c.id];
        removed += 1;
        return false;
      });
    });

    if (activeDeleted) {
      const next = firstRemainingConv();
      app.state.activeWorkspaceId = next.ws.id;
      app.state.activeConvId = next.conv.id;
    }

    exitManageMode();
    app.save();
    app.renderAll();
    restoreDraft();
    app.toast('已删除 ' + removed + ' 个会话');
    app.announce('已删除 ' + removed + ' 个会话');
  }

  function deleteConv(id) {
    if (app.stream && app.stream.convId === id) app.stopStreaming();
    const found = app.findConv(id);
    if (!found) return;
    const ws = found.ws;
    const index = ws.convs.findIndex((c) => c.id === id);
    const removed = ws.convs.splice(index, 1)[0];
    if (!removed) return;
    const draft = app.state.drafts[id];
    delete app.state.drafts[id];

    if (app.state.activeConvId === id) {
      const next = ws.convs[Math.max(0, index - 1)] || ws.convs[0];
      if (next) {
        app.state.activeConvId = next.id;
      } else {
        const fresh = { id: uid(), title: '新会话', pinned: false, messages: [], updatedAt: now() };
        ws.convs.unshift(fresh);
        app.state.activeConvId = fresh.id;
      }
      app.state.activeWorkspaceId = ws.id;
    }
    app.save();
    app.renderAll();
    restoreDraft();
    app.toast('已删除对话', '撤销', () => {
      const target = app.state.workspaces.find((w) => w.id === ws.id);
      if (!target) return;
      target.convs.splice(Math.min(index, target.convs.length), 0, removed);
      if (app.sortConvs) app.sortConvs(target);
      if (typeof draft === 'string') app.state.drafts[removed.id] = draft;
      app.state.activeWorkspaceId = target.id;
      app.state.activeConvId = removed.id;
      app.save();
      app.renderAll();
      app.toast('已恢复对话');
    });
  }

  function retryMessage(mid) {
    if (app.stream) app.stopStreaming();
    const conv = app.activeConv();
    if (!conv) return;
    const idx = conv.messages.findIndex((m) => m.id === mid);
    if (idx < 1) return;
    let u = idx - 1;
    while (u >= 0 && conv.messages[u].role !== 'user') u--;
    if (u < 0) return;
    const prompt = conv.messages[u].text;
    conv.messages = conv.messages.slice(0, u + 1);
    conv.updatedAt = now();
    app.save();
    respond(conv, prompt, []);
  }

  Object.assign(app, {
    mirrorSession, moveConvToTop, refreshConvChrome, handleSend, respond, startApiStream,
    liveMsg, scheduleStreamPaint, paintStream, stopStreaming, finishStream,
    focusInput, closeDrawer, setSidebarCollapsed, saveDraft, restoreDraft,
    selectConv, selectWorkspace, newChat, deleteConv, retryMessage,
    startRename, commitRename, cancelRename, togglePin, openConvMenu,
    exportConv, convMarkdown, safeFilename,
    refreshManageBar, enterManageMode, exitManageMode, toggleManageMode,
    setManageChecked, selectAllConvs, clearManageSelection, deleteSelectedConvs
  });
})();
