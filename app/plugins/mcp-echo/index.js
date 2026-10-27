#!/usr/bin/env node
/* 顾清影 MCP 示例：最小 stdio 服务端，零依赖。
 * 协议见 docs/design/PluginRuntime.md。
 */
'use strict';

const readline = require('readline');
const rl = readline.createInterface({ input: process.stdin, crlfDelay: Infinity });

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
    reply(req.id, {
      protocolVersion: '2024-11-05',
      capabilities: { tools: {} },
      serverInfo: { name: 'mcp-echo', version: '1.0.0' }
    });
    return;
  }
  if (req.method === 'notifications/initialized') return;
  if (req.method === 'tools/list') {
    reply(req.id, {
      tools: [{
        name: 'add',
        description: '把两个数字相加',
        inputSchema: {
          type: 'object',
          properties: {
            a: { type: 'number', description: '第一个数' },
            b: { type: 'number', description: '第二个数' }
          },
          required: ['a', 'b']
        }
      }]
    });
    return;
  }
  if (req.method === 'tools/call') {
    const name = req.params && req.params.name;
    if (name !== 'add') { fail(req.id, '未知工具: ' + name); return; }
    const args = (req.params && req.params.arguments) || {};
    const a = Number(args.a) || 0;
    const b = Number(args.b) || 0;
    reply(req.id, { content: [{ type: 'text', text: String(a + b) }] });
    return;
  }
  if (req.method === 'shutdown') {
    reply(req.id, {});
    setTimeout(() => process.exit(0), 10);
    return;
  }
  if (req.method && req.id != null) fail(req.id, '未知 method: ' + req.method);
});

rl.on('close', () => process.exit(0));
process.on('SIGTERM', () => process.exit(0));
process.on('SIGINT', () => process.exit(0));
