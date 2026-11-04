// wemanage-log-audit checks an existing immutable journal snapshot without
// calling a model or writing to the database.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"strconv"
	"time"

	"aiseat/internal/db"
	"aiseat/internal/wmjournal"
	"github.com/tidwall/gjson"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: wemanage-log-audit RUN_ID")
		os.Exit(2)
	}
	run, err := strconv.ParseInt(os.Args[1], 10, 64)
	if err != nil || run <= 0 {
		fmt.Fprintln(os.Stderr, "invalid run")
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
		panic(err)
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, `SET TRANSACTION READ ONLY`); err != nil {
		panic(err)
	}
	var unsafe bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM wm_full_journal_sources s JOIN employees e ON e.id=s.employee_id WHERE s.run_id=$1 AND (e.private_work_since IS NOT NULL OR e.name='林伊默'))`, run).Scan(&unsafe); err != nil || unsafe {
		fmt.Fprintln(os.Stderr, "privacy check failed")
		os.Exit(1)
	}
	rows, err := tx.Query(ctx, `SELECT data FROM wm_full_journal_blobs WHERE run_id=$1`, run)
	if err != nil {
		fmt.Fprintln(os.Stderr, "snapshot unavailable")
		os.Exit(1)
	}
	defer rows.Close()
	var total, media, opaque, unsupported, sourceBytes int64
	errorsByKind := map[string]int{}
	mediaKinds := map[string]int{}
	mediaEndings := map[string]int{}
	var unsupportedHashes [][]byte
	var starts, complete, noComma, emptyPayload int
	roles := map[string]int64{}
	roleBytes := map[string]int64{}
	var hasBase64Header, slashEscapes, leadingSpace int
	for rows.Next() {
		var data []byte
		if rows.Scan(&data) != nil {
			fmt.Fprintln(os.Stderr, "read failed")
			os.Exit(1)
		}
		total++
		sourceBytes += int64(len(data))
		role := gjson.GetBytes(data, "role").String()
		switch role {
		case "user", "assistant", "system", "developer":
		default:
			role = "other"
		}
		roles[role]++
		roleBytes[role] += int64(len(data))
		spans, gap, e := wmjournal.UnreadableSpans(data)
		if e != nil {
			unsupported++
			hash := sha256.Sum256(data)
			unsupportedHashes = append(unsupportedHashes, hash[:])
			errorsByKind[e.Error()]++
			a, b, c, d := wmjournal.MediaShape(data)
			for kind, n := range wmjournal.UnmatchedMediaKinds(data) {
				mediaKinds[kind] += n
			}
			for kind, n := range wmjournal.MediaTermination(data) {
				mediaEndings[kind] += n
			}
			starts += a
			complete += b
			noComma += c
			emptyPayload += d
			if bytes.Contains(bytes.ToLower(data), []byte(";base64,")) {
				hasBase64Header++
			}
			if bytes.Contains(data, []byte(`\/`)) {
				slashEscapes++
			}
			if bytes.Contains(bytes.ToLower(data), []byte("data:image/ ")) {
				leadingSpace++
			}
			continue
		}
		if gap {
			media++
		}
		for _, s := range spans {
			if s.Kind == "加密推理" {
				opaque++
			}
		}
	}
	if rows.Err() != nil {
		fmt.Fprintln(os.Stderr, "read failed")
		os.Exit(1)
	}
	rows.Close()
	fmt.Printf("segments=%d original_bytes=%d media_segments=%d encrypted_segments=%d unsupported_segments=%d\n", total, sourceBytes, media, opaque, unsupported)
	for kind, count := range errorsByKind {
		fmt.Printf("unsupported_kind=%s count=%d\n", kind, count)
	}
	for role, count := range roles {
		fmt.Printf("role=%s segments=%d bytes=%d\n", role, count, roleBytes[role])
	}
	fmt.Printf("unsupported_media_starts=%d matched=%d missing_comma=%d empty_payload=%d\n", starts, complete, noComma, emptyPayload)
	fmt.Printf("unsupported_with_base64_header=%d slash_escape=%d space_after_media=%d\n", hasBase64Header, slashEscapes, leadingSpace)
	for kind, count := range mediaKinds {
		fmt.Printf("media_shape=%s count=%d\n", kind, count)
	}
	for kind, count := range mediaEndings {
		fmt.Printf("media_termination=%s count=%d\n", kind, count)
	}
	if len(unsupportedHashes) > 0 {
		var affected int
		var first *int
		err = tx.QueryRow(ctx, `SELECT count(*),min(ordinal) FROM wm_full_journal_batches b WHERE b.run_id=$1 AND b.phase='extract' AND b.state='queued' AND EXISTS(SELECT 1 FROM jsonb_array_elements(b.input_manifest->'parts') p WHERE decode(p->>'hash','hex')=ANY($2))`, run, unsupportedHashes).Scan(&affected, &first)
		if err != nil {
			fmt.Fprintln(os.Stderr, "batch impact unavailable")
			os.Exit(1)
		}
		if first != nil {
			fmt.Printf("unsupported_queued_batches=%d first_ordinal=%d\n", affected, *first)
		}
	}
}
