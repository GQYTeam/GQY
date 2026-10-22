/* 顾清影 · 共享工具与常量（无依赖，最先加载） */
(() => {
  'use strict';
  const G = (window.GQY = window.GQY || {});

  const HOUR = 3600 * 1000;
  const DAY = 24 * HOUR;
  const uid = () => Date.now().toString(36) + Math.random().toString(36).slice(2, 7);
  const now = () => Date.now();
  const esc = (s) => String(s).replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
  function timeAgo(ts) {
    const diff = Math.max(0, now() - (ts || 0));
    if (diff < 60 * 1000) return '刚刚';
    if (diff < HOUR) return Math.max(1, Math.floor(diff / 60000)) + '分钟';
    if (diff < DAY) return Math.floor(diff / HOUR) + '小时';
    if (diff < 7 * DAY) return Math.floor(diff / DAY) + '天';
    const d = new Date(ts);
    return (d.getMonth() + 1) + '月' + d.getDate() + '日';
  }
  function titleFrom(text) {
    const clean = String(text)
      .replace(/```[\s\S]*?```/g, ' ')
      .replace(/[#>*_`]/g, '')
      .replace(/\s+/g, ' ')
      .trim();
    if (!clean) return '新会话';
    return clean.length > 18 ? clean.slice(0, 18) + '…' : clean;
  }
  function stamp(ts) {
    const d = new Date(ts || now());
    return String(d.getHours()).padStart(2, '0') + ':' + String(d.getMinutes()).padStart(2, '0');
  }
  function estimateTokens(text) {
    const str = String(text || '');
    let n = 0;
    for (let i = 0; i < str.length; i += 1) {
      const code = str.charCodeAt(i);
      n += (code >= 0x2e80 && code <= 0x9fff) ? 1 : 0.28;
    }
    return Math.round(n);
  }
  function fmtTok(n) {
    if (n >= 1000000) return '~' + (n / 1000000).toFixed(1) + 'M';
    if (n >= 1000) return '~' + (n / 1000).toFixed(1) + 'K';
    return '~' + Math.round(n);
  }
  function fmtDur(ms) {
    const total = Math.round(ms / 1000);
    const m = Math.floor(total / 60);
    const s = total % 60;
    return m ? m + ' 分 ' + s + ' 秒' : s + ' 秒';
  }
  function formatBytes(n) {
    const num = Number(n) || 0;
    if (num < 1024) return num + ' B';
    if (num < 1024 * 1024) return (num / 1024).toFixed(num < 10240 ? 1 : 0) + ' KB';
    return (num / (1024 * 1024)).toFixed(1) + ' MB';
  }
  function formatCount(n) {
    const num = Number(n) || 0;
    if (num < 1000) return String(num);
    if (num < 1000000) return (num / 1000).toFixed(num < 10000 ? 1 : 0) + 'k';
    return (num / 1000000).toFixed(1) + 'M';
  }

  Object.assign(G, { HOUR, DAY, uid, now, esc, timeAgo, titleFrom, stamp, estimateTokens, fmtTok, fmtDur, formatBytes, formatCount });
})();
