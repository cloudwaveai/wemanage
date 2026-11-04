// wemanage-session-audit measures structural log categories without printing
// any source text or invoking a model.
package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"time"

	"aiseat/internal/db"
	"github.com/tidwall/gjson"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: wemanage-session-audit RUN_ID")
		os.Exit(2)
	}
	run, err := strconv.ParseInt(os.Args[1], 10, 64)
	if err != nil || run <= 0 {
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	pool, err := db.Open(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "database unavailable")
		os.Exit(1)
	}
	defer pool.Close()
	tx, err := pool.Begin(ctx)
	if err != nil {
		os.Exit(1)
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, `SET TRANSACTION READ ONLY`); err != nil {
		os.Exit(1)
	}
	var unsafe bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM wm_full_journal_sources s JOIN employees e ON e.id=s.employee_id WHERE s.run_id=$1 AND (e.private_work_since IS NOT NULL OR e.name='林伊默'))`, run).Scan(&unsafe); err != nil || unsafe {
		fmt.Fprintln(os.Stderr, "privacy check failed")
		os.Exit(1)
	}
	rows, err := tx.Query(ctx, `SELECT kind,data FROM wm_full_journal_blobs WHERE run_id=$1`, run)
	if err != nil {
		os.Exit(1)
	}
	defer rows.Close()
	type stats struct{ count, bytes int64 }
	groups := map[string]stats{}
	for rows.Next() {
		var kind string
		var data []byte
		if rows.Scan(&kind, &data) != nil {
			os.Exit(1)
		}
		switch kind {
		case "input", "output", "params", "instructions", "tools":
		default:
			kind = "other"
		}
		role := gjson.GetBytes(data, "role").String()
		switch role {
		case "user", "assistant", "system", "developer":
		default:
			role = "other"
		}
		typ := gjson.GetBytes(data, "type").String()
		switch typ {
		case "message", "reasoning", "function_call", "function_call_output", "custom_tool_call", "custom_tool_call_output", "tool_use", "tool_result", "input_text", "output_text":
		default:
			typ = "other"
		}
		key := kind + "/" + role + "/" + typ
		x := groups[key]
		x.count++
		x.bytes += int64(len(data))
		groups[key] = x
	}
	if rows.Err() != nil {
		os.Exit(1)
	}
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := groups[k]
		fmt.Printf("%s segments=%d bytes=%d\n", k, v.count, v.bytes)
	}
}
