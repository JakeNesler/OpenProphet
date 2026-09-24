import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { Client } from '@modelcontextprotocol/sdk/client/index.js';
import { StdioClientTransport } from '@modelcontextprotocol/sdk/client/stdio.js';

test('MCP starts and lists tools without optional Gemini credentials', { timeout: 15000 }, async (t) => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'openprophet-mcp-'));
  const client = new Client({ name: 'startup-test', version: '1.0.0' });
  const transport = new StdioClientTransport({
    command: process.execPath,
    args: [fileURLToPath(new URL('../mcp-server.js', import.meta.url))],
    cwd: dir,
    env: { PATH: process.env.PATH, GEMINI_API_KEY: '', GOOGLE_API_KEY: '', DATABASE_PATH: path.join(dir, 'test.db') },
    stderr: 'pipe',
  });
  t.after(async () => {
    await client.close();
    await transport.close();
    fs.rmSync(dir, { recursive: true, force: true });
  });
  await client.connect(transport);
  const { tools } = await client.listTools();
  assert.ok(tools.some(tool => tool.name === 'aggregate_and_summarize_news'));
  assert.ok(tools.some(tool => tool.name === 'get_positions'));
});
