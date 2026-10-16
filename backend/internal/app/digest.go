package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	schedule "tracking/internal/subscriptions"
)

// DigestWorker processes only the supplied Moscow business day. The durable
// snapshot precedes the external send; committed successes and empty days stay
// processed across restarts. Concurrent claims and failure recovery are issue 07.
type DigestWorker struct {
	db       *sql.DB
	config   Config
	telegram BotAPI
}

func NewDigestWorker(db *sql.DB, c Config, telegram BotAPI) *DigestWorker {
	return &DigestWorker{db: db, config: c, telegram: telegram}
}

func (w *DigestWorker) Cycle(ctx context.Context, now time.Time) error {
	local := now.In(schedule.Moscow)
	if local.Hour() < 10 {
		return nil
	}
	date := schedule.Today(now)
	var message, status string
	var chat int64
	err := w.db.QueryRowContext(ctx, `SELECT chat_id,message,status FROM daily_digests WHERE business_date=$1`, date).Scan(&chat, &message, &status)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && status != "pending" {
		return nil
	}
	// A saved snapshot never grants permission to send to a disconnected bot.
	var recipient int64
	connectionErr := w.db.QueryRowContext(ctx, `SELECT chat_id FROM telegram_connections WHERE owner_id=$1 AND status='connected' AND chat_id IS NOT NULL`, w.config.OwnerID).Scan(&recipient)
	if errors.Is(connectionErr, sql.ErrNoRows) {
		return nil
	}
	if connectionErr != nil {
		return connectionErr
	}
	if errors.Is(err, sql.ErrNoRows) {
		items, err := listSubscriptions(ctx, w.db, false, now)
		if err != nil {
			return err
		}
		message = formatDigest(items, local, w.config.Origin)
		status = "pending"
		if message == "" {
			status = "empty"
		}
		chat = recipient
		// Autocommit freezes content before the first external attempt. If a row
		// already exists, leave it for the next cycle rather than send another snapshot.
		result, err := w.db.ExecContext(ctx, `INSERT INTO daily_digests(business_date,chat_id,message,status,created_at) VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`, date, chat, message, status, now)
		if err != nil {
			return err
		}
		inserted, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if inserted == 0 || status == "empty" {
			return nil
		}
	}
	if err := w.telegram.Send(ctx, chat, message); err != nil {
		return err
	}
	_, err = w.db.ExecContext(ctx, `UPDATE daily_digests SET status='sent',sent_at=$2 WHERE business_date=$1`, date, now)
	return err
}

// Telegram receives plain text (no parse_mode), so subscription names are
// literal even when they contain HTML or Markdown. Count UTF-16 units to keep
// supplementary Unicode characters within a conservative 4096 character bound.
func formatDigest(items []Subscription, now time.Time, origin string) string {
	dates := []string{schedule.Today(now), schedule.Today(now.AddDate(0, 0, 3))}
	titles := []string{"Сегодня", "Через 3 дня"}
	sections := make([]string, 0, 2)
	counts := [2]int{}
	for i, date := range dates {
		lines := []string{titles[i]}
		for _, sub := range items {
			if sub.NextPaymentDate != date {
				continue
			}
			counts[i]++
			lines = append(lines, fmt.Sprintf("%s — %s %s · %s", sub.Name, sub.Amount, sub.Currency, sub.NextPaymentDate))
		}
		if counts[i] > 0 {
			sections = append(sections, strings.Join(lines, "\n"))
		}
	}
	if counts[0]+counts[1] == 0 {
		return ""
	}
	link := "\n\nПолный список: " + origin + "/"
	message := "Плановые платежи\n\n" + strings.Join(sections, "\n\n") + link
	if digestLength(message) <= 4096 {
		return message
	}
	sections = sections[:0]
	for i, count := range counts {
		if count > 0 {
			sections = append(sections, fmt.Sprintf("%s: %d", titles[i], count))
		}
	}
	return fmt.Sprintf("Плановые платежи: %d\n\n%s%s", counts[0]+counts[1], strings.Join(sections, "\n"), link)
}
func digestLength(text string) int {
	size := 0
	for _, r := range text {
		size++
		if r > 0xffff {
			size++
		}
	}
	return size
}
