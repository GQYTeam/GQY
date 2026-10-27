/* 顾清影 · 应用壳：事件接线 + 启动
 *
 * 状态、DOM、渲染、各服务拆到 js/store.js + js/{render,chat,files,menu,settings}.js，
 * 本文件只做事件绑定与 boot，保持短小易维护。
 */
(() => {
  'use strict';
  const G = window.GQY;
  const app = G.app;
  const { uid, now } = G;
  const el = app.el;
  const $$ = app.$$;

  /* ---------------- 事件接线 ---------------- */

  el.newChatBtn.addEventListener('click', app.newChat);
  el.collapseBtn.addEventListener('click', () => {
    if (app.mqMobile.matches) { app.closeDrawer(); return; }
    app.setSidebarCollapsed(true);
    el.openSideBtn.focus({ preventScroll: true });
  });
  el.openSideBtn.addEventListener('click', () => {
    if (app.mqMobile.matches) el.app.classList.add('drawer-open');
    else app.setSidebarCollapsed(false);
  });
  el.scrim.addEventListener('click', app.closeDrawer);
  el.accountBtn.addEventListener('click', app.openSettings);
  if (el.boardBtn) el.boardBtn.addEventListener('click', () => app.setView('board'));
  if (el.reopenOnboarding) el.reopenOnboarding.addEventListener('click', () => { app.closeSettings(); app.onboarding.open(); });
  if (el.settingsSearch) el.settingsSearch.addEventListener('input', () => app.filterSettings(el.settingsSearch.value));

  function setSearchOpen(open, focus) {
    app.searchOpen = !!open;
    el.searchWrap.hidden = !app.searchOpen;
    el.searchBtn.setAttribute('aria-expanded', String(app.searchOpen));
    if (app.searchOpen) {
      if (focus !== false) {
        el.searchInput.focus({ preventScroll: true });
        el.searchInput.select();
      }
    } else {
      el.searchInput.value = '';
      app.searchQuery = '';
      app.renderSidebar();
    }
  }

  el.searchBtn.addEventListener('click', () => setSearchOpen(!app.searchOpen));

  el.searchInput.addEventListener('input', () => {
    app.searchQuery = el.searchInput.value;
    app.renderSidebar();
  });

  el.searchInput.addEventListener('keydown', (e) => {
    if (e.key === 'Escape') {
      e.stopPropagation();
      el.searchBtn.click();
    }
  });

  el.wsList.addEventListener('click', (e) => {
    const more = e.target.closest('[data-more]');
    if (more) { e.stopPropagation(); app.openConvMenu(more.getAttribute('data-more'), more); return; }
    const del = e.target.closest('[data-del]');
    if (del) { app.deleteConv(del.getAttribute('data-del')); return; }
    if (e.target.closest('.conv-check, .conv-row--manage, .conv-rename')) return;
    const conv = e.target.closest('[data-conv]');
    if (conv) { app.selectConv(conv.getAttribute('data-conv')); return; }
    const ws = e.target.closest('[data-ws]');
    if (ws) { app.selectWorkspace(ws.getAttribute('data-ws')); }
  });

  el.wsList.addEventListener('change', (e) => {
    const check = e.target.closest('.conv-check');
    if (!check) return;
    app.setManageChecked(check.getAttribute('data-check'), check.checked);
  });

  el.wsList.addEventListener('dblclick', (e) => {
    if (app.manageMode || app.renamingId) return;
    const title = e.target.closest('.conv-title');
    const row = title && title.closest('[data-conv]');
    if (row) app.startRename(row.getAttribute('data-conv'));
  });

  el.wsList.addEventListener('contextmenu', (e) => {
    if (app.manageMode) return;
    const row = e.target.closest('[data-conv]');
    if (!row) return;
    e.preventDefault();
    app.openConvMenu(row.getAttribute('data-conv'), row);
  });

  el.wsList.addEventListener('keydown', (e) => {
    const input = e.target.closest('.conv-rename');
    if (!input) return;
    const id = input.getAttribute('data-rename-input');
    if (e.isComposing || e.keyCode === 229) return;
    if (e.key === 'Enter') {
      e.preventDefault();
      e.stopPropagation();
      app.commitRename(id, input.value, false);
    } else if (e.key === 'Escape') {
      e.preventDefault();
      e.stopPropagation();
      app.cancelRename(id);
    }
  });

  el.wsList.addEventListener('focusout', (e) => {
    const input = e.target.closest('.conv-rename');
    if (!input) return;
    const id = input.getAttribute('data-rename-input');
    const value = input.value;
    // 让点击其他会话的 click 先完成，再按失焦保存。
    setTimeout(() => app.commitRename(id, value, false), 0);
  });

  if (el.manageBtn) el.manageBtn.addEventListener('click', app.toggleManageMode);
  if (el.manageAll) el.manageAll.addEventListener('click', app.selectAllConvs);
  if (el.manageNone) el.manageNone.addEventListener('click', app.clearManageSelection);
  if (el.manageDelete) el.manageDelete.addEventListener('click', app.deleteSelectedConvs);
  if (el.manageCancel) el.manageCancel.addEventListener('click', app.exitManageMode);

  el.newWsBtn.addEventListener('click', app.openWsDialog);
  el.form.addEventListener('submit', (e) => {
    e.preventDefault();
    app.handleSend();
  });

  el.input.addEventListener('input', () => {
    G.clawd.wake();
    app.autoGrow();
    app.updateSendState();
    clearTimeout(app.draftTimer);
    app.draftTimer = setTimeout(() => { app.saveDraft(); app.save(); }, 500);
  });

  el.input.addEventListener('focus', () => G.clawd.wake());

  el.input.addEventListener('keydown', (e) => {
    if (e.key === 'Enter' && !e.shiftKey && !e.metaKey && !e.ctrlKey && !e.isComposing && e.keyCode !== 229 && !app.mqMobile.matches) {
      e.preventDefault();
      app.handleSend();
    }
  });

  el.input.addEventListener('paste', (e) => {
    const files = e.clipboardData && e.clipboardData.files ? e.clipboardData.files : null;
    if (!files || !files.length) return;
    e.preventDefault();
    app.addFiles(files);
  });

  el.plusBtn.addEventListener('click', () => app.pickFile(false));
  el.modelBtn.addEventListener('click', app.openModelMenu);
  el.micBtn.addEventListener('click', app.toggleMic);
  el.usageBtn.addEventListener('click', app.openContextPanel);
  if (el.usageTopBtn) el.usageTopBtn.addEventListener('click', app.openUsage);
  if (el.extensionsBtn) el.extensionsBtn.addEventListener('click', app.openExtensions);
  if (el.installExampleBtn) el.installExampleBtn.addEventListener('click', () => {
    if (!G.api || !G.api.installExample) return;
    G.api.installExample().then(() => { app.refreshPlugins(); app.toast('示例插件已安装'); })
      .catch((err) => app.toast(err.message));
  });
  if (el.githubConnectBtn) el.githubConnectBtn.addEventListener('click', app.connectGithub);
  if (el.githubDisconnectBtn) el.githubDisconnectBtn.addEventListener('click', app.disconnectGithub);
  el.filesTree.addEventListener('click', (e) => {
    const row = e.target.closest('[role="treeitem"]');
    if (!row) return;
    const key = row.getAttribute('data-key');
    if (row.getAttribute('data-kind') === 'folder') app.toggleFolder(key);
    else app.selectFile(key, { focus: true });
  });

  el.filesTree.addEventListener('keydown', (e) => {
    const rows = app.treeRows();
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
        if (!expanded) app.toggleFolder(key);
        else {
          const btn = rows[idx + 1];
          if (btn) { rows.forEach((r) => { r.tabIndex = -1; }); btn.tabIndex = 0; btn.focus({ preventScroll: true }); }
        }
      } else app.selectFile(key, { focus: true });
    } else if (e.key === 'ArrowLeft') {
      e.preventDefault();
      if (kind === 'folder' && expanded) app.toggleFolder(key);
      else if (idx > 0) {
        let at = idx - 1;
        const level = Number(row.getAttribute('aria-level')) - 1;
        while (at > 0 && Number(rows[at].getAttribute('aria-level')) >= level + 1) at -= 1;
        const parent = rows[at];
        if (parent) { rows.forEach((r) => { r.tabIndex = -1; }); parent.tabIndex = 0; parent.focus({ preventScroll: true }); }
      }
    } else if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      if (kind === 'folder') app.toggleFolder(key);
      else app.selectFile(key, { focus: false });
    }
  });
  el.filesClose.addEventListener('click', app.closeFilesPanel);
  el.filesTreeBtn.addEventListener('click', app.toggleFilesTree);
  el.filesExpand.addEventListener('click', () => { if (app.filesState.current) app.openViewer(app.filesState.current, el.filesExpand); });

  el.attachRow.addEventListener('click', (e) => {
    const rm = e.target.closest('[data-rm]');
    if (rm) { app.removeAttached(Number(rm.getAttribute('data-rm'))); return; }
    const preview = e.target.closest('[data-preview]');
    if (preview) app.openComposerImage(Number(preview.getAttribute('data-preview')), preview);
  });

  el.fileInput.addEventListener('change', () => {
    app.addFiles(el.fileInput.files);
    el.fileInput.value = '';
    el.input.focus();
  });

  el.thread.addEventListener('click', (e) => {
    const term = e.target.closest('[data-term]');
    if (term) { G.glossary.open(term.getAttribute('data-term'), term); return; }
    const img = e.target.closest('[data-att-msg]');
    if (img) {
      app.openMessageImage(img.getAttribute('data-att-msg'), Number(img.getAttribute('data-att-img')) || 0, img);
      return;
    }
    const art = e.target.closest('[data-artifact]');
    if (art) { app.openFilesPanel(art.getAttribute('data-artifact')); return; }
    const fileRow = e.target.closest('[data-file]');
    if (fileRow) { app.selectFile(fileRow.getAttribute('data-file')); return; }
    const other = e.target.closest('[data-ask-other]');
    if (other) { app.openAskCustom(other.getAttribute('data-ask-other')); return; }
    const submit = e.target.closest('[data-ask-submit]');
    if (submit) {
      const card = submit.closest('.ask');
      const picks = Array.prototype.map.call(card.querySelectorAll('.ask-opt.is-picked'), (b) => Number(b.getAttribute('data-opt')));
      if (!picks.length) { app.toast('先选至少一项'); return; }
      app.answerQuestion(submit.getAttribute('data-ask-submit'), picks);
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
      app.answerQuestion(mid, [Number(ask.getAttribute('data-opt'))]);
      return;
    }
    const copy = e.target.closest('[data-copy]');
    if (copy) { app.copyMessage(copy.getAttribute('data-copy')); return; }
    const retry = e.target.closest('[data-retry]');
    if (retry) { app.retryMessage(retry.getAttribute('data-retry')); return; }
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
      app.copyText(pre ? pre.textContent : '');
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

  el.scroller.addEventListener('scroll', app.updateScrollBtn, { passive: true });
  el.scrollBtn.addEventListener('click', () => {
    el.scroller.scrollTo({ top: el.scroller.scrollHeight, behavior: app.state.reduceMotion ? 'auto' : 'smooth' });
    el.input.focus({ preventScroll: true });
  });

  el.importData.addEventListener('click', () => el.importInput.click());
  el.importInput.addEventListener('change', () => {
    app.importData(el.importInput.files && el.importInput.files[0]);
    el.importInput.value = '';
  });

  el.fullBtn.addEventListener('click', () => {
    if (document.fullscreenElement) {
      document.exitFullscreen().catch(() => {});
    } else if (document.documentElement.requestFullscreen) {
      document.documentElement.requestFullscreen().catch(() => app.toast('当前浏览器不支持全屏'));
    } else {
      app.toast('当前浏览器不支持全屏');
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
    if (app.stream) app.stopStreaming();
    const conv = { id: uid(), title: '新会话', pinned: false, messages: [], updatedAt: now() };
    const ws = { id: uid(), name: name.slice(0, 40), convs: [conv] };
    app.state.workspaces.unshift(ws);
    app.state.activeWorkspaceId = ws.id;
    app.state.activeConvId = conv.id;
    el.wsDialog.close();
    app.save();
    app.renderAll();
    app.focusInput();
    app.toast('已创建工作区「' + ws.name + '」');
  });

  el.motionSwitch.addEventListener('click', () => {
    app.state.reduceMotion = !app.state.reduceMotion;
    app.applyReduceMotion();
    app.save();
  });

  el.sideSwitch.addEventListener('click', () => {
    app.setSidebarCollapsed(!app.state.sidebarCollapsed);
  });

  el.clearAll.addEventListener('click', app.clearEverything);
  el.exportData.addEventListener('click', app.exportData);

  $$('[data-settings-close]').forEach((node) => node.addEventListener('click', app.closeSettings));
  $$('[data-usage-close]').forEach((node) => node.addEventListener('click', app.closeUsage));
  $$('[data-ext-close]').forEach((node) => node.addEventListener('click', app.closeExtensions));
  el.settingsTabs.forEach((tab) => {
    tab.addEventListener('click', () => app.openSettingsTab(tab.getAttribute('data-settings-tab'), false));
    tab.addEventListener('keydown', (e) => {
      if (e.key !== 'ArrowDown' && e.key !== 'ArrowUp') return;
      e.preventDefault();
      const idx = el.settingsTabs.indexOf(tab);
      const dir = e.key === 'ArrowDown' ? 1 : -1;
      const next = el.settingsTabs[(idx + dir + el.settingsTabs.length) % el.settingsTabs.length];
      app.openSettingsTab(next.getAttribute('data-settings-tab'), true);
    });
  });

  $$('button', el.fontSeg).forEach((b) => {
    b.addEventListener('click', () => {
      app.state.fontSize = b.getAttribute('data-font');
      app.applyFontSize();
      app.save();
      app.syncSettingsUI();
    });
  });

  el.thinkSwitch.addEventListener('click', () => {
    app.state.thinkOpen = !app.state.thinkOpen;
    app.save();
    app.syncSettingsUI();
    app.renderThread();
  });

  $$('[data-lb-close]', el.lightbox).forEach((n) => n.addEventListener('click', app.closeLightbox));

  $$('[data-img-close]', el.imgbox).forEach((n) => n.addEventListener('click', app.closeImgbox));
  el.imgboxPrev.addEventListener('click', () => app.stepImgbox(-1));
  el.imgboxNext.addEventListener('click', () => app.stepImgbox(1));

  function hasFileDrag(e) {
    return !!e.dataTransfer && Array.prototype.indexOf.call(e.dataTransfer.types || [], 'Files') >= 0;
  }
  function hideDropzone() {
    app.dragDepth = 0;
    el.dropzone.hidden = true;
  }
  function overlayOpen() {
    return !el.settingsLayer.hidden || !el.lightbox.hidden || !el.imgbox.hidden ||
      (el.usageLayer && !el.usageLayer.hidden) || (el.extensionsLayer && !el.extensionsLayer.hidden);
  }
  window.addEventListener('dragenter', (e) => {
    if (!hasFileDrag(e) || overlayOpen()) return;
    e.preventDefault();
    app.dragDepth += 1;
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
    app.dragDepth = Math.max(0, app.dragDepth - 1);
    if (!app.dragDepth) hideDropzone();
  });
  window.addEventListener('drop', (e) => {
    if (!hasFileDrag(e)) return;
    e.preventDefault();
    hideDropzone();
    const files = e.dataTransfer && e.dataTransfer.files;
    if (files && files.length) app.addFiles(files);
  });
  window.addEventListener('dragend', hideDropzone);

  document.addEventListener('keydown', (e) => {
    const mod = e.metaKey || e.ctrlKey;
    const typing = e.target && /^(INPUT|TEXTAREA)$/.test(e.target.tagName);
    const overlay = !el.settingsLayer.hidden || !el.lightbox.hidden || !el.imgbox.hidden ||
      (el.usageLayer && !el.usageLayer.hidden) || (el.extensionsLayer && !el.extensionsLayer.hidden);

    if (mod && !e.shiftKey && !e.altKey && String(e.key).toLowerCase() === 'k') {
      e.preventDefault();
      if (el.app.classList.contains('drawer-open') || app.mqMobile.matches) el.app.classList.add('drawer-open');
      setSearchOpen(true);
      return;
    }
    if (e.altKey && !mod && String(e.key).toLowerCase() === 'n') {
      e.preventDefault();
      if (!overlay) app.newChat();
      return;
    }
    if (mod && e.key === 'Enter') {
      if (overlay || (typing && e.target !== el.input)) return;
      e.preventDefault();
      app.handleSend();
      return;
    }
    if (e.key === 'Escape') {
      if (!el.imgbox.hidden) { app.closeImgbox(); return; }
      if (!el.lightbox.hidden) { app.closeLightbox(); return; }
      if (el.extensionsLayer && !el.extensionsLayer.hidden) { app.closeExtensions(); return; }
      if (el.usageLayer && !el.usageLayer.hidden) { app.closeUsage(); return; }
      if (!el.settingsLayer.hidden) { app.closeSettings(); return; }
      if (app.contextNode) { app.closeContextPanel(); return; }
      if (el.filesPanel.classList.contains('is-open')) { app.closeFilesPanel(); return; }
      if (app.menu) { app.closeMenu(true); return; }
      if (app.searchOpen) { setSearchOpen(false); el.searchBtn.focus({ preventScroll: true }); return; }
      if (el.app.classList.contains('drawer-open')) { app.closeDrawer(); return; }
      if (app.view === 'board') { app.setView('chat'); return; }
      return;
    }
    if (e.key === 'Tab') {
      if (!el.settingsLayer.hidden) { app.trapTab(el.settingsWindow, e); return; }
      if (el.usageLayer && !el.usageLayer.hidden) { app.trapTab(el.usageLayer.querySelector('.usage-window'), e); return; }
      if (el.extensionsLayer && !el.extensionsLayer.hidden) { app.trapTab(el.extensionsLayer.querySelector('#extensionsWindow'), e); return; }
      if (!el.lightbox.hidden) { app.trapTab(el.lightbox.querySelector('.lb-panel'), e); return; }
      if (!el.imgbox.hidden) { app.trapTab(el.imgbox.querySelector('.imgbox-panel'), e); return; }
    }
    if (!el.imgbox.hidden && (e.key === 'ArrowLeft' || e.key === 'ArrowRight')) {
      e.preventDefault();
      app.stepImgbox(e.key === 'ArrowLeft' ? -1 : 1);
    }
  });

  if (app.mqMobile.addEventListener) {
    app.mqMobile.addEventListener('change', (e) => {
      if (!e.matches) el.app.classList.remove('drawer-open');
    });
  }

  /* ---------------- boot ---------------- */

  el.app.setAttribute('data-sidebar', app.state.sidebarCollapsed ? 'collapsed' : 'open');
  app.applyReduceMotion();
  app.applyFontSize();
  app.syncSettingsUI();
  app.updateContextChip();
  app.initFilesResize();
  G.clawd.init({ el: el.clawd, reduced: () => app.state.reduceMotion });
  G.clawd.idle();
  app.renderAll();
  app.refreshUsage();
  app.renderAttachments();
  app.setupDetectedApi();
  app.restoreDraft();
  app.autoGrow();
  app.updateSendState();
  app.updateScrollBtn();
  if (!app.mqMobile.matches) el.input.focus({ preventScroll: true });

})();
