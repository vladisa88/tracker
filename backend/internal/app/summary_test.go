package app_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"

	"tracking/internal/app"
)

func TestSummary(t *testing.T) {
	// Give this scenario its own real database schema, including sessions and migrations.
	db := testDB(t)
	const schema = "expense_forecast_test"
	if _, err := db.Exec(`CREATE SCHEMA ` + schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Exec(`DROP SCHEMA ` + schema + ` CASCADE`) })
	dsn, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	query := dsn.Query()
	query.Set("search_path", schema)
	dsn.RawQuery = query.Encode()
	t.Setenv("TEST_DATABASE_URL", dsn.String())
	a := newSubscriptionAPI(t)
	savedClient := a.client
	a.client = &http.Client{}
	a.request("GET", "/api/summary", "", 401)
	a.client = savedClient
	var summary struct {
		Today              string `json:"today"`
		WindowStart        string `json:"windowStart"`
		WindowEndExclusive string `json:"windowEndExclusive"`
		Currencies         []struct {
			Currency string `json:"currency"`
			Upcoming string `json:"upcoming"`
			Average  string `json:"average"`
		} `json:"currencies"`
	}
	read := func() {
		t.Helper()
		if err := json.Unmarshal(a.request("GET", "/api/summary", "", 200), &summary); err != nil {
			t.Fatal(err)
		}
	}
	read()
	if summary.Today != "2028-02-29" || summary.WindowStart != "2028-02-29" || summary.WindowEndExclusive != "2028-03-30" || summary.Currencies == nil || len(summary.Currencies) != 0 {
		t.Fatalf("empty Moscow forecast: %+v", summary)
	}
	create := func(body string) app.Subscription {
		return a.decode(a.request("POST", "/api/subscriptions", body, 201))
	}
	sub := create(`{"name":"Annual","amount":"365.00","currency":"USD","periodDays":365,"nextPaymentDate":"2028-03-30"}`)
	read()
	if len(summary.Currencies) != 1 || summary.Currencies[0].Currency != "USD" || summary.Currencies[0].Upcoming != "0.00" || summary.Currencies[0].Average != "30.00" {
		t.Fatalf("annual expense outside window: %+v", summary)
	}
	a.request("POST", "/api/subscriptions/"+sub.ID+"/archive", "", 200)
	read()
	if len(summary.Currencies) != 0 {
		t.Fatalf("archive included: %+v", summary)
	}
	create(`{"name":"Today","amount":"12345678901234567890.10","currency":"RUB","periodDays":30,"nextPaymentDate":"2028-02-29"}`)
	create(`{"name":"Day 29","amount":"0.20","currency":"RUB","periodDays":90,"nextPaymentDate":"2028-03-29"}`)
	create(`{"name":"Day 30","amount":"0.30","currency":"RUB","periodDays":180,"nextPaymentDate":"2028-03-30"}`)
	read()
	if len(summary.Currencies) != 1 || summary.Currencies[0].Upcoming != "12345678901234567890.30" || summary.Currencies[0].Average != "12345678901234567890.22" {
		t.Fatalf("window or exact decimals: %+v", summary)
	}
	// Three exact sixths of a cent total half a cent: round once, away from zero.
	for i := 0; i < 3; i++ {
		create(`{"name":"Fraction","amount":"0.01","currency":"EUR","periodDays":180,"nextPaymentDate":"2028-03-30"}`)
	}
	read()
	if len(summary.Currencies) != 2 || summary.Currencies[1].Currency != "EUR" || summary.Currencies[1].Upcoming != "0.00" || summary.Currencies[1].Average != "0.01" {
		t.Fatalf("round only after summation: %+v", summary)
	}
	create(`{"name":"Annual fraction","amount":"1.00","currency":"EUR","periodDays":365,"nextPaymentDate":"2028-03-29"}`)
	read()
	if summary.Currencies[1].Upcoming != "1.00" || summary.Currencies[1].Average != "0.09" {
		t.Fatalf("annual division with fractions: %+v", summary)
	}
	// Without any intervening list read, advance over years and a leap day.
	a.now = time.Date(2032, 2, 28, 21, 0, 0, 0, time.UTC)
	a.login()
	read()
	if summary.Today != "2032-02-29" || summary.WindowEndExclusive != "2032-03-30" || summary.Currencies[0].Upcoming != "12345678901234567890.60" || summary.Currencies[0].Average != "12345678901234567890.22" {
		t.Fatalf("normalize overdue schedules without list reads: %+v", summary)
	}
}
