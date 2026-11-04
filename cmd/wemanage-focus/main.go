package main

import (
	"aiseat/internal/db"
	"aiseat/internal/settings"
	"aiseat/internal/store"
	"aiseat/internal/wmfocus"
	"aiseat/internal/workdigest"
	"context"
	"fmt"
	"os"
)

func main() {
	url, key := os.Getenv("WM_MODEL_URL"), os.Getenv("WM_MODEL_KEY")
	if url != "http://127.0.0.1:18320" || key == "" {
		os.Exit(1)
	}
	ctx := context.Background()
	pool, e := db.Open(ctx, os.Getenv("DATABASE_URL"))
	if e != nil {
		os.Exit(1)
	}
	defer pool.Close()
	st := store.New(pool)
	var model string
	if pool.QueryRow(ctx, `select sol_model from wm_full_journal_settings where id=1`).Scan(&model) != nil {
		os.Exit(1)
	}
	runner := &workdigest.Runner{Store: st, Settings: settings.NewService(st), CPABaseURL: url, CPAAPIKey: key}
	for {
		worked, err := wmfocus.ProcessOne(ctx, st, runner, model)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			if !worked {
				os.Exit(1)
			}
		}
		if !worked {
			return
		}
	}
}
