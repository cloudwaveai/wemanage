// full-journal-preparer keeps data preparation next to the database. It never
// receives an upstream model credential and cannot make model calls.
package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"aiseat/internal/db"
	"aiseat/internal/store"
	"aiseat/internal/workrecords"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		log.Error("DATABASE_URL is required")
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	pool, err := db.Open(ctx, url)
	if err != nil {
		log.Error("database connection failed", "error", err)
		os.Exit(1)
	}
	defer pool.Close()
	queue := &workrecords.Queue{Store: store.New(pool)}
	log.Info("journal preparer started")
	for ctx.Err() == nil {
		run, steps, err := queue.PrepareAvailable(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			log.Error("journal preparation stopped", "run", run, "error", err)
		}
		if steps > 0 {
			log.Info("journal preparation checkpoint", "run", run, "steps", steps)
		}
		timer := time.NewTimer(3 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
}
