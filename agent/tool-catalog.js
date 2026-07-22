// Single source of truth for the curated tool menu injected into agent prompts.
// The MCP server exposes more tools than the agent needs to see; this is the intentional,
// curated subset. Keeping it here (instead of hand-copied into two prompt strings in
// harness.js) prevents the two copies from drifting apart or from the real tool set.
export const TOOL_CATALOG = {
  'Trading': ['get_account', 'get_positions', 'get_orders', 'place_buy_order', 'place_sell_order', 'place_managed_position', 'get_managed_positions', 'close_managed_position', 'cancel_order'],
  'Options': ['place_options_order', 'get_options_positions', 'get_options_position', 'get_options_chain'],
  'Market Data': ['get_quote', 'get_latest_bar', 'get_historical_bars', 'analyze_stocks'],
  'News': ['get_news', 'search_news', 'get_market_news', 'get_quick_market_intelligence', 'get_cleaned_news', 'get_marketwatch_topstories', 'get_marketwatch_realtime'],
  'Intelligence': ['aggregate_and_summarize_news', 'list_news_summaries', 'get_news_summary'],
  'Agent Config': ['update_agent_prompt', 'update_strategy_rules', 'get_agent_config', 'set_heartbeat', 'update_permissions', 'set_session_mode', 'create_agent', 'create_strategy', 'assign_agent_to_sandbox'],
  'Heartbeat': ['get_heartbeat_profiles', 'apply_heartbeat_profile', 'get_heartbeat_phases', 'update_heartbeat_phase'],
  'Logging': ['log_decision', 'log_activity', 'get_activity_log'],
  'Trade History': ['find_similar_setups', 'store_trade_setup', 'get_trade_stats'],
  'Utility': ['get_datetime', 'wait'],
};

// Render the catalog as the `**Category**: tool, tool` block used in the prompts.
export function renderToolMenu() {
  return Object.entries(TOOL_CATALOG)
    .map(([category, tools]) => `**${category}**: ${tools.join(', ')}`)
    .join('\n');
}
