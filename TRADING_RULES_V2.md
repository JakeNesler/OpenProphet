# Trading Rules

**Updated:** November 26, 2025
**Style:** Aggressive discretionary options trading with scalping overlay

---

## Core Philosophy

- **Options-only trading** - No stock positions
- **Long bias** - Calls preferred, occasional puts for hedging
- **Active management** - Multiple positions, frequent monitoring
- **Discretionary execution** - Rules are guidelines, not hard constraints
- **Pattern Day Trader** - Unlimited day trades with $100K+ equity

---

## Position Sizing

**Rule:** Maximum 12% of portfolio per position
- Calculate: `position_value / portfolio_value ≤ 0.12`
- Example: $100K portfolio → max $12K per position
- Rationale: Allows concentrated bets on high-conviction setups

**Rule:** Maximum 30% in any single sector
- Sectors: Tech (NVDA/AMD/TSLA), Crypto (MSTR/MARA/COIN), Broad Market (SPY/QQQ)
- Prevents: Sector-wide correlation wipeouts
- Allow: Multiple positions in strong trending sectors

**Rule:** Maximum 10 positions simultaneously
- Simplifies: Portfolio management and monitoring
- Prevents: Over-diversification (diworsification)
- Focus: Quality over quantity

**Rule:** Maximum 40% of portfolio deployed in V2 positions at any time (segment cap)
- Calculate: sum of `position_value` across all V2 positions / `portfolio_value` ≤ 0.40
- This is the V2 strategy's lane in the multi-agent capital model. The other lanes are HARVEST (12%), PENNY (30%), and TREND (18%) — total ≤ 100%
- Reconciles with the existing "50-70% cash" rule under Portfolio Construction: at the 40% cap, V2 alone leaves 60% available; the other agents draw from that 60% within their own caps
- If a candidate trade would push V2 deployed above 40%, skip the entry (or close an existing V2 position first to make room)
- This cap applies regardless of conviction; high-conviction setups do not override it

---

## Day Trading

**Rule:** Unlimited day trades (Pattern Day Trader status)
- Requirement: Maintain $25K+ equity at all times
- Current status: $108K portfolio ✅
- Monitor: Don't abuse - each round trip has costs
- Target: <5 scalps per session to maintain selectivity

**Why:** Full flexibility to enter/exit positions same-day without restrictions

---

## Risk Management

**Rule:** Manual discretionary stops (no automatic -15%)
- Monitor: Positions 2-3x per day (open, midday, close)
- Cut losers: When thesis breaks or position down >15%
- Examples from today: QQQ -15.6% (cut immediately), MSTR/TSLA/NVDA (cut when weak)

**Rule:** Take profits at +40-100% or on technical signals
- Partial exits: Consider 50% reduction at +40%, let rest run
- Full exits: Lock profits before major events (holidays, earnings)
- Don't get greedy: A winner can become a loser quickly

**Rule:** Maximum -5% portfolio loss per day
- Hard stop: If portfolio drops 5% intraday, cease trading
- Reset: Come back next session with clear head
- Prevent: Revenge trading and emotional spirals

---

## Options Selection

### **Swing Positions (Core Holdings)**

**Rule:** 50-120 DTE for swing positions
- Rationale: Minimize theta decay, time for thesis to develop
- Sweet spot: 60-90 DTE (monthlies 2-3 months out)
- Examples from today: Jan positions (51 DTE), March positions (114 DTE)

**Rule:** Delta 0.40-0.70 preferred (ATM to slightly ITM)
- Avoid: Deep OTM lottery tickets (delta <0.30)
- Avoid: Deep ITM inefficiency (delta >0.80)
- Sweet spot: ATM calls with leverage and probability

---

### **Scalp Positions (Short-Term)**

**Rule:** 2-5 DTE allowed for scalps
- Purpose: Capture intraday/overnight momentum
- Risk: High theta decay, must close same day or next day
- Examples from today: SPY/QQQ 2 DTE scalps (11/28 exp on 11/26)

**Rule:** Close all scalps by EOD or by -15% stop
- No overnight holds: On 1-2 DTE options (too much weekend/gap risk)
- Exception: 3-5 DTE can hold overnight if strong conviction
- Always set mental stop: Exit immediately if down >15%

---

### **Liquidity & Spreads**

**Rule:** Bid-ask spread <10% of mid-price
- Check: `(ask - bid) / mid < 0.10`
- Prevents: Slippage eating into profits
- Examples: SPY/QQQ/NVDA options (tight spreads)

**Rule:** LIMIT ORDERS ONLY
- Never: Use market orders on options (too much slippage)
- Always: Set limit at mid-price or better
- Patient: Let order fill, don't chase

---

## Trade Execution

**Rule:** Opening volatility trading allowed (9:30-9:45 AM)
- Rationale: Best momentum and volume during first 15 minutes
- Caution: Use limit orders, don't chase gaps
- Examples from today: 9:30 AM scalp entries on SPY/QQQ

