// full-journal-verify checks prepared evidence without invoking any model.
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
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: full-journal-verify RUN_ID ORDINAL...")
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
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
	defer pool.Close()
	q := workrecords.Queue{Store: store.New(pool)}
	out := []workrecords.EvidenceVerification{}
	for _, s := range os.Args[2:] {
		i, e := strconv.Atoi(s)
		if e != nil || i < 0 {
			fmt.Fprintln(os.Stderr, "invalid ordinal")
			os.Exit(2)
		}
		x, e := q.VerifyEvidenceBatch(ctx, run, i)
		if e != nil {
			fmt.Fprintln(os.Stderr, e)
			os.Exit(1)
		}
		out = append(out, x)
	}
	if e = json.NewEncoder(os.Stdout).Encode(out); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
