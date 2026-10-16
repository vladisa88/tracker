package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/lib/pq"
	"tracking/internal/app"
)

func main() {
	c, err := app.LoadBotConfig()
	if err != nil {
		log.Fatal(err)
	}
	db, err := sql.Open("postgres", c.DatabaseURL)
	if err != nil {
		log.Fatal("invalid database configuration")
	}
	defer db.Close()
	db.SetMaxOpenConns(2)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	initCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	err = app.Migrate(initCtx, db)
	cancel()
	if err != nil {
		log.Fatal("database initialization failed")
	}
	worker := app.NewBotWorker(db, c, app.NewBot(c))
	log.Print("Telegram poller started")
	for ctx.Err() == nil {
		pollCtx, cancel := context.WithTimeout(ctx, 70*time.Second)
		err = worker.Poll(pollCtx)
		cancel()
		if err == nil {
			continue
		}
		if ctx.Err() != nil {
			break
		}
		// Never log transport errors: URLs may contain the bot token.
		log.Print("Telegram polling failed; retry scheduled")
		delay := 5 * time.Second
		var failure *app.BotError
		if errors.As(err, &failure) {
			if failure.RetryAfter > delay {
				delay = failure.RetryAfter
			}
			if failure.Permanent && delay < time.Minute {
				delay = time.Minute
			}
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
}