**Rule:** Maximum 10 trades per day (entries + exits)
- Prevents: Over-trading and transaction cost bleed
- Focus: Quality setups, not quantity
- Track: Each trade costs ~$5-10 in fees + slippage

**Rule:** Maximum 5 scalp entries per day
- Core positions: Can open 5+ swing trades if high conviction
- Scalps: Limit to 5 per day to maintain discipline
- Rationale: Scalping is high-cost, need high win rate

---

## Decision Logging

**Rule:** Log all major decisions to `decisive_actions/`
- Before: Major position entries (optional but recommended)
- After: End of day summary, major exits, strategic decisions
- Format: Use `mcp__prophet__log_decision` tool
- Purpose: Audit trail, learning from mistakes

**Rule:** Log daily activity to `activity_logs/`
- Track: Position checks, analysis, intelligence gathering
- Format: Use `mcp__prophet__log_activity` tool
- Review: Weekly to identify patterns

---

## Agent Consultation (Optional)

Agents are **advisory, not required**. Use them when you want:

### **1. Strategic Analysis (CEO Agent)**
- Portfolio-level strategy decisions
- Capital allocation across multiple positions
- Risk assessment before major deployments
- Post-mortem analysis of bad trades

### **2. Technical Setup Identification (Strategy Agent)**
- High-conviction directional setups
- Technical confluence analysis
- Entry/exit price recommendations
- Risk/reward optimization

### **3. Risk De-Risking (Consultant/Daedalus Agent)**
- Pressure-test your assumptions
- Identify blind spots and biases
- Challenge emotional trades
- Behavioral pattern recognition

### **4. Macro Scenario Analysis (Scenario Analyst + Strategy Reviewer)**
- Build 18-month Base/Bull/Bear scenarios from a macro news catalyst
- Map 1st/2nd/3rd order sector and stock impacts via web research
- Strategy Reviewer runs a second pass: catches coverage gaps, probability errors, and bias
- Best used before entering any macro-driven position over $20K

**When to use:**
- Before deploying >$20K on a macro-driven thesis (FOMC, tariffs, earnings catalysts, geopolitical events)
- When a major macro event could shift the thesis on existing positions
- After 3 consecutive losses
- When feeling emotional or uncertain
- Weekly portfolio review

**When NOT needed:**
- Routine position management
- Small scalp trades (<$5K)
- Taking profits on winners
- Following pre-defined exits

---

## Overnight & Weekend Positions

**Rule:** Review all positions at 12:50 PM on early close days, 3:50 PM on normal days
- Decide: Hold overnight or close?
- Consider: Overnight news risk, earnings, economic data
- Weekend: Close <7 DTE positions by Friday close if uncomfortable

**Rule:** Close <3 DTE positions before holidays/weekends
- Rationale: Gap risk over 3-4 day weekends
- Examples from today: Closed all 2 DTE scalps, held 51-114 DTE swings
- Exception: Can hold 3-7 DTE if high conviction and willing to accept gap risk

---

## Profit-Taking Strategy

**Rule:** Lock partial profits at +40%
- Action: Consider closing 50% of position
- Benefit: Take some off table, let rest run
- Move stop: On remaining position to breakeven

**Rule:** Full exit at +100% or on technical breakdown
- Don't be greedy: 100% is an excellent win
- Technical: If trend breaks, take profits even if no target hit
- Protect: Winners can become losers quickly

**Rule:** Before major events (holidays, earnings)
- Today's example: Closed December SPY calls (+22%, +34%) before Thanksgiving
- Rationale: 4-day weekend gap risk, lock in gains
- Keep: Only positions with 50+ DTE and willing to hold through event

---

## Loss-Cutting Discipline

**Rule:** Cut losers when thesis breaks OR down >15%
- Thesis break: Expected catalyst didn't materialize, technical structure failed
- Down >15%: Automatic exit regardless of thesis
- No hope: Don't hold and hope it comes back

**Rule:** Cut all positions if daily loss hits -5%
- Circuit breaker: Prevents catastrophic loss days
- Reset: Stop trading, come back tomorrow
- Reflect: What went wrong? Discipline failure or bad luck?

**Rule:** No revenge trading
- Definition: Re-entering same symbol within 2 hours after stop out
- Why: Emotional decision, usually loses more money
- Cool off: Wait until next session or at least 2 hours

---

## Transaction Cost Management

**Rule:** Target <5% transaction costs per trade
- Calculate: `(fees + slippage) / gross profit < 0.05`
- Minimize: Use limit orders, trade liquid options, hold longer
- Track: Monthly transaction cost budget = $200

**Rule:** Hold scalps at least until profitable or stop hit
- Avoid: Panic exits on minor pullbacks
- Allow: Thesis time to develop (at least 30 minutes to 1 hour)
- Exception: If down >10% and momentum clearly broken, cut early

