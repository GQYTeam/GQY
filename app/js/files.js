/* 顾清影 · 文件/灯箱 service：文件面板、代码灯箱、图片灯箱、提问卡片 */
(() => {
  'use strict';
  const G = window.GQY;
  const app = G.app;
  const { uid, now, esc, formatBytes, highlightHTML } = G;
  const el = app.el;

  function copyText(text) {
    if (!text) return;
    const done = () => app.toast('已复制');
    const fallback = () => {
      const ta = document.createElement('textarea');
      ta.value = text;
      ta.setAttribute('readonly', '');
      ta.style.position = 'fixed';
      ta.style.top = '-1000px';
      ta.style.opacity = '0';
      document.body.appendChild(ta);
      ta.select();
      try { document.execCommand('copy'); done(); } catch (err) { app.toast('复制失败，请手动选择文本'); }
      ta.remove();
    };
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(text).then(done).catch(fallback);
    } else {
      fallback();
    }
  }

  function syncInert() {
    el.app.inert = !el.settingsLayer.hidden || !el.lightbox.hidden || !el.imgbox.hidden ||
      (el.usageLayer && !el.usageLayer.hidden) || (el.extensionsLayer && !el.extensionsLayer.hidden);
  }

  /* ---------------- 图片灯箱 ---------------- */

  function openImgbox(items, index, trigger) {
    if (!items || !items.length) return;
    app.imgboxItems = items.slice();
    app.imgboxIndex = Math.max(0, Math.min(Number(index) || 0, app.imgboxItems.length - 1));
    app.imgboxTrigger = trigger || document.activeElement;
    el.imgbox.hidden = false;
    document.body.classList.add('imgbox-open');
    syncInert();
    paintImgbox();
    const panel = el.imgbox.querySelector('.imgbox-panel');
    if (panel) panel.focus({ preventScroll: true });
  }

  function paintImgbox() {
    if (!app.imgboxItems.length) return;
    const item = app.imgboxItems[app.imgboxIndex];
    el.imgboxTitle.textContent = item.name || '图片预览';
    el.imgboxMeta.textContent = formatBytes(item.size || 0);
    el.imgboxCount.textContent = (app.imgboxIndex + 1) + ' / ' + app.imgboxItems.length;
    el.imgboxPrev.disabled = app.imgboxItems.length < 2;
    el.imgboxNext.disabled = app.imgboxItems.length < 2;
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
    if (!app.imgboxItems.length || app.imgboxItems.length < 2) return;
    app.imgboxIndex = (app.imgboxIndex + delta + app.imgboxItems.length) % app.imgboxItems.length;
    paintImgbox();
  }

  function closeImgbox() {
    if (el.imgbox.hidden) return;
    el.imgbox.hidden = true;
    document.body.classList.remove('imgbox-open');
    el.imgboxImg.removeAttribute('src');
    syncInert();
    const trigger = app.imgboxTrigger;
    app.imgboxTrigger = null;
    app.imgboxItems = [];
    if (trigger && trigger.isConnected && typeof trigger.focus === 'function') trigger.focus({ preventScroll: true });
  }

  function openComposerImage(index, trigger) {
    const file = app.attached[index];
    if (!file) return;
    const items = app.imageItemsFromFiles(app.attached);
    let pos = -1;
    items.forEach((item, i) => { if (item.name === file.name && item.size === (file.size || 0)) pos = i; });
    openImgbox(items, pos >= 0 ? pos : 0, trigger);
  }

  function openMessageImage(mid, imgIndex, trigger) {
    const conv = app.activeConv();
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

  /* ---------------- 文件面板 ---------------- */

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
    return app.filesState.open[key] === true;
  }

  function treeIndent(depth) {
    let out = '';
    for (let i = 0; i < depth; i += 1) out += '<span class="fp-guide"></span>';
    return out;
  }

  function renderFilesTree() {
    const filesState = app.filesState;
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
    app.filesState.open[key] = !isFolderOpen(key);
    renderFilesTree();
    focusTreeRow(key);
  }

  function selectFile(id, opts) {
    const filesState = app.filesState;
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
    app.filesState.treeView = true;
    el.filesPanel.setAttribute('data-view', 'tree');
    el.filesTree.hidden = false;
    el.filesCodePane.hidden = true;
    el.filesTreeBtn.setAttribute('aria-pressed', 'true');
    renderFilesTree();
    const row = el.filesTree.querySelector('[role="treeitem"][tabindex="0"]');
    if (row) row.focus({ preventScroll: true });
  }

  function openFilesPanel(mid) {
    const m = app.findMessage(mid);
    if (!m || !m.artifact) return;
    const conv = app.activeConv();
    const data = buildFiles(conv ? conv.messages : []);
    app.filesState.root = data.root;
    app.filesState.files = data.files;
    app.filesState.tree = buildTree(data.files);
    app.filesState.open = { '': true, data: true, 'data/train': false };
    app.filesState.current = null;
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
    if (!app.filesState.current || el.filesTree.hidden) showFilesTree();
    else selectFile(app.filesState.current.id);
  }

  /* ---------------- 面板宽度拖动 ---------------- */

  function clampFilesWidth(width) {
    const row = el.mainRow ? el.mainRow.getBoundingClientRect().width : 960;
    const max = Math.max(340, row - 380);
    return Math.max(320, Math.min(width, Math.min(720, max)));
  }

  function applyStoredFilesWidth() {
    let width = 0;
    try { width = Number(localStorage.getItem(app.FILES_WIDTH_KEY)) || 0; } catch (err) {}
    if (width > 0) el.filesPanel.style.flexBasis = clampFilesWidth(width) + 'px';
  }

  function initFilesResize() {
    if (!el.filesResize) return;
    el.filesResize.addEventListener('pointerdown', (e) => {
      if (e.button !== 0) return;
      app.filesDrag = { x: e.clientX, width: el.filesPanel.getBoundingClientRect().width };
      el.filesResize.classList.add('is-active');
      try { el.filesResize.setPointerCapture(e.pointerId); } catch (err) {}
      e.preventDefault();
    });
    el.filesResize.addEventListener('pointermove', (e) => {
      if (!app.filesDrag) return;
      const width = clampFilesWidth(app.filesDrag.width - (e.clientX - app.filesDrag.x));
      el.filesPanel.style.flexBasis = width + 'px';
    });
    const end = (e) => {
      if (!app.filesDrag) return;
      app.filesDrag = null;
      el.filesResize.classList.remove('is-active');
      try { el.filesResize.releasePointerCapture(e.pointerId); } catch (err) {}
      try { localStorage.setItem(app.FILES_WIDTH_KEY, String(Math.round(el.filesPanel.getBoundingClientRect().width))); } catch (err) {}
    };
    el.filesResize.addEventListener('pointerup', end);
    el.filesResize.addEventListener('pointercancel', end);
    el.filesResize.addEventListener('dblclick', () => {
      el.filesPanel.style.flexBasis = '';
      try { localStorage.removeItem(app.FILES_WIDTH_KEY); } catch (err) {}
    });
  }

  /* ---------------- 代码灯箱 ---------------- */

  function openViewer(file, trigger) {
    if (!file) return;
    app.lbTrigger = trigger || document.activeElement;
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
    const m = app.findMessage(mid);
    if (!m || !m.artifact) return;
    const a = m.artifact;
    openViewer({ name: a.title, path: a.path, lang: a.lang, content: a.content, lines: a.lines }, trigger);
  }

  function closeLightbox() {
    if (el.lightbox.hidden) return;
    el.lightbox.hidden = true;
    document.body.classList.remove('lb-open');
    syncInert();
    const trigger = app.lbTrigger;
    app.lbTrigger = null;
    if (trigger && trigger.isConnected && typeof trigger.focus === 'function') trigger.focus({ preventScroll: true });
  }

  /* ---------------- 提问卡片 ---------------- */

  function answerQuestion(mid, picks, custom) {
    const msg = app.findMessage(mid);
    if (!msg || !msg.question || msg.question.answer) return;
    const opts = msg.question.options.map((o) => (typeof o === 'string' ? { label: o } : o));
    const chosen = picks || [];
    const labels = custom ? [custom] : chosen.map((i) => (opts[i] ? opts[i].label : '')).filter(Boolean);
    if (!labels.length) return;
    msg.question.answer = { picks: chosen, custom: custom || '' };
    const conv = app.activeConv();
    if (!conv || !conv.messages.some((m) => m.id === msg.id)) {
      app.save();
      app.renderThread();
      return;
    }
    conv.messages.push({ id: uid(), role: 'user', text: labels.join('、'), ts: now() });
    conv.updatedAt = now();
    app.moveConvToTop(conv);
    app.save();
    app.renderThread();
    app.scrollToBottom(true);
    app.respond(conv, labels.join('、'), []);
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
    const conv = app.activeConv();
    if (!conv) return;
    const msg = conv.messages.find((m) => m.id === mid);
    if (msg) copyText(msg.text);
  }

  Object.assign(app, {
    copyText, syncInert,
    openImgbox, paintImgbox, stepImgbox, closeImgbox, openComposerImage, openMessageImage,
    buildFiles, buildTree, renderFilesTree, treeRows, toggleFolder, selectFile,
    showFilesTree, openFilesPanel, closeFilesPanel, toggleFilesTree,
    clampFilesWidth, applyStoredFilesWidth, initFilesResize,
    openViewer, openArtifact, closeLightbox,
    answerQuestion, openAskCustom, copyMessage
  });
})();
