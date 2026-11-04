// wemanage-journal-worker executes one already planned extraction on Aliyun.
// It never enables the old scheduler and does not retry ambiguous calls.
package main

import (
	"context"
	"fmt"
	"net/url"
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
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: wemanage-journal-worker RUN_ID")
		os.Exit(2)
	}
	run, err := strconv.ParseInt(os.Args[1], 10, 64)
	if err != nil || run <= 0 {
		fmt.Fprintln(os.Stderr, "invalid run")
		os.Exit(2)
	}
	endpoint := os.Getenv("WM_MODEL_URL")
	key := os.Getenv("WM_MODEL_KEY")
	model := os.Getenv("WM_MODEL_NAME")
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || key == "" || (u.Scheme != "https" && !(u.Scheme == "http" && u.Hostname() == "127.0.0.1")) {
		fmt.Fprintln(os.Stderr, "model configuration missing or insecure")
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
	var extractor wmjournal.Extractor
	var runner *workdigest.Runner
	st := store.New(pool)
	if os.Getenv("WM_CONFIGURED_ACCOUNT") == "1" {
		runner = &workdigest.Runner{Store: st, Settings: settings.NewService(st), CPABaseURL: endpoint, CPAAPIKey: key}
		extractor = wmjournal.ConfiguredExtractor{Runner: runner}
	} else {
		if model == "" {
			fmt.Fprintln(os.Stderr, "model name required")
			os.Exit(1)
		}
		extractor = &wmjournal.Client{URL: endpoint, Key: key, Model: model}
	}
	var state, phase, sol string
	var queued int
	err = pool.QueryRow(ctx, `SELECT r.state,COALESCE((SELECT b.phase FROM wm_full_journal_batches b WHERE b.run_id=r.id ORDER BY CASE b.phase WHEN 'synthesis' THEN 4 WHEN 'daily' THEN 3 WHEN 'reduce' THEN 2 ELSE 1 END DESC,b.level DESC LIMIT 1),''),(SELECT count(*) FROM wm_full_journal_batches b WHERE b.run_id=r.id AND b.phase='extract' AND b.state='queued'),s.sol_model FROM wm_full_journal_runs r CROSS JOIN wm_full_journal_settings s WHERE r.id=$1 AND s.id=1 AND NOT r.superseded`, run).Scan(&state, &phase, &queued, &sol)
	if err != nil {
		fmt.Fprintln(os.Stderr, "task unavailable")
		os.Exit(1)
	}
	if state == "done" || state == "partial" {
		fmt.Println("finished=true")
		return
	}
	var processed bool
	if phase == "extract" && queued > 0 {
		processed, err = wmjournal.ProcessOne(ctx, pool, run, extractor)
	} else if runner != nil {
		processed, err = wmjournal.AdvanceReduction(ctx, st, run, runner, sol)
	} else {
		err = fmt.Errorf("configured account required for synthesis")
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "batch stopped:", err)
		os.Exit(1)
	}
	fmt.Printf("processed=%t\n", processed)
}
