// full-journal-replan creates or plans an unexecuted version of a paused run.
package main

import (
	"aiseat/internal/db"
	"aiseat/internal/store"
	"aiseat/internal/workrecords"
	"context"
	"fmt"
	"os"
	"strconv"
)

func main() {
	if len(os.Args) != 3 || (os.Args[1] != "clone" && os.Args[1] != "plan") {
		fmt.Fprintln(os.Stderr, "usage: full-journal-replan clone|plan RUN_ID")
		os.Exit(2)
	}
	id, err := strconv.ParseInt(os.Args[2], 10, 64)
	if err != nil || id <= 0 {
		fmt.Fprintln(os.Stderr, "invalid run id")
		os.Exit(2)
	}
	ctx := context.Background()
	pool, err := db.Open(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer pool.Close()
	q := &workrecords.Queue{Store: store.New(pool)}
	if os.Args[1] == "clone" {
		run, e := q.ReplanSnapshot(ctx, id)
		if e != nil {
			fmt.Fprintln(os.Stderr, e)
			os.Exit(1)
		}
		fmt.Printf("created run %d; original %d retained\n", run, id)
		return
	}
	err = q.PlanPaused(ctx, id, func(n int) { fmt.Printf("planned %d sources\n", n) })
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