---

## Position Management

**Rule:** Check positions 2-3x per day
- Open (9:30-10:00 AM): Review overnight action
- Midday (12:00-1:00 PM): Check if any stops need adjusting
- Close (3:30-4:00 PM): Decide holds vs. closes

**Rule:** Don't obsessively watch positions
- Avoid: Staring at screens and reacting to every tick
- Trust: Your thesis and stops
- Detach: Emotional attachment leads to bad decisions

---

## Portfolio Construction

**Rule:** Maintain 50-70% cash at all times
- Rationale: Dry powder for opportunities
- Prevents: Being fully invested at market tops
- Allows: Deploying capital when great setups appear

**Rule:** Diversify across time frames
- Core swings: 50-120 DTE positions (60-70% of deployed capital)
- Short-term: 2-7 DTE scalps (30-40% of deployed capital)
- Balance: Theta decay vs. leverage

**Rule:** Diversify across sectors (but allow concentration)
- Preferred: 3-5 different underlyings
- Allow: Up to 30% in one hot sector if trending hard
- Examples from today: SPY, AMD, NVDA, COIN, PLTR, AMZN (6 underlyings)

---

## Behavioral Discipline

**Rule:** No trading when emotional
- Angry, frustrated, anxious, euphoric = bad decisions
- Step away: Take a walk, come back in 30 minutes
- Reset: Clear head required for good trading

**Rule:** No "I need to make back losses" thinking
- Each trade: Independent decision
- Sunk costs: Ignore previous losses
- Focus: Best trade right now, not making back yesterday

**Rule:** Accept that losses are part of trading
- Win rate: Target 40-60% (most trades will lose)
- What matters: Profit factor (winners bigger than losers)
- Today's example: QQQ -$960, SPY +$1,920 = net +$960

---

## Weekly Review (Sunday)

**Rule:** Review all trades from the week
- What worked: Which setups, which decisions
- What didn't: Mistakes, violations, emotional trades
- Patterns: Am I repeating same mistakes?

**Rule:** Update rules if needed
- Evolve: Based on actual behavior and results
- Document: What you're actually doing, not aspirational rules
- Simplify: Remove rules you never follow

---

## Simple Pre-Trade Checklist

- [ ] Position size under 12% of portfolio?
- [ ] Total positions under 10?
- [ ] Daily trades under 10?
- [ ] Limit order at mid-price or better?
- [ ] Stop loss level mentally defined?
- [ ] Profit target mentally defined?
- [ ] Spread <10% of mid-price?
- [ ] Liquid options with volume?
- [ ] Clear thesis (why this trade, why now)?

**If any answer is NO, reconsider the trade.**

---

## Key Lessons from Today (November 26, 2025)

✅ **Cut losers fast** - QQQ scalp -15.6%, cut immediately
✅ **Let winners run** - SPY scalp went -$180 → +$1,920
✅ **Lock profits before holidays** - Closed December positions
✅ **Clean portfolio** - Cut all losing positions (MSTR, TSLA, NVDA, SPY put)
✅ **No theta risk** - All remaining positions 51-114 DTE
✅ **Use limit orders** - Zero market orders, zero slippage disasters
✅ **Log decisions** - Documented HOLD_ALL decision at 11:50 AM

---

**The goal is profitable trading with manageable risk.**

These rules reflect what you're actually doing. Adjust based on results. Stay flexible, stay disciplined.

## Loss Review Protocol

At every heartbeat, before taking any new position:

1. **Check daily P&L**: Use get_account to compare current portfolio value to the previous session's closing value.

2. **If down more than 3.5% intraday**: pause all new entries for the current heartbeat cycle, run get_trade_stats to review recent performance, log a HOLD decision with reasoning via log_decision, resume only if losses stabilize or reverse. (Note: applies to the full portfolio. If losses are isolated to the scalp book, the threshold is 4% of scalp capital deployed before pausing new scalp entries only.)

3. **If down more than 5% intraday**: close all non-core positions immediately, do not open new positions for the remainder of the trading day, log a detailed HOLD decision explaining what triggered the circuit breaker. (This is consistent with the existing -5% daily loss rule and serves as a hard enforcement checkpoint.)

4. **Weekly review (every Monday pre-market)**: run get_trade_stats for the prior week. Only act on the results if at least 10 trades occurred that week — smaller samples are noise, not signal. If the sample is sufficient:
   - Calculate profit factor: total P&L of winning trades divided by total P&L of losing trades (absolute value).
   - If profit factor < 1.0 over the prior week AND over the rolling 3-week window, adjust position sizing down by 25% for the coming week.
   - Do NOT reduce sizing based on win rate alone — a win rate of 35–45% is normal and expected for this strategy given asymmetric payoffs.
   - Run find_similar_setups for any losing trades to identify patterns.
   - Log findings via log_activity with type ANALYSIS.
