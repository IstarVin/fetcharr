// Command fetcharr is the Fetcharr server entry point.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/istarvin/fetcharr/server/internal/api"
	"github.com/istarvin/fetcharr/server/internal/config"
	"github.com/istarvin/fetcharr/server/internal/events"
	"github.com/istarvin/fetcharr/server/internal/history"
	"github.com/istarvin/fetcharr/server/internal/jobs"
	"github.com/istarvin/fetcharr/server/internal/mediaserver"
	"github.com/istarvin/fetcharr/server/internal/process"
	"github.com/istarvin/fetcharr/server/internal/runner"
	"github.com/istarvin/fetcharr/server/internal/service"
	"github.com/istarvin/fetcharr/server/internal/store"
	"github.com/istarvin/fetcharr/server/internal/ws"
	"github.com/istarvin/fetcharr/server/internal/ytdlpmgr"
)

// version is reported by GET /api/info; release builds override it with
// -ldflags "-X main.version=...".
var version = "1.0.0"

const shutdownTimeout = 10 * time.Second

func main() {
	os.Exit(run(os.Args[1:]))
}

// run returns the process exit code: 0 on a clean shutdown, 1 on a startup or
// runtime failure, 2 on a usage error.
func run(args []string) int {
	fs := flag.NewFlagSet("fetcharr", flag.ContinueOnError)
	cfgFlag := fs.String("config", "", "path to config.yaml (default $FETCHARR_CONFIG, else ./config.yaml)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	path := config.ResolvePath(*cfgFlag)
	cfg, err := config.Load(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fetcharr: %v\n", err)
		return 1
	}
	log := newLogger(cfg.Server.LogLevel)

	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", cfg.Addr())
	if err != nil {
		log.Error("cannot listen", "addr", cfg.Addr(), "err", err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log.Info("fetcharr starting", "version", version, "config", path, "addr", ln.Addr().String())
	if err := serve(ctx, cfg, log, ln); err != nil {
		log.Error("fetcharr stopped with an error", "err", err)
		return 1
	}
	log.Info("fetcharr stopped")
	return 0
}

func newLogger(level string) *slog.Logger {
	var lv slog.Level
	if err := lv.UnmarshalText([]byte(level)); err != nil {
		lv = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: lv}))
}

// serve opens the store, wires the service and the HTTP API, and serves on ln
// until ctx is cancelled; then it shuts the server down gracefully.
func serve(ctx context.Context, cfg *config.Config, log *slog.Logger, ln net.Listener) error {
	st, err := store.Open(cfg.DBPath())
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer func() { _ = st.Close() }()

	// Startup recovery (spec §4.4): jobs a crash left running go back to pending.
	requeued, err := st.RequeueInterrupted(ctx)
	if err != nil {
		return fmt.Errorf("recover interrupted jobs: %w", err)
	}
	if len(requeued) > 0 {
		log.Info("requeued interrupted jobs", "count", len(requeued))
	}

	bus := events.NewBus()
	defer bus.Close()
	svc := service.New(st, cfg, service.WithPublisher(bus))
	procOpts := process.Options{Config: cfg, Store: st, Publisher: bus, Logger: log}
	var refresher *mediaserver.Refresher
	if len(cfg.MediaServers) > 0 {
		refresher = mediaserver.New(mediaserver.Options{Servers: cfg.MediaServers, Logger: log})
		procOpts.OnCompleted = refresher.OnCompleted
		// Safety net for early returns; the normal path closes it after the
		// runner stopped, so the last completions are flushed.
		defer closeRefresher(refresher)
	}
	proc := process.New(procOpts)
	rn := runner.New(runner.Options{Config: cfg, Store: st, Publisher: bus, Processor: proc, Logger: log})
	svc.SetController(rn)

	// Managed yt-dlp (spec §8): the binary must exist before the first job
	// can start, so it is downloaded before the runner starts.
	var updater api.YtdlpUpdater
	var ytdlpMgr *ytdlpmgr.Manager
	if cfg.YtdlpManaged() {
		ytdlpMgr = ytdlpmgr.New(ytdlpmgr.Options{
			Binary: cfg.ManagedYtdlpPath(), Runner: rn, Logger: log, AutoUpdate: cfg.Ytdlp.AutoUpdate,
		})
		if err := ytdlpMgr.Ensure(ctx); err != nil {
			return fmt.Errorf("managed yt-dlp: %w", err)
		}
		updater = ytdlpMgr
	}
	rn.Start()
	// Safety net for early returns; the normal path shuts the runner down
	// explicitly, between the HTTP server and the hub.
	defer func() { _ = rn.Shutdown(context.Background()) }()

	// Background work: yt-dlp auto-update and history pruning. Stopped (and
	// waited for) before the runner and the bus go away.
	bgCtx, bgCancel := context.WithCancel(ctx)
	var bg sync.WaitGroup
	stopBackground := func() {
		bgCancel()
		bg.Wait()
	}
	defer stopBackground()
	if ytdlpMgr != nil {
		bg.Go(func() { ytdlpMgr.Run(bgCtx) })
	}
	pruner := history.New(history.Options{
		Store: st, Publisher: bus, Logger: log, RetentionDays: cfg.History.RetentionDays,
	})
	bg.Go(func() { pruner.Run(bgCtx) })

	hub := ws.New(ws.Options{
		Bus:    bus,
		Logger: log,
		Snapshot: func(ctx context.Context) ([]*store.Job, error) {
			active, _, err := svc.List(ctx, store.ListFilter{Statuses: jobs.ActiveStatuses()})
			return active, err
		},
	})
	defer hub.Close()
	srv := &http.Server{
		Handler: api.New(api.Options{
			Config:    cfg,
			Service:   svc,
			Version:   version,
			Logger:    log,
			WebSocket: hub,
			Updater:   updater,
		}),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}

	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()

	select {
	case err := <-errc:
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
	}
	log.Info("shutting down")
	// Stop accepting requests, then kill running downloads and put their jobs
	// back to pending (their change events still reach WebSocket clients),
	// then disconnect the WebSocket clients. Hijacked WebSocket connections
	// are not tracked by http.Server, so Shutdown neither closes nor waits for
	// them.
	sctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	shutdownErr := srv.Shutdown(sctx)
	if shutdownErr != nil {
		_ = srv.Close()
	}
	stopBackground()
	if err := rn.Shutdown(sctx); err != nil {
		log.Error("runner shutdown", "err", err)
	}
	if refresher != nil {
		refresher.Close(sctx)
	}
	hub.Close()
	if shutdownErr != nil {
		return fmt.Errorf("graceful shutdown: %w", shutdownErr)
	}
	if err := <-errc; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("http server: %w", err)
	}
	return nil
}

// closeRefresher flushes pending media server refreshes, waiting at most
// shutdownTimeout.
func closeRefresher(r *mediaserver.Refresher) {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	r.Close(ctx)
}
