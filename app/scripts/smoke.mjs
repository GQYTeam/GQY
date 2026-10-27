#!/usr/bin/env node
/* 顾清影 · 冒烟测试（零依赖）
 *
 * 起一个 mock 服务端 + 无头 Chrome，跑一遍真实链路：
 *   启动 → 发送 → SSE 流式 → 会话落盘 → 设置 → 快捷键 → XSS 拦截
 *
 * 用法：npm run smoke        需要本机装有 Chrome / Chromium（或设置 CHROME 环境变量）
 */
import { spawn, execFile } from 'node:child_process';
import http from 'node:http';
import { mkdtemp, rm, readFile, readdir } from 'node:fs/promises';
import { existsSync, cpSync, mkdirSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const ROOT = dirname(fileURLToPath(import.meta.url)) + '/..';
const PORT = 5199 + (process.pid % 200);
const BASE = 'http://127.0.0.1:' + PORT;
const HOME = await mkdtemp(join(tmpdir(), 'gqy-smoke-'));
const PROFILE = await mkdtemp(join(tmpdir(), 'gqy-chrome-'));
const DBG = PORT + 1000;

const CHROME_CANDIDATES = [
  process.env.CHROME,
  '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
  '/Applications/Chromium.app/Contents/MacOS/Chromium',
  '/usr/bin/google-chrome',
  '/usr/bin/chromium',
  '/usr/bin/chromium-browser'
].filter(Boolean);

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const consoleErrors = [];
const results = [];
let failed = 0;

function check(name, ok, detail) {
  results.push({ name, ok, detail });
  if (!ok) failed += 1;
  console.log((ok ? '  ok   ' : '  FAIL ') + name + (ok || detail == null ? '' : '  →  ' + detail));
}

async function waitFor(check, timeout, label) {
  const started = Date.now();
  while (Date.now() - started < timeout) {
    try { const value = await check(); if (value) return value; } catch (err) {}
    await sleep(150);
  }
  throw new Error('超时：' + label);
}

const chromePath = CHROME_CANDIDATES.find((p) => existsSync(p));
if (!chromePath) {
  console.log('跳过：没找到 Chrome / Chromium。设置 CHROME=/path/to/chrome 后重跑。');
  process.exit(0);
}

function run(cmd, args, opts) {
  return new Promise((resolve, reject) => {
    execFile(cmd, args, Object.assign({ cwd: ROOT }, opts), (err, stdout, stderr) => {
      if (err) reject(new Error((stderr || stdout || err.message).trim()));
      else resolve(stdout);
    });
  });
}

async function buildServer() {
  try {
    await run('go', ['version']);
  } catch (err) {
    console.log('跳过：没找到 go。安装 Go >= 1.22 后重跑（npm start 也依赖它）。');
    process.exit(0);
  }
  const out = join(PROFILE, 'gqy-server');
  await run('go', ['build', '-o', out, './cmd/gqy-server']);
  return out;
}

let serverBin = null;
let server = null;
let chrome = null;
let ws = null;
const extras = [];

async function cleanup() {
  try { if (ws) ws.close(); } catch (err) {}
  try { if (chrome) chrome.kill('SIGKILL'); } catch (err) {}
  try { if (server) server.kill('SIGTERM'); } catch (err) {}
  for (const proc of extras) { try { proc.kill('SIGTERM'); } catch (err) {} }
  await sleep(120);
  try { await rm(PROFILE, { recursive: true, force: true }); } catch (err) {}
  try { await rm(HOME, { recursive: true, force: true }); } catch (err) {}
  try { await rm(HOME + '-real', { recursive: true, force: true }); } catch (err) {}
}

try {
  serverBin = await buildServer();
  console.log('  编译 cmd/gqy-server: ok');

  try {
    mkdirSync(join(HOME, 'plugins'), { recursive: true });
    cpSync(join(ROOT, 'plugins', 'echo'), join(HOME, 'plugins', 'echo'), { recursive: true });
  } catch (err) {
    console.log('  跳过示例插件复制：' + err.message);
  }

  server = spawn(serverBin, [], {
    cwd: ROOT,
    env: Object.assign({}, process.env, {
      PORT: String(PORT), HOST: '127.0.0.1', GQY_MOCK: '1', GQY_API_KEY: '', GQY_HOME: HOME
    }),
    stdio: ['ignore', 'pipe', 'pipe']
  });
  const serverLog = [];
  server.stdout.on('data', (d) => serverLog.push(String(d)));
  server.stderr.on('data', (d) => serverLog.push(String(d)));

  const health = await waitFor(async () => {
    const res = await fetch(BASE + '/api/health');
    return res.ok ? res.json() : null;
  }, 8000, 'server /api/health').catch((err) => {
    console.error(serverLog.join(''));
    throw err;
  });
  check('服务端健康检查', health.ok === true && health.provider === 'mock', JSON.stringify(health));

  /* 静态资源白名单：后端源码 / 包描述 / 脚本目录不可访问 */
  const blocked = await Promise.all(['/server.go', '/package.json', '/scripts/smoke.mjs', '/.env']
    .map((p) => fetch(BASE + p).then((r) => r.status)));
  check('静态白名单挡住源码与脚本', blocked.every((code) => code === 404), blocked.join(','));

  const wrongMethod = await fetch(BASE + '/api/chat').then((r) => r.status);
  check('错误方法返回 405', wrongMethod === 405, String(wrongMethod));

  /* 模型目录：/api/models 返回 models.dev 精简结果（聊天模型） */
  const modelsRes = await fetch(BASE + '/api/models').then((r) => r.json());
  check('模型目录返回非空且过滤非聊天模型',
    Array.isArray(modelsRes.models) && modelsRes.models.length > 0 &&
    modelsRes.models.every((m) => m && typeof m.id === 'string' && typeof m.name === 'string') &&
    modelsRes.models.some((m) => /deepseek/i.test(m.id + m.provider)),
    JSON.stringify({ count: modelsRes.count, sample: modelsRes.models[0] }));

  /* 用量：/api/usage 返回真实聚合结构 */
  const usageRes = await fetch(BASE + '/api/usage').then((r) => r.json());
  check('用量接口返回聚合结构',
    usageRes && usageRes.usage && typeof usageRes.usage.sessions === 'number' &&
    Array.isArray(usageRes.usage.days) && usageRes.usage.days.length === 7,
    JSON.stringify(usageRes));

  chrome = spawn(chromePath, [
    '--headless=new', '--disable-gpu', '--no-first-run', '--no-default-browser-check',
    '--user-data-dir=' + PROFILE, '--remote-debugging-port=' + DBG,
    '--window-size=1280,900', 'about:blank'
  ], { stdio: 'ignore' });

  const target = await waitFor(async () => {
    const res = await fetch('http://127.0.0.1:' + DBG + '/json/list');
    const list = await res.json();
    return list.find((t) => t.type === 'page') || null;
  }, 12000, 'chrome devtools');

  ws = new WebSocket(target.webSocketDebuggerUrl);
  const pending = new Map();
  let seq = 0;

  ws.onmessage = (ev) => {
    const msg = JSON.parse(ev.data);
    if (msg.id && pending.has(msg.id)) { pending.get(msg.id)(msg); pending.delete(msg.id); return; }
    if (msg.method === 'Runtime.exceptionThrown') {
      const d = msg.params.exceptionDetails;
      consoleErrors.push('exception: ' + ((d.exception && d.exception.description) || d.text));
    }
    if (msg.method === 'Runtime.consoleAPICalled' && msg.params.type === 'error') {
      consoleErrors.push('console.error: ' + msg.params.args.map((a) => a.value || a.description).join(' '));
    }
    if (msg.method === 'Log.entryAdded' && msg.params.entry.level === 'error') {
      consoleErrors.push('log: ' + msg.params.entry.text);
    }
  };
  await new Promise((resolve, reject) => {
    ws.onopen = resolve;
    ws.onerror = () => reject(new Error('CDP 连接失败'));
  });

  const send = (method, params = {}) => new Promise((resolve) => {
    const id = ++seq;
    pending.set(id, resolve);
    ws.send(JSON.stringify({ id, method, params }));
  });

  const evaluate = async (expression) => {
    const reply = await send('Runtime.evaluate', {
      expression, awaitPromise: true, returnByValue: true, userGesture: true
    });
    const r = reply.result || {};
    if (r.exceptionDetails) {
      throw new Error((r.exceptionDetails.exception && r.exceptionDetails.exception.description) || r.exceptionDetails.text);
    }
    return r.result ? r.result.value : undefined;
  };

  await send('Runtime.enable');
  await send('Log.enable');
  await send('Page.enable');
  await send('Page.navigate', { url: BASE + '/index.html' });
  await waitFor(() => evaluate('document.readyState === "complete" && !!document.querySelector("#composerInput")'), 10000, '页面加载');

  check('首屏渲染空会话', await waitFor(async () => (await evaluate('document.querySelectorAll(".msg").length')) === 0, 5000, 'empty thread'));
  check('服务端状态已探测', await waitFor(async () => (await evaluate('document.querySelector("#serverStatus").textContent')).includes('已连接'), 5000, 'server status'));

  /* 新会话 → 真实 SSE 链路 */
  await evaluate('document.querySelector("#newChatBtn").click()');
  await sleep(200);
  await evaluate(`(() => {
    const input = document.querySelector('#composerInput');
    input.value = '写一个 debounce 函数，并说明容易踩的坑';
    input.dispatchEvent(new Event('input', { bubbles: true }));
    document.querySelector('#composerForm').requestSubmit();
    return true;
  })()`);

  await waitFor(() => evaluate('!document.querySelector("#sendBtn").classList.contains("is-stop") && document.querySelectorAll(".msg--ai").length >= 1'), 25000, 'SSE 流式结束');

  const turn = await evaluate(`(() => {
    const last = [...document.querySelectorAll('.msg--ai')].pop();
    return {
      text: last.querySelector('.md') ? last.querySelector('.md').textContent.trim().length : 0,
      think: !!last.querySelector('.think'),
      trace: !!last.querySelector('.trace'),
      error: !!last.querySelector('.msg-error'),
      sendLabel: document.querySelector('#sendBtn').getAttribute('aria-label'),
      content: last.textContent.slice(0, 60)
    };
  })()`);
  check('流式回复有正文', turn.text > 20, JSON.stringify(turn));
  check('思考与工具调用都渲染', turn.think && turn.trace, JSON.stringify(turn));
  check('无错误态，发送键复位', !turn.error && turn.sendLabel === '发送', JSON.stringify(turn));

  /* 会话落盘：$GQY_HOME/sessions/<id>/... */
  const sessionFile = await waitFor(async () => {
    const sessionsDir = join(HOME, 'sessions');
    let ids = [];
    try { ids = await readdir(sessionsDir); } catch (err) { return null; }
    for (const id of ids) {
      try {
        const raw = await readFile(join(sessionsDir, id, 'io', 'context', 'messages.jsonl'), 'utf8');
        if (raw.includes('debounce')) {
          const tools = await readdir(join(sessionsDir, id, 'logs', 'tools')).catch(() => []);
          return { id, tools: tools.length, manifest: existsSync(join(sessionsDir, id, 'manifest.json')) };
        }
      } catch (err) {}
    }
    return null;
  }, 8000, '会话镜像落盘').catch(() => null);
  check('会话镜像落盘（messages.jsonl / logs / manifest）',
    !!sessionFile && sessionFile.manifest,
    JSON.stringify(sessionFile));

  /* XSS：javascript: 链接必须降级成纯文本 */
  const md = await evaluate(`GQY.renderMarkdown('[点我](javascript:alert(1))\\n\\n![图](javascript:alert(2))')`);
  check('Markdown 拦截 javascript: 链接', !/javascript:/i.test(md) && /点我/.test(md), md);

  /* 设置：十个 tab 都要能打开 */
  await evaluate('document.querySelector("#accountBtn").click()');
  await sleep(300);
  const tabs = await evaluate(`(() => {
    const out = [];
    for (const tab of document.querySelectorAll('.settings-tab')) {
      tab.click();
      const name = tab.dataset.settingsTab;
      const panel = document.querySelector('[data-settings-panel="' + name + '"]');
      out.push(name + ':' + (panel && !panel.hidden));
    }
    return out;
  })()`);
  check('设置十个 tab 全部可切换', tabs.length === 10 && tabs.every((t) => t.endsWith(':true')), tabs.join(' '));
  const dataTab = await evaluate('!!document.querySelector("#importData") && !!document.querySelector("#exportData") && !!document.querySelector("#clearAll")');
  check('通用页有导入 / 导出 / 清空', dataTab === true);
  // 搜索过滤
  const searchFilter = await evaluate(`(() => {
    const input = document.querySelector('#settingsSearch');
    input.value = '人设';
    input.dispatchEvent(new Event('input', { bubbles: true }));
    const hidden = [...document.querySelectorAll('.settings-tab')].filter((t) => t.hidden).length;
    return { hidden, total: document.querySelectorAll('.settings-tab').length };
  })()`);
  check('设置搜索过滤生效', searchFilter.hidden > 0 && searchFilter.hidden < searchFilter.total, JSON.stringify(searchFilter));
  await evaluate(`(() => { const i = document.querySelector('#settingsSearch'); i.value=''; i.dispatchEvent(new Event('input',{bubbles:true})); })()`);
  await evaluate('document.querySelector("#settingsLayer [data-settings-close]").click()');
  await sleep(200);

  /* 用量独立浮层 + 扩展占位 */
  const usageLayer = await evaluate(`(() => {
    document.querySelector('#usageTopBtn').click();
    const opened = !document.querySelector('#usageLayer').hidden;
    document.querySelector('[data-usage-close]').click();
    const closed = document.querySelector('#usageLayer').hidden;
    document.querySelector('#extensionsBtn').click();
    const extOpened = !document.querySelector('#extensionsLayer').hidden;
    document.querySelector('[data-ext-close]').click();
    return { opened, closed, extOpened };
  })()`);
  check('用量浮层可开可关、扩展入口占位可开', usageLayer.opened && usageLayer.closed && usageLayer.extOpened, JSON.stringify(usageLayer));

  /* 快捷键 */
  const shortcuts = await evaluate(`(() => {
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'k', metaKey: true, bubbles: true, cancelable: true }));
    const searchOpen = !document.querySelector('#searchWrap').hidden;
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true }));
    const searchClosed = document.querySelector('#searchWrap').hidden;
    const before = document.querySelectorAll('.conv-row').length;
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'n', altKey: true, bubbles: true, cancelable: true }));
    return { searchOpen, searchClosed, grew: document.querySelectorAll('.conv-row').length >= before };
  })()`);
  check('快捷键 ⌘K / Esc / Alt+N', shortcuts.searchOpen && shortcuts.searchClosed && shortcuts.grew, JSON.stringify(shortcuts));

  /* 草稿：切换会话不丢 */
  const draft = await evaluate(`(async () => {
    document.querySelector('#newChatBtn').click();
    const input = document.querySelector('#composerInput');
    input.value = '草稿内容 ABC';
    input.dispatchEvent(new Event('input', { bubbles: true }));
    const rows = () => [...document.querySelectorAll('.conv-row')];
    const first = rows()[0].dataset.conv;
    rows()[1].click();
    await new Promise((r) => setTimeout(r, 150));
    const cleared = document.querySelector('#composerInput').value;
    document.querySelector('[data-conv="' + first + '"]').click();
    await new Promise((r) => setTimeout(r, 150));
    return { cleared, restored: document.querySelector('#composerInput').value };
  })()`);
  check('切换会话保留草稿', draft.cleared === '' && draft.restored === '草稿内容 ABC', JSON.stringify(draft));

  /* 会话管理：重命名 / 置顶 / 批量删除 */
  const renamed = await evaluate(`(async () => {
    const app = GQY.app;
    const target = app.state.workspaces.flatMap((w) => w.convs).find((c) => c.messages && c.messages.length);
    if (!target) return { error: 'no message conv' };
    const row = document.querySelector('[data-conv="' + target.id + '"]');
    if (!row) return { error: 'no row' };
    row.click();
    await new Promise((r) => setTimeout(r, 120));
    const titleNode = document.querySelector('.conv-row.active .conv-title');
    if (!titleNode) return { error: 'no title' };
    titleNode.dispatchEvent(new MouseEvent('dblclick', { bubbles: true }));
    await new Promise((r) => setTimeout(r, 80));
    const input = document.querySelector('.conv-rename');
    if (!input) return { error: 'no rename input' };
    input.value = '重命名冒烟会话';
    input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true }));
    await new Promise((r) => setTimeout(r, 160));
    const saved = JSON.parse(localStorage.getItem('claude-webchat:v7'));
    const conv = saved.workspaces.flatMap((w) => w.convs).find((c) => c.id === target.id);
    return {
      id: target.id,
      title: conv && conv.title,
      topbar: document.querySelector('#topbarTitle').textContent,
      inputGone: !document.querySelector('.conv-rename')
    };
  })()`);
  check('重命名会话并持久化到标题',
    renamed.title === '重命名冒烟会话' && renamed.topbar === '重命名冒烟会话' && renamed.inputGone,
    JSON.stringify(renamed));

  const pinned = await evaluate(`(async () => {
    const app = GQY.app;
    const ws = app.state.workspaces.find((w) => w.convs.some((c) => c.id === '${renamed.id}'));
    app.togglePin('${renamed.id}');
    await new Promise((r) => setTimeout(r, 100));
    const saved = JSON.parse(localStorage.getItem('claude-webchat:v7'));
    const conv = saved.workspaces.flatMap((w) => w.convs).find((c) => c.id === '${renamed.id}');
    return {
      pinned: !!(conv && conv.pinned),
      first: !!ws && ws.convs[0].id === '${renamed.id}',
      icon: !!document.querySelector('.conv-row.is-pinned .conv-pin')
    };
  })()`);
  check('置顶持久化并排在同工作区最前',
    pinned.pinned === true && pinned.first === true && pinned.icon === true,
    JSON.stringify(pinned));

  const exported = await evaluate(`(() => {
    const app = GQY.app;
    const found = app.findConv('${renamed.id}');
    if (!found) return { error: 'conv gone' };
    const md = app.convMarkdown(found.conv, found.ws);
    const name = app.safeFilename('项目/会话:草稿*?', found.conv.id, 'json');
    return {
      title: md.indexOf('# 重命名冒烟会话') === 0,
      roles: md.indexOf('## 用户') >= 0 && md.indexOf('## 助手') >= 0,
      trace: md.indexOf('**工具轨迹**') >= 0,
      safe: !/[\\\\/:*?"<>|]/.test(name) && /\\.json$/.test(name),
      name
    };
  })()`);
  check('单会话导出内容与文件名安全',
    exported.title === true && exported.roles === true && exported.trace === true && exported.safe === true,
    JSON.stringify(exported));

  const batch = await evaluate(`(async () => {
    const app = GQY.app;
    document.querySelector('#newChatBtn').click();
    await new Promise((r) => setTimeout(r, 120));
    const stateCount = app.state.workspaces.reduce((n, w) => n + w.convs.length, 0);
    document.querySelector('#manageBtn').click();
    await new Promise((r) => setTimeout(r, 120));
    const before = document.querySelectorAll('.conv-check').length;
    const first = document.querySelector('.conv-check');
    if (!first) return { error: 'no checkbox', stateCount, before };
    first.click();
    const count = document.querySelector('#manageCount').textContent;
    window.confirm = () => true;
    document.querySelector('#manageDelete').click();
    await new Promise((r) => setTimeout(r, 200));
    return {
      stateCount,
      before,
      after: document.querySelectorAll('.conv-row').length,
      count,
      manageGone: document.querySelector('#manageBar').hidden
    };
  })()`);
  check('批量删除选中会话并退出管理',
    batch.before === batch.stateCount && batch.after === batch.stateCount - 1 &&
    batch.after >= 2 && batch.count === '已选 1' && batch.manageGone === true,
    JSON.stringify(batch));

  /* 导入：导出结构能原样回灌 */
  const imported = await evaluate(`(async () => {
    const data = JSON.parse(localStorage.getItem('claude-webchat:v7'));
    data.workspaces[0].name = 'imported-ws';
    window.confirm = () => true;
    const dt = new DataTransfer();
    dt.items.add(new File([JSON.stringify(data)], 'backup.json', { type: 'application/json' }));
    const input = document.querySelector('#importInput');
    input.files = dt.files;
    input.dispatchEvent(new Event('change'));
    await new Promise((r) => setTimeout(r, 800));
    return { named: document.querySelector('#wsList').textContent.includes('imported-ws'), convs: document.querySelectorAll('.conv-row').length };
  })()`);
  check('导入 JSON 覆盖并重载状态', imported.named && imported.convs >= 2, JSON.stringify(imported));

  /* 产物预览：HTML 默认预览、iframe 沙箱 + CSP、源码互切 */
  const preview = await evaluate(`(async () => {
    const trigger = document.querySelector('#newChatBtn');
    GQY.app.openViewer({
      name: 'demo.html',
      path: '~/项目/demo.html',
      lang: 'html',
      content: '<!doctype html><html><body><h1>预览测试</h1></body></html>',
      lines: 1
    }, trigger);
    await new Promise((r) => setTimeout(r, 80));
    const seg = document.querySelector('#lbViewSeg');
    const frame = document.querySelector('#lbFrame');
    const previewBtn = seg.querySelector('[data-lb-view="preview"]');
    const sourceBtn = seg.querySelector('[data-lb-view="source"]');
    const opened = !document.querySelector('#lightbox').hidden;
    const defaultPreview = !seg.hidden && previewBtn.getAttribute('aria-pressed') === 'true' &&
      document.querySelector('#lbPreview').hidden === false && frame.hidden === false;
    const sandbox = frame.getAttribute('sandbox') || '';
    const srcdoc = frame.getAttribute('srcdoc') || '';
    const csp = /Content-Security-Policy/.test(srcdoc) && /default-src 'none'/.test(srcdoc);
    sourceBtn.click();
    const sourceShown = document.querySelector('#lbCode').hidden === false &&
      document.querySelector('#lbPreview').hidden === true &&
      sourceBtn.getAttribute('aria-pressed') === 'true' &&
      previewBtn.getAttribute('aria-pressed') === 'false';
    const hasCode = document.querySelector('#lbCode').textContent.includes('预览测试');
    previewBtn.click();
    const backToPreview = document.querySelector('#lbPreview').hidden === false &&
      document.querySelector('#lbCode').hidden === true && frame.hidden === false;
    document.querySelector('#lightbox [data-lb-close]').click();
    const closed = document.querySelector('#lightbox').hidden === true;
    return { opened, defaultPreview, sandbox, csp, sourceShown, hasCode, backToPreview, closed };
  })()`);
  check('产物预览：默认预览 / iframe 沙箱 + CSP / 源码切换',
    preview.opened && preview.defaultPreview && preview.sandbox === 'allow-scripts' &&
    !/allow-same-origin/.test(preview.sandbox) && preview.csp && preview.sourceShown &&
    preview.hasCode && preview.backToPreview && preview.closed,
    JSON.stringify(preview));

  /* 产物预览：SVG 无脚本沙箱；超过 2 MB 降级为源码并给出原因 */
  const previewEdge = await evaluate(`(async () => {
    GQY.app.openViewer({
      name: 'icon.svg',
      path: '~/项目/icon.svg',
      lang: 'svg',
      content: '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10"><circle cx="5" cy="5" r="4"/></svg>',
      lines: 1
    }, document.querySelector('#newChatBtn'));
    await new Promise((r) => setTimeout(r, 60));
    const frame = document.querySelector('#lbFrame');
    const sandbox = frame.getAttribute('sandbox');
    const srcdoc = frame.getAttribute('srcdoc') || '';
    const svgOk = !document.querySelector('#lbViewSeg').hidden && sandbox === '' &&
      /<svg/.test(srcdoc) && !/allow-scripts/.test(sandbox);
    GQY.app.openViewer({
      name: 'big.md',
      path: '~/项目/big.md',
      lang: 'md',
      content: 'x'.repeat(2 * 1024 * 1024 + 64),
      lines: 1
    }, document.querySelector('#newChatBtn'));
    await new Promise((r) => setTimeout(r, 60));
    const bigSegHidden = document.querySelector('#lbViewSeg').hidden === true;
    const bigSource = document.querySelector('#lbCode').hidden === false &&
      document.querySelector('#lbPreview').hidden === true;
    const bigReason = /2 MB/.test(document.querySelector('#lbBarTag').textContent);
    document.querySelector('#lightbox [data-lb-close]').click();
    return { svgOk, bigSegHidden, bigSource, bigReason };
  })()`);
  check('产物预览：SVG 沙箱无脚本 + 超过 2 MB 降级',
    previewEdge.svgOk && previewEdge.bigSegHidden && previewEdge.bigSource && previewEdge.bigReason,
    JSON.stringify(previewEdge));

  /* Markdown 预览复用渲染器与链接白名单 */
  const mdPreview = await evaluate(`(async () => {
    GQY.app.openViewer({
      name: 'notes.md',
      path: '~/项目/notes.md',
      lang: 'md',
      content: '**粗体** [坏链](javascript:alert(1)) <script>window.__x=1<\\/script>',
      lines: 1
    }, document.querySelector('#newChatBtn'));
    await new Promise((r) => setTimeout(r, 60));
    const md = document.querySelector('#lbMd');
    const shown = document.querySelector('#lbPreview').hidden === false && md.hidden === false &&
      document.querySelector('#lbViewSeg').querySelector('[data-lb-view="preview"]').getAttribute('aria-pressed') === 'true';
    const html = md.innerHTML;
    const safe = /<strong>粗体<\\/strong>/.test(html) && !/javascript:/i.test(html) && !/<script/i.test(html);
    document.querySelector('#lightbox [data-lb-close]').click();
    return { shown, safe };
  })()`);
  check('产物预览：Markdown 复用渲染器并拦截危险链接', mdPreview.shown && mdPreview.safe, JSON.stringify(mdPreview));

  /* 灯箱在三个关键宽度下不横向溢出 */
  const widthChecks = [];
  for (const width of [390, 600, 1100]) {
    await send('Emulation.setDeviceMetricsOverride', { width, height: 820, deviceScaleFactor: 1, mobile: width <= 600 });
    await sleep(90);
    widthChecks.push(await evaluate(`(() => {
      GQY.app.openViewer({
        name: 'responsive.html', path: '~/项目/responsive.html', lang: 'html',
        content: '<h1>宽度检查</h1>', lines: 1
      }, document.querySelector('#newChatBtn'));
      const panel = document.querySelector('#lightbox .lb-panel');
      const top = document.querySelector('#lightbox .lb-top');
      const bar = document.querySelector('#lightbox .lb-bar');
      const result = {
        panel: panel.scrollWidth <= panel.clientWidth + 1,
        top: top.scrollWidth <= top.clientWidth + 1,
        bar: bar.scrollWidth <= bar.clientWidth + 1
      };
      document.querySelector('#lightbox [data-lb-close]').click();
      return result;
    })()`));
  }
  await send('Emulation.clearDeviceMetricsOverride');
  await sleep(90);
  check('灯箱在 390 / 600 / 1100 宽度不溢出',
    widthChecks.every((x) => x.panel && x.top && x.bar), JSON.stringify(widthChecks));

  /* 新功能：引导 / 看板 / 术语 / 设置页 / 插件 */
  const onboarding = await evaluate(`(() => {
    const el = document.querySelector('#onboarding');
    const shown = !!el && !el.hidden;
    document.querySelector('#onboardSkip').click();
    const saved = JSON.parse(localStorage.getItem('claude-webchat:v7') || '{}');
    return { shown, hidden: el.hidden, onboarded: saved.onboarded === true };
  })()`);
  check('首次引导可跳过', onboarding.shown && onboarding.hidden && onboarding.onboarded, JSON.stringify(onboarding));

  const board = await evaluate(`(() => {
    document.querySelector('#boardBtn').click();
    const opened = !document.querySelector('#boardView').hidden;
    const input = document.querySelector('#boardInput');
    input.value = '测试卡片';
    document.querySelector('#boardForm').requestSubmit();
    const cards = document.querySelectorAll('.board-card').length;
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true }));
    return { opened, cards, back: document.querySelector('#boardView').hidden };
  })()`);
  check('看板可打开 / 加卡 / 返回', board.opened && board.cards === 1 && board.back, JSON.stringify(board));

  const glossary = await evaluate(`(() => {
    const html = GQY.glossary.decorate('RAG 与 embedding 的 token');
    const code = GQY.glossary.decorate('<code>RAG</code>');
    return {
      rag: html.includes('data-term="RAG"'),
      emb: html.includes('data-term="embedding"'),
      token: html.includes('data-term="token"'),
      codeSkipped: !code.includes('data-term')
    };
  })()`);
  check('术语浮窗装饰正文且跳过代码', glossary.rag && glossary.emb && glossary.token && glossary.codeSkipped, JSON.stringify(glossary));

  await evaluate('document.querySelector("#accountBtn").click()');
  await sleep(250);

  await evaluate('document.querySelector(\'[data-settings-tab="personas"]\').click()');
  await sleep(120);
  const personaAdded = await evaluate(`(() => {
    const form = document.querySelector('[data-persona-form]');
    form.querySelector('[name=name]').value = '测试人设';
    form.querySelector('[name=system]').value = '你是测试人设';
    form.requestSubmit();
    const saved = JSON.parse(localStorage.getItem('claude-webchat:v7') || '{}');
    return saved.personas ? saved.personas.length : 0;
  })()`);
  check('人设可新增', personaAdded >= 2, String(personaAdded));

  await evaluate('document.querySelector(\'[data-settings-tab="memory"]\').click()');
  await sleep(120);
  const memoryAdded = await evaluate(`(() => {
    const form = document.querySelector('[data-memory-form]');
    form.querySelector('[name=text]').value = '我偏好 Go 标准库';
    form.requestSubmit();
    const saved = JSON.parse(localStorage.getItem('claude-webchat:v7') || '{}');
    return saved.memories ? saved.memories.length : 0;
  })()`);
  check('记忆可新增', memoryAdded === 1, String(memoryAdded));

  await evaluate('document.querySelector(\'[data-settings-tab="knowledge"]\').click()');
  await sleep(120);
  const glossaryAdded = await evaluate(`(() => {
    const form = document.querySelector('[data-glossary-form]');
    form.querySelector('[name=term]').value = '测试术语';
    form.querySelector('[name=definition]').value = '测试定义';
    form.requestSubmit();
    const saved = JSON.parse(localStorage.getItem('claude-webchat:v7') || '{}');
    return saved.glossary ? saved.glossary.length : 0;
  })()`);
  check('自定义术语可新增', glossaryAdded === 1, String(glossaryAdded));

  const docAdded = await evaluate(`(() => {
    const form = document.querySelector('[data-doc-form]');
    form.querySelector('[name=title]').value = '测试文档';
    form.querySelector('[name=content]').value = '知识测试内容';
    form.requestSubmit();
    const saved = JSON.parse(localStorage.getItem('claude-webchat:v7') || '{}');
    return saved.knowledge ? saved.knowledge.length : 0;
  })()`);
  check('知识文档可新增', docAdded === 1, String(docAdded));

  const systemCtx = await evaluate(`GQY.app.systemContext('测试文档')`);
  check('system 上下文包含人设/记忆/知识库',
    /【人设】/.test(systemCtx) && /【记忆】/.test(systemCtx) && /【知识库】/.test(systemCtx),
    systemCtx.slice(0, 100));

  await evaluate('document.querySelector(\'[data-settings-tab="tools"]\').click()');
  await sleep(500);
  const toolsPanel = await evaluate(`(() => {
    const panel = document.querySelector('#toolsPanel');
    const hasRead = /read/.test(panel.textContent);
    const sw = panel.querySelector('[data-external-toggle]');
    if (sw) sw.click();
    return { hasRead, hasSwitch: !!sw };
  })()`);
  await sleep(500);
  const externalOn = await evaluate(`(() => {
    const sw = document.querySelector('#toolsPanel [data-external-toggle]');
    return sw ? sw.getAttribute('aria-checked') : '';
  })()`);
  check('工具页列出工具并可切外部注入', toolsPanel.hasRead && toolsPanel.hasSwitch && externalOn === 'true', JSON.stringify({ toolsPanel, externalOn }));

  await evaluate('document.querySelector(\'[data-settings-tab="packages"]\').click()');
  await sleep(500);
  const packagesBefore = await evaluate(`document.querySelector('#packagesPanel').textContent`);
  const pluginLoaded = await evaluate(`(async () => {
    const btn = document.querySelector('[data-plugin-load="echo"]');
    if (!btn) return { error: 'no load button', text: document.querySelector('#packagesPanel').textContent.slice(0, 80) };
    btn.click();
    await new Promise((r) => setTimeout(r, 600));
    const text = document.querySelector('#packagesPanel').textContent;
    const unload = document.querySelector('[data-plugin-unload="echo"]');
    if (unload) unload.click();
    await new Promise((r) => setTimeout(r, 400));
    return { ready: /ready/.test(text), unloaded: /discovered/.test(document.querySelector('#packagesPanel').textContent) };
  })()`);
  check('软件包页发现 / 加载 / 卸载插件',
    /Echo/.test(packagesBefore) && pluginLoaded.ready && pluginLoaded.unloaded,
    JSON.stringify({ packages: packagesBefore.slice(0, 80), pluginLoaded }));
  check('扩展页有安装示例入口', await evaluate('!!document.querySelector("#installExampleBtn")') === true);

  await evaluate('document.querySelector(\'[data-settings-tab="usage"]\').click()');
  await sleep(400);
  const usagePanel = await evaluate(`document.querySelector('#usagePanel').textContent`);
  check('用量页渲染真实数据', /Token/.test(usagePanel) && /本地存储/.test(usagePanel), usagePanel.slice(0, 80));

  await evaluate('document.querySelector(\'[data-settings-tab="accounts"]\').click()');
  await sleep(500);
  const accounts = await evaluate(`document.querySelector('#accountsPanel').textContent`);
  check('账户页渲染本机状态', /本机单用户/.test(accounts) && /GitHub/.test(accounts), accounts.slice(0, 80));

  await evaluate('document.querySelector(\'[data-settings-tab="models"]\').click()');
  await sleep(500);
  const providerAdmin = await evaluate(`(async () => {
    const form = document.querySelector('[data-provider-form]');
    form.querySelector('[name=id]').value = 'smoke';
    form.querySelector('[name=name]').value = 'Smoke';
    form.querySelector('[name=base_url]').value = 'https://example.invalid/v1';
    form.querySelector('[name=default_model]').value = 'smoke-model';
    form.querySelector('[name=api_key]').value = 'test-key';
    form.requestSubmit();
    await new Promise((r) => setTimeout(r, 700));
    const added = /smoke/.test(document.querySelector('#providerList').textContent);
    const groupForm = document.querySelector('[data-group-form]');
    groupForm.querySelector('[name=id]').value = 'smoke-group';
    groupForm.querySelector('[name=strategy]').value = 'order';
    groupForm.querySelector('[name=members]').value = 'smoke/smoke-model';
    groupForm.requestSubmit();
    await new Promise((r) => setTimeout(r, 600));
    const grouped = /smoke-group/.test(document.querySelector('#providerEditor').textContent);
    const delGroup = document.querySelector('[data-group-del="smoke-group"]');
    if (delGroup) delGroup.click();
    await new Promise((r) => setTimeout(r, 300));
    const delProvider = document.querySelector('[data-provider-del="smoke"]');
    if (delProvider) delProvider.click();
    await new Promise((r) => setTimeout(r, 500));
    const removed = !/smoke/.test(document.querySelector('#providerList').textContent);
    return { added, grouped, removed };
  })()`);
  check('provider / 路由组可增删', providerAdmin.added && providerAdmin.grouped && providerAdmin.removed, JSON.stringify(providerAdmin));

  const cleared = await evaluate(`(() => {
    window.confirm = () => true;
    document.querySelector('[data-settings-tab="general"]').click();
    document.querySelector('#clearAll').click();
    const s = JSON.parse(localStorage.getItem('claude-webchat:v7') || '{}');
    return {
      personas: (s.personas || []).length,
      memories: (s.memories || []).length,
      convs: (s.workspaces && s.workspaces[0] && s.workspaces[0].convs || []).length,
      messages: (s.workspaces && s.workspaces[0] && s.workspaces[0].convs[0] && s.workspaces[0].convs[0].messages || []).length
    };
  })()`);
  check('清空对话保留人设/记忆', cleared.personas >= 2 && cleared.memories === 1 && cleared.convs === 1 && cleared.messages === 0, JSON.stringify(cleared));

  await evaluate('document.querySelector("#settingsLayer [data-settings-close]").click()');
  await sleep(200);

  check('浏览器控制台无报错', consoleErrors.length === 0, consoleErrors.slice(0, 3).join(' | '));

  /* 真实上游：agent loop —— 第一轮返回工具调用，第二轮返回最终答案 */
  const upstreamPort = PORT + 400;
  let upstreamCalls = 0;
  const upstream = http.createServer((req, res) => {
    let body = '';
    req.on('data', (d) => { body += d; });
    req.on('end', () => {
      const parsed = JSON.parse(body || '{}');
      upstreamCalls += 1;
      res.writeHead(200, { 'content-type': 'application/json' });
      // 第一次：返回 list_dir 工具调用；第二次：返回最终答案
      if (upstreamCalls === 1) {
        res.end(JSON.stringify({
          choices: [{ message: { role: 'assistant', content: null, tool_calls: [{ id: 'call_1', type: 'function', function: { name: 'list_dir', arguments: '{"path":"."}' } }] } }],
          usage: { prompt_tokens: 3, completion_tokens: 2, total_tokens: 5 }
        }));
      } else {
        res.end(JSON.stringify({
          choices: [{ message: { role: 'assistant', content: '已列出当前目录，模型是 ' + parsed.model } }],
          usage: { prompt_tokens: 4, completion_tokens: 3, total_tokens: 7 }
        }));
      }
    });
  });
  await new Promise((r) => upstream.listen(upstreamPort, '127.0.0.1', r));

  const realPort = PORT + 401;
  const realServer = spawn(serverBin, [], {
    cwd: ROOT,
    env: Object.assign({}, process.env, {
      PORT: String(realPort), HOST: '127.0.0.1',
      GQY_API_KEY: 'test-key', GQY_API_BASE: 'http://127.0.0.1:' + upstreamPort + '/v1',
      GQY_MODEL: 'smoke-model', GQY_MOCK: '', GQY_HOME: HOME + '-real'
    }),
    stdio: 'ignore'
  });
  extras.push(realServer, { kill: () => upstream.close() });
  await waitFor(async () => {
    const res = await fetch('http://127.0.0.1:' + realPort + '/api/health').catch(() => null);
    return res && res.ok;
  }, 8000, 'real provider server');

  const sse = await fetch('http://127.0.0.1:' + realPort + '/api/chat', {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ messages: [{ role: 'user', content: '列一下当前目录' }], effort: 'High' })
  }).then((res) => res.text());
  const events = sse.split('\n\n').filter((b) => b.startsWith('data:')).map((b) => JSON.parse(b.slice(5)));
  const text = events.filter((e) => e.type === 'text').map((e) => e.delta).join('');
  const tool = events.find((e) => e.type === 'tool');
  const done = events.find((e) => e.type === 'done');
  check('agent loop：工具调用 + 最终答案 + 累积 usage',
    upstreamCalls === 2 && tool && tool.tool && tool.tool.name === 'list_dir' &&
    tool.tool.status === 'done' && /smoke-model/.test(text) && done && done.usage.total_tokens === 12,
    JSON.stringify({ upstreamCalls, text: text.slice(0, 40), tool: tool && tool.tool && tool.tool.name, status: tool && tool.tool && tool.tool.status, usage: done && done.usage }));
} catch (err) {
  failed += 1;
  console.error('\n  FATAL ' + err.message);
  if (consoleErrors.length) console.error('  浏览器报错：\n   - ' + consoleErrors.slice(0, 6).join('\n   - '));
} finally {
  await cleanup();
}

console.log('\n' + (failed ? failed + ' 项失败 / ' + results.length + ' 项' : '全部通过（' + results.length + ' 项）'));
process.exit(failed ? 1 : 0);
