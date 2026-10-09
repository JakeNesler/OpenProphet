// permissions.js — pure trading-permission policy (no network), shared by the MCP gate and tests.
// enforcePermissions() in mcp-server.js fetches `perms` from the agent server, then delegates
// the actual policy decision here so it can be unit-tested without a running server.

export const ORDER_TOOLS = ['place_buy_order', 'place_sell_order', 'place_options_order', 'place_managed_position', 'close_managed_position'];

// US equity options are 100-share contracts, so an options order is worth premium × 100 × contracts.
export const OPTION_CONTRACT_MULTIPLIER = 100;

// OCC option symbol: ROOT (≤6) + YYMMDD + C/P + 8-digit strike, e.g. SPY251219C00680000.
const OCC_SYMBOL = /^[A-Z.]{1,6}\d{6}[CP]\d{8}$/;
export function isOptionSymbol(symbol) {
  return typeof symbol === 'string' && OCC_SYMBOL.test(symbol);
}

// Throws an Error describing the violation if the call is not permitted; returns undefined if allowed.
// `now` is injectable so the 0DTE (same-day expiry) rule is deterministic in tests.
// `args.estimated_price` is an optional quote the MCP server attaches to market orders so
// maxOrderValue can value them; it is never forwarded to the broker.
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
      // Build the expiry as a LOCAL calendar date. `new Date('YYYY-MM-DD')` parses as UTC
      // midnight, which in any US timezone is the previous evening — that let same-day
      // expiries through and flagged next-day ones instead.
      const expDate = new Date(2000 + Number(expStr.slice(0, 2)), Number(expStr.slice(2, 4)) - 1, Number(expStr.slice(4, 6)));
      const today = new Date(now);
      today.setHours(0, 0, 0, 0);
      if (expDate.getTime() === today.getTime()) {
        throw new Error('0DTE options are NOT allowed by permissions.');
      }
    }
  }
  // Require confirmation
  if (perms.requireConfirmation) {
    throw new Error('Order requires operator confirmation (requireConfirmation is enabled). Tell the operator what you want to do and wait for them to disable this setting or approve via the dashboard.');
  }
  // Max order value. Closing a managed position is an exit (risk-reducing, no size argument), so
  // it is exempt; every other order must be valued or it fails closed.
  if (perms.maxOrderValue > 0 && toolName !== 'close_managed_position') {
    const quantity = Number(args.quantity ?? args.qty ?? 0);
    const unitPrice = Number(args.limit_price ?? args.entry_price ?? args.estimated_price ?? 0);
    const multiplier = (toolName === 'place_options_order' || isOptionSymbol(args.symbol)) ? OPTION_CONTRACT_MULTIPLIER : 1;
    const allocValue = Number(args.allocation_dollars || 0);
    const checkValue = allocValue || unitPrice * quantity * multiplier;
    if (!(checkValue > 0)) {
      throw new Error('maxOrderValue is set but this order carries no price to value it against (market order with no quote available). Use a limit order with limit_price, or retry.');
    }
    if (checkValue > perms.maxOrderValue) {
      throw new Error(`Order value $${checkValue.toFixed(2)} exceeds max allowed $${perms.maxOrderValue}. Reduce size or change permissions.`);
    }
  }
}
