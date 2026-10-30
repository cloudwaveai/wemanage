// full-journal-sample processes at most one batch using the gateway's existing model account.
package main

import (
	"aiseat/internal/config"
	"aiseat/internal/db"
	"aiseat/internal/settings"
	"aiseat/internal/store"
	"aiseat/internal/workdigest"
	"aiseat/internal/workrecords"
	"context"
	"fmt"
	"os"
	"strconv"
	"time"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: full-journal-sample RUN_ID")
		os.Exit(2)
	}
	run, e := strconv.ParseInt(os.Args[1], 10, 64)
	if e != nil || run <= 0 {
		fmt.Fprintln(os.Stderr, "invalid run")
		os.Exit(2)
	}
	cfg, e := config.Load()
	if e != nil {
		fmt.Fprintln(os.Stderr, "configuration unavailable")
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	pool, e := db.Open(ctx, cfg.DatabaseURL)
	if e != nil {
		fmt.Fprintln(os.Stderr, "database unavailable")
		os.Exit(1)
	}
	defer pool.Close()
	st := store.New(pool)
	digest := &workdigest.Runner{Store: st, Settings: settings.NewService(st), CPABaseURL: cfg.CPABaseURL, CPAAPIKey: cfg.CPAAPIKey, Loc: cfg.Timezone}
	ok, e := (&workrecords.Queue{Store: st}).ProcessOnePaused(ctx, run, digest)
	if e != nil {
		fmt.Fprintln(os.Stderr, "sample did not complete; inspect recorded task state")
		os.Exit(1)
	}
	fmt.Printf("sample processed: %t\n", ok)
}
