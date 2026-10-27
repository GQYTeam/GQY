/* 顾清影 · 关键词浮窗：本地术语表，不联网、不调用模型 */
(() => {
  'use strict';
  const G = window.GQY;
  const app = G.app;

  const DEFAULTS = [
    { term: 'RAG', definition: '检索增强生成：先从知识库检索相关内容，再把检索结果放进提示词，让模型基于材料回答。' },
    { term: 'embedding', definition: '把文本、图片等映射成向量；语义相近的内容向量距离更近，用于检索、聚类和去重。' },
    { term: 'token', definition: '模型处理文本的基本单位，可能是字、词或词的一部分。计费和上下文长度都按 token 算。' },
    { term: '上下文窗口', aliases: ['context window'], definition: '模型一次请求能看到的 token 上限，包括系统提示、历史消息、工具结果和本次回复。' },
    { term: 'prompt', definition: '发给模型的输入文本，通常包含系统提示、用户消息和工具返回结果。' },
    { term: 'fine-tuning', aliases: ['微调'], definition: '在已有模型上用特定数据继续训练，让输出更贴近某个领域或风格。' },
    { term: '向量数据库', aliases: ['vector database'], definition: '存储 embedding 并支持相似度检索的数据库，常用于 RAG 和语义搜索。' },
    { term: 'function calling', aliases: ['工具调用', 'tool calling'], definition: '模型输出结构化的工具名和参数，由宿主执行后把结果回填给模型继续推理。' },
    { term: 'JSON Schema', definition: '描述 JSON 数据结构的规范；工具参数用它声明字段、类型和必填项。' },
    { term: 'MCP', aliases: ['Model Context Protocol'], definition: '一个让模型通过标准协议访问外部工具和数据源的协议；本项目的插件运行时支持 MCP stdio。' },
    { term: 'SSE', aliases: ['Server-Sent Events'], definition: '服务器向浏览器单向推送事件的 HTTP 技术；流式回复和工具事件用它传输。' },
    { term: 'agent', aliases: ['智能体'], definition: '能拆解任务、调用工具、根据结果继续行动的模型程序。' },
    { term: 'temperature', aliases: ['温度'], definition: '采样随机性参数；越低越稳定，越高越发散。评测通常固定为 0 或很低。' },
    { term: '幻觉', aliases: ['hallucination'], definition: '模型生成看似合理但事实错误或没有依据的内容。工具结果和检索能降低它。' },
    { term: 'plugin', aliases: ['插件'], definition: '在宿主进程之外运行、通过协议暴露动作的程序；本项目用 manifest + stdio JSON-RPC 管理。' },
    { term: 'gateway', aliases: ['网关'], definition: '统一接收请求、选择 provider/model、处理失败转移和日志的中间层。' },
    { term: 'provider', aliases: ['供应商'], definition: '提供模型 API 的上游服务；本项目通过 OpenAI 兼容协议接入。' },
    { term: '路由组', aliases: ['routing group'], definition: '把多个 provider/model 当成一个模型使用，按 order / rotate / usage 策略选择。' },
    { term: 'affinity', aliases: ['会话亲和'], definition: '让同一会话尽量落在同一个 provider 上，复用上游 prompt cache。' },
    { term: 'eval', aliases: ['评测'], definition: '用固定用例和规则比较不同提示词、工具说明、返回值渲染的效果。' }
  ];

  function esc(s) {
    return String(s).replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
  }

  function all() {
    const custom = Array.isArray(app.state.glossary) ? app.state.glossary : [];
    const seen = new Map();
    DEFAULTS.concat(custom).forEach((entry) => {
      if (!entry || !entry.term || !entry.definition) return;
      const key = String(entry.term).toLowerCase();
      if (!seen.has(key)) seen.set(key, entry);
    });
    return Array.from(seen.values());
  }

  function find(term) {
    const key = String(term || '').toLowerCase();
    return all().find((e) => String(e.term).toLowerCase() === key) ||
      all().find((e) => (e.aliases || []).some((a) => String(a).toLowerCase() === key)) || null;
  }

  function pattern() {
    const terms = all();
    const parts = [];
    terms.forEach((entry) => {
      [entry.term].concat(entry.aliases || []).forEach((raw) => {
        if (!raw) return;
        const t = String(raw);
        const safe = t.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
        const ascii = /^[A-Za-z0-9 +.#/_-]+$/.test(t);
        parts.push({ raw: t, source: ascii ? '(?<![A-Za-z0-9])' + safe + '(?![A-Za-z0-9])' : safe });
      });
    });
    parts.sort((a, b) => b.raw.length - a.raw.length);
    return parts.length ? new RegExp(parts.map((p) => '(' + p.source + ')').join('|'), 'gi') : null;
  }

  let cachedPattern = null;
  let cachedCount = -1;

  function decorate(html) {
    if (!html) return html;
    const terms = all();
    if (cachedCount !== terms.length || !cachedPattern) {
      cachedPattern = pattern();
      cachedCount = terms.length;
    }
    if (!cachedPattern) return html;
    const re = cachedPattern;
    let skipDepth = 0;
    return String(html).split(/(<[^>]*>)/).map((part) => {
      if (!part) return part;
      if (part[0] === '<') {
        const tag = part.toLowerCase();
        if (/^<(code|pre|a|button)\b/.test(tag) && !/\/>$/.test(tag)) skipDepth += 1;
        else if (/^<\/(code|pre|a|button)>/.test(tag)) skipDepth = Math.max(0, skipDepth - 1);
        return part;
      }
      if (skipDepth > 0) return part;
      return part.replace(re, (match) => {
        const entry = find(match);
        if (!entry) return match;
        return '<button type="button" class="term" data-term="' + esc(entry.term) + '">' + match + '</button>';
      });
    }).join('');
  }

  let lastTrigger = null;

  function close() {
    if (!app.el.termPop || app.el.termPop.hidden) return;
    app.el.termPop.hidden = true;
    if (lastTrigger && lastTrigger.isConnected) lastTrigger.focus({ preventScroll: true });
    lastTrigger = null;
  }

  function open(term, trigger) {
    const entry = find(term);
    if (!entry || !app.el.termPop) return;
    lastTrigger = trigger || null;
    app.el.termPopTitle.textContent = entry.term;
    app.el.termPopBody.textContent = entry.definition;
    const pop = app.el.termPop;
    pop.hidden = false;
    const r = trigger ? trigger.getBoundingClientRect() : { left: 24, bottom: 80, top: 64 };
    const w = pop.offsetWidth;
    const h = pop.offsetHeight;
    let left = Math.min(window.innerWidth - w - 12, Math.max(12, r.left));
    let top = r.bottom + 8;
    if (top + h > window.innerHeight - 12) top = Math.max(12, r.top - h - 8);
    pop.style.left = left + 'px';
    pop.style.top = top + 'px';
  }

  G.glossary = { all, find, decorate, open, close, defaults: DEFAULTS };

  if (app.el.termPopClose) app.el.termPopClose.addEventListener('click', close);
  document.addEventListener('click', (e) => {
    if (!app.el.termPop || app.el.termPop.hidden) return;
    if (e.target.closest('#termPop') || e.target.closest('[data-term]')) return;
    close();
  });
  document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape' && app.el.termPop && !app.el.termPop.hidden) {
      e.stopPropagation();
      close();
    }
  });
})();
