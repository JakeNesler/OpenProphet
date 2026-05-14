/**
 * Analysis Scheduler - Runs pre-market, weekly, and event-driven analysis jobs.
 *
 * Time-based (while server is running):
 *   6:00 AM ET weekdays            → daily_briefing
 *   6:00 AM ET 1st of each month   → harvest_parameter_review (monthly)
 *   6:00 AM ET 1st of Jan/Apr/Jul/Oct → trend_parameter_review (quarterly)
 *   6:05 AM ET Mondays             → review_performance (if not done this week) → adapt_strategy
 *   6:10 AM ET Mondays             → review_performance_penny (if not done this week) → adapt_strategy_penny
 *   4:30 PM ET weekdays            → loss checks: Prophet (≥-4% latest) → postmortem + adapt_strategy
 *                                                  Penny (≥-3% latest)  → postmortem_penny + adapt_strategy_penny
 *   6:00 PM ET Sundays             → weekly_screeners
 *
 * Startup-based (on server start, if criteria met):
 *   daily_briefing               → data/reports/daily_brief_YYYYMMDD.json missing
 *   scenario_analysis            → no data/reports/scenario_*_YYYYMMDD.md today
 *   review_performance           → not run this ISO week  → then adapt_strategy
 *   review_performance_penny     → not run this ISO week  → then adapt_strategy_penny
 *   postmortem                   → last Prophet session had ≥-4% loss and not yet run → adapt_strategy
 *   postmortem_penny             → last Penny session had ≥-3% loss and not yet run → adapt_strategy_penny
 *   harvest_parameter_review     → not run this calendar month
 *   trend_parameter_review       → not run this calendar quarter
 *   adapt_strategy               → after review or postmortem, or 3 consecutive losing days
 *
 * Persisted state: data/scheduler-state.json
 */
import { spawn } from 'child_process';
import fs from 'fs/promises';
import os from 'os';
import path from 'path';
import { fileURLToPath } from 'url';
import { EventEmitter } from 'events';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const PROJECT_ROOT = path.join(__dirname, '..');
const OPENCODE_BIN = process.platform === 'win32' ? 'cmd.exe' : 'opencode';
const OPENCODE_WIN_PREFIX = process.platform === 'win32' ? ['/c', 'opencode.cmd'] : [];
const STATE_FILE = path.join(PROJECT_ROOT, 'data', 'scheduler-state.json');
const SANDBOXES_DIR = path.join(PROJECT_ROOT, 'data', 'sandboxes');
const REPORTS_DIR = path.join(PROJECT_ROOT, 'data', 'reports');

export class AnalysisScheduler extends EventEmitter {
  constructor(options = {}) {
    super();
    this.model = options.model || 'anthropic/claude-sonnet-4-6';
    this.onEmergencyWake = options.onEmergencyWake || null;
    this._timer = null;
    this._running = false;
    this._activeJob = null;
    this._scanTimer = null;
    this._scanActive = false;
    this._lastAlertTime = null;
    this._firedAlertFingerprints = new Map(); // date -> Set<fingerprint>
    // File-detectable (reset on restart is fine — file presence is the guard)
    this._lastDailyBriefDate = null;
    this._lastWeeklyScreenDate = null;
    this._lastScenarioDate = null;
    // State-persisted (no output file — must survive restarts)
    this._lastReviewWeek = null;
    this._lastPostmortemDate = null;
    this._lastAdaptDate = null;
    this._lastLossCheckDate = null;
    this._lastPennyReviewWeek = null;
    this._lastPennyPostmortemDate = null;
    this._lastHarvestParamReviewMonth = null;   // YYYY-MM
    this._lastTrendParamReviewQuarter = null;   // YYYY-Q
  }

  async start() {
    if (this._running) return;
    await this._loadState();
    this._running = true;
    this._timer = setInterval(() => this._checkSchedule(), 60 * 1000);
    this._checkSchedule();
    this._startScanLoop();
    this._log('Analysis scheduler started.', 'info');
  }

  stop() {
    this._running = false;
    if (this._timer) { clearInterval(this._timer); this._timer = null; }
    this._stopScanLoop();
    this._log('Analysis scheduler stopped.', 'warning');
  }

  getStatus() {
    return {
      running: this._running,
      activeJob: this._activeJob,
      scanActive: this._scanActive,
      lastAlertTime: this._lastAlertTime,
      lastDailyBriefDate: this._lastDailyBriefDate,
      lastWeeklyScreenDate: this._lastWeeklyScreenDate,
      lastScenarioDate: this._lastScenarioDate,
      lastReviewWeek: this._lastReviewWeek,
      lastPostmortemDate: this._lastPostmortemDate,
      lastAdaptDate: this._lastAdaptDate,
      lastPennyReviewWeek: this._lastPennyReviewWeek,
      lastPennyPostmortemDate: this._lastPennyPostmortemDate,
      lastHarvestParamReviewMonth: this._lastHarvestParamReviewMonth,
      lastTrendParamReviewQuarter: this._lastTrendParamReviewQuarter,
    };
  }

