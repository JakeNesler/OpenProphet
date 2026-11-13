const orderTools = new Set(['place_buy_order', 'place_sell_order', 'place_options_order', 'place_managed_position', 'close_managed_position']);
const decode = value => {
  if (typeof value !== 'string') return value;
  const warning = value.indexOf('\n\n⚠ No `thesis` provided');
  return JSON.parse(warning < 0 ? value : value.slice(0, warning));
};

// A tool name or a prose success message is not a broker acknowledgement.
export function confirmedOrder(tool, input, output, state = {}) {
  if (!orderTools.has(tool) || state.status === 'error' || state.error) return null;
  let value = output;
  try {
    value = decode(value);
    if (value?.isError || value?.error) return null;
    if (Array.isArray(value?.content)) {
      const text = value.content.find(part => part.type === 'text')?.text;
      value = decode(text);
    }
    if (value?.error || value?.success === false) return null;
    value = value?.order || value?.position || value;
    const orderId = tool === 'place_managed_position' ? value?.entry_order_id : (value?.id || value?.order_id);
    const status = String(value?.status || 'accepted').toLowerCase();
    if (typeof orderId !== 'string' || !orderId.trim() || /reject|cancel|fail|expire/.test(status)) return null;
    const symbol = value.symbol || input.symbol;
    const side = value.side || input.side || (tool === 'place_buy_order' ? 'buy' : tool === 'place_sell_order' ? 'sell' : null);
    if (typeof symbol !== 'string' || !symbol || !['buy', 'sell'].includes(side)) return null;
    const number = value => value != null && Number.isFinite(Number(value)) && Number(value) >= 0 ? Number(value) : undefined;
    return {
      type: 'order', tool, orderId: String(orderId), status, symbol, side,
      quantity: number(value.qty ?? value.quantity ?? input.quantity ?? input.qty),
      price: number(value.filled_avg_price ?? value.limit_price ?? input.limit_price),
    };
  } catch { return null; }
}
