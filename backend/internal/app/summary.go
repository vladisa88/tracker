package app

import (
	"math/big"
	"net/http"
	"time"

	schedule "tracking/internal/subscriptions"
)

type currencySummary struct {
	Currency string `json:"currency"`
	Upcoming string `json:"upcoming"`
	Average  string `json:"average"`
}

type expenseSummary struct {
	Today              string            `json:"today"`
	WindowStart        string            `json:"windowStart"`
	WindowEndExclusive string            `json:"windowEndExclusive"`
	Currencies         []currencySummary `json:"currencies"`
}

func (s *Server) summary(w http.ResponseWriter, r *http.Request) {
	now := s.Now()
	items, err := s.ListSubscriptions(r.Context(), false, now)
	if err != nil {
		fail(w, 500, "internal", "Не удалось загрузить итоги")
		return
	}
	respond(w, 200, forecast(items, now))
}

// forecast consumes the shared normalized schedule. Rational arithmetic retains
// every fraction until the final currency total; FloatString rounds ties away from zero.
func forecast(items []Subscription, now time.Time) expenseSummary {
	today := schedule.Today(now)
	start, _ := time.Parse("2006-01-02", today)
	end := start.AddDate(0, 0, 30).Format("2006-01-02")
	result := expenseSummary{Today: today, WindowStart: today, WindowEndExclusive: end, Currencies: make([]currencySummary, 0)}
	for _, c := range currencies {
		upcoming, average := new(big.Rat), new(big.Rat)
		present := false
		for _, sub := range items {
			if sub.Archived || sub.Currency != c.Code {
				continue
			}
			present = true
			amount, _ := new(big.Rat).SetString(sub.Amount)
			average.Add(average, new(big.Rat).Mul(amount, big.NewRat(30, int64(sub.PeriodDays))))
			date, _ := time.Parse("2006-01-02", sub.NextPaymentDate)
			for date.Format("2006-01-02") < end {
				upcoming.Add(upcoming, amount)
				date = date.AddDate(0, 0, sub.PeriodDays)
			}
		}
		if present {
			result.Currencies = append(result.Currencies, currencySummary{Currency: c.Code, Upcoming: upcoming.FloatString(c.FractionDigits), Average: average.FloatString(c.FractionDigits)})
		}
	}
	return result
}
