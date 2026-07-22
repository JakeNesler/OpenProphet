// vectorDB.js - Vector similarity search for trading decisions
import Database from 'better-sqlite3';
import * as sqliteVec from 'sqlite-vec';
import fs from 'fs';
import path from 'path';
import { fileURLToPath } from 'url';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

// Initialize embedding pipeline (local model, no API key needed)
let embedder = null;
async function getEmbedder() {
  if (!embedder) {
    console.log('🔄 Loading local embedding model (first run may take 30s)...');
    // Lazy-load the heavy ML stack (@xenova/transformers → sharp/onnx) only when an
    // embedding is actually needed, so DB/stats paths (getTradeStats, getEmbeddingCount)
    // and module import don't pull it in.
    const { pipeline } = await import('@xenova/transformers');
    embedder = await pipeline('feature-extraction', 'Xenova/all-MiniLM-L6-v2');
    console.log('✅ Embedding model loaded');
  }
  return embedder;
}

// Lazy-initialized SQLite database — deferred until first use so the MCP server
// doesn't crash on import when data/ directory doesn't exist yet.
const dbPath = process.env.DATABASE_PATH || path.join(__dirname, 'data', 'prophet_trader.db');
let _db = null;

function getDb() {
  if (!_db) {
    fs.mkdirSync(path.dirname(dbPath), { recursive: true });
    _db = new Database(dbPath);
    sqliteVec.load(_db);
    _db.exec(`
      CREATE TABLE IF NOT EXISTS trade_embeddings (
        id TEXT PRIMARY KEY,
        decision_file TEXT NOT NULL,
        symbol TEXT,
        action TEXT,
        strategy TEXT,
        result_pct REAL,
        result_dollars REAL,
        date TEXT,
        reasoning TEXT,
        market_context TEXT,
        created_at TEXT DEFAULT CURRENT_TIMESTAMP
      );

      CREATE VIRTUAL TABLE IF NOT EXISTS trade_vectors USING vec0(
        trade_id TEXT PRIMARY KEY,
        embedding FLOAT[384]
      );
    `);
  }
  return _db;
}

/**
 * Get local embedding for text (no API key required)
 * @param {string} text - Text to embed
 * @returns {Promise<number[]>} - 384-dimensional embedding vector
 */
export async function getEmbedding(text) {
  try {
    const model = await getEmbedder();
    const output = await model(text, { pooling: 'mean', normalize: true });
    return Array.from(output.data);
  } catch (error) {
    console.error('Embedding error:', error.message);
    throw error;
  }
}

/**
 * Store trade decision with embedding for similarity search
 * @param {Object} trade - Trade decision object
 * @param {string} trade.id - Unique trade ID
 * @param {string} trade.decision_file - Path to decision JSON file
 * @param {string} trade.symbol - Stock symbol
 * @param {string} trade.action - BUY, SELL, HOLD, etc.
 * @param {string} trade.strategy - SCALP, SWING, etc.
 * @param {number} trade.result_pct - Result percentage (e.g., 26.5 for +26.5%)
 * @param {number} trade.result_dollars - Result in dollars (e.g., 1920 for +$1920)
 * @param {string} trade.date - Date in YYYY-MM-DD format
 * @param {string} trade.reasoning - Trade reasoning/thesis
 * @param {string} trade.market_context - Market conditions, catalysts, etc.
 * @returns {Promise<void>}
 */
export async function storeTrade(trade) {
  try {
    // Create embedding from reasoning + market context
    const textToEmbed = `${trade.reasoning}\n\nMarket Context: ${trade.market_context}`;
    const embedding = await getEmbedding(textToEmbed);

    // Store trade metadata
    const insertTrade = getDb().prepare(`
      INSERT OR REPLACE INTO trade_embeddings (
        id, decision_file, symbol, action, strategy,
        result_pct, result_dollars, date, reasoning, market_context
      ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
    `);
    insertTrade.run(
      trade.id,
      trade.decision_file,
      trade.symbol,
      trade.action,
      trade.strategy,
      trade.result_pct,
      trade.result_dollars,
      trade.date,
      trade.reasoning,
      trade.market_context
    );

    // Store embedding vector
    const insertVector = getDb().prepare(`
      INSERT OR REPLACE INTO trade_vectors (trade_id, embedding)
      VALUES (?, vec_f32(?))
    `);
    insertVector.run(trade.id, new Float32Array(embedding));

    console.log(`✅ Stored trade: ${trade.id} (${trade.symbol} ${trade.action})`);
  } catch (error) {
    console.error(`❌ Error storing trade ${trade.id}:`, error.message);
    throw error;
  }
}

/**
 * Find trades similar to a query
 * @param {string} queryText - Query describing the setup (e.g., "SPY gap up scalp")
 * @param {number} limit - Number of similar trades to return
 * @param {Object} filters - Optional filters (symbol, action, strategy)
 * @returns {Promise<Array>} - Array of similar trades with metadata
 */
