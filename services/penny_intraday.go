package services

import (
	"context"
	"fmt"
	"sync"
	"time"

	"prophet-trader/interfaces"
)

// PennyIntradayCache provides PennyScreenerService with two pieces of
// intraday context the screener can't get from Alpaca's snapshot endpoint:
//   - The 9:30-9:45 ET opening range (high + low) per ticker per session.
//   - The trailing 20-day average daily volume per ticker.
//
// Both are bounded fetches that cache for the rest of the trading day, so
// the screener's 60s scan cycle doesn't multiply Alpaca calls.

const (
	orbDurationMin = 15 // first 15 minutes of regular session
	pennyAvgVolLookbackDays = 20
)

// intradayDataLike is the narrow subset of interfaces.DataService used here.
// Kept small so tests can stub without implementing the full DataService.
type intradayDataLike interface {
	GetHistoricalBars(ctx context.Context, symbol string, start, end time.Time, timeframe string) ([]*interfaces.Bar, error)
}

type orbRecord struct {
	day  string // YYYY-MM-DD in America/New_York
	high float64
	low  float64
}

type avgVolRecord struct {
	day string
	avg int64
}

// PennyIntradayCache holds the two per-ticker caches behind a single mutex.
type PennyIntradayCache struct {
	data intradayDataLike
	mu   sync.Mutex
	orb  map[string]orbRecord
	avg  map[string]avgVolRecord
}

// NewPennyIntradayCache constructs the cache over the given data source.
func NewPennyIntradayCache(data intradayDataLike) *PennyIntradayCache {
	return &PennyIntradayCache{
		data: data,
		orb:  make(map[string]orbRecord),
		avg:  make(map[string]avgVolRecord),
	}
}

// GetORB returns the captured 15-min opening range for `ticker` at `now`.
// Returns ok=false when:
//   - now is before 9:45 ET (window still capturing); or
//   - the data fetch errors (caller should treat as "no ORB yet" not "broken").
//
// On first successful call per session per ticker, fetches the 1-min bars
// from session open (9:30 ET) up to 9:45 ET and caches.
func (c *PennyIntradayCache) GetORB(ctx context.Context, ticker string, now time.Time) (high, low float64, ok bool) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		return 0, 0, false
	}
	et := now.In(loc)
	sessionOpen := time.Date(et.Year(), et.Month(), et.Day(), 9, 30, 0, 0, loc)
	orbEnd := sessionOpen.Add(orbDurationMin * time.Minute)
	if !et.After(orbEnd) {
		// Still capturing; nothing to surface yet.
		return 0, 0, false
	}
	dayKey := et.Format("2006-01-02")

	c.mu.Lock()
	if r, hit := c.orb[ticker]; hit && r.day == dayKey {
		c.mu.Unlock()
		return r.high, r.low, true
	}
	c.mu.Unlock()

	// Fetch the OR window in UTC for the data API.
	bars, err := c.data.GetHistoricalBars(ctx, ticker, sessionOpen.UTC(), orbEnd.UTC(), "1Min")
	if err != nil || len(bars) == 0 {
		return 0, 0, false
	}
	h, l, found := calcORB(bars)
	if !found {
		return 0, 0, false
	}

	c.mu.Lock()
	c.orb[ticker] = orbRecord{day: dayKey, high: h, low: l}
	c.mu.Unlock()
	return h, l, true
}

// GetAvgDailyVolume20d returns the trailing 20-day average daily volume.
// Cached per ticker per UTC day. Returns 0 on insufficient data — callers
// must treat 0 as "no signal" (penny_screener_service already does).
func (c *PennyIntradayCache) GetAvgDailyVolume20d(ctx context.Context, ticker string, now time.Time) (int64, error) {
	dayKey := now.UTC().Format("2006-01-02")

	c.mu.Lock()
	if r, hit := c.avg[ticker]; hit && r.day == dayKey {
		c.mu.Unlock()
		return r.avg, nil
	}
	c.mu.Unlock()

	end := now.UTC()
	start := end.AddDate(0, 0, -int(float64(pennyAvgVolLookbackDays)*2.0)) // wall-clock buffer for weekends/holidays
	bars, err := c.data.GetHistoricalBars(ctx, ticker, start, end, "1Day")
	if err != nil {
		return 0, fmt.Errorf("avg daily volume fetch for %s: %w", ticker, err)
	}
	if len(bars) > pennyAvgVolLookbackDays {
		bars = bars[len(bars)-pennyAvgVolLookbackDays:]
	}
	if len(bars) == 0 {
		return 0, nil
	}
	var sum int64
	for _, b := range bars {
		sum += b.Volume
	}
	avg := sum / int64(len(bars))

	c.mu.Lock()
	c.avg[ticker] = avgVolRecord{day: dayKey, avg: avg}
	c.mu.Unlock()
	return avg, nil
}

// calcORB returns the high/low across the supplied bars. Empty input → ok=false.
func calcORB(bars []*interfaces.Bar) (high, low float64, ok bool) {
	if len(bars) == 0 {
		return 0, 0, false
	}
	high = bars[0].High
	low = bars[0].Low
	for _, b := range bars[1:] {
		if b.High > high {
			high = b.High
		}
		if b.Low < low {
			low = b.Low
		}
	}
	return high, low, true
}

// classifyORBStatus reports where `price` sits relative to a captured OR.
// Returns "awaiting" when both bounds are zero (uncaptured). Strict
// inequalities for the break classes — exactly at the boundary is still
// "inside_or" so a print at the OR high doesn't trigger a false break.
func classifyORBStatus(price, orHigh, orLow float64) string {
	if orHigh == 0 && orLow == 0 {
		return "awaiting"
	}
	if price > orHigh {
		return "above_or_high"
	}
	if price < orLow {
		return "below_or_low"
	}
	return "inside_or"
}
