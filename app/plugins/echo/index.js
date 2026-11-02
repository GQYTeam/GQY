#!/usr/bin/env node
// gqy-jsonrpc-v1 示例插件：回显参数。
const readline = require('readline');
const rl = readline.createInterface({ input: process.stdin, crlfDelay: Infinity });
rl.on('line', (line) => {
  let msg;
  try { msg = JSON.parse(line); } catch { return; }
  const reply = { jsonrpc: '2.0', id: msg.id };
  switch (msg.method) {
    case 'initialize':
      reply.result = { ok: true, name: 'echo', version: '1.0.0' };
      break;
    case 'call':
      reply.result = { content: JSON.stringify(msg.params && msg.params.arguments ? msg.params.arguments : {}) };
      break;
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