  async triggerJob(jobName, date, target) {
    if (this._activeJob) return { error: `Job already running: ${this._activeJob}` };
    const validJobs = [
      'daily_briefing', 'weekly_screeners', 'scenario_analysis',
      'review_performance', 'postmortem', 'adapt_strategy',
      'review_performance_penny', 'postmortem_penny', 'adapt_strategy_penny',
      'harvest_parameter_review', 'trend_parameter_review',
    ];
    if (!validJobs.includes(jobName)) {
      return { error: `Unknown job: ${jobName}. Valid: ${validJobs.join(', ')}` };
    }
    const isoDate = date || new Date().toLocaleDateString('en-CA', { timeZone: 'America/New_York' });
    const lockKey = this._getLockKey(jobName, isoDate, target);
    if (!await this._acquireLock(lockKey)) {
      this._log(`${jobName} already running in another process — skipping duplicate.`, 'info');
      return { skipped: true, reason: 'already_running', job: jobName };
    }
    this._activeJob = jobName;
    try {
      if (jobName === 'daily_briefing') {
        this._lastDailyBriefDate = isoDate;
        await this._runDailyBriefing(isoDate);
      } else if (jobName === 'weekly_screeners') {
        this._lastWeeklyScreenDate = isoDate;
        await this._runWeeklyScreeners(isoDate);
      } else if (jobName === 'scenario_analysis') {
        this._lastScenarioDate = isoDate;
        await this._runScenarioAnalysis(isoDate);
        await this._saveState();
      } else if (jobName === 'review_performance') {
        this._lastReviewWeek = this._getISOWeek(isoDate);
        await this._runSkill('review-performance', isoDate, null, 10 * 60 * 1000);
        await this._saveState();
      } else if (jobName === 'postmortem') {
        this._lastPostmortemDate = isoDate;
        await this._runSkill('postmortem', isoDate, target || isoDate, 10 * 60 * 1000);
        await this._saveState();
      } else if (jobName === 'adapt_strategy') {
        this._lastAdaptDate = isoDate;
        await this._runAdaptStrategy(isoDate);
        await this._saveState();
      } else if (jobName === 'review_performance_penny') {
        this._lastPennyReviewWeek = this._getISOWeek(isoDate);
        await this._runSkill('review-performance-penny', isoDate, null, 10 * 60 * 1000);
        await this._saveState();
      } else if (jobName === 'postmortem_penny') {
        this._lastPennyPostmortemDate = target || isoDate;
        await this._runSkill('postmortem-penny', isoDate, target || isoDate, 10 * 60 * 1000);
        await this._saveState();
      } else if (jobName === 'adapt_strategy_penny') {
        await this._runSkill('adapt-strategy-penny', isoDate, null, 15 * 60 * 1000, this._automatedRunAppendix({
          confirmStep: 'Step 6 (user confirmation)',
          targetFile: 'data/agent-config.json',
          changeNoun: 'rule',
        }));
        await this._saveState();
      } else if (jobName === 'harvest_parameter_review') {
        this._lastHarvestParamReviewMonth = this._getMonth(isoDate);
        await this._runSkill('harvest-parameter-review', isoDate, null, 15 * 60 * 1000, this._automatedRunAppendix({
          confirmStep: 'Step 8 (user confirmation)',
          targetFile: 'TRADING_RULES_HARVEST.md',
          changeNoun: 'parameter',
          guardNote: 'If the Step 3 sample-size guard fires, exit with the INSUFFICIENT_SAMPLE block as instructed and do NOT edit any file. If Step 7 structural escalation fires, write STRUCTURAL_REVIEW_NEEDED.md and do NOT edit TRADING_RULES_HARVEST.md.',
        }));
        await this._saveState();
      } else if (jobName === 'trend_parameter_review') {
        this._lastTrendParamReviewQuarter = this._getQuarter(isoDate);
        await this._runSkill('trend-parameter-review', isoDate, null, 15 * 60 * 1000, this._automatedRunAppendix({
          confirmStep: 'Step 9 (user confirmation)',
          targetFile: 'TRADING_RULES_TREND.md',
          changeNoun: 'parameter or universe',
          guardNote: 'If the Step 3 sample-size guard fires, exit with the INSUFFICIENT_SAMPLE block as instructed and do NOT edit any file. If Step 8 structural escalation fires, write STRUCTURAL_REVIEW_NEEDED_TREND.md and do NOT edit TRADING_RULES_TREND.md.',
        }));
        await this._saveState();
      }
      return { success: true, job: jobName, date: isoDate };
    } finally {
      this._activeJob = null;
      await this._releaseLock(lockKey);
    }
  }

  // Build the AUTOMATED RUN appendix for skills that normally request user confirmation.
  _automatedRunAppendix({ confirmStep, targetFile, changeNoun, guardNote }) {
    const guard = guardNote ? `${guardNote} ` : '';
    return `**AUTOMATED RUN**: This analysis was triggered automatically by the scheduler. ${guard}Otherwise, after completing the analysis and proposing edits, skip ${confirmStep} and automatically apply all proposed changes to ${targetFile}. List every ${changeNoun} that was changed in your final response.`;
  }

