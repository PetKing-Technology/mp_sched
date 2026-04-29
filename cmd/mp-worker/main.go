package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"mp_sched/internal/callback"
	"mp_sched/internal/config"
	"mp_sched/internal/database"
	"mp_sched/internal/pipeline"
	"mp_sched/internal/provider"
	"mp_sched/internal/provider/docker"
	"mp_sched/internal/provider/k8s"
	"mp_sched/internal/reconciler"
	"mp_sched/internal/recordrepo"
	"mp_sched/internal/scheduler"
	"mp_sched/internal/taskrepo"
	"mp_sched/internal/telemetry"
	"mp_sched/internal/worker"
)

func main() {
	configPath := flag.String("config", "configs/default.yaml", "config path (YAML)")
	flag.Parse()
	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load: %v\n", err)
		os.Exit(1)
	}
	cfg.ApplyDefaults()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	stopLog := telemetry.InitLogger(ctx, cfg, "mp-worker")
	defer stopLog()

	db, err := database.Open(&cfg.Database)
	if err != nil {
		slog.Error("db", "err", err.Error())
		os.Exit(1)
	}
	if err := database.Migrate(db); err != nil {
		slog.Error("migrate", "err", err.Error())
		os.Exit(1)
	}

	dck, err := docker.New(&cfg.Docker)
	if err != nil {
		slog.Error("docker provider", "err", err.Error())
		os.Exit(1)
	}
	reg := provider.Registry{
		"docker": dck,
		"k8s":    k8s.New(),
	}
	repo := &taskrepo.Repo{DB: db}
	rec := &recordrepo.Repo{DB: db}
	cb := callback.New(&cfg.Callback)
	pl := &pipeline.Pipeline{Cfg: cfg, Repo: repo, Reg: reg, Rec: rec, CB: cb, DockerEng: dck.Engine()}

	telemetry.StartDockerMonitors(ctx, cfg, dck.Engine(), repo)

	if cfg.Reconciler.Enable || cfg.Reconciler.OrphanReapEnable {
		intv := time.Duration(cfg.Reconciler.IntervalSeconds) * time.Second
		run := &reconciler.Runner{Repo: repo, Reg: reg, CB: cb, Cfg: &cfg.Reconciler, App: cfg, Eng: dck.Engine()}
		go run.RunLoop(ctx, intv)
		slog.Info("reconciler", "interval", intv.String(), "status_tick", cfg.Reconciler.Enable, "orphan_reap", cfg.Reconciler.OrphanReapEnable)
	}
	counts, _ := repo.RunningSlotCounts()
	slog.Info("worker slots", "counts", counts, "policy", scheduler.Describe(cfg.Scheduler, counts))
	worker.StartRuntimeTimeoutSweeper(ctx, cfg, pl, repo)
	fmt.Fprintln(os.Stdout, "mp-worker processing…")
	worker.Run(ctx, cfg, pl, repo)
}
