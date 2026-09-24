import { test } from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import { once } from 'node:events';
import { GoogleGenAI } from '@google/genai';
import { generateNewsSummary } from '../news-summary.js';

test('news summary uses the GenAI request and response format', async (t) => {
  let request;
  const server = http.createServer(async (req, res) => {
    let body = '';
    for await (const chunk of req) body += chunk;
    request = { method: req.method, url: req.url, body: JSON.parse(body) };
    res.setHeader('Content-Type', 'application/json');
    res.end(JSON.stringify({
      candidates: [{ content: { role: 'model', parts: [{ text: 'Market summary.' }] } }],
    }));
  });
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  t.after(() => new Promise(resolve => server.close(resolve)));

  const client = new GoogleGenAI({
    apiKey: 'test-only',
    httpOptions: { baseUrl: `http://127.0.0.1:${server.address().port}` },
  });
  const summary = await generateNewsSummary('Summarize these articles.', {
    client, model: 'test-news-model',
  });
  assert.equal(summary, 'Market summary.');
  assert.equal(request.method, 'POST');
  assert.match(request.url, /models\/test-news-model:generateContent$/);
  assert.equal(request.body.contents[0].parts[0].text, 'Summarize these articles.');
});

test('missing optional Gemini credentials fail only when summarization is requested', async () => {
  await assert.rejects(generateNewsSummary('News', { apiKey: '' }), /GEMINI_API_KEY/);
});

test('an empty or blocked Gemini response is not saved as a summary', async () => {
  const client = { models: { generateContent: async () => ({ text: '' }) } };
  await assert.rejects(generateNewsSummary('News', { client }), /returned no text/);
});
