package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"mp_sched/internal/api"
	"mp_sched/internal/callback"
	"mp_sched/internal/config"
	"mp_sched/internal/controller"
	"mp_sched/internal/database"
	"mp_sched/internal/pipeline"
	"mp_sched/internal/provider"
	"mp_sched/internal/provider/docker"
	"mp_sched/internal/provider/k8s"
	"mp_sched/internal/recordrepo"
	"mp_sched/internal/scheduler"
	"mp_sched/internal/taskrepo"
	"mp_sched/internal/telemetry"
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
	stopLog := telemetry.InitLogger(ctx, cfg, "mp-controller")
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
	cb := callback.New(&cfg.Callback, db)
	go cb.RunLoop(ctx, 5*time.Second)
	pl := &pipeline.Pipeline{Cfg: cfg, Repo: repo, Reg: reg, Rec: rec, CB: cb}

	h := &controller.Handlers{Pl: pl, Reg: reg, Repo: repo}
	addr := cfg.Controller.HTTP.Addr
	if addr == "" {
		addr = cfg.Server.Addr
	}
	if cfg.Controller.HTTP.Enable {
		_ = (&api.Server{H: h}).Start(ctx, addr)
		slog.Info("api listening", "addr", addr)
	} else {
		slog.Info("controller http disabled; set controller.http.enable=true")
	}

	counts, _ := repo.RunningSlotCounts()
	slog.Info("slots", "counts", counts, "policy", scheduler.Describe(cfg.Scheduler, counts))
	fmt.Fprintln(os.Stdout, "mp-controller ready; run mp-worker to process pending tasks")
	<-ctx.Done()
}
