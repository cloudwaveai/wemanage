package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"aiseat/internal/db"
	"aiseat/internal/settings"
	"aiseat/internal/store"
	"aiseat/internal/wemanage"
	"aiseat/internal/workdigest"
)

func main() {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		log.Fatal("DATABASE_URL is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool, err := db.Open(ctx, url)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	st := store.New(pool)
	if len(os.Args) > 1 && os.Args[1] == "install" {
		if err = wemanage.Install(ctx, st); err != nil {
			log.Fatal(err)
		}
		fmt.Println("We-Manage schema ready")
		return
	}
	runner := &workdigest.Runner{HC: &http.Client{Timeout: 8 * time.Minute}, Store: st, Settings: settings.NewService(st), CPABaseURL: os.Getenv("WM_MODEL_URL"), CPAAPIKey: os.Getenv("WM_MODEL_KEY")}
	if len(os.Args) > 1 && os.Args[1] == "curate-revalidate" {
		if len(os.Args) != 3 {
			log.Fatal("usage: wemanage curate-revalidate <run id>")
		}
		id, e := strconv.ParseInt(os.Args[2], 10, 64)
		if e != nil || id < 1 {
			log.Fatal("invalid run id")
		}
		if e = (&wemanage.Service{Store: st}).RevalidateCuration(ctx, id); e != nil {
			log.Fatal(e)
		}
		fmt.Printf("Curation %d revalidated without a model call\n", id)
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "curate" {
		if len(os.Args) != 3 {
			log.Fatal("usage: wemanage curate <administrator employee id>")
		}
		owner, e := strconv.ParseInt(os.Args[2], 10, 64)
		if e != nil || owner < 1 {
			log.Fatal("invalid administrator employee id")
		}
		runCtx, stop := context.WithTimeout(context.Background(), 9*time.Minute)
		defer stop()
		id, e := (&wemanage.Service{Store: st, Summarizer: runner}).CurateOnce(runCtx, owner)
		if e != nil {
			log.Fatalf("curation %d: %v", id, e)
		}
		fmt.Printf("Curation %d ready for review\n", id)
		return
	}
	// A restart cannot know whether the upstream completed a previous call.
	if _, err := st.Pool.Exec(ctx, `update wm_custom_summaries set state='failed', error='服务重启，生成结果未知；请检查后重新生成' where state='running'`); err != nil {
		log.Fatal("custom summary initialization: ", err)
	}
	addr := os.Getenv("WEMANAGE_LISTEN")
	if addr == "" {
		addr = "127.0.0.1:19081"
	}

	srv := &http.Server{Addr: addr, Handler: (&wemanage.Service{Store: st, Summarizer: runner}).Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}
	log.Printf("We-Manage listening on %s", addr)
	log.Fatal(srv.ListenAndServe())
}
