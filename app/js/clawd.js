/* 顾清影 · Clawd 吉祥物（精灵图逐帧播放与活动状态机）
 *
 * box = [x, y, w, h]：单元格宽高（w/h）+ 内容在格内的偏移（x/y，当前全部 0，
 * 即整格底部对齐）。整格取景能让思考气泡 / 笔记本 / 挥手这些「溢出主体」的
 * 素材不被裁掉，同时底部对齐保证不同状态切换时宠物脚底不跳。
 *
 * 缩放用整数 CLAWD_SCALE：整数倍放大像素锐利，且 backgroundSize 与
 * backgroundPosition 都用同一套整数乘积，不存在逐帧 rounding 漂移。
 */
(() => {
  'use strict';
  const G = (window.GQY = window.GQY || {});
  let root = null;
  let isReduced = () => false;

  const CLAWD_COLS = 8;
  const CLAWD_MS = 80;
  const CLAWD_SCALE = 2;
  const SLEEP_AFTER = 60000; // 无操作 60s 后入睡

  const CLAWD_SHEETS = {
    idle:         { frames: 24, box: [0, 0, 24, 16], still: 0 },
    'idle-look':  { frames: 31, box: [0, 0, 24, 16], still: 0 },
    'idle-wave':  { frames: 12, box: [0, 0, 24, 21], still: 0 },
    'idle-laptop':{ frames: 43, box: [0, 0, 34, 23], still: 0 },
    thinking:     { frames: 32, box: [0, 0, 34, 32], still: 18 },
    typing:       { frames: 6,  box: [0, 0, 28, 14], still: 0 },
    happy:        { frames: 32, box: [0, 0, 34, 30], still: 3 },
    sleeping:     { frames: 32, box: [0, 0, 29, 33], still: 0 }
  };

  const clawdState = { anim: '', frame: 0, tick: 0, timer: 0, sleepTimer: 0, asleep: false };
  // 只有「活动」动画才重置入睡倒计时；idle 循环（look/wave/laptop）不算活动，
  // 否则每轮随机小动作都会把睡眠往后推，宠物永远睡不着。
  const ACTIVE = { thinking: true, typing: true, happy: true };

  function clawdPaint() {
    const cfg = CLAWD_SHEETS[clawdState.anim];
    if (!cfg || !root) return;
    const bw = cfg.box[2];
    const bh = cfg.box[3];
    const col = clawdState.frame % CLAWD_COLS;
    const row = Math.floor(clawdState.frame / CLAWD_COLS);
    const pos = (-col * bw * CLAWD_SCALE) + 'px ' + (-row * bh * CLAWD_SCALE) + 'px';
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

    const w = bw * CLAWD_SCALE;
    const h = bh * CLAWD_SCALE;
    const rows = Math.ceil(cfg.frames / CLAWD_COLS);
    root.style.width = w + 'px';
    root.style.height = h + 'px';
    [body, ink].forEach((node) => {
      node.style.width = w + 'px';
      node.style.height = h + 'px';
      node.style.backgroundSize = (bw * CLAWD_COLS * CLAWD_SCALE) + 'px ' + (bh * rows * CLAWD_SCALE) + 'px';
    });
    body.style.backgroundImage = 'url(assets/clawd/' + key + '.png)';
    ink.style.backgroundImage = 'url(assets/clawd/' + key + '-ink.png)';

    // 活动动画推后入睡；入睡本身除外（此时计时器已触发，正在进入睡眠）。
    if (key !== 'sleeping') {
      if (ACTIVE[key] || key === 'idle') {
        clawdState.asleep = false;
        clearTimeout(clawdState.sleepTimer);
        clawdState.sleepTimer = 0;
      }
    }

    clawdState.anim = key;
    clawdState.frame = isReduced() ? cfg.still : 0;
    root.classList.add('is-on');
    clawdPaint();
    if (!isReduced()) clawdState.tick = setInterval(clawdTick, CLAWD_MS);
  }

  function scheduleSleep() {
    clearTimeout(clawdState.sleepTimer);
    clawdState.sleepTimer = setTimeout(() => {
      clawdState.asleep = true;
      clawdSet('sleeping');
    }, SLEEP_AFTER);
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
    scheduleSleep();
  }

  function clawdWake() {
    if (!root || clawdState.anim !== 'sleeping') return;
    clawdIdle();
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

  G.clawd = {
    init: init,
    set: clawdSet,
    idle: clawdIdle,
    happy: happy,
    sleep: function () { clawdState.asleep = true; clawdSet('sleeping'); },
    wake: clawdWake,
    sync: function () { if (clawdState.anim) clawdSet(clawdState.anim); }
  };
})();
