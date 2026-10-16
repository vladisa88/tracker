package main

import (
	"context"
	"database/sql"
	"log"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/lib/pq"
	"tracking/internal/app"
)

func main() {
	c, err := app.LoadDigestConfig()
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
	worker := app.NewDigestWorker(db, c, app.NewBot(c))
	log.Print("Daily digest worker started")
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for ctx.Err() == nil {
		cycleCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
		err := worker.Cycle(cycleCtx, time.Now())
		cancel()
		if err != nil {
			// Transport errors may contain the bot token; never log raw errors.
			log.Print("Daily digest cycle failed")
		}
		select {
		case <-ctx.Done():
		case <-ticker.C:
		}
	}
}
