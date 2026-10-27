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

  function moveConvToTop(conv) {
    const found = app.findConv(conv.id);
    if (!found) return;
    const list = found.ws.convs;
    const idx = list.indexOf(conv);
    if (idx > 0) { list.splice(idx, 1); list.unshift(conv); }
  }

  function handleSend() {
    if (app.stream) { app.stopStreaming(); return; }
    const text = el.input.value.trim();
    if (!text) return;

    let conv = app.activeConv();
    if (!conv) {
      const ws = app.activeWs();
      conv = { id: uid(), title: '新会话', messages: [], updatedAt: now() };
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
      conv = { id: uid(), title: '新会话', messages: [], updatedAt: now() };
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
      conv = { id: uid(), title: '新会话', messages: [], updatedAt: now() };
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

  function deleteConv(id) {
    if (app.stream && app.stream.convId === id) app.stopStreaming();
    const found = app.findConv(id);
    if (!found) return;
    const ws = found.ws;
    const index = ws.convs.findIndex((c) => c.id === id);
    const removed = ws.convs.splice(index, 1)[0];
    if (!removed) return;

    if (app.state.activeConvId === id) {
      const next = ws.convs[Math.max(0, index - 1)] || ws.convs[0];
      if (next) {
        app.state.activeConvId = next.id;
      } else {
        const fresh = { id: uid(), title: '新会话', messages: [], updatedAt: now() };
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
    mirrorSession, moveConvToTop, handleSend, respond, startApiStream,
    liveMsg, scheduleStreamPaint, paintStream, stopStreaming, finishStream,
    focusInput, closeDrawer, setSidebarCollapsed, saveDraft, restoreDraft,
    selectConv, selectWorkspace, newChat, deleteConv, retryMessage
  });
})();
