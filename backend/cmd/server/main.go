package main

import (
	"context"
	"database/sql"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/lib/pq"
	"tracking/internal/app"
)

func main() {
	c, err := app.LoadConfig()
	if err != nil {
		log.Fatal(err)
	}
	db, err := sql.Open("postgres", c.DatabaseURL)
	if err != nil {
		log.Fatal("invalid database configuration")
	}
	defer db.Close()
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(time.Hour)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	err = app.Migrate(ctx, db)
	cancel()
	if err != nil {
		log.Fatal("database initialization failed")
	}
	srv := &http.Server{Addr: ":8080", Handler: app.New(db, c, app.NewTelegram(c)), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 20 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16384}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-stop
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	}()
	log.Print("API listening on :8080")
	if err = srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal("HTTP server failed")
	}
}
