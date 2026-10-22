/* 顾清影 · Clawd 吉祥物（精灵图逐帧播放与活动状态机） */
(() => {
  'use strict';
  const G = (window.GQY = window.GQY || {});
  let root = null;
  let isReduced = () => false;

  const CLAWD_SHEETS = {
    idle: { frames: 24, box: [24, 20, 24, 16], still: 0 },
    'idle-look': { frames: 31, box: [24, 20, 24, 16], still: 0 },
    'idle-wave': { frames: 12, box: [24, 15, 24, 21], still: 0 },
    'idle-laptop': { frames: 43, box: [14, 13, 34, 23], still: 0 },
    thinking: { frames: 32, box: [14, 4, 34, 32], still: 18 },
    typing: { frames: 6, box: [15, 22, 28, 14], still: 0 },
    happy: { frames: 32, box: [18, 6, 34, 30], still: 3 }
  };
  const CLAWD_MS = 80;
  const CLAWD_SCALE = 1.6;
  const clawdState = { anim: '', frame: 0, tick: 0, timer: 0 };

  function clawdPaint() {
    const cfg = CLAWD_SHEETS[clawdState.anim];
    if (!cfg || !root) return;
    const bw = cfg.box[2];
    const bh = cfg.box[3];
    const col = clawdState.frame % 8;
    const row = Math.floor(clawdState.frame / 8);
    const pos = '-' + Math.round(col * bw * CLAWD_SCALE) + 'px -' + Math.round(row * bh * CLAWD_SCALE) + 'px';
    const body = root.querySelector('.clawd-body');
    const ink = root.querySelector('.clawd-ink');
    if (body) body.style.backgroundPosition = pos;
    if (ink) ink.style.backgroundPosition = pos;
  }

  function clawdTick() {
    const cfg = CLAWD_SHEETS[clawdState.anim];
    if (!cfg) return;
    clawdState.frame = (clawdState.frame + 1) % cfg.frames;
    clawdPaint();
  }

  function clawdSet(anim) {
    if (!root) return;
    const key = CLAWD_SHEETS[anim] ? anim : 'idle';
    const cfg = CLAWD_SHEETS[key];
    const bw = cfg.box[2];
    const bh = cfg.box[3];
    const body = root.querySelector('.clawd-body');
    const ink = root.querySelector('.clawd-ink');
    if (!body || !ink) return;
    clearTimeout(clawdState.timer);
    clearInterval(clawdState.tick);
    clawdState.tick = 0;
    root.style.width = Math.round(bw * CLAWD_SCALE) + 'px';
    root.style.height = Math.round(bh * CLAWD_SCALE) + 'px';
    [body, ink].forEach((node) => {
      node.style.width = Math.round(bw * CLAWD_SCALE) + 'px';
      node.style.height = Math.round(bh * CLAWD_SCALE) + 'px';
      node.style.backgroundSize = Math.round(bw * 8 * CLAWD_SCALE) + 'px ' + Math.round(bh * Math.ceil(cfg.frames / 8) * CLAWD_SCALE) + 'px';
    });
    body.style.backgroundImage = 'url(assets/clawd/' + key + '.png)';
    ink.style.backgroundImage = 'url(assets/clawd/' + key + '-ink.png)';
    clawdState.anim = key;
    clawdState.frame = isReduced() ? cfg.still : 0;
    root.classList.add('is-on');
    clawdPaint();
    if (!isReduced()) clawdState.tick = setInterval(clawdTick, CLAWD_MS);
  }

  function clawdIdle() {
    clearTimeout(clawdState.timer);
    clawdSet('idle');
    const extras = ['idle-look', 'idle-wave', 'idle-laptop'];
    clawdState.timer = setTimeout(() => {
      const extra = extras[Math.floor(Math.random() * extras.length)];
      clawdSet(extra);
      clawdState.timer = setTimeout(clawdIdle, CLAWD_SHEETS[extra].frames * CLAWD_MS + 600);
    }, 7000 + Math.random() * 6000);
  }

  function init(opts) {
    root = opts && opts.el ? opts.el : null;
    if (opts && typeof opts.reduced === 'function') isReduced = opts.reduced;
  }

  function happy() {
    clawdSet('happy');
    clearTimeout(clawdState.timer);
    clawdState.timer = setTimeout(clawdIdle, 2200);
  }

  G.clawd = { init: init, set: clawdSet, idle: clawdIdle, happy: happy, sync: function () { if (clawdState.anim) clawdSet(clawdState.anim); } };
})();
