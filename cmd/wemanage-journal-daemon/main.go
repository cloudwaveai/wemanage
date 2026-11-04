// wemanage-journal-daemon runs the independent log pipeline on the database host.
package main

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"aiseat/internal/db"
	"aiseat/internal/settings"
	"aiseat/internal/store"
	"aiseat/internal/wmjournal"
	"aiseat/internal/workdigest"
)

func main() {
	endpoint, key := os.Getenv("WM_MODEL_URL"), os.Getenv("WM_MODEL_KEY")
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || key == "" {
		log.Fatal("invalid local model tunnel configuration")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, err := db.Open(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatal("database unavailable")
	}
	defer pool.Close()
	st := store.New(pool)
	runner := &workdigest.Runner{Store: st, Settings: settings.NewService(st), CPABaseURL: endpoint, CPAAPIKey: key}
	extract := wmjournal.ConfiguredExtractor{Runner: runner}
	log.Print("independent log worker started")
	for ctx.Err() == nil {
		var disk syscall.Statfs_t
		if err := syscall.Statfs("/", &disk); err != nil || uint64(disk.Bavail)*uint64(disk.Bsize) < 5<<30 {
			pause(ctx, st, "磁盘可用空间不足 5 GB 或无法检查空间")
			sleep(ctx, 10*time.Second)
			continue
		}
		stepCtx, cancel := context.WithTimeout(ctx, 45*time.Minute)
		worked, e := wmjournal.ScheduleOne(stepCtx, st, extract, runner, time.Now())
		cancel()
		if e != nil && ctx.Err() == nil {
			pause(ctx, st, safeError(e))
			log.Print("journal stopped for inspection")
			sleep(ctx, 10*time.Second)
			continue
		}
		if worked {
			sleep(ctx, 100*time.Millisecond)
		} else {
			sleep(ctx, 10*time.Second)
		}
	}
}
func sleep(ctx context.Context, d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
func pause(ctx context.Context, st *store.Store, message string) {
	_, _ = st.Pool.Exec(ctx, `UPDATE wm_log_worker_control SET enabled=false,last_error=$1,updated_at=now() WHERE id=1 AND enabled`, message)
}
func safeError(err error) string {
	switch err.Error() {
	case "unrecognized media encoding", "unsupported media encoding":
		return "记录中存在暂不支持的媒体编码，已暂停；原始记录保留"
	case "invalid encrypted field", "unrecognized encrypted source":
		return "记录中存在无法确认的加密字段，已暂停；原始记录保留"
	case "source privacy changed":
		return "来源权限发生变化，已暂停"
	case "batch failed or outcome unknown", "任务需要检查":
		return "已有批次失败或结果未知，已暂停并保留原稿"
	case "知识候选任务需要检查", "知识候选格式或来源未通过核验，原稿已保留":
		return "知识候选需要核对，已暂停并保留模型原稿"
	case "周期复盘需要检查", "period review needs attention":
		return "周期复盘需要检查，已暂停并保留原稿"
	case "旧整理程序仍启用", "scheduler must remain paused":
		return "旧整理程序仍在启用状态，独立整理已暂停"
	default:
		return fmt.Sprintf("整理遇到问题，已暂停；请检查服务状态")
	}
}
