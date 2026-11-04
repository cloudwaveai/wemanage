// Run the existing daily-report knowledge extractor from the independent host.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"time"

	"aiseat/internal/db"
	"aiseat/internal/knowledge"
	"aiseat/internal/settings"
	"aiseat/internal/store"
	"aiseat/internal/workdigest"
)

type guarded struct {
	store  *store.Store
	runner *workdigest.Runner
	model  string
}

func (g guarded) Extract(ctx context.Context, system, input string) (string, string, workdigest.ModelUsage, error) {
	var payload struct {
		Sources []struct {
			EmployeeID int64 `json:"employee_id"`
		} `json:"sources"`
	}
	if json.Unmarshal([]byte(input), &payload) != nil || len(payload.Sources) == 0 {
		return "", "", workdigest.ModelUsage{}, errors.New("来源无效")
	}
	ids := map[int64]bool{}
	for _, s := range payload.Sources {
		if s.EmployeeID <= 0 {
			return "", "", workdigest.ModelUsage{}, errors.New("来源无效")
		}
		ids[s.EmployeeID] = true
	}
	var ordered []int64
	for id := range ids {
		ordered = append(ordered, id)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	tx, err := g.store.Pool.Begin(ctx)
	if err != nil {
		return "", "", workdigest.ModelUsage{}, err
	}
	defer tx.Rollback(context.Background())
	for _, id := range ordered {
		var allowed bool
		if err = tx.QueryRow(ctx, `select private_work_since is null and name<>'林伊默' from employees where id=$1 for share`, id).Scan(&allowed); err != nil || !allowed {
			return "", "", workdigest.ModelUsage{}, errors.New("来源权限已变化")
		}
	}
	text, model, usage, err := g.runner.ExtractWithModel(ctx, g.model, system, input)
	return text, model, usage, err
}
func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: wemanage-digest-knowledge daily|weekly YYYY-MM-DD")
		os.Exit(2)
	}
	kind := os.Args[1]
	if kind != "daily" && kind != "weekly" {
		os.Exit(2)
	}
	loc, _ := time.LoadLocation("Asia/Shanghai")
	from, err := time.ParseInLocation(time.DateOnly, os.Args[2], loc)
	if err != nil {
		os.Exit(2)
	}
	to := from.AddDate(0, 0, 1)
	if kind == "weekly" {
		to = from.AddDate(0, 0, 7)
	}
	key, url := os.Getenv("WM_MODEL_KEY"), os.Getenv("WM_MODEL_URL")
	if key == "" || url != "http://127.0.0.1:18320" {
		fmt.Fprintln(os.Stderr, "模型连接不可用")
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	pool, err := db.Open(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "数据库不可用")
		os.Exit(1)
	}
	defer pool.Close()
	st := store.New(pool)
	// Do not automatically resubmit an earlier failed or interrupted model call.
	var blocked bool
	err = pool.QueryRow(ctx, `select exists(select 1 from wm_knowledge_runs where kind=$1 and period_start=$2::date and period_end=$3::date and status in ('running','failed'))`, kind, from, to).Scan(&blocked)
	if err != nil || blocked {
		fmt.Fprintln(os.Stderr, "已有失败或未知结果，停止自动重试")
		os.Exit(1)
	}
	var ready int
	if err = pool.QueryRow(ctx, `select count(*) from work_digest_days where day >= $1::date and day < $2::date and status='done'`, from, to).Scan(&ready); err != nil || ready != int(to.Sub(from).Hours()/24) {
		fmt.Fprintln(os.Stderr, "日报尚未就绪，稍后再检查")
		os.Exit(1)
	}
	var model string
	if err = pool.QueryRow(ctx, `select sol_model from wm_full_journal_settings where id=1`).Scan(&model); err != nil {
		os.Exit(1)
	}
	base := &workdigest.Runner{Store: st, Settings: settings.NewService(st), CPABaseURL: url, CPAAPIKey: key}
	r := &knowledge.Runner{Store: st, Extractor: guarded{st, base, model}, Loc: loc}
	if err = r.Run(ctx, kind, from, to); err != nil {
		fmt.Fprintln(os.Stderr, "日报提炼未完成，请检查模型或来源状态")
		os.Exit(1)
	}
	fmt.Printf("%s %s 已处理\n", kind, from.Format(time.DateOnly))
}