  // Run all startup checks in order. Call once after start() — runs in background.
  async runStartupChecks() {
    const isoDate = new Date().toLocaleDateString('en-CA', { timeZone: 'America/New_York' });
    const todaySlug = isoDate.replace(/-/g, '');
    const { hour, dayOfWeek } = this._getETInfo();
    const isWeekday = dayOfWeek >= 1 && dayOfWeek <= 5;
    let adaptNeeded = false;

    // 1. Daily briefing (file-based) — skip if market has already closed (≥4 PM ET); will fire at 6 AM ET next weekday
    try { await fs.access(path.join(REPORTS_DIR, `daily_brief_${todaySlug}.json`)); }
    catch {
      if (await this._isLocked(this._getLockKey('daily_briefing', isoDate))) {
        this._log('Daily briefing already running in another process — skipping startup trigger.', 'info');
      } else if (isWeekday && hour < 16) {
        this._log('No daily briefing for today — triggering now...', 'info');
        await this.triggerJob('daily_briefing').catch(() => {});
      } else {
        this._log('No daily briefing for today — skipping (market closed, will run at 6 AM ET next weekday).', 'info');
      }
    }

    // 2. Scenario analysis (state-based)
    if (this._lastScenarioDate !== isoDate) {
      if (await this._isLocked(this._getLockKey('scenario_analysis', isoDate))) {
        this._log('Scenario analysis already running in another process — skipping startup trigger.', 'info');
      } else {
        this._log('No scenario analysis for today — triggering now...', 'info');
        await this.triggerJob('scenario_analysis').catch(() => {});
      }
    }

    // 3. Weekly performance review (state-based)
    if (this._lastReviewWeek !== this._getISOWeek(isoDate)) {
      if (await this._isLocked(this._getLockKey('review_performance', isoDate))) {
        this._log('Weekly performance review already running in another process — skipping startup trigger.', 'info');
      } else {
        this._log('No performance review this week — triggering now...', 'info');
        await this.triggerJob('review_performance').catch(() => {});
        adaptNeeded = true;
      }
    }

    // 4. Postmortem for significant loss (activity log detection)
    const lossInfo = await this._detectLossConditions();
    if (lossInfo?.significantLoss && this._lastPostmortemDate !== lossInfo.lossDate) {
      if (await this._isLocked(this._getLockKey('postmortem', lossInfo.lossDate))) {
        this._log('Postmortem already running in another process — skipping startup trigger.', 'info');
      } else {
        this._log(`Significant loss on ${lossInfo.lossDate} (${lossInfo.lossPercent.toFixed(1)}%) — triggering postmortem...`, 'warning');
        await this.triggerJob('postmortem', lossInfo.lossDate).catch(() => {});
        adaptNeeded = true;
      }
    }
    if (lossInfo?.consecutiveLossDays >= 3) {
      this._log('3 consecutive losing days detected.', 'warning');
      adaptNeeded = true;
    }

    // 5. Adapt strategy if anything above triggered it
    if (adaptNeeded && this._lastAdaptDate !== isoDate) {
      if (await this._isLocked(this._getLockKey('adapt_strategy', isoDate))) {
        this._log('Adapt strategy already running in another process — skipping startup trigger.', 'info');
      } else {
        this._log('Triggering adapt-strategy...', 'info');
        await this.triggerJob('adapt_strategy').catch(() => {});
      }
    }

    // 6. Penny weekly performance review (state-based, separate state key)
    let pennyAdaptNeeded = false;
    if (this._lastPennyReviewWeek !== this._getISOWeek(isoDate)) {
      if (await this._isLocked(this._getLockKey('review_performance_penny', isoDate))) {
        this._log('Penny performance review already running in another process — skipping startup trigger.', 'info');
      } else {
        this._log('No Penny performance review this week — triggering now...', 'info');
        await this.triggerJob('review_performance_penny').catch(() => {});
        pennyAdaptNeeded = true;
      }
    }

    // 7. Penny postmortem for significant loss (penny-scoped, -3% threshold)
    const pennyLoss = await this._detectPennyLossConditions();
    if (pennyLoss?.significantLoss && this._lastPennyPostmortemDate !== pennyLoss.lossDate) {
      if (await this._isLocked(this._getLockKey('postmortem_penny', isoDate, pennyLoss.lossDate))) {
        this._log('Penny postmortem already running in another process — skipping startup trigger.', 'info');
      } else {
        this._log(`Significant Penny loss on ${pennyLoss.lossDate} (${pennyLoss.lossPercent.toFixed(1)}%) — triggering postmortem-penny...`, 'warning');
        await this.triggerJob('postmortem_penny', pennyLoss.lossDate, pennyLoss.lossDate).catch(() => {});
        pennyAdaptNeeded = true;
      }
    }

    // 8. Apply penny adapt-strategy if review or postmortem triggered it (separate from Prophet's adapt)
    if (pennyAdaptNeeded) {
      if (await this._isLocked(this._getLockKey('adapt_strategy_penny', isoDate))) {
        this._log('Adapt-strategy-penny already running in another process — skipping startup trigger.', 'info');
      } else {
        this._log('Triggering adapt-strategy-penny...', 'info');
        await this.triggerJob('adapt_strategy_penny').catch(() => {});
      }
    }

    // 9. Monthly Harvest parameter review (state-based)
    if (this._lastHarvestParamReviewMonth !== this._getMonth(isoDate)) {
      if (await this._isLocked(this._getLockKey('harvest_parameter_review', isoDate))) {
        this._log('Harvest parameter review already running in another process — skipping startup trigger.', 'info');
      } else {
        this._log('No Harvest parameter review this month — triggering now...', 'info');
        await this.triggerJob('harvest_parameter_review').catch(() => {});
      }
    }

    // 10. Quarterly Trend parameter review (state-based)
    if (this._lastTrendParamReviewQuarter !== this._getQuarter(isoDate)) {
      if (await this._isLocked(this._getLockKey('trend_parameter_review', isoDate))) {
        this._log('Trend parameter review already running in another process — skipping startup trigger.', 'info');
      } else {
        this._log('No Trend parameter review this quarter — triggering now...', 'info');
        await this.triggerJob('trend_parameter_review').catch(() => {});
      }
    }
  }

  // ── Mid-session scan loop ────────────────────────────────────────

  _startScanLoop() {
    if (this._scanTimer) return;
    this._scanTimer = setInterval(() => this._runMidSessionScan(), 15 * 60 * 1000);
    // Run immediately if market is currently open
    this._runMidSessionScan();
  }

  _stopScanLoop() {
    if (this._scanTimer) { clearInterval(this._scanTimer); this._scanTimer = null; }
  }

  async _runMidSessionScan() {
    if (this._scanActive) return;
    const { hour, minute, isoDate, dayOfWeek } = this._getETInfo();
    const isWeekday = dayOfWeek >= 1 && dayOfWeek <= 5;
    const isMarketHours = isWeekday && (hour > 9 || (hour === 9 && minute >= 30)) && hour < 16;
    if (!isMarketHours) return;

    this._scanActive = true;
    try {
      await this._runScan(isoDate);
    } catch (err) {
      this._log(`Mid-session scan error: ${err.message}`, 'error');
    } finally {
      this._scanActive = false;
    }
  }

