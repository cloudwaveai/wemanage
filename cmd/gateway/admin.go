package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"

	"aiseat/internal/config"
	"aiseat/internal/db"
	"aiseat/internal/store"
)

// runAdmin handles: admin bootstrap | issue-key <employee_id>
func runAdmin(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: gateway admin <bootstrap|issue-key> [args]")
	}
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
	st := store.New(pool)

	switch args[0] {
	case "bootstrap":
		if len(cfg.BootstrapSuperAdmins) == 0 {
			return errors.New("BOOTSTRAP_SUPER_ADMINS is empty")
		}
		for _, oid := range cfg.BootstrapSuperAdmins {
			if err := st.EnsureSuperAdmin(ctx, oid, ""); err != nil {
				return fmt.Errorf("ensure super admin %s: %w", oid, err)
			}
			fmt.Println("super admin ensured:", oid)
		}
		return nil

	case "issue-key":
		if len(args) < 2 {
			return errors.New("usage: gateway admin issue-key <employee_id>")
		}
		id, err := strconv.ParseInt(args[1], 10, 64)
		if err != nil {
			return fmt.Errorf("bad employee id: %w", err)
		}
		plain, key, err := st.IssueKey(ctx, id)
		if err != nil {
			return err
		}
		fmt.Fprintln(os.Stderr, "issued key (prefix "+key.KeyPrefix+"); the plaintext is printed once below:")
		fmt.Println(plain)
		return nil

	default:
		return fmt.Errorf("unknown admin subcommand %q", args[0])
	}
}
