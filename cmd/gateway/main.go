// Command gateway is the AI subscription gateway entrypoint.
package main

import (
	"context"
	"fmt"
	"os"

	"aiseat/internal/config"
	"aiseat/internal/db"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: gateway <serve|migrate|admin>")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "migrate":
		if err := runMigrate(); err != nil {
			fmt.Fprintln(os.Stderr, "migrate:", err)
			os.Exit(1)
		}
	case "serve":
		fmt.Fprintln(os.Stderr, "serve: not yet implemented")
		os.Exit(1)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", os.Args[1])
		os.Exit(2)
	}
}

func runMigrate() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx := context.Background()
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		return err
	}
	fmt.Println("migrations applied")
	return nil
}
