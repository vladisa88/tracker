package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
	schedule "tracking/internal/subscriptions"
)

type Subscription struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	Amount          string    `json:"amount"`
	Currency        string    `json:"currency"`
	PeriodDays      int       `json:"periodDays"`
	NextPaymentDate string    `json:"nextPaymentDate"`
	Archived        bool      `json:"archived"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

type currency struct {
	Code           string `json:"code"`
	FractionDigits int    `json:"fractionDigits"`
}

var currencies = []currency{{"RUB", 2}, {"USD", 2}, {"EUR", 2}}
var periods = []int{30, 90, 180, 365}
var decimalAmount = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?$`)

func respond(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(value)
}
func invalid(w http.ResponseWriter, fields map[string]string) {
	respond(w, 422, map[string]any{"error": map[string]any{"code": "validation", "message": "Проверьте поля формы", "fields": fields}})
}
func (s *Server) metadata(w http.ResponseWriter) {
	respond(w, 200, map[string]any{"periodDays": periods, "currencies": currencies, "timezone": "Europe/Moscow", "today": schedule.Today(s.Now())})
}

// ListSubscriptions is the common read operation for API and future background
// consumers. Forecasts always use the supplied time, even without prior UI reads.
// Stored dates retain the original schedule; reading never mutates archive or timestamps.
func (s *Server) ListSubscriptions(ctx context.Context, archived bool, now time.Time) ([]Subscription, error) {
	return listSubscriptions(ctx, s.db, archived, now)
}

func listSubscriptions(ctx context.Context, db *sql.DB, archived bool, now time.Time) ([]Subscription, error) {
	rows, err := db.QueryContext(ctx, `SELECT id,name,amount::text,currency,period_days,to_char(next_payment_date,'YYYY-MM-DD'),archived,created_at,updated_at FROM subscriptions WHERE archived=$1`, archived)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Subscription, 0)
	for rows.Next() {
		var sub Subscription
		if err := rows.Scan(&sub.ID, &sub.Name, &sub.Amount, &sub.Currency, &sub.PeriodDays, &sub.NextPaymentDate, &sub.Archived, &sub.CreatedAt, &sub.UpdatedAt); err != nil {
			return nil, err
		}
		sub.NextPaymentDate = schedule.NextDate(sub.NextPaymentDate, sub.PeriodDays, sub.Archived, now)
		items = append(items, sub)
	}
	sort.Slice(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if a.NextPaymentDate != b.NextPaymentDate {
			return a.NextPaymentDate < b.NextPaymentDate
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.ID < b.ID
	})
	return items, rows.Err()
}
func (s *Server) subscriptions(w http.ResponseWriter, r *http.Request) {
	now := s.Now()
	if r.Method == "GET" {
		state := r.URL.Query().Get("state")
		if state != "active" && state != "archived" {
			invalid(w, map[string]string{"state": "Выберите active или archived"})
			return
		}
		items, err := s.ListSubscriptions(r.Context(), state == "archived", now)
		if err != nil {
			fail(w, 500, "internal", "Не удалось загрузить подписки")
			return
		}
		respond(w, 200, items)
		return
	}
	var input Subscription
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384))
	err := decoder.Decode(&input)
	if err != nil {
		var mismatch *json.UnmarshalTypeError
		if errors.As(err, &mismatch) {
			invalid(w, map[string]string{mismatch.Field: "Неверный тип значения"})
			return
		}
		fail(w, 400, "invalid_json", "Некорректный JSON")
		return
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		fail(w, 400, "invalid_json", "Ожидается один JSON объект")
		return
	}
	if fields := validateSubscription(&input, now, true); len(fields) > 0 {
		invalid(w, fields)
		return
	}
	sub := Subscription{ID: randomToken(), Name: input.Name, Amount: input.Amount, Currency: input.Currency, PeriodDays: input.PeriodDays, NextPaymentDate: input.NextPaymentDate, CreatedAt: now, UpdatedAt: now}
	_, err = s.db.ExecContext(r.Context(), `INSERT INTO subscriptions(id,name,amount,currency,period_days,next_payment_date,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$7)`, sub.ID, sub.Name, sub.Amount, sub.Currency, sub.PeriodDays, sub.NextPaymentDate, now)
	if err != nil {
		fail(w, 500, "internal", "Не удалось сохранить подписку")
		return
	}
	respond(w, 201, sub)
}

func validateSubscription(input *Subscription, now time.Time, checkDate bool) map[string]string {
	fields := map[string]string{}
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" {
		fields["name"] = "Укажите название"
	}
	precision := -1
	for _, c := range currencies {
		if input.Currency == c.Code {
			precision = c.FractionDigits
		}
	}
	if precision < 0 {
		fields["currency"] = "Выберите поддерживаемую валюту"
	}
	parts := strings.Split(input.Amount, ".")
	if len(input.Amount) > 100 || !decimalAmount.MatchString(input.Amount) || strings.Trim(input.Amount, "0.") == "" || (len(parts) == 2 && len(parts[1]) > precision) {
		fields["amount"] = "Введите положительную сумму с точностью валюты"
	}
	validPeriod := false
	for _, p := range periods {
		if input.PeriodDays == p {
			validPeriod = true
		}
	}
	if !validPeriod {
		fields["periodDays"] = "Выберите 30, 90, 180 или 365 дней"
	}
	date, e := time.Parse("2006-01-02", input.NextPaymentDate)
	if checkDate && (e != nil || date.Format("2006-01-02") != input.NextPaymentDate || input.NextPaymentDate < schedule.Today(now)) {
		fields["nextPaymentDate"] = "Укажите сегодняшнюю или будущую дату по Москве"
	}
	if len(fields) > 0 {
		return fields
	}
	// Pad to the currency precision using strings, without binary floating point.
	amount := strings.TrimLeft(parts[0], "0")
	if amount == "" {
		amount = "0"
	}
	if precision > 0 {
		fraction := ""
		if len(parts) == 2 {
			fraction = parts[1]
		}
		amount += "." + fraction + strings.Repeat("0", precision-len(fraction))
	}
	input.Amount = amount
	return nil
}

