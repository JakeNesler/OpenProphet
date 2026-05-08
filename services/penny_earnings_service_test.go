package services

import (
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

// monFriCalendar returns a list of AlpacaCalendarEntry for Mon-Fri across the given week range,
// skipping weekends. Dates are formatted as "YYYY-MM-DD".
func monFriCalendar(start time.Time, days int) []AlpacaCalendarEntry {
	out := []AlpacaCalendarEntry{}
	d := start
	for i := 0; i < days; i++ {
		wd := d.Weekday()
		if wd != time.Saturday && wd != time.Sunday {
			out = append(out, AlpacaCalendarEntry{Date: d.Format("2006-01-02")})
		}
		d = d.AddDate(0, 0, 1)
	}
	return out
}

func TestEarningsTradingDayDistance_SameDay(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	mon := time.Date(2026, 5, 4, 0, 0, 0, 0, loc) // Monday
	cal := monFriCalendar(mon, 10)
	got := tradingDayDistance(mon, mon, cal)
	if got != 0 {
		t.Errorf("expected 0 for same day, got %d", got)
	}
}

func TestEarningsTradingDayDistance_NextTradingDay(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	mon := time.Date(2026, 5, 4, 0, 0, 0, 0, loc)
	tue := time.Date(2026, 5, 5, 0, 0, 0, 0, loc)
	cal := monFriCalendar(mon, 10)
	got := tradingDayDistance(mon, tue, cal)
	if got != 1 {
		t.Errorf("expected 1 trading day Mon→Tue, got %d", got)
	}
}

func TestEarningsTradingDayDistance_AcrossWeekend(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	fri := time.Date(2026, 5, 8, 0, 0, 0, 0, loc)  // Friday
	nextMon := time.Date(2026, 5, 11, 0, 0, 0, 0, loc) // following Monday
	cal := monFriCalendar(fri, 7)
	got := tradingDayDistance(fri, nextMon, cal)
	if got != 1 {
		t.Errorf("expected 1 trading day Fri→Mon (skipping weekend), got %d", got)
	}
}

func TestEarningsTradingDayDistance_FullWeek(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	mon := time.Date(2026, 5, 4, 0, 0, 0, 0, loc)
	fri := time.Date(2026, 5, 8, 0, 0, 0, 0, loc)
	cal := monFriCalendar(mon, 10)
	got := tradingDayDistance(mon, fri, cal)
	if got != 4 {
		t.Errorf("expected 4 trading days Mon→Fri, got %d", got)
	}
}

func TestEarningsTradingDayDistance_EffectiveBeforeNow(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	mon := time.Date(2026, 5, 4, 0, 0, 0, 0, loc)
	prevFri := time.Date(2026, 5, 1, 0, 0, 0, 0, loc)
	cal := monFriCalendar(prevFri, 10)
	got := tradingDayDistance(mon, prevFri, cal)
	if got != -1 {
		t.Errorf("expected -1 sentinel when effective is before now, got %d", got)
	}
}

func TestEarningsEffectiveDate_BMO_returnsCalendarDate(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	mon := time.Date(2026, 5, 4, 0, 0, 0, 0, loc)
	cal := monFriCalendar(mon, 10)
	entry := earningsEntry{Ticker: "AAA", Date: mon, Time: "bmo"}
	s := &EarningsCalendarService{}
	got := s.effectiveDate(entry, cal)
	if got.Format("2006-01-02") != mon.Format("2006-01-02") {
		t.Errorf("BMO Mon: expected %s, got %s", mon.Format("2006-01-02"), got.Format("2006-01-02"))
	}
}

func TestEarningsEffectiveDate_AMC_returnsNextTradingDay(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	mon := time.Date(2026, 5, 4, 0, 0, 0, 0, loc)
	tue := time.Date(2026, 5, 5, 0, 0, 0, 0, loc)
	cal := monFriCalendar(mon, 10)
	entry := earningsEntry{Ticker: "AAA", Date: mon, Time: "amc"}
	s := &EarningsCalendarService{}
	got := s.effectiveDate(entry, cal)
	if got.Format("2006-01-02") != tue.Format("2006-01-02") {
		t.Errorf("AMC Mon: expected %s, got %s", tue.Format("2006-01-02"), got.Format("2006-01-02"))
	}
}

func TestEarningsEffectiveDate_AMCFriday_returnsNextMonday(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	fri := time.Date(2026, 5, 8, 0, 0, 0, 0, loc)
	mon := time.Date(2026, 5, 11, 0, 0, 0, 0, loc)
	cal := monFriCalendar(fri, 7)
	entry := earningsEntry{Ticker: "AAA", Date: fri, Time: "amc"}
	s := &EarningsCalendarService{}
	got := s.effectiveDate(entry, cal)
	if got.Format("2006-01-02") != mon.Format("2006-01-02") {
		t.Errorf("AMC Fri: expected next Mon %s, got %s", mon.Format("2006-01-02"), got.Format("2006-01-02"))
	}
}

func TestEarningsEffectiveDate_EmptyTime_treatedAsBMO(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	mon := time.Date(2026, 5, 4, 0, 0, 0, 0, loc)
	cal := monFriCalendar(mon, 10)
	entry := earningsEntry{Ticker: "AAA", Date: mon, Time: ""}
	s := &EarningsCalendarService{}
	got := s.effectiveDate(entry, cal)
	if got.Format("2006-01-02") != mon.Format("2006-01-02") {
		t.Errorf("empty time: expected BMO behavior, got %s", got.Format("2006-01-02"))
	}
}

func TestEarningsEffectiveDate_EmptyCalendar_returnsEntryDateUnchanged(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	mon := time.Date(2026, 5, 4, 0, 0, 0, 0, loc)
	entry := earningsEntry{Ticker: "AAA", Date: mon, Time: "amc"}
	s := &EarningsCalendarService{}
	got := s.effectiveDate(entry, nil)
	if got.Format("2006-01-02") != mon.Format("2006-01-02") {
		t.Errorf("empty calendar: expected entry.Date unchanged, got %s", got.Format("2006-01-02"))
	}
}

func TestEarningsEffectiveDate_DateBeforeCalendarStart_returnsFirstCalendarDay(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	dateBeforeCal := time.Date(2026, 5, 1, 0, 0, 0, 0, loc) // Friday
	monStart := time.Date(2026, 5, 4, 0, 0, 0, 0, loc)
	cal := monFriCalendar(monStart, 5)
	entry := earningsEntry{Ticker: "AAA", Date: dateBeforeCal, Time: "bmo"}
	s := &EarningsCalendarService{}
	got := s.effectiveDate(entry, cal)
	// First trading day in calendar on or after dateBeforeCal is Mon 2026-05-04
	if got.Format("2006-01-02") != monStart.Format("2006-01-02") {
		t.Errorf("expected %s (first cal day), got %s", monStart.Format("2006-01-02"), got.Format("2006-01-02"))
	}
}

func TestEarningsMaybeWarn_FirstCallReturnsTrue(t *testing.T) {
	s := &EarningsCalendarService{logger: logrus.New()}
	if !s.maybeWarn(time.Now()) {
		t.Error("expected first maybeWarn call to return true")
	}
}

func TestEarningsMaybeWarn_SecondCallWithinIntervalReturnsFalse(t *testing.T) {
	now := time.Now()
	s := &EarningsCalendarService{logger: logrus.New(), lastWarnTime: now}
	if s.maybeWarn(now.Add(1 * time.Minute)) {
		t.Error("expected second maybeWarn within interval to return false")
	}
}

func TestEarningsMaybeWarn_SecondCallAfterIntervalReturnsTrue(t *testing.T) {
	now := time.Now()
	s := &EarningsCalendarService{logger: logrus.New(), lastWarnTime: now}
	if !s.maybeWarn(now.Add(staleWarnInterval + time.Second)) {
		t.Error("expected maybeWarn after interval to return true")
	}
}

// helper: build a service in a fresh, fully-populated state at a known "today"
func earningsServiceAt(today time.Time, entries map[string]earningsEntry, calendar []AlpacaCalendarEntry) *EarningsCalendarService {
	s := &EarningsCalendarService{
		entries:          entries,
		calendar:         calendar,
		lastRefresh:      today,
		firstRefreshDone: make(chan struct{}),
		logger:           logrus.New(),
	}
	return s
}

func TestEarningsIsExcluded_NeverRefreshed_returnsFalse(t *testing.T) {
	s := &EarningsCalendarService{
		entries:          map[string]earningsEntry{},
		firstRefreshDone: make(chan struct{}),
		logger:           logrus.New(),
	}
	if s.IsExcluded("AAA", time.Now()) {
		t.Error("expected false when lastRefresh is zero")
	}
}

func TestEarningsIsExcluded_UnknownTicker_returnsFalse(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	mon := time.Date(2026, 5, 4, 12, 0, 0, 0, loc)
	cal := monFriCalendar(time.Date(2026, 5, 4, 0, 0, 0, 0, loc), 10)
	s := earningsServiceAt(mon, map[string]earningsEntry{}, cal)
	if s.IsExcluded("AAA", mon) {
		t.Error("expected false for unknown ticker")
	}
}

func TestEarningsIsExcluded_PastEarnings_returnsFalse(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	mon := time.Date(2026, 5, 4, 12, 0, 0, 0, loc)
	prevFri := time.Date(2026, 5, 1, 0, 0, 0, 0, loc)
	cal := monFriCalendar(prevFri, 10)
	entries := map[string]earningsEntry{
		"AAA": {Ticker: "AAA", Date: prevFri, Time: "bmo"},
	}
	s := earningsServiceAt(mon, entries, cal)
	if s.IsExcluded("AAA", mon) {
		t.Error("expected false for past earnings")
	}
}

func TestEarningsIsExcluded_SameDayBMO_returnsTrue(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	mon := time.Date(2026, 5, 4, 12, 0, 0, 0, loc)
	cal := monFriCalendar(time.Date(2026, 5, 4, 0, 0, 0, 0, loc), 10)
	entries := map[string]earningsEntry{
		"AAA": {Ticker: "AAA", Date: time.Date(2026, 5, 4, 0, 0, 0, 0, loc), Time: "bmo"},
	}
	s := earningsServiceAt(mon, entries, cal)
	if !s.IsExcluded("AAA", mon) {
		t.Error("expected true for same-day BMO earnings (distance 0)")
	}
}

func TestEarningsIsExcluded_ThreeTradingDaysOut_returnsTrue(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	mon := time.Date(2026, 5, 4, 12, 0, 0, 0, loc)
	thu := time.Date(2026, 5, 7, 0, 0, 0, 0, loc) // 3 trading days from Mon
	cal := monFriCalendar(time.Date(2026, 5, 4, 0, 0, 0, 0, loc), 10)
	entries := map[string]earningsEntry{
		"AAA": {Ticker: "AAA", Date: thu, Time: "bmo"},
	}
	s := earningsServiceAt(mon, entries, cal)
	if !s.IsExcluded("AAA", mon) {
		t.Error("expected true for 3-trading-days-out BMO")
	}
}

func TestEarningsIsExcluded_FourTradingDaysOut_returnsFalse(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	mon := time.Date(2026, 5, 4, 12, 0, 0, 0, loc)
	fri := time.Date(2026, 5, 8, 0, 0, 0, 0, loc) // 4 trading days from Mon
	cal := monFriCalendar(time.Date(2026, 5, 4, 0, 0, 0, 0, loc), 10)
	entries := map[string]earningsEntry{
		"AAA": {Ticker: "AAA", Date: fri, Time: "bmo"},
	}
	s := earningsServiceAt(mon, entries, cal)
	if s.IsExcluded("AAA", mon) {
		t.Error("expected false for 4-trading-days-out BMO")
	}
}

func TestEarningsIsExcluded_EmptyCalendar_returnsFalseAndWarns(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	mon := time.Date(2026, 5, 4, 12, 0, 0, 0, loc)
	entries := map[string]earningsEntry{
		"AAA": {Ticker: "AAA", Date: mon, Time: "bmo"},
	}
	s := earningsServiceAt(mon, entries, nil)
	if s.IsExcluded("AAA", mon) {
		t.Error("expected false when calendar is empty (cannot compute distance)")
	}
	// lastWarnTime should now be set (warn was emitted)
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.lastWarnTime.IsZero() {
		t.Error("expected maybeWarn to have fired (lastWarnTime should be non-zero)")
	}
}

func TestEarningsIsExcluded_StaleAndEmptyCalendar_SharesThrottle(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	mon := time.Date(2026, 5, 4, 12, 0, 0, 0, loc)
	entries := map[string]earningsEntry{
		"AAA": {Ticker: "AAA", Date: mon, Time: "bmo"},
	}
	s := earningsServiceAt(mon, entries, nil) // empty calendar
	s.lastRefresh = mon.Add(-48 * time.Hour) // also stale

	// First call: both stale and empty-calendar conditions true.
	// Expect IsExcluded → false (calendar empty), and exactly ONE warn fired (shared throttle).
	_ = s.IsExcluded("AAA", mon)
	s.mu.RLock()
	first := s.lastWarnTime
	s.mu.RUnlock()
	if first.IsZero() {
		t.Fatal("expected at least one warn to fire on first call")
	}

	// Second call within the throttle interval: lastWarnTime should NOT advance.
	_ = s.IsExcluded("AAA", mon.Add(1*time.Minute))
	s.mu.RLock()
	second := s.lastWarnTime
	s.mu.RUnlock()
	if !second.Equal(first) {
		t.Errorf("expected lastWarnTime unchanged within throttle interval, got %v vs %v", first, second)
	}
}

func TestEarningsIsExcluded_StaleData_stillApplies(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	mon := time.Date(2026, 5, 4, 12, 0, 0, 0, loc)
	cal := monFriCalendar(time.Date(2026, 5, 4, 0, 0, 0, 0, loc), 10)
	entries := map[string]earningsEntry{
		"AAA": {Ticker: "AAA", Date: time.Date(2026, 5, 4, 0, 0, 0, 0, loc), Time: "bmo"},
	}
	s := earningsServiceAt(mon, entries, cal)
	// Force stale: lastRefresh is 48h before "now"
	s.lastRefresh = mon.Add(-48 * time.Hour)
	if !s.IsExcluded("AAA", mon) {
		t.Error("expected stale-but-populated cache to still apply exclusion")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.lastWarnTime.IsZero() {
		t.Error("expected stale warn to have fired")
	}
}

func TestEarningsWaitForFirstRefresh_TimesOutWhenNotSignaled(t *testing.T) {
	s := &EarningsCalendarService{
		firstRefreshDone: make(chan struct{}),
		logger:           logrus.New(),
	}
	if s.WaitForFirstRefresh(50 * time.Millisecond) {
		t.Error("expected timeout when firstRefreshDone is never closed")
	}
}

func TestEarningsWaitForFirstRefresh_ReturnsTrueWhenChannelClosed(t *testing.T) {
	s := &EarningsCalendarService{
		firstRefreshDone: make(chan struct{}),
		logger:           logrus.New(),
	}
	close(s.firstRefreshDone)
	if !s.WaitForFirstRefresh(50 * time.Millisecond) {
		t.Error("expected true when firstRefreshDone is closed")
	}
}

func TestEarningsWaitForFirstRefresh_CompletesWhenSignaledMidWait(t *testing.T) {
	s := &EarningsCalendarService{
		firstRefreshDone: make(chan struct{}),
		logger:           logrus.New(),
	}
	go func() {
		time.Sleep(20 * time.Millisecond)
		s.firstRefreshOnce.Do(func() { close(s.firstRefreshDone) })
	}()
	if !s.WaitForFirstRefresh(200 * time.Millisecond) {
		t.Error("expected WaitForFirstRefresh to return true after mid-wait signal")
	}
}
