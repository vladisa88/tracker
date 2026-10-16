// Package subscriptions owns the calendar rules shared by API, summaries and reminders.
package subscriptions

import (
	"time"
	_ "time/tzdata"
)

var Moscow = func() *time.Location {
	zone, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		panic(err)
	}
	return zone
}()

func Today(now time.Time) string { return now.In(Moscow).Format("2006-01-02") }

// NextDate advances an active schedule by whole fixed-day periods. It does not
// record or imply payment. UTC midnight represents a date, never an instant in Moscow.
func NextDate(date string, periodDays int, archived bool, now time.Time) string {
	if archived {
		return date
	}
	start, err := time.Parse("2006-01-02", date)
	if err != nil || periodDays <= 0 {
		return date
	}
	today, _ := time.Parse("2006-01-02", Today(now))
	days := (today.Unix() - start.Unix()) / 86400
	if days <= 0 {
		return date
	}
	periods := (days + int64(periodDays) - 1) / int64(periodDays)
	return start.AddDate(0, 0, int(periods)*periodDays).Format("2006-01-02")
}