func (s *Server) changeSubscription(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/subscriptions/"), "/")
	id := parts[0]
	operation := "edit"
	if len(parts) == 2 {
		operation = parts[1]
	}
	valid := len(parts) == 1 && r.Method == "PATCH" || len(parts) == 2 && r.Method == "POST" && (operation == "archive" || operation == "restore")
	if id == "" || !valid {
		fail(w, 404, "not_found", "Маршрут не найден")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		fail(w, 500, "internal", "Не удалось сохранить подписку")
		return
	}
	defer tx.Rollback()
	var sub Subscription
	err = tx.QueryRowContext(r.Context(), `SELECT id,name,amount::text,currency,period_days,to_char(next_payment_date,'YYYY-MM-DD'),archived,created_at,updated_at FROM subscriptions WHERE id=$1 FOR UPDATE`, id).Scan(&sub.ID, &sub.Name, &sub.Amount, &sub.Currency, &sub.PeriodDays, &sub.NextPaymentDate, &sub.Archived, &sub.CreatedAt, &sub.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, 404, "not_found", "Подписка не найдена")
		return
	}
	if err != nil {
		fail(w, 500, "internal", "Не удалось загрузить подписку")
		return
	}
	now := s.Now()
	if operation == "archive" && sub.Archived {
		respond(w, 200, sub)
		return
	}
	if operation == "restore" && !sub.Archived {
		fail(w, 409, "conflict", "Подписка уже активна")
		return
	}
	sub.NextPaymentDate = schedule.NextDate(sub.NextPaymentDate, sub.PeriodDays, sub.Archived, now)
	if operation == "archive" {
		sub.Archived = true
	} else {
		allowed := []string{"name", "amount", "currency", "periodDays", "nextPaymentDate"}
		if operation == "restore" {
			allowed = []string{"nextPaymentDate"}
		}
		fields, ok := subscriptionFields(w, r, allowed)
		if !ok {
			return
		}
		if operation == "restore" && fields["nextPaymentDate"] == nil {
			invalid(w, map[string]string{"nextPaymentDate": "Укажите новую дату платежа"})
			return
		}
		body, _ := json.Marshal(fields)
		if err := json.Unmarshal(body, &sub); err != nil {
			var mismatch *json.UnmarshalTypeError
			if errors.As(err, &mismatch) {
				invalid(w, map[string]string{mismatch.Field: "Неверный тип значения"})
			} else {
				fail(w, 400, "invalid_json", "Некорректный JSON")
			}
			return
		}
		if invalidFields := validateSubscription(&sub, now, fields["nextPaymentDate"] != nil || !sub.Archived); len(invalidFields) > 0 {
			invalid(w, invalidFields)
			return
		}
		if operation == "restore" {
			sub.Archived = false
		}
	}
	sub.UpdatedAt = now
	_, err = tx.ExecContext(r.Context(), `UPDATE subscriptions SET name=$2,amount=$3,currency=$4,period_days=$5,next_payment_date=$6,updated_at=$7,archived=$8 WHERE id=$1`, sub.ID, sub.Name, sub.Amount, sub.Currency, sub.PeriodDays, sub.NextPaymentDate, now, sub.Archived)
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		fail(w, 500, "internal", "Не удалось сохранить подписку")
		return
	}
	respond(w, 200, sub)
}

func subscriptionFields(w http.ResponseWriter, r *http.Request, allowed []string) (map[string]json.RawMessage, bool) {
	var fields map[string]json.RawMessage
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384))
	if err := decoder.Decode(&fields); err != nil {
		var mismatch *json.UnmarshalTypeError
		if errors.As(err, &mismatch) {
			invalid(w, map[string]string{"form": "Ожидается JSON объект"})
		} else {
			fail(w, 400, "invalid_json", "Некорректный JSON")
		}
		return nil, false
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		fail(w, 400, "invalid_json", "Ожидается один JSON объект")
		return nil, false
	}
	invalidFields := map[string]string{}
	for field, value := range fields {
		permitted := false
		for _, name := range allowed {
			if name == field {
				permitted = true
				break
			}
		}
		if !permitted {
			invalidFields[field] = "Недопустимое поле"
		} else if string(value) == "null" {
			invalidFields[field] = "Укажите значение"
		}
	}
	if len(fields) == 0 {
		invalidFields["nextPaymentDate"] = "Укажите поля подписки и новую дату для восстановления"
	}
	if len(invalidFields) > 0 {
		invalid(w, invalidFields)
		return nil, false
	}
	return fields, true
}
