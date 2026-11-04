// Independent, resumable session-based organization journal.
package main

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"aiseat/internal/db"
	"aiseat/internal/settings"
	"aiseat/internal/store"
	"aiseat/internal/wmjournal"
	"aiseat/internal/workdigest"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: wemanage-compact plan|run RUN_ID")
		os.Exit(2)
	}
	run, err := strconv.ParseInt(os.Args[2], 10, 64)
	if err != nil || run <= 0 {
		os.Exit(2)
	}
	ctx := context.Background()
	pool, err := db.Open(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "数据库不可用")
		os.Exit(1)
	}
	defer pool.Close()
	st := store.New(pool)
	switch os.Args[1] {
	case "plan":
		n, e := wmjournal.PlanCompact(ctx, st, run)
		if e != nil {
			fmt.Fprintln(os.Stderr, e)
			os.Exit(1)
		}
		fmt.Printf("已规划 %d 批（包含复用结果）\n", n)
	case "pack":
		n, e := wmjournal.PackCompact(ctx, st, run)
		if e != nil {
			fmt.Fprintln(os.Stderr, e)
			os.Exit(1)
		}
		fmt.Printf("合并后待处理 %d 批\n", n)
	case "run", "one":
		url, key := os.Getenv("WM_MODEL_URL"), os.Getenv("WM_MODEL_KEY")
		if url != "http://127.0.0.1:18320" || key == "" {
			fmt.Fprintln(os.Stderr, "模型连接不可用")
			os.Exit(1)
		}
		runner := &workdigest.Runner{Store: st, Settings: settings.NewService(st), CPABaseURL: url, CPAAPIKey: key}
		for {
			var pending int
			var phase string
			err = pool.QueryRow(ctx, `select count(*),coalesce((select phase from wm_compact_jobs where run_id=$1 and state='queued' order by id limit 1),'') from wm_compact_jobs where run_id=$1 and state='queued'`, run).Scan(&pending, &phase)
			if err != nil {
				break
			}
			if pending == 0 {
				var more bool
				more, err = wmjournal.PrepareCompactNext(ctx, st, run)
				if err != nil || !more {
					break
				}
				continue
			}
			name := "gpt-6-luna"
			if phase != "extract" {
				if err = pool.QueryRow(ctx, `select sol_model from wm_full_journal_settings where id=1`).Scan(&name); err != nil {
					break
				}
			}
			_, err = wmjournal.CompactStep(ctx, st, run, runner, name)
			if err != nil {
				break
			}
			fmt.Printf("%s 完成一批，剩余 %d\n", phase, pending-1)
			if os.Args[1] == "one" {
				return
			}
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("当天报告整理完成")
	default:
		os.Exit(2)
	}
}