  async _runScan(date) {
    const scanStart = Date.now();
    const dateSlug = date.replace(/-/g, '');
    const now = new Date();
    const timeSlug = now.toLocaleTimeString('en-US', { timeZone: 'America/New_York', hour12: false })
      .replace(/:/g, '').slice(0, 6);
    const alertFile = `market_alert_${dateSlug}_${timeSlug}.json`;

    // Option 2: Inject today's already-fired alert summaries into the prompt
    const existingAlertSummaries = [];
    try {
      const allFiles = await fs.readdir(REPORTS_DIR).catch(() => []);
      for (const f of allFiles.filter(f => f.startsWith(`market_alert_${dateSlug}_`) && f.endsWith('.json'))) {
        try {
          const c = JSON.parse(await fs.readFile(path.join(REPORTS_DIR, f), 'utf-8'));
          if (c.alert_summary) existingAlertSummaries.push(c.alert_summary);
        } catch {}
      }
    } catch {}

    const alreadyAlertedBlock = existingAlertSummaries.length > 0
      ? `\nALREADY ALERTED TODAY — do NOT re-alert on these stories (assign score < 7 for any news that overlaps with them):\n${existingAlertSummaries.map((s, i) => `${i + 1}. ${s}`).join('\n')}\n`
      : '';

    const prompt = `You are the Prophet Mid-Session Market Scanner. The time is ${now.toLocaleString('en-US', { timeZone: 'America/New_York' })} ET. Today is ${date}.

Your job: scan for breaking market-moving news and determine if it warrants an emergency alert to the trading agents.
${alreadyAlertedBlock}
Step 1: Call get_marketwatch_bulletins to fetch real-time market bulletins.
Step 2: Call get_marketwatch_realtime to fetch the latest real-time headlines.

Step 3: Assess significance. Score 1-10 based on:
- 8-10: Systemic impact — FOMC surprise, major index move >1%, banking crisis, circuit breakers, major war escalation, or large-cap earnings surprise with sector contagion (e.g., a cloud/AI company miss that drags related infrastructure names)
- 6-7: Cross-asset — single sector move >3%, large-cap earnings beat/miss, commodity spike/drop >3%
- 4-5: Notable — moderate earnings impact, in-line economic data, geopolitical update without escalation
- 1-3: Routine — no market-moving news, or nothing meaningfully new since market open

Step 4: Only if significance score >= 7, use the Write tool to save to exactly this path:
data/reports/${alertFile}

The JSON must be exactly this structure:
{
  "generated_at": "<current UTC ISO timestamp>",
  "significance_score": <integer 1-10>,
  "alert_summary": "<1-2 sentences: what happened and what markets/sectors are affected and in which direction>",
  "affected_tickers": ["<ticker1>", ...],
  "affected_sectors": ["<sector1>", ...],
  "direction": "<bullish|bearish|mixed>",
  "headlines": [<up to 3 objects: {"headline": "<title>", "source": "<pub>", "impact": "<1 sentence>"}>]
}

If significance score < 7: do NOT write any file. Output only: SCAN_COMPLETE: no significant news (score: N)
If significance score >= 7: write the file, then output: SCAN_ALERT: <your alert_summary>`;

    await this._runOneshotOpencode(prompt, 'mid_session_scan', 3 * 60 * 1000);

    if (!this.onEmergencyWake) return;
    try {
      const files = (await fs.readdir(REPORTS_DIR).catch(() => []));
      const alertFiles = files.filter(f => f.startsWith('market_alert_') && f.endsWith('.json'));
      for (const f of alertFiles) {
        const fullPath = path.join(REPORTS_DIR, f);
        const stat = await fs.stat(fullPath);
        if (stat.mtimeMs >= scanStart) {
          const content = JSON.parse(await fs.readFile(fullPath, 'utf-8'));
          if ((content.significance_score || 0) >= 7) {
            // Option 1: ticker-based fingerprint guard — suppress if already fired today
            const fingerprint = this._getAlertFingerprint(date, content);
            const todayFired = this._firedAlertFingerprints.get(date) || new Set();
            if (todayFired.has(fingerprint)) {
              this._log(`Duplicate alert suppressed (fingerprint already fired today): ${fingerprint}`, 'info');
              break;
            }
            todayFired.add(fingerprint);
            this._firedAlertFingerprints.set(date, todayFired);
            await this._saveState();
            this._lastAlertTime = new Date().toISOString();
            this._log(`ALERT fired: ${content.alert_summary}`, 'warning');
            this.onEmergencyWake(content.alert_summary);
          }
          break;
        }
      }
    } catch (err) {
      this._log(`Alert file check error: ${err.message}`, 'error');
    }
  }

  // ── Private helpers ──────────────────────────────────────────────

  _log(message, level = 'info') {
    this.emit('agent_log', { message: `[Scheduler] ${message}`, level, timestamp: new Date().toISOString() });
  }

  // Returns the lock file key for a given job — used as `${key}.running` in REPORTS_DIR.
  _getLockKey(jobName, date, target) {
    const dateSlug = (date || '').replace(/-/g, '');
    switch (jobName) {
      case 'daily_briefing':    return `daily_brief_${dateSlug}`;
      case 'weekly_screeners':  return `weekly_screeners_${dateSlug}`;
      case 'scenario_analysis': return `scenario_analysis_${dateSlug}`;
      case 'review_performance': return `review_performance_${this._getISOWeek(date).replace(/[^a-z0-9]/gi, '_')}`;
      case 'postmortem':        return `postmortem_${(target || date || '').replace(/-/g, '')}`;
      case 'adapt_strategy':    return `adapt_strategy_${dateSlug}`;
      case 'review_performance_penny': return `review_performance_penny_${this._getISOWeek(date).replace(/[^a-z0-9]/gi, '_')}`;
      case 'postmortem_penny':  return `postmortem_penny_${(target || date || '').replace(/-/g, '')}`;
      case 'adapt_strategy_penny': return `adapt_strategy_penny_${dateSlug}`;
      case 'harvest_parameter_review': return `harvest_parameter_review_${this._getMonth(date).replace(/[^a-z0-9]/gi, '_')}`;
      case 'trend_parameter_review':   return `trend_parameter_review_${this._getQuarter(date).replace(/[^a-z0-9]/gi, '_')}`;
      default:                  return `${jobName.replace(/[^a-z0-9]/gi, '_')}_${dateSlug}`;
    }
  }

  // Exclusive lock via atomic file creation — returns true if acquired, false if already held.
  async _acquireLock(lockKey) {
    await fs.mkdir(REPORTS_DIR, { recursive: true });
    try {
      const fd = await fs.open(path.join(REPORTS_DIR, `${lockKey}.running`), 'wx');
      await fd.close();
      return true;
    } catch {
      return false;
    }
  }

  async _releaseLock(lockKey) {
    await fs.unlink(path.join(REPORTS_DIR, `${lockKey}.running`)).catch(() => {});
  }

  async _isLocked(lockKey) {
    return fs.access(path.join(REPORTS_DIR, `${lockKey}.running`)).then(() => true).catch(() => false);
  }

  _getETInfo() {
    const now = new Date();
    const et = now.toLocaleTimeString('en-US', {
      timeZone: 'America/New_York', hour: '2-digit', minute: '2-digit', hour12: false,
    });
    const [hour, minute] = et.split(':').map(Number);
    const isoDate = now.toLocaleDateString('en-CA', { timeZone: 'America/New_York' });
    const dayName = now.toLocaleDateString('en-US', { timeZone: 'America/New_York', weekday: 'long' });
    const dayOfWeek = ['Sunday','Monday','Tuesday','Wednesday','Thursday','Friday','Saturday'].indexOf(dayName);
    return { hour, minute, isoDate, dayOfWeek };
  }

