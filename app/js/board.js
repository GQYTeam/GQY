/* 顾清影 · 看板：本机任务卡，可关联当前会话 */
(() => {
  'use strict';
  const G = window.GQY;
  const app = G.app;
  const el = app.el;
  const { uid, now, esc } = G;

  const STATUS = ['todo', 'doing', 'done'];
  const LABEL = { todo: '待办', doing: '进行中', done: '已完成' };

  function cards() {
    if (!app.state.board || !Array.isArray(app.state.board.cards)) app.state.board = { cards: [] };
    return app.state.board.cards;
  }

  function render() {
    if (!el.boardTodo) return;
    STATUS.forEach((status) => {
      const node = status === 'todo' ? el.boardTodo : status === 'doing' ? el.boardDoing : el.boardDone;
      const list = cards().filter((c) => c.status === status);
      node.innerHTML = list.length ? list.map((c) => cardHTML(c)).join('') : '<p class="board-empty">暂无卡片</p>';
    });
  }

  function cardHTML(c) {
    const conv = c.convId ? '<button class="board-mini" type="button" data-open-conv="' + esc(c.convId) + '" title="打开关联会话" aria-label="打开关联会话">↗</button>' : '';
    const left = c.status === 'todo' ? '' : '<button class="board-mini" type="button" data-move="' + esc(c.id) + '" data-dir="-1" aria-label="移到上一列">←</button>';
    const right = c.status === 'done' ? '' : '<button class="board-mini" type="button" data-move="' + esc(c.id) + '" data-dir="1" aria-label="移到下一列">→</button>';
    return '<article class="board-card' + (c.status === 'done' ? ' is-done' : '') + '">' +
      '<div class="board-card-title">' + esc(c.title) + '</div>' +
      '<div class="board-card-meta"><span>' + LABEL[c.status] + '</span><span class="board-card-actions">' +
      conv + left + right +
      '<button class="board-mini danger" type="button" data-del-card="' + esc(c.id) + '" aria-label="删除卡片">×</button>' +
      '</span></div></article>';
  }

  function add(title) {
    const text = String(title || '').trim();
    if (!text) return;
    cards().unshift({
      id: uid(),
      title: text.slice(0, 120),
      status: 'todo',
      convId: app.state.activeConvId || '',
      createdAt: now()
    });
    app.save();
    render();
  }

  function move(id, dir) {
    const card = cards().find((c) => c.id === id);
    if (!card) return;
    const idx = STATUS.indexOf(card.status);
    const next = Math.max(0, Math.min(STATUS.length - 1, idx + dir));
    if (next === idx) return;
    card.status = STATUS[next];
    app.save();
    render();
  }

  function remove(id) {
    const list = cards();
    const idx = list.findIndex((c) => c.id === id);
    if (idx < 0) return;
    const removed = list.splice(idx, 1)[0];
    app.save();
    render();
    app.toast('已删除卡片', '撤销', () => {
      list.splice(Math.min(idx, list.length), 0, removed);
      app.save();
      render();
    });
  }

  function init() {
    if (!el.boardForm) return;
    el.boardForm.addEventListener('submit', (e) => {
      e.preventDefault();
      add(el.boardInput.value);
      el.boardInput.value = '';
      el.boardInput.focus();
    });
    el.boardView.addEventListener('click', (e) => {
      const moveBtn = e.target.closest('[data-move]');
      if (moveBtn) { move(moveBtn.getAttribute('data-move'), Number(moveBtn.getAttribute('data-dir'))); return; }
      const del = e.target.closest('[data-del-card]');
      if (del) { remove(del.getAttribute('data-del-card')); return; }
      const open = e.target.closest('[data-open-conv]');
      if (open) { app.selectConv(open.getAttribute('data-open-conv')); return; }
    });
  }

  app.renderBoard = render;
  app.addBoardCard = add;
  G.board = { init, render, add };
  init();
})();
