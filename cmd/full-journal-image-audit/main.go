// full-journal-image-audit counts readable inline images without model calls.
package main

import (
	"aiseat/internal/db"
	"aiseat/internal/store"
	"aiseat/internal/workrecords"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: full-journal-image-audit RUN_ID")
		os.Exit(2)
	}
	run, e := strconv.ParseInt(os.Args[1], 10, 64)
	if e != nil || run <= 0 {
		fmt.Fprintln(os.Stderr, "invalid run")
		os.Exit(2)
	}
	ctx := context.Background()
	pool, e := db.Open(ctx, os.Getenv("DATABASE_URL"))
	if e != nil {
		fmt.Fprintln(os.Stderr, "database unavailable")
		os.Exit(1)
	}
	defer pool.Close()
	q := workrecords.Queue{Store: store.New(pool)}
	out, e := q.AuditInlineImages(ctx, run)
	if e != nil {
		fmt.Fprintln(os.Stderr, "image audit did not complete")
		os.Exit(1)
	}
	if e = json.NewEncoder(os.Stdout).Encode(out); e != nil {
		os.Exit(1)
	}
}
