/* 顾清影 · 后端适配层（可选；file:// 下自动禁用）
 *
 * 只做三件事：探测服务端、流式聊天、会话镜像。
 * 探测失败时前端显示「未连接服务端」，不报错、不阻塞。
 */
(() => {
  'use strict';
  const G = (window.GQY = window.GQY || {});

  let health = null;
  let probing = null;

  function canProbe() {
    return location.protocol === 'http:' || location.protocol === 'https:';
  }

  function detect() {
    if (probing) return probing;
    if (!canProbe()) { health = null; return Promise.resolve(null); }
    const controller = typeof AbortController !== 'undefined' ? new AbortController() : null;
    const timer = setTimeout(() => { if (controller) controller.abort(); }, 1500);
    probing = fetch('/api/health', {
      headers: { accept: 'application/json' },
      signal: controller ? controller.signal : undefined,
      cache: 'no-store'
    }).then((res) => (res.ok ? res.json() : null))
      .then((data) => { health = data && data.ok ? data : null; return health; })
      .catch(() => { health = null; return null; })
      .finally(() => { clearTimeout(timer); probing = null; });
    return probing;
  }

  const available = () => !!(health && health.ok && health.provider !== 'none');
  const info = () => health;

  /* 读取 SSE：返回 Promise，事件回调同步抛错不影响后续解析 */
  async function readStream(res, onEvent) {
    if (!res.body) throw new Error('stream_unsupported');
    const reader = res.body.getReader();
    const decoder = new TextDecoder();
    let buffer = '';
    let ended = false;

    const flush = (block) => {
      for (const line of block.split('\n')) {
        if (!line.startsWith('data:')) continue;
        const payload = line.slice(5).trim();
        if (!payload) continue;
        let event;
        try { event = JSON.parse(payload); } catch (err) { continue; }
        if (event.type === 'error') {
          const error = new Error(event.message || 'chat_failed');
          error.code = event.code || 'chat_failed';
          throw error;
        }
        if (event.type === 'end') { ended = true; continue; }
        if (event.type === 'done') ended = true;
        onEvent(event);
      }
    };

    while (true) {
      const { value, done } = await reader.read();
      if (done) break;
      buffer += decoder.decode(value, { stream: true });
      let cut;
      while ((cut = buffer.indexOf('\n\n')) >= 0) {
        const block = buffer.slice(0, cut);
        buffer = buffer.slice(cut + 2);
        flush(block);
      }
    }
    if (buffer.trim()) flush(buffer);
    return { ended };
  }

  async function chat(options) {
    const { messages, model, effort, session, signal, onEvent } = options || {};
    const res = await fetch('/api/chat', {
      method: 'POST',
      headers: { 'content-type': 'application/json', accept: 'text/event-stream' },
      body: JSON.stringify({ messages, model, effort, session }),
      signal
    });
    if (!res.ok) {
      let detail = {};
      try { detail = await res.json(); } catch (err) {}
      const error = new Error(detail.message || '服务端返回 ' + res.status);
      error.code = detail.error || 'http_' + res.status;
      error.status = res.status;
      throw error;
    }
    return readStream(res, onEvent || (() => {}));
  }

  /* 模型目录：由服务端从 models.dev 拉取并缓存，前端只消费精简结果 */
  async function models() {
    const res = await fetch('/api/models', {
      headers: { accept: 'application/json' },
      cache: 'no-store'
    });
    if (!res.ok) {
      const detail = await res.json().catch(() => ({}));
      throw new Error(detail.message || '模型列表获取失败');
    }
    const data = await res.json();
    return data.models || [];
  }

  /* 用量：服务端聚合的真实 token 用量与消息量 */
  async function usage() {
    const res = await fetch('/api/usage', {
      headers: { accept: 'application/json' },
      cache: 'no-store'
    });
    if (!res.ok) throw new Error('用量获取失败');
    const data = await res.json();
    return data.usage || {};
  }

  /* 会话镜像：失败只记一次日志，不影响本地使用 */
  let syncTimer = 0;
  let syncChain = Promise.resolve();
  function syncSession(id, payload, delay) {
    if (!available() || !/^[A-Za-z0-9_-]{1,64}$/.test(String(id || ''))) return;
    clearTimeout(syncTimer);
    syncTimer = setTimeout(() => {
      syncChain = syncChain.then(() =>
        fetch('/api/sessions/' + encodeURIComponent(id), {
          method: 'PUT',
          headers: { 'content-type': 'application/json' },
          body: JSON.stringify(payload)
        }).then((res) => {
          if (!res.ok) throw new Error('sync ' + res.status);
        }).catch((err) => {
          console.warn('[gqy] 会话镜像失败：' + err.message);
        })
      );
    }, delay == null ? 1200 : delay);
  }

  /* GitHub 只读账户：auth URL / 当前账户 / 断开 */
  async function githubAuth() {
    const res = await fetch('/api/github/auth', { headers: { accept: 'application/json' } });
    if (!res.ok) throw new Error('GitHub 未配置');
    return res.json();
  }

  async function githubMe() {
    const res = await fetch('/api/github/me', { headers: { accept: 'application/json' } });
    if (!res.ok) return { connected: false };
    return res.json();
  }

  async function githubDisconnect() {
    await fetch('/api/github', { method: 'DELETE' });
  }

  /* 供应商列表（导入的 providers.json） */
  async function providers() {
    const res = await fetch('/api/providers', { headers: { accept: 'application/json' } });
    if (!res.ok) return [];
    const data = await res.json();
    return data.providers || [];
  }

  /* 插件与工具 */
  async function plugins() {
    const res = await fetch('/api/plugins', { headers: { accept: 'application/json' }, cache: 'no-store' });
    if (!res.ok) throw new Error('插件列表获取失败');
    const data = await res.json();
    return data.plugins || [];
  }

  async function pluginLoad(id) {
    const res = await fetch('/api/plugins/' + encodeURIComponent(id) + '/load', { method: 'POST' });
    if (!res.ok) throw new Error((await res.json().catch(() => ({}))).message || '加载失败');
    return res.json();
  }

  async function pluginUnload(id) {
    const res = await fetch('/api/plugins/' + encodeURIComponent(id) + '/unload', { method: 'POST' });
    if (!res.ok) throw new Error((await res.json().catch(() => ({}))).message || '卸载失败');
    return res.json();
  }

  async function pluginCall(id, action, args) {
    const res = await fetch('/api/plugins/' + encodeURIComponent(id) + '/call', {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ action, arguments: args || {} })
    });
    if (!res.ok) throw new Error((await res.json().catch(() => ({}))).message || '调用失败');
    return res.json();
  }

  async function tools() {
    const res = await fetch('/api/tools', { headers: { accept: 'application/json' }, cache: 'no-store' });
    if (!res.ok) throw new Error('工具列表获取失败');
    return res.json();
  }

  async function setExternal(enabled) {
    const res = await fetch('/api/tools/external', {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ enabled: !!enabled })
    });
    if (!res.ok) throw new Error('开关失败');
    return res.json();
  }

  async function gatewayStatus() {
    const res = await fetch('/api/gateway/status', { headers: { accept: 'application/json' }, cache: 'no-store' });
    if (!res.ok) throw new Error('网关状态获取失败');
    return res.json();
  }

  G.api = {
    detect, available, info, chat, models, usage, providers,
    plugins, pluginLoad, pluginUnload, pluginCall, tools, setExternal, gatewayStatus,
    syncSession, githubAuth, githubMe, githubDisconnect
  };
})();