  _getISOWeek(dateStr) {
    const d = new Date(dateStr + 'T12:00:00Z');
    const day = d.getUTCDay() || 7;
    d.setUTCDate(d.getUTCDate() + 4 - day);
    const yearStart = new Date(Date.UTC(d.getUTCFullYear(), 0, 1));
    const weekNo = Math.ceil((((d - yearStart) / 86400000) + 1) / 7);
    return `${d.getUTCFullYear()}-W${String(weekNo).padStart(2, '0')}`;
  }

  // YYYY-MM (e.g. 2026-05). dateStr is an ISO date in ET (matches the en-CA / America/New_York format used elsewhere).
  _getMonth(dateStr) {
    return (dateStr || '').slice(0, 7);
  }

  // YYYY-Q{1|2|3|4} (e.g. 2026-Q2).
  _getQuarter(dateStr) {
    const [y, m] = (dateStr || '').split('-').map(Number);
    if (!y || !m) return '';
    const q = Math.floor((m - 1) / 3) + 1;
    return `${y}-Q${q}`;
  }

  async _loadState() {
    try {
      const raw = await fs.readFile(STATE_FILE, 'utf-8');
      const s = JSON.parse(raw);
      this._lastReviewWeek = s.lastReviewWeek || null;
      this._lastPostmortemDate = s.lastPostmortemDate || null;
      this._lastAdaptDate = s.lastAdaptDate || null;
      this._lastLossCheckDate = s.lastLossCheckDate || null;
      this._lastScenarioDate = s.lastScenarioDate || null;
      this._lastPennyReviewWeek = s.lastPennyReviewWeek || null;
      this._lastPennyPostmortemDate = s.lastPennyPostmortemDate || null;
      this._lastHarvestParamReviewMonth = s.lastHarvestParamReviewMonth || null;
      this._lastTrendParamReviewQuarter = s.lastTrendParamReviewQuarter || null;
      const today = new Date().toLocaleDateString('en-CA', { timeZone: 'America/New_York' });
      const persisted = s.firedAlertFingerprints || {};
      // Only restore today's fingerprints — older dates are irrelevant
      if (persisted[today]) {
        this._firedAlertFingerprints.set(today, new Set(persisted[today]));
      }
    } catch {}
  }

  async _saveState() {
    try {
      const today = new Date().toLocaleDateString('en-CA', { timeZone: 'America/New_York' });
      const firedAlertFingerprints = {};
      const todaySet = this._firedAlertFingerprints.get(today);
      if (todaySet?.size) firedAlertFingerprints[today] = [...todaySet];
      await fs.writeFile(STATE_FILE, JSON.stringify({
        lastReviewWeek: this._lastReviewWeek,
        lastPostmortemDate: this._lastPostmortemDate,
        lastAdaptDate: this._lastAdaptDate,
        lastLossCheckDate: this._lastLossCheckDate,
        lastScenarioDate: this._lastScenarioDate,
        lastPennyReviewWeek: this._lastPennyReviewWeek,
        lastPennyPostmortemDate: this._lastPennyPostmortemDate,
        lastHarvestParamReviewMonth: this._lastHarvestParamReviewMonth,
        lastTrendParamReviewQuarter: this._lastTrendParamReviewQuarter,
        firedAlertFingerprints,
      }, null, 2), 'utf-8');
    } catch {}
  }

  // Fingerprint for dedup: date + sorted affected_tickers. Falls back to normalized summary.
  _getAlertFingerprint(date, content) {
    const tickers = (content.affected_tickers || []).slice().sort().join(',');
    if (tickers) return `${date}::${tickers}`;
    const normalized = (content.alert_summary || '').toLowerCase().replace(/[^a-z0-9 ]/g, '').slice(0, 80);
    return `${date}::${normalized}`;
  }

  // Returns { significantLoss, lossDate, lossPercent, consecutiveLossDays } or null.
  async _detectLossConditions() {
    try {
      // Find sandboxes that have activity logs
      const sandboxes = await fs.readdir(SANDBOXES_DIR).catch(() => []);
      let allLogs = [];
      for (const sb of sandboxes) {
        const logsDir = path.join(SANDBOXES_DIR, sb, 'activity_logs');
        let files;
        try { files = (await fs.readdir(logsDir)).filter(f => f.startsWith('activity_') && f.endsWith('.json')).sort(); }
        catch { continue; }
        for (const f of files.slice(-5)) {
          try {
            const log = JSON.parse(await fs.readFile(path.join(logsDir, f), 'utf-8'));
            const s = log.summary || {};
            const hasTrades = (s.winning_trades || 0) + (s.losing_trades || 0) > 0;
            if (hasTrades) allLogs.push({ date: log.date, pnlPct: s.total_pnl_percent || 0 });
          } catch {}
        }
      }
      if (allLogs.length === 0) return null;
      allLogs.sort((a, b) => a.date.localeCompare(b.date));

      const latest = allLogs[allLogs.length - 1];
      const significantLoss = latest.pnlPct <= -4.0;

      // Count consecutive losing days from the end
      let consecutiveLossDays = 0;
      for (let i = allLogs.length - 1; i >= 0; i--) {
        if (allLogs[i].pnlPct < 0) consecutiveLossDays++;
        else break;
      }

      return {
        significantLoss,
        lossDate: significantLoss ? latest.date : null,
        lossPercent: latest.pnlPct,
        consecutiveLossDays,
      };
    } catch { return null; }
  }

  // Resolves the on-disk directory name for the Penny sandbox by reading agent-config.json
  // and picking the sandbox whose name matches /penny/i. Returns null if not found.
  async _resolvePennySandboxDir() {
    try {
      const configPath = path.join(PROJECT_ROOT, 'data', 'agent-config.json');
      const config = JSON.parse(await fs.readFile(configPath, 'utf-8'));
      const sandboxes = config.sandboxes || {};
      for (const sb of Object.values(sandboxes)) {
        if (sb && typeof sb.name === 'string' && /penny/i.test(sb.name) && sb.accountId) {
          return sb.accountId;
        }
      }
      return null;
    } catch { return null; }
  }

