package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"aiseat/internal/db"
	"aiseat/internal/settings"
	"aiseat/internal/store"
	"aiseat/internal/wmjournal"
	"aiseat/internal/workdigest"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: wemanage-report-preview RUN_ID SESSION_ID")
		os.Exit(2)
	}
	run, e1 := strconv.ParseInt(os.Args[1], 10, 64)
	session, e2 := strconv.ParseInt(os.Args[2], 10, 64)
	if e1 != nil || e2 != nil || run <= 0 || session <= 0 {
		os.Exit(2)
	}
	url, key := os.Getenv("WM_MODEL_URL"), os.Getenv("WM_MODEL_KEY")
	if url != "http://127.0.0.1:18320" || key == "" {
		fmt.Fprintln(os.Stderr, "local model tunnel unavailable")
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	pool, err := db.Open(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "database unavailable")
		os.Exit(1)
	}
	defer pool.Close()
	st := store.New(pool)
	var sol string
	if err = pool.QueryRow(ctx, `SELECT sol_model FROM wm_full_journal_settings WHERE id=1`).Scan(&sol); err != nil {
		os.Exit(1)
	}
	runner := &workdigest.Runner{Store: st, Settings: settings.NewService(st), CPABaseURL: url, CPAAPIKey: key}
	id, err := wmjournal.GeneratePreview(ctx, st, run, session, runner, sol)
	if err != nil {
		fmt.Fprintln(os.Stderr, "preview stopped:", err)
		os.Exit(1)
	}
	fmt.Printf("preview_id=%d\n", id)
}
