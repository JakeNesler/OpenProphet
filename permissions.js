// permissions.js — pure trading-permission policy (no network), shared by the MCP gate and tests.
// enforcePermissions() in mcp-server.js fetches `perms` from the agent server, then delegates
// the actual policy decision here so it can be unit-tested without a running server.

export const ORDER_TOOLS = ['place_buy_order', 'place_sell_order', 'place_options_order', 'place_managed_position', 'close_managed_position'];

// Throws an Error describing the violation if the call is not permitted; returns undefined if allowed.
// `now` is injectable so the 0DTE (same-day expiry) rule is deterministic in tests.
export function checkPermissions(toolName, args = {}, perms = {}, now = new Date()) {
  // Blocked tools
  if (perms.blockedTools?.length && perms.blockedTools.includes(toolName)) {
    throw new Error(`Tool "${toolName}" is blocked by permissions. Blocked tools: ${perms.blockedTools.join(', ')}`);
  }

  // Everything below is order-specific
  if (!ORDER_TOOLS.includes(toolName)) return;

  // Live trading disabled
  if (!perms.allowLiveTrading) {
    throw new Error('Live trading is DISABLED (read-only mode). Cannot place orders. Change permissions to enable.');
  }
  // Options check
  if (!perms.allowOptions && (toolName === 'place_options_order' || (args.symbol && args.symbol.length > 10))) {
    throw new Error('Options trading is DISABLED by permissions.');
  }
  // Stock check
  if (!perms.allowStocks && (toolName === 'place_buy_order' || toolName === 'place_sell_order')) {
    throw new Error('Stock trading is DISABLED by permissions.');
  }
  // 0DTE check for options — OCC format: SYMBOL + YYMMDD + C/P + strike
  if (!perms.allow0DTE && toolName === 'place_options_order' && args.symbol) {
    const match = args.symbol.match(/(\d{6})[CP]/);
    if (match) {
      const expStr = match[1]; // YYMMDD
      const expDate = new Date(`20${expStr.slice(0, 2)}-${expStr.slice(2, 4)}-${expStr.slice(4, 6)}`);
      const today = new Date(now);
      today.setHours(0, 0, 0, 0);
      expDate.setHours(0, 0, 0, 0);
      if (expDate.getTime() === today.getTime()) {
        throw new Error('0DTE options are NOT allowed by permissions.');
      }
    }
  }
  // Require confirmation
  if (perms.requireConfirmation) {
    throw new Error('Order requires operator confirmation (requireConfirmation is enabled). Tell the operator what you want to do and wait for them to disable this setting or approve via the dashboard.');
  }
  // Max order value
  if (perms.maxOrderValue > 0) {
    const orderValue = (args.limit_price || args.entry_price || 0) * (args.quantity || args.qty || 0);
    const allocValue = args.allocation_dollars || 0;
    const checkValue = allocValue || orderValue;
    if (checkValue > perms.maxOrderValue) {
      throw new Error(`Order value $${checkValue.toFixed(2)} exceeds max allowed $${perms.maxOrderValue}. Reduce size or change permissions.`);
    }
  }
}
