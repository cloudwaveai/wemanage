// full-journal-replan creates an unexecuted snapshot version for a paused run.
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
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: full-journal-replan RUN_ID")
		os.Exit(2)
	}
	id, err := strconv.ParseInt(os.Args[1], 10, 64)
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
	run, err := (&workrecords.Queue{Store: store.New(pool)}).ReplanSnapshot(ctx, id)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("created run %d; original %d retained\n", run, id)
}
