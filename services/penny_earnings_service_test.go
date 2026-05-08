package services

import (
	"testing"
	"time"
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
