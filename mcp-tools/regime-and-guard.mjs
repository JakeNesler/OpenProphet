// MCP tools for the cross-agent risk infrastructure shipped in Item 1
// (TradeGuard sector aggregation) and Item 2 (regime gate). Exposed to all
// agents so they can read current sector exposure + regime tier inside their
// preflight / sizing logic instead of finding out via a guard rejection error.
//
// Both tools are no-arg HTTP wrappers. They live in this module (not inline
// in mcp-server.js) so node:test can exercise the dispatch logic without
// spinning up the MCP server.

export const regimeAndGuardTools = [
  {
    name: 'get_regime_gate_status',
    description:
      'Get the current cross-agent regime gate status. Returns the daily-computed regime score (0-100), tier (RED/DEFENSIVE/NORMAL/GREEN), sizing_multiplier (apply to position sizes), block_new_entries (RED only), and components breakdown (breadth/macro/top_risk/bubble). Read this before sizing any new entry — multiply intended position size by sizing_multiplier, and skip the entry entirely if block_new_entries is true. Source: services/regime_gate_service.go, populated daily by scripts/compute_daily_regime_score.py.',
    inputSchema: {
      type: 'object',
      properties: {},
      required: [],
    },
  },
  {
    name: 'get_guard_status',
    description:
      'Get the current TradeGuard status across all four agents. Returns penny-cap usage, daily-loss circuit state, and (when sector aggregation is enabled) sector_exposure_dollars + sector_max_by_bucket_dollars — current dollar exposure per sector bucket (TECH, INDEX_BETA, FINANCIALS, etc.) and the cap per bucket. Read this before sizing entries to know how much headroom remains in the relevant sector bucket. Source: services/trade_guard.go Status().',
    inputSchema: {
      type: 'object',
      properties: {},
      required: [],
    },
  },
];

const TOOL_ROUTES = {
  get_regime_gate_status: '/regime-gate/status',
  get_guard_status: '/guard/status',
};

/**
 * Dispatcher for the regime + guard tools. Returns null if `name` is not one
 * of ours — mcp-server.js can keep walking its own switch in that case.
 * Errors from callTradingBot are wrapped as `{error: <message>}` inside the
 * standard MCP content envelope, matching the pattern of existing tools so
 * the agent's tool-use loop doesn't see raw exceptions.
 */
export async function handleRegimeAndGuardTool(name, _args, callTradingBot) {
  const endpoint = TOOL_ROUTES[name];
  if (!endpoint) return null;

  let payload;
  try {
    payload = await callTradingBot(endpoint);
  } catch (err) {
    payload = { error: err?.message ?? String(err) };
  }
  return {
    content: [
      {
        type: 'text',
        text: JSON.stringify(payload, null, 2),
      },
    ],
  };
}
