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
