#!/usr/bin/env node
// gqy-jsonrpc-v1 示例插件：回显参数。
const readline = require('readline');
const rl = readline.createInterface({ input: process.stdin, crlfDelay: Infinity });
// 设置由 GQY 在 initialize 时下发（默认值 + 用户保存值），插件只读不算写。
let settings = { prefix: 'echo:', uppercase: false };
rl.on('line', (line) => {
  let msg;
  try { msg = JSON.parse(line); } catch { return; }
  const reply = { jsonrpc: '2.0', id: msg.id };
  switch (msg.method) {
    case 'initialize':
      if (msg.params && msg.params.settings) settings = { ...settings, ...msg.params.settings };
      reply.result = { ok: true, name: 'echo', version: '1.1.0', settings };
      break;
    case 'call': {
      const args = msg.params && msg.params.arguments ? msg.params.arguments : {};
      let text = args.text === undefined ? JSON.stringify(args) : String(args.text);
      if (settings.uppercase) text = text.toUpperCase();
      reply.result = { content: String(settings.prefix || '') + text, settings };
      break;
    }
    case 'shutdown':
      reply.result = { ok: true };
      process.stdout.write(JSON.stringify(reply) + '\n');
      process.exit(0);
      return;
    default:
      reply.error = { code: -32601, message: 'unknown action' };
  }
  process.stdout.write(JSON.stringify(reply) + '\n');
});
