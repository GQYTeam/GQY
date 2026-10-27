#!/usr/bin/env node
/* 顾清影 Echo 插件：换行分隔 JSON-RPC，零依赖。
 * 协议见 docs/design/PluginRuntime.md。
 */
'use strict';

const readline = require('readline');

const rl = readline.createInterface({ input: process.stdin, crlfDelay: Infinity });
let closing = false;

function send(msg) {
  process.stdout.write(JSON.stringify(msg) + '\n');
}

function reply(id, result) {
  send({ jsonrpc: '2.0', id, result });
}

function fail(id, message) {
  send({ jsonrpc: '2.0', id, error: { code: -32000, message } });
}

rl.on('line', (line) => {
  if (!line.trim()) return;
  let req;
  try {
    req = JSON.parse(line);
  } catch (err) {
    return;
  }
  if (req.method === 'initialize') {
    reply(req.id, { ok: true, name: 'echo', version: '1.0.0' });
    return;
  }
  if (req.method === 'shutdown') {
    reply(req.id, { ok: true });
    closing = true;
    setTimeout(() => process.exit(0), 20);
    return;
  }
  if (req.method === 'call') {
    const params = req.params || {};
    if (params.action !== 'echo') {
      fail(req.id, '未知 action: ' + params.action);
      return;
    }
    const args = params.arguments || {};
    const content = typeof args.text === 'string' ? args.text : JSON.stringify(args);
    reply(req.id, { content });
    return;
  }
  fail(req.id, '未知 method: ' + req.method);
});

rl.on('close', () => process.exit(0));
process.on('SIGTERM', () => process.exit(0));
process.on('SIGINT', () => process.exit(0));
process.on('uncaughtException', (err) => {
  if (!closing) {
    process.stderr.write('[echo] ' + (err && err.stack ? err.stack : err) + '\n');
  }
  process.exit(1);
});
