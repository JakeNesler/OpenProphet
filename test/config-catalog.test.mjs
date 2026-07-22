import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';

const FORBIDDEN_MODE_TERMS = [
  'paper trading',
  'paper account',
  'paper mode',
  'live trading',
  'live account',
  'live mode',
  'real money',
  'real-money',
  'real account',
  'demo account',
  'practice account',
];

const NEW_AGENT_IDS = ['momentum', 'mean-reversion', 'macro-rotation', 'trend-follower', 'catalyst', 'long-vol'];
const NEW_STRATEGY_IDS = [
  'capital-preservation',
  'equity-momentum',
  'etf-mean-reversion',
  'macro-rotation',
  'long-horizon-trend',
  'catalyst-news',
  'long-premium-volatility',
];

async function freshStore(label) {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), 'openprophet-catalog-'));
  const configPath = path.join(dir, 'agent-config.json');
  const previousPath = process.env.OPENPROPHET_CONFIG_PATH;
  process.env.OPENPROPHET_CONFIG_PATH = configPath;
  const store = await import(`../agent/config-store.js?catalog-test=${label}-${Date.now()}-${Math.random()}`);
  return {
    store,
    configPath,
    async cleanup() {
      if (previousPath === undefined) delete process.env.OPENPROPHET_CONFIG_PATH;
      else process.env.OPENPROPHET_CONFIG_PATH = previousPath;
      await fs.rm(dir, { recursive: true, force: true });
    },
  };
}

test('built-in catalog has exactly eight agents and eight strategies with valid, unique, linked ids', async () => {
  const { store, cleanup } = await freshStore('shape');
  try {
    const config = await store.loadConfig();

    assert.equal(config.agents.length, 8, 'eight built-in agents');
    assert.equal(config.strategies.length, 8, 'eight built-in strategies');

    const agentIds = config.agents.map(a => a.id);
    const strategyIds = config.strategies.map(s => s.id);
    assert.equal(new Set(agentIds).size, 8, 'agent ids are unique');
    assert.equal(new Set(strategyIds).size, 8, 'strategy ids are unique');
    for (const id of [...agentIds, ...strategyIds]) {
      assert.match(id, /^[a-z][a-z0-9-]*$/, `id "${id}" is a stable lowercase slug`);
    }

    for (const agent of config.agents) {
      assert.ok(agent.strategyId, `${agent.id} references a strategy`);
      assert.ok(strategyIds.includes(agent.strategyId), `${agent.id} -> ${agent.strategyId} resolves to a real strategy`);
    }

    for (const strategy of config.strategies) {
      if (strategy.rulesFile) continue;
      assert.equal(typeof strategy.customRules, 'string');
      assert.ok(strategy.customRules.length > 200, `${strategy.id} has detailed rules`);
    }
  } finally {
    await cleanup();
  }
});

test('built-in catalog text never recommends or references an execution mode', async () => {
  const { store, cleanup } = await freshStore('mode-neutral');
  try {
    const config = await store.loadConfig();
    const haystacks = [];
    for (const agent of config.agents) {
      haystacks.push([`agent:${agent.id}:name`, agent.name]);
      haystacks.push([`agent:${agent.id}:description`, agent.description]);
      haystacks.push([`agent:${agent.id}:customSystemPrompt`, agent.customSystemPrompt]);
    }
    for (const strategy of config.strategies) {
      haystacks.push([`strategy:${strategy.id}:name`, strategy.name]);
      haystacks.push([`strategy:${strategy.id}:description`, strategy.description]);
      haystacks.push([`strategy:${strategy.id}:customRules`, strategy.customRules]);
    }

    for (const [label, text] of haystacks) {
      if (!text) continue;
      const lower = String(text).toLowerCase();
      for (const term of FORBIDDEN_MODE_TERMS) {
        assert.ok(!lower.includes(term), `${label} must not mention "${term}"`);
      }
    }
  } finally {
    await cleanup();
  }
});

test('loading an existing installation backfills missing built-ins without overwriting a user customization', async () => {
  const { store, configPath, cleanup } = await freshStore('merge');
  try {
    const legacy = {
      schemaVersion: 1,
      activeAccountId: null,
      activeSandboxId: null,
      activeAgentId: 'conservative',
      activeModel: 'test/model',
      accounts: [],
      sandboxes: {},
      agents: [
        {
          id: 'default', name: 'Prophet', description: 'legacy prophet',
          systemPromptTemplate: 'default', strategyId: 'default', model: 'test/model',
          heartbeatOverrides: {}, customSystemPrompt: '', createdAt: '2020-01-01T00:00:00.000Z',
        },
        {
          id: 'conservative', name: 'My Custom Guardian', description: 'user customized guardian',
          systemPromptTemplate: 'custom', customSystemPrompt: 'user prompt', strategyId: null,
          model: 'test/model', heartbeatOverrides: {}, createdAt: '2020-01-01T00:00:00.000Z',
        },
      ],
      strategies: [
        {
          id: 'default', name: 'Legacy Aggressive Options', description: 'user customized',
          rulesFile: 'TRADING_RULES.md', customRules: null, createdAt: '2020-01-01T00:00:00.000Z',
        },
      ],
    };
    await fs.mkdir(path.dirname(configPath), { recursive: true });
    await fs.writeFile(configPath, JSON.stringify(legacy));

    const config = await store.loadConfig();

    assert.equal(config.agents.length, 8, 'missing built-in agents are backfilled');
    assert.equal(config.strategies.length, 8, 'missing built-in strategies are backfilled');

    assert.equal(config.agents[0].id, 'default');
    assert.equal(config.agents[1].id, 'conservative');
    assert.equal(config.agents[1].name, 'My Custom Guardian', 'existing user customization on a built-in id is preserved');
    assert.equal(config.agents[1].strategyId, null, 'user override of a built-in field is not clobbered');

    assert.equal(config.strategies[0].id, 'default');
    assert.equal(config.strategies[0].name, 'Legacy Aggressive Options', 'existing user customization on a built-in strategy id is preserved');

    assert.deepEqual(config.agents.slice(2).map(a => a.id), NEW_AGENT_IDS, 'new built-ins appended in catalog order after preserved entries');
    assert.deepEqual(config.strategies.slice(1).map(s => s.id), NEW_STRATEGY_IDS, 'new built-in strategies appended in catalog order after preserved entries');
  } finally {
    await cleanup();
  }
});
