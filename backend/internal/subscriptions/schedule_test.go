package subscriptions_test

import (
	"testing"
	"time"
	"tracking/internal/subscriptions"
)

func TestFixedDayForecast(t *testing.T) {
	for _, tc := range []struct {
		name, date, now, want string
		period                int
		archived              bool
	}{
		{"Moscow today before midnight", "2028-02-29", "2028-02-29T20:59:59Z", "2028-02-29", 30, false},
		{"Moscow tomorrow at midnight", "2028-02-29", "2028-02-29T21:00:00Z", "2028-03-30", 30, false},
		{"January has 31 days", "2028-01-31", "2028-02-01T12:00:00Z", "2028-03-01", 30, false},
		{"90 days across leap day", "2028-01-01", "2028-01-02T12:00:00Z", "2028-03-31", 90, false},
		{"180 days across leap day", "2028-01-01", "2028-01-02T12:00:00Z", "2028-06-29", 180, false},
		{"365 is not a leap year", "2028-02-29", "2028-03-01T12:00:00Z", "2029-02-28", 365, false},
		{"crossing year", "2027-12-31", "2028-01-01T12:00:00Z", "2028-01-30", 30, false},
		{"archived retains old date", "2020-01-01", "2032-03-01T12:00:00Z", "2020-01-01", 30, true},
		{"long idle exact period boundary", "2020-01-01", "2030-01-08T12:00:00Z", "2030-01-08", 30, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			instant, err := time.Parse(time.RFC3339, tc.now)
			if err != nil {
				t.Fatal(err)
			}
			for _, zone := range []string{"Pacific/Honolulu", "Asia/Tokyo", "UTC"} {
				location, err := time.LoadLocation(zone)
				if err != nil {
					t.Fatal(err)
				}
				if got := subscriptions.NextDate(tc.date, tc.period, tc.archived, instant.In(location)); got != tc.want {
					t.Fatalf("zone %s got %s want %s", zone, got, tc.want)
				}
			}
		})
	}
}