  // Penny-scoped detector: only reads the Penny sandbox's activity_logs/.
  // Sandbox directory is resolved from agent-config.json (sandbox name matches /penny/i).
  // Threshold is -3% per spec (Penny is more loss-sensitive than Prophet's -4%).
  async _detectPennyLossConditions() {
    try {
      const pennyDir = await this._resolvePennySandboxDir();
      if (!pennyDir) return null;
      const logsDir = path.join(SANDBOXES_DIR, pennyDir, 'activity_logs');
      let files;
      try { files = (await fs.readdir(logsDir)).filter(f => f.startsWith('activity_') && f.endsWith('.json')).sort(); }
      catch { return null; }
      const logs = [];
      for (const f of files.slice(-5)) {
        try {
          const log = JSON.parse(await fs.readFile(path.join(logsDir, f), 'utf-8'));
          const s = log.summary || {};
          const hasTrades = (s.winning_trades || 0) + (s.losing_trades || 0) > 0;
          if (hasTrades) logs.push({ date: log.date, pnlPct: s.total_pnl_percent || 0 });
        } catch {}
      }
      if (logs.length === 0) return null;
      logs.sort((a, b) => a.date.localeCompare(b.date));
      const latest = logs[logs.length - 1];
      const significantLoss = latest.pnlPct <= -3.0;
      return {
        significantLoss,
        lossDate: significantLoss ? latest.date : null,
        lossPercent: latest.pnlPct,
      };
    } catch { return null; }
  }

  async _checkAndRunLossJobs(isoDate) {
    // Prophet (aggregate-sandbox) loss flow — unchanged behavior.
    const lossInfo = await this._detectLossConditions();
    let adaptNeeded = false;
    if (lossInfo) {
      if (lossInfo.significantLoss && this._lastPostmortemDate !== lossInfo.lossDate) {
        this._log(`Significant loss on ${lossInfo.lossDate} (${lossInfo.lossPercent.toFixed(1)}%) — triggering postmortem...`, 'warning');
        await this.triggerJob('postmortem', lossInfo.lossDate).catch(() => {});
        adaptNeeded = true;
      }
      if (lossInfo.consecutiveLossDays >= 3) adaptNeeded = true;
      if (adaptNeeded && this._lastAdaptDate !== isoDate) {
        await this.triggerJob('adapt_strategy').catch(() => {});
      }
    }

    // Penny-scoped loss flow (separate state key, -3% threshold).
    const pennyLoss = await this._detectPennyLossConditions();
    if (pennyLoss?.significantLoss && this._lastPennyPostmortemDate !== pennyLoss.lossDate) {
      this._log(`Significant Penny loss on ${pennyLoss.lossDate} (${pennyLoss.lossPercent.toFixed(1)}%) — triggering postmortem-penny...`, 'warning');
      await this.triggerJob('postmortem_penny', pennyLoss.lossDate, pennyLoss.lossDate).catch(() => {});
      await this.triggerJob('adapt_strategy_penny').catch(() => {});
    }
  }

  async _checkSchedule() {
    if (!this._running || this._activeJob) return;
    const { hour, minute, isoDate, dayOfWeek } = this._getETInfo();
    const isWeekday = dayOfWeek >= 1 && dayOfWeek <= 5;
    const isMonday = dayOfWeek === 1;
    const isSunday = dayOfWeek === 0;
    const dayOfMonth = Number(isoDate.split('-')[2]);
    const monthOfYear = Number(isoDate.split('-')[1]);
    const isQuarterStartMonth = monthOfYear === 1 || monthOfYear === 4 || monthOfYear === 7 || monthOfYear === 10;
    const currentMonth = this._getMonth(isoDate);
    const currentQuarter = this._getQuarter(isoDate);
    const currentWeek = this._getISOWeek(isoDate);

    if (isWeekday && hour === 6 && minute === 0 && this._lastDailyBriefDate !== isoDate) {
      await this.triggerJob('daily_briefing').catch(() => {});
    }

    // Monthly Harvest parameter review — 1st of each month at 6:00 AM ET (any day-of-week).
    if (dayOfMonth === 1 && hour === 6 && minute === 0 && this._lastHarvestParamReviewMonth !== currentMonth) {
      await this.triggerJob('harvest_parameter_review').catch(() => {});
    }

    // Quarterly Trend parameter review — 1st of Jan/Apr/Jul/Oct at 6:00 AM ET.
    if (dayOfMonth === 1 && isQuarterStartMonth && hour === 6 && minute === 0 && this._lastTrendParamReviewQuarter !== currentQuarter) {
      await this.triggerJob('trend_parameter_review').catch(() => {});
    }

    if (isMonday && hour === 6 && minute === 5 && this._lastReviewWeek !== currentWeek) {
      await this.triggerJob('review_performance').catch(() => {});
      if (this._lastAdaptDate !== isoDate) {
        await this.triggerJob('adapt_strategy').catch(() => {});
      }
    }

    // Penny weekly review — 5 minutes after Prophet's, so the two adapt jobs don't try to run concurrently.
    if (isMonday && hour === 6 && minute === 10 && this._lastPennyReviewWeek !== currentWeek) {
      await this.triggerJob('review_performance_penny').catch(() => {});
      await this.triggerJob('adapt_strategy_penny').catch(() => {});
    }

    if (isWeekday && hour === 16 && minute === 30 && this._lastLossCheckDate !== isoDate) {
      this._lastLossCheckDate = isoDate;
      await this._saveState();
      await this._checkAndRunLossJobs(isoDate);
    }

    if (isSunday && hour === 18 && minute === 0 && this._lastWeeklyScreenDate !== isoDate) {
      await this.triggerJob('weekly_screeners').catch(() => {});
    }
  }

  // ── Job runners ──────────────────────────────────────────────────

