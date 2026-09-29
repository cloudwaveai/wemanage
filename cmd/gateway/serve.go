package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"aiseat/internal/blobstore"
	"aiseat/internal/config"
	"aiseat/internal/cpa"
	"aiseat/internal/db"
	"aiseat/internal/feishu"
	"aiseat/internal/health"
	"aiseat/internal/jobs"
	"aiseat/internal/notify"
	"aiseat/internal/proxy"
	"aiseat/internal/recorder"
	"aiseat/internal/report"
	"aiseat/internal/risk"
	"aiseat/internal/secure"
	"aiseat/internal/settings"
	"aiseat/internal/store"
	"aiseat/internal/web"
	"aiseat/internal/webui"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/robfig/cron/v3"
)

func runServe() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		return err
	}

	st := store.New(pool)
	set := settings.NewService(st)
	box, err := secure.NewBox(cfg.MasterKey)
	if err != nil {
		return err
	}

	// Optional external blob backend (S3/R2/OSS) for conversation content.
	if cfg.BlobBackend == "s3" {
		bs, err := blobstore.NewS3(ctx, blobstore.S3Config{
			Endpoint: cfg.S3Endpoint, Region: cfg.S3Region, Bucket: cfg.S3Bucket,
			AccessKey: cfg.S3AccessKey, SecretKey: cfg.S3SecretKey, UseSSL: cfg.S3UseSSL,
		})
		if err != nil {
			return err
		}
		st.SetBlobBackend(bs)
		log.Info("blob backend: s3", "endpoint", cfg.S3Endpoint, "bucket", cfg.S3Bucket)
	} else {
		log.Info("blob backend: postgres (inline)")
	}

	upstream := &http.Client{} // no client timeout; per-request contexts bound it
	cpaClient := cpa.NewClient(cfg.CPABaseURL, cfg.CPAManagementKey, &http.Client{Timeout: 30 * time.Second})
	quota := &health.QuotaProber{Store: st, CPA: cpaClient, Settings: set, Log: log}

	var fs *feishu.Client
	if cfg.FeishuAppID != "" && cfg.FeishuAppSecret != "" {
		fs = feishu.NewClient(cfg.FeishuBaseURL, cfg.FeishuAppID, cfg.FeishuAppSecret, &http.Client{Timeout: 20 * time.Second})
	}

	// Queue bound keeps memory predictable (each record carries its request
	// body); overflow is spooled to disk and replayed.
	rec := recorder.New(st, set, cfg.SpoolDir, log, 512)
	// The recorder outlives the HTTP server: requests still finishing during
	// shutdown submit records, which must be written (or spooled) afterwards.
	recCtx, recCancel := context.WithCancel(context.Background())
	defer recCancel()
	rec.Start(recCtx)

	// Bootstrap super admins from config (idempotent).
	for _, oid := range cfg.BootstrapSuperAdmins {
		if err := st.EnsureSuperAdmin(ctx, oid, ""); err != nil {
			log.Warn("bootstrap super admin", "open_id", oid, "err", err)
		}
	}

	// Anti key-sharing engine.
	var geo risk.GeoLookup
	if cfg.GeoIPDB != "" {
		g, err := risk.NewIP2Region(cfg.GeoIPDB)
		if err != nil {
			log.Warn("geoip disabled", "err", err)
		} else {
			geo = g
			log.Info("geoip enabled", "db", cfg.GeoIPDB)
		}
	}
	riskEngine := risk.NewEngine(risk.Config{
		Store: st, Settings: set, Geo: geo, Messenger: riskMessenger(fs),
		PortalURL: cfg.PublicBaseURL, Log: log,
	})
	riskEngine.Start(ctx)

	// Data plane.
	dp := proxy.NewHandler(proxy.HandlerDeps{
		Store: st, Settings: set, Limiter: proxy.NewLimiter(), Recorder: rec,
		CPABaseURL: cfg.CPABaseURL, CPAAPIKey: cfg.CPAAPIKey, Upstream: upstream, Log: log,
		Risk: riskEngine, TrustProxy: cfg.TrustProxyHeaders,
		PortalURL: cfg.PublicBaseURL, Loc: cfg.Timezone,
	})

	// Web / management plane.
	var superAdmins []string
	superAdmins = append(superAdmins, cfg.BootstrapSuperAdmins...)
	notifier := notify.NewNotifier(st, notifySender(fs), superAdmins)

	webSrv := web.NewServer(web.Deps{
		Loc:                  cfg.Timezone,
		Models:               dp.AccountModels,
		Quota:                quota,
		BootstrapSuperAdmins: cfg.BootstrapSuperAdmins,
		Store:                st, Settings: set, Box: box, CPA: cpaClient, Feishu: fs,
		FeishuLoginScope: cfg.FeishuLoginScope,
		PublicBaseURL:    cfg.PublicBaseURL, SecureCookies: isHTTPS(cfg.PublicBaseURL),
		DevLogin: cfg.DevLogin, Log: log, Risk: riskEngine,
		SyncFeishu: func(ctx context.Context) (int, int, error) {
			if fs == nil {
				return 0, 0, errors.New("feishu not configured")
			}
			return feishu.Run(ctx, st, fs)
		},
	})

	// Scheduled jobs.
	c := cron.New(cron.WithLocation(cfg.Timezone))
	jobs.Register(c, jobs.Deps{
		Store: st, Settings: set, Box: box, Notifier: notifier, Feishu: fs,
		Report: report.Deps{
			Store: st, LLM: report.LLM{BaseURL: cfg.ReportLLMBaseURL, APIKey: cfg.ReportLLMAPIKey, Model: cfg.ReportLLMModel},
			Sender: reportSender(fs), Loc: cfg.Timezone, Enabled: set.Get(ctx).ReportEnabled && cfg.ReportLLMAPIKey != "",
		},
		EgressEchoURL: cfg.EgressEchoURL, Loc: cfg.Timezone,
		GitHub: health.GitHub{}, Risk: riskEngine, Quota: quota, Log: log,
	})
	c.Start()
	defer c.Stop()

	// Router.
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	dp.Routes(r)
	webSrv.Mount(r)
	r.Handle("/*", webui.Handler())

	srv := &http.Server{Addr: cfg.ListenAddr, Handler: r}
	// Hijacked WebSocket connections are not closed by Shutdown; close them.
	srv.RegisterOnShutdown(dp.CloseWebsockets)
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx) // waits for in-flight requests
	}()

	log.Info("gateway listening", "addr", cfg.ListenAddr, "public", cfg.PublicBaseURL)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	<-shutdownDone
	if !rec.FlushTimeout(15 * time.Second) {
		log.Warn("recorder: flush timed out; spooling the rest to disk")
	}
	recCancel()
	rec.FlushTimeout(5 * time.Second) // cancelled loops spool what is left
	return nil
}

// reportSender / notifySender adapt the feishu client to the (distinct) Sender
// interfaces, returning a true nil interface when feishu is not configured so
// callers' nil checks work.
func reportSender(fs *feishu.Client) report.Sender {
	if fs == nil {
		return nil
	}
	return fs
}

func notifySender(fs *feishu.Client) notify.Sender {
	if fs == nil {
		return nil
	}
	return fs
}

func riskMessenger(fs *feishu.Client) risk.Messenger {
	if fs == nil {
		return nil
	}
	return fs
}

func isHTTPS(u string) bool {
	return len(u) >= 8 && u[:8] == "https://"
}
