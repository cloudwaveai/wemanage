// full-journal-estimate is read-only and has no model credentials or client.
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
	if len(os.Args) != 2 && !(len(os.Args) == 3 && os.Args[1] == "compact") {
		fmt.Fprintln(os.Stderr, "usage: full-journal-estimate [compact] RUN_ID")
		os.Exit(2)
	}
	id, err := strconv.ParseInt(os.Args[len(os.Args)-1], 10, 64)
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
	q := workrecords.Queue{Store: store.New(pool)}
	var result workrecords.EvidenceEstimate
	progress := func(n int64) { fmt.Fprintf(os.Stderr, "estimated %d groups\n", n) }
	if len(os.Args) == 3 {
		result, err = q.EstimateCompactEvidence(ctx, id, progress)
	} else {
		result, err = q.EstimateEvidence(ctx, id, progress)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err = json.NewEncoder(os.Stdout).Encode(result); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