  async _runDailyBriefing(date) {
    const dateSlug = date.replace(/-/g, '');
    // Lock is acquired by triggerJob — no per-runner lock needed here.

    this._log(`Starting daily briefing for ${date}...`, 'info');
    this.emit('scheduler_job_start', { job: 'daily_briefing', date });

    const hasFmp = !!process.env.FMP_API_KEY;
    const fmpNote = hasFmp ? '' : '\nNote: FMP_API_KEY not set — FTD check, economic calendar, earnings calendar, and analyst actions will be skipped.';

    const prompt = `You are the Prophet Pre-Market Analysis Agent. Today is ${date}. Your job is to run the daily pre-market briefing pipeline and save the results.${fmpNote}

Call these MCP tools in this exact order:
1. run_market_briefing — fetches breadth and uptrend ratio data from public CSV sources (no API key needed). Wait for it to complete.
${hasFmp ? `2. run_ftd_check — detects Follow-Through Day signals (requires FMP API).
3. run_economic_calendar — fetches this week's tier-1 macro events (FOMC, CPI, NFP, GDP).
4. run_earnings_calendar — fetches key earnings announcements for this week.
5. run_analyst_actions — fetches the last 24h of analyst rating changes and price-target updates across Prophet's liquid optionable universe (~50 names). Tier-1 banks (Goldman, Morgan Stanley, JPM, BofA, Citi, Wells Fargo) and large PT moves rank highest.
6. get_marketwatch_all — fetches all MarketWatch feeds (top stories, realtime headlines, bulletins, market pulse). Scan for any market-moving news: earnings results or misses (including private companies), executive commentary, sector contagion, macro surprises, or geopolitical events. Extract up to 7 headlines that a trader must know about today.` : `2. (Skipping FTD, economic calendar, earnings calendar, and analyst actions — FMP_API_KEY not set)
3. get_marketwatch_all — fetches all MarketWatch feeds. Scan for market-moving headlines and extract up to 7 that a trader must know about today.`}

After all tools have returned, use the Write tool to save the briefing to exactly this path:
data/reports/daily_brief_${dateSlug}.json

The JSON must be exactly this structure (fill all values from tool results):
{
  "date": "${date}",
  "generated_at": "<current UTC ISO timestamp>",
  "market_posture": "<BULLISH|NEUTRAL|BEARISH — based on breadth score: BULLISH >70, NEUTRAL 40-70, BEARISH <40>",
  "breadth_score": <integer 0-100 from run_market_briefing composite score>,
  "uptrend_ratio": <float 0-100 from run_market_briefing uptrend ratio field>,
  "ftd_status": "<active_ftd|rally_attempt|no_signal|correction — from run_ftd_check, or null if skipped>",
  "tier1_macro_events": [<objects from run_economic_calendar with date, event, impact fields — empty array if skipped or none>],
  "key_earnings_this_week": [<objects from run_earnings_calendar with date, ticker, timing fields — empty array if skipped or none>],
  "analyst_actions": [<top-ranked objects from run_analyst_actions — pass through the JSON array as-is (up to 15 events). Each event: {ticker, type ("pt_change"|"rating_change"), firm, action, from, to, date}. Empty array if skipped or none.>],
  "market_headlines": [<up to 7 objects from get_marketwatch_all that represent market-moving news — each object: {"headline": "<title>", "source": "<publication>", "impact": "<1 sentence: what moves and which direction>", "sectors_affected": ["<sector1>", ...]}. Include earnings misses/beats, executive statements, sector contagion, macro shocks, and geopolitical news. Empty array only if no significant news found.>],
  "exposure_ceiling_pct": <integer 0-100 — your recommended max exposure: 100 if BULLISH, 60 if NEUTRAL, 20 if BEARISH; reduce further if active_ftd, tier-1 event today, or major negative market_headlines>,
  "summary": "<2-3 sentences describing today's market setup, key risks from headlines, notable analyst actions on Prophet's universe, and any sector-specific warnings>"
}

Use null for any field where the corresponding tool failed. Use [] for analyst_actions if the tool was skipped or returned empty. Write only the JSON — no markdown, no explanation.`;

    await this._runOneshotOpencode(prompt, 'daily_briefing', 10 * 60 * 1000);
    this._log(`Daily briefing complete → data/reports/daily_brief_${dateSlug}.json`, 'success');
    this.emit('scheduler_job_end', { job: 'daily_briefing', date, output: `data/reports/daily_brief_${dateSlug}.json` });
  }

  async _runWeeklyScreeners(date) {
    const dateSlug = date.replace(/-/g, '');
    this._log(`Starting weekly screeners for week of ${date}...`, 'info');
    this.emit('scheduler_job_start', { job: 'weekly_screeners', date });

    const hasFmp = !!process.env.FMP_API_KEY;
    const fmpNote = hasFmp ? '' : '\nNote: FMP_API_KEY not set — market top check, VCP, and PEAD screeners will be skipped.';

    const prompt = `You are the Prophet Weekly Research Agent. Today is ${date} (Sunday). Run the weekly screening pipeline.${fmpNote}

Call these MCP tools in this exact order:
1. run_market_briefing — fetch current breadth and uptrend data.
${hasFmp ? `2. run_market_top_check — get distribution day count and market top probability (runs synchronously, ~90 seconds).
3. run_vcp_screener — start background VCP screener (returns immediately with job status).
4. run_pead_screener — start background PEAD screener (returns immediately with job status).
5. wait(210) — wait 3.5 minutes for background screeners to finish.
6. read_latest_report("vcp") — retrieve VCP screener results.
7. read_latest_report("pead") — retrieve PEAD screener results.` : `2. (Skipping FMP screeners — FMP_API_KEY not set)`}

After all tools complete, use the Write tool to save to data/reports/weekly_regime_${dateSlug}.json:
{
  "date": "${date}",
  "generated_at": "<current UTC ISO timestamp>",
  "breadth_score": <0-100 integer from run_market_briefing>,
  "uptrend_ratio": <float 0-100>,
  "market_top_score": <0-100 integer from run_market_top_check, or null if skipped>,
  "distribution_days": <count from run_market_top_check, or null if skipped>,
  "market_posture": "<BULLISH|NEUTRAL|BEARISH|CORRECTION|TOP_RISK>",
  "vcp_candidates": [<top 5 VCP candidates: {ticker, score, execution_state, pivot_price, stop_loss} — empty array if skipped>],
  "pead_candidates": [<top 5 PEAD candidates: {ticker, score, entry_zone, stop_loss} — empty array if skipped>],
  "weekly_thesis": "<2-3 sentences: current market conditions, key risks for the week, general posture>"
}

Limit vcp_candidates and pead_candidates to top 5 each by score. Write only the JSON.`;

    await this._runOneshotOpencode(prompt, 'weekly_screeners', 25 * 60 * 1000);
    this._log(`Weekly screeners complete → data/reports/weekly_regime_${dateSlug}.json`, 'success');
    this.emit('scheduler_job_end', { job: 'weekly_screeners', date, output: `data/reports/weekly_regime_${dateSlug}.json` });
  }

