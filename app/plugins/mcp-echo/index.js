#!/usr/bin/env node
// MCP stdio 示例插件：tools/list 暴露一个回显工具。
const readline = require('readline');
const rl = readline.createInterface({ input: process.stdin, crlfDelay: Infinity });
rl.on('line', (line) => {
  let msg;
  try { msg = JSON.parse(line); } catch { return; }
  const reply = { jsonrpc: '2.0', id: msg.id };
  switch (msg.method) {
    case 'initialize':
      reply.result = { protocolVersion: '2024-11-05', capabilities: {}, serverInfo: { name: 'mcp-echo', version: '1.0.0' } };
      break;
    case 'tools/list':
      reply.result = { tools: [{ name: 'echo', description: '回显文本', inputSchema: { type: 'object', properties: { text: { type: 'string' } }, required: ['text'] } }] };
      break;
    case 'tools/call':
      reply.result = { content: [{ type: 'text', text: JSON.stringify(msg.params.arguments) }], isError: false };
      break;
    case 'shutdown':
      reply.result = null;
      process.stdout.write(JSON.stringify(reply) + '\n');
      process.exit(0);
      return;
    default:
      reply.error = { code: -32601, message: 'unknown method' };
  }
  process.stdout.write(JSON.stringify(reply) + '\n');
});