export async function findSimilarTrades(queryText, limit = 5, filters = {}) {
  try {
    // Get query embedding
    const queryEmbedding = await getEmbedding(queryText);

    // Build filter conditions
    let whereClause = '';
    const params = [];

    if (filters.symbol) {
      whereClause += ' AND te.symbol = ?';
      params.push(filters.symbol);
    }
    if (filters.action) {
      whereClause += ' AND te.action = ?';
      params.push(filters.action);
    }
    if (filters.strategy) {
      whereClause += ' AND te.strategy = ?';
      params.push(filters.strategy);
    }

    // Search for similar trades
    const query = getDb().prepare(`
      SELECT
        te.id,
        te.symbol,
        te.action,
        te.strategy,
        te.result_pct,
        te.result_dollars,
        te.date,
        te.reasoning,
        te.market_context,
        tv.distance
      FROM trade_vectors tv
      JOIN trade_embeddings te ON tv.trade_id = te.id
      WHERE tv.embedding MATCH vec_f32(?)
        AND k = ?
        ${whereClause}
      ORDER BY tv.distance
    `);

    const results = query.all(new Float32Array(queryEmbedding), limit, ...params);

    return results.map(r => ({
      id: r.id,
      symbol: r.symbol,
      action: r.action,
      strategy: r.strategy,
      result_pct: r.result_pct,
      result_dollars: r.result_dollars,
      date: r.date,
      reasoning: r.reasoning,
      market_context: r.market_context,
      similarity: Math.max(0, 1 - (r.distance / 2)), // Cosine distance 0-2 → similarity 0-1
    }));
  } catch (error) {
    console.error('Error finding similar trades:', error.message);
    throw error;
  }
}

/**
 * Get trade statistics by filters
 * @param {Object} filters - Filters (symbol, action, strategy, min_result, max_result)
 * @returns {Object} - Statistics (count, win_rate, avg_result, best, worst)
 */
export function getTradeStats(filters = {}) {
  try {
    let whereClause = 'WHERE 1=1';
    const params = [];

    if (filters.symbol) {
      whereClause += ' AND symbol = ?';
      params.push(filters.symbol);
    }
    if (filters.action) {
      whereClause += ' AND action = ?';
      params.push(filters.action);
    }
    if (filters.strategy) {
      whereClause += ' AND strategy = ?';
      params.push(filters.strategy);
    }
    if (filters.min_result !== undefined) {
      whereClause += ' AND result_pct >= ?';
      params.push(filters.min_result);
    }
    if (filters.max_result !== undefined) {
      whereClause += ' AND result_pct <= ?';
      params.push(filters.max_result);
    }

    const query = getDb().prepare(`
      SELECT
        COUNT(*) as count,
        SUM(CASE WHEN result_pct IS NOT NULL THEN 1 ELSE 0 END) as resolved,
        SUM(CASE WHEN result_pct > 0 THEN 1 ELSE 0 END) as winners,
        SUM(CASE WHEN result_pct < 0 THEN 1 ELSE 0 END) as losers,
        AVG(result_pct) as avg_result_pct,
        AVG(result_dollars) as avg_result_dollars,
        MAX(result_pct) as best_pct,
        MIN(result_pct) as worst_pct,
        MAX(result_dollars) as best_dollars,
        MIN(result_dollars) as worst_dollars
      FROM trade_embeddings
      ${whereClause}
    `);

    const stats = query.get(...params);

    // win_rate is computed over RESOLVED trades only (result_pct IS NOT NULL) so that
    // pending setups auto-captured at entry don't deflate the rate.
    const resolved = stats.resolved || 0;
    return {
      count: stats.count || 0,
      resolved,
      pending: (stats.count || 0) - resolved,
      winners: stats.winners || 0,
      losers: stats.losers || 0,
      win_rate: resolved > 0 ? (stats.winners / resolved) * 100 : 0,
      avg_result_pct: stats.avg_result_pct || 0,
      avg_result_dollars: stats.avg_result_dollars || 0,
      best_result_pct: stats.best_pct || 0,
      worst_result_pct: stats.worst_pct || 0,
      best_result_dollars: stats.best_dollars || 0,
      worst_result_dollars: stats.worst_dollars || 0,
    };
  } catch (error) {
    console.error('Error getting trade stats:', error.message);
    throw error;
  }
}

/**
 * Delete all embeddings (useful for re-indexing)
 */
export function clearAllEmbeddings() {
  getDb().exec('DELETE FROM trade_embeddings');
  getDb().exec('DELETE FROM trade_vectors');
  console.log('✅ Cleared all embeddings');
}

/**
 * Get total number of embedded trades
 */
export function getEmbeddingCount() {
  const result = getDb().prepare('SELECT COUNT(*) as count FROM trade_embeddings').get();
  return result.count;
}

export default {
  getEmbedding,
  storeTrade,
  findSimilarTrades,
  getTradeStats,
  clearAllEmbeddings,
  getEmbeddingCount,
};