  async _runScenarioAnalysis(date) {
    const dateSlug = date.replace(/-/g, '');
    this._log(`Starting scenario analysis for ${date}...`, 'info');
    this.emit('scheduler_job_start', { job: 'scenario_analysis', date });

    const prompt = await this._readSkillPrompt('scenario-analysis');
    if (!prompt) {
      this.emit('scheduler_job_end', { job: 'scenario_analysis', date, output: null });
      return;
    }

    await this._runOneshotOpencode(prompt, 'scenario_analysis', 15 * 60 * 1000);
    this._log(`Scenario analysis complete → data/reports/scenario_*_${dateSlug}.md`, 'success');
    this.emit('scheduler_job_end', { job: 'scenario_analysis', date, output: `data/reports/scenario_*_${dateSlug}.md` });
  }

  // Generic skill runner. Replaces $ARGUMENTS in the prompt with `target` if provided.
  // Optional `appendix` is concatenated after a `---` separator (used for AUTOMATED RUN
  // instructions that override skills' user-confirmation steps when scheduled).
  async _runSkill(skillName, date, target, timeoutMs, appendix = null) {
    this._log(`Starting ${skillName} for ${date}${target ? ` (target: ${target})` : ''}...`, 'info');
    this.emit('scheduler_job_start', { job: skillName.replace(/-/g, '_'), date });

    let prompt = await this._readSkillPrompt(skillName);
    if (!prompt) {
      this.emit('scheduler_job_end', { job: skillName.replace(/-/g, '_'), date, output: null });
      return;
    }
    if (target !== null && target !== undefined) {
      prompt = prompt.replace(/\$ARGUMENTS/g, target);
    }
    if (appendix) {
      prompt += '\n\n---\n' + appendix;
    }

    await this._runOneshotOpencode(prompt, skillName, timeoutMs);
    this._log(`${skillName} complete.`, 'success');
    this.emit('scheduler_job_end', { job: skillName.replace(/-/g, '_'), date, output: null });
  }

  async _runAdaptStrategy(date) {
    this._log(`Starting adapt-strategy for ${date}...`, 'info');
    this.emit('scheduler_job_start', { job: 'adapt_strategy', date });

    let prompt = await this._readSkillPrompt('adapt-strategy');
    if (!prompt) {
      this.emit('scheduler_job_end', { job: 'adapt_strategy', date, output: null });
      return;
    }

    // Automated run: skip the confirmation step and apply all proposed edits autonomously.
    prompt += '\n\n---\n**AUTOMATED RUN**: This analysis was triggered automatically by the scheduler. After completing the gap analysis and proposing edits, skip Step 6 (user confirmation) and automatically apply all proposed changes to data/agent-config.json. List every rule that was changed in your final response.';

    await this._runOneshotOpencode(prompt, 'adapt_strategy', 15 * 60 * 1000);
    this._log('adapt-strategy complete.', 'success');
    this.emit('scheduler_job_end', { job: 'adapt_strategy', date, output: 'data/agent-config.json' });
  }

  // Read a skill's SKILL.md and strip the YAML frontmatter.
  async _readSkillPrompt(skillName) {
    const skillPath = path.join(PROJECT_ROOT, '.claude', 'skills', skillName, 'SKILL.md');
    try {
      const raw = await fs.readFile(skillPath, 'utf-8');
      const match = raw.match(/^---[\s\S]*?---\n([\s\S]*)$/);
      return match ? match[1].trim() : raw.trim();
    } catch (err) {
      this._log(`Cannot read ${skillName} skill: ${err.message}`, 'error');
      return null;
    }
  }

  async _runOneshotOpencode(prompt, jobName, timeoutMs) {
    return new Promise(async (resolve) => {
      const ocModel = this.model?.includes('/') ? this.model : `anthropic/${this.model}`;
      const args = ['run', '--format', 'json', '--model', ocModel];

      let tempFile = null;
      if (process.platform === 'win32') {
        tempFile = path.join(os.tmpdir(), `prophet_sched_${Date.now()}.txt`);
        await fs.writeFile(tempFile, prompt, 'utf-8');
        args.push('Process the prompt from the attached file.', '--file', tempFile);
      } else {
        args.push(prompt);
      }

      const proc = spawn(OPENCODE_BIN, [...OPENCODE_WIN_PREFIX, ...args], {
        cwd: PROJECT_ROOT,
        env: {
          ...process.env,
          ANTHROPIC_API_KEY: process.env.CLAUDE_API_KEY || process.env.ANTHROPIC_API_KEY || '',
          OPENPROPHET_SANDBOX_ID: 'analysis',
          OPENPROPHET_ACCOUNT_ID: 'analysis',
        },
        stdio: ['pipe', 'pipe', 'pipe'],
      });

      proc.stdin.end();

      let buffer = '';
      proc.stdout.on('data', (chunk) => {
        buffer += chunk.toString();
        const lines = buffer.split('\n');
        buffer = lines.pop();
        for (const line of lines) {
          if (!line.trim()) continue;
          try {
            const event = JSON.parse(line);
            if (event.type === 'text' && event.part?.text?.trim()) {
              this._log(`${event.part.text.trim().slice(0, 200)}`, 'info');
            }
          } catch {}
        }
      });

      proc.stderr.on('data', (chunk) => {
        const msg = chunk.toString().trim();
        if (msg && !msg.toLowerCase().startsWith('warn')) {
          this._log(`[stderr] ${msg.slice(0, 200)}`, 'info');
        }
      });

      const timeout = setTimeout(() => {
        if (!proc.killed) {
          this._log(`Job timed out after ${Math.round(timeoutMs / 60000)} min`, 'warning');
          proc.kill('SIGTERM');
        }
      }, timeoutMs);

      proc.on('exit', (code) => {
        clearTimeout(timeout);
        if (tempFile) fs.unlink(tempFile).catch(() => {});
        this._log(`[${jobName}] finished (exit: ${code})`, code === 0 ? 'success' : 'warning');
        resolve();
      });

      proc.on('error', (err) => {
        clearTimeout(timeout);
        if (tempFile) fs.unlink(tempFile).catch(() => {});
        this._log(`[${jobName}] spawn failed: ${err.message}`, 'error');
        resolve();
      });
    });
  }
}
