/* 顾清影 · 菜单/模型/附件/语音 service：浮层菜单、推理滑块、模型选择、附件、语音输入 */
(() => {
  'use strict';
  const G = window.GQY;
  const app = G.app;
  const { esc, formatCount } = G;
  const el = app.el;
  const $ = app.$;
  const $$ = app.$$;

  /* ---------------- 菜单 ---------------- */

  function closeMenu(restoreFocus) {
    if (!app.menu) return;
    app.menu.remove();
    app.menu = null;
    const anchor = app.menuAnchor;
    app.menuAnchor = null;
    if (anchor) {
      anchor.setAttribute('aria-expanded', 'false');
      if (restoreFocus) anchor.focus({ preventScroll: true });
    }
    document.removeEventListener('pointerdown', onDocPointer, true);
    document.removeEventListener('keydown', onMenuKey, true);
  }

  function onDocPointer(e) {
    if (!app.menu) return;
    if (app.menu.contains(e.target)) return;
    if (app.menuAnchor && app.menuAnchor.contains(e.target)) return;
    closeMenu();
  }

  function onMenuKey(e) {
    if (!app.menu) return;
    if (e.key === 'Escape') { e.preventDefault(); closeMenu(true); return; }
    if (e.target && e.target.tagName === 'INPUT') return;
    if (e.target && e.target.closest && e.target.closest('.effort-track')) return;
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault();
      const items = $$('button', app.menu).filter((b) => !b.disabled);
      if (!items.length) return;
      const idx = items.indexOf(document.activeElement);
      const dir = e.key === 'ArrowDown' ? 1 : -1;
      const next = idx === -1 ? 0 : (idx + dir + items.length) % items.length;
      items[next].focus({ preventScroll: true });
    }
  }

  function openMenu(anchor, build, opts) {
    opts = opts || {};
    const sameAnchor = !!app.menu && app.menuAnchor === anchor;
    closeMenu(false);
    if (sameAnchor) return;
    app.menuAnchor = anchor;
    anchor.setAttribute('aria-expanded', 'true');
    const node = document.createElement('div');
    node.className = 'menu';
    node.setAttribute('role', 'menu');
    build(node);
    document.body.appendChild(node);
    app.menu = node;
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

  /* ---------------- 推理滑块 ---------------- */

  function mountEffort(card) {
    const steps = app.EFFORTS;
    const track = $('.effort-track', card);
    const knob = $('.effort-knob', card);
    const fill = $('.effort-fill', card);
    const ticks = $('.effort-ticks', card);
    const matrix = $('.effort-matrix', card);
    const valueEl = $('.effort-value', card);
    const ghost = $('.effort-ghost', card);
    const descEl = $('.effort-desc', card);
    let selected = Math.max(0, steps.indexOf(app.state.effort));
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
      descEl.textContent = app.EFFORT_DESC[steps[at]] || '';
      track.setAttribute('aria-valuenow', String(at));
      track.setAttribute('aria-valuetext', steps[at]);
    }

    function commit(index) {
      selected = Math.max(0, Math.min(steps.length - 1, index));
      live = selected;
      const next = steps[selected];
      if (next !== app.state.effort) {
        app.state.effort = next;
        app.save();
        app.updateComposer();
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

  /* ---------------- 模型选择 ---------------- */

  function modelLabel(m) {
    if (!m) return app.activeModel();
    return m.name || m.id || '';
  }

  function selectModel(m) {
    app.state.model = m ? m.id : '';
    app.save();
    app.updateComposer();
    if (el.aboutModelInline) el.aboutModelInline.textContent = app.activeModel();
  }

  async function ensureModelCatalog() {
    if (app.modelCatalog.length || app.modelCatalogLoading) return app.modelCatalog;
    if (!G.api || !G.api.models || !G.api.available()) return app.modelCatalog;
    app.modelCatalogLoading = true;
    try {
      app.modelCatalog = await G.api.models();
    } catch (err) {
      app.modelCatalog = [];
    } finally {
      app.modelCatalogLoading = false;
    }
    return app.modelCatalog;
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
            checked: app.state.model === m.id,
            onSelect: () => selectModel(m)
          }));
        });
      };

      const info2 = G.api && G.api.info ? G.api.info() : null;
      if (online && info2 && info2.model) {
        list.appendChild(menuItem('默认（' + info2.model + '）', {
          desc: '服务端 GQY_MODEL',
          checked: app.state.model === '',
          onSelect: () => selectModel(null)
        }));
      }

      search.querySelector('input').addEventListener('input', () => {
        if (app.modelCatalog.length) applyModels(app.modelCatalog);
      });

      if (app.modelCatalog.length) {
        applyModels(app.modelCatalog);
      } else if (online) {
        ensureModelCatalog().then(applyModels);
      } else {
        applyModels([]);
      }

      const sep = document.createElement('div');
      sep.className = 'menu-sep';
      node.appendChild(sep);

      const effort = document.createElement('div');
      effort.className = 'effort';
      effort.innerHTML =
        '<div class="effort-head"><span class="effort-label">推理强度</span>' +
        '<span class="effort-value-wrap"><span class="effort-value">' + esc(app.state.effort) + '</span>' +
        '<span class="effort-ghost" aria-hidden="true"></span></span></div>' +
        '<div class="effort-ends"><span>更快</span><span>更强</span></div>' +
        '<div class="effort-track" role="slider" tabindex="0" aria-label="推理强度" ' +
        'aria-valuemin="0" aria-valuemax="' + (app.EFFORTS.length - 1) + '" aria-valuenow="' + Math.max(0, app.EFFORTS.indexOf(app.state.effort)) + '">' +
        '<div class="effort-fill"></div><div class="effort-ticks"></div>' +
        '<div class="effort-matrix" aria-hidden="true"></div><div class="effort-knob"></div></div>' +
        '<p class="effort-desc"></p>';
      node.appendChild(effort);
      mountEffort(effort);

      const inp = search.querySelector('input');
      if (inp) setTimeout(() => inp.focus(), 20);
    }, { align: 'right', place: 'top' });
  }

  /* ---------------- 附件 / 语音 ---------------- */

  function isImageFile(file) {
    return !!file && /^image\//.test(file.type || '');
  }

  function urlForFile(file) {
    if (!isImageFile(file)) return '';
    if (app.attachUrls.has(file)) return app.attachUrls.get(file);
    const url = URL.createObjectURL(file);
    app.attachUrls.set(file, url);
    return url;
  }

  function addFiles(fileList) {
    const incoming = Array.prototype.slice.call(fileList || []);
    if (!incoming.length) return;
    let added = 0;
    let skipped = 0;
    incoming.forEach((file) => {
      if (!file || !file.name) return;
      const duplicate = app.attached.some((f) => f.name === file.name && f.size === file.size && f.lastModified === file.lastModified);
      if (app.attached.length >= app.MAX_ATTACH || duplicate) { skipped += 1; return; }
      app.attached.push(file);
      added += 1;
    });
    renderAttachments();
    app.updateSendState();
    if (added && skipped) app.toast('已添加 ' + added + ' 个附件，跳过 ' + skipped + ' 个');
    else if (added) app.toast('已添加 ' + added + ' 个附件');
    else if (skipped) app.toast('没有可添加的文件：重复或超过 ' + app.MAX_ATTACH + ' 个');
  }

  function removeAttached(index) {
    const file = app.attached[index];
    if (!file) return;
    if (isImageFile(file) && app.attachUrls.has(file)) {
      URL.revokeObjectURL(app.attachUrls.get(file));
      app.attachUrls.delete(file);
    }
    app.attached.splice(index, 1);
    renderAttachments();
    app.updateSendState();
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
    if (!app.attached.length) {
      el.attachRow.hidden = true;
      el.attachRow.innerHTML = '';
      return;
    }
    el.attachRow.hidden = false;
    el.attachRow.innerHTML = app.attached.map((f, i) => {
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
    if (!app.state.voiceEnabled) { app.toast('语音输入已关闭：设置 → 语音'); return; }
    const SR = window.SpeechRecognition || window.webkitSpeechRecognition;
    if (!SR) { app.toast('当前浏览器不支持语音输入'); return; }
    if (app.recog) {
      try { app.recog.stop(); } catch (err) {}
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
      app.autoGrow();
      app.updateSendState();
    };
    r.onend = () => {
      app.recog = null;
      el.micBtn.classList.remove('recording');
      el.micBtn.setAttribute('aria-pressed', 'false');
    };
    r.onerror = () => { app.toast('语音输入没有启动成功'); };
    app.recog = r;
    el.micBtn.classList.add('recording');
    el.micBtn.setAttribute('aria-pressed', 'true');
    try { r.start(); } catch (err) {
      app.recog = null;
      el.micBtn.classList.remove('recording');
      el.micBtn.setAttribute('aria-pressed', 'false');
      app.toast('语音输入没有启动成功');
    }
  }

  Object.assign(app, {
    closeMenu, openMenu, positionMenu, menuItem,
    speechSupported, pickFile, openPlusMenu, mountEffort,
    modelLabel, selectModel, ensureModelCatalog, openModelMenu,
    isImageFile, urlForFile, addFiles, removeAttached, imageItemsFromFiles, renderAttachments, toggleMic
  });
})();
