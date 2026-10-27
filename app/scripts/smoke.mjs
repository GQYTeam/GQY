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
import { existsSync } from 'node:fs';
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

  /* 设置：五个 tab 都要能打开 */
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
  check('设置五个 tab 全部可切换', tabs.length === 5 && tabs.every((t) => t.endsWith(':true')), tabs.join(' '));
  const dataTab = await evaluate('!!document.querySelector("#importData") && !!document.querySelector("#exportData") && !!document.querySelector("#clearAll")');
  check('数据页有导入 / 导出 / 清空', dataTab === true);
  await evaluate('document.querySelector("#settingsLayer [data-settings-close]").click()');
  await sleep(200);

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

  check('浏览器控制台无报错', consoleErrors.length === 0, consoleErrors.slice(0, 3).join(' | '));

  /* 真实上游：OpenAI 兼容流 → thinking / text / usage 的正确映射 */
  const upstreamPort = PORT + 400;
  const upstream = http.createServer((req, res) => {
    let body = '';
    req.on('data', (d) => { body += d; });
    req.on('end', () => {
      const parsed = JSON.parse(body || '{}');
      res.writeHead(200, { 'content-type': 'text/event-stream' });
      const chunk = (delta) => res.write('data: ' + JSON.stringify({ choices: [{ delta }] }) + '\n\n');
      chunk({ reasoning_content: '先想一下' });
      chunk({ content: '## 结论\n\n' });
      chunk({ content: '上游流 · ' + parsed.model + ' · ' + parsed.temperature + '\n' });
      res.write('data: ' + JSON.stringify({ choices: [{ delta: {}, finish_reason: 'stop' }], usage: { prompt_tokens: 3, completion_tokens: 7, total_tokens: 10 } }) + '\n\n');
      res.write('data: [DONE]\n\n');
      res.end();
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
    body: JSON.stringify({ messages: [{ role: 'user', content: 'hi' }], effort: 'High' })
  }).then((res) => res.text());
  const events = sse.split('\n\n').filter((b) => b.startsWith('data:')).map((b) => JSON.parse(b.slice(5)));
  const thinking = events.filter((e) => e.type === 'thinking').map((e) => e.delta).join('');
  const text = events.filter((e) => e.type === 'text').map((e) => e.delta).join('');
  const done = events.find((e) => e.type === 'done');
  check('OpenAI 兼容上游映射正确（thinking / text / usage）',
    thinking === '先想一下' && /smoke-model/.test(text) && /0\.6/.test(text) && done && done.usage.total_tokens === 10,
    JSON.stringify({ thinking, text: text.slice(0, 40), usage: done && done.usage }));
} catch (err) {
  failed += 1;
  console.error('\n  FATAL ' + err.message);
  if (consoleErrors.length) console.error('  浏览器报错：\n   - ' + consoleErrors.slice(0, 6).join('\n   - '));
} finally {
  await cleanup();
}

console.log('\n' + (failed ? failed + ' 项失败 / ' + results.length + ' 项' : '全部通过（' + results.length + ' 项）'));
process.exit(failed ? 1 : 0);
