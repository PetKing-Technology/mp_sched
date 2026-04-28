// 集成测试。配置：默认从仓库根下 configs/development.yaml 完整加载（与 mp-worker -config 同源结构），可用 E2E_CONFIG 覆盖路径。
// database.dsn：若设置 E2E_DSN 则覆盖文件内 dsn；否则使用文件中的 database.dsn。
// 默认会略放宽 scheduler 并发上限以兼容共享库中历史 running 行；严格使用文件数值时设 E2E_STRICT_SCHED=1。
// Postgres：未设置且文件无 dsn 时 Skip。CH 探针：e2e_clickhouse_test.go（E2E_CH_ADDR）。
//
// Docker：默认走真实 Client（ContainerCreate+Start 即 provider.Run），需本机 Docker 可 Ping、能拉 e2e 中使用的镜像（如 alpine:3.20）。
// 不启 Docker 或 CI 仅测 API 时设 E2E_DOCKER=stub 使用不真正 create/start 的桩（runtime_ref 形如 e2e-stub-…）。
// bind 挂载仅来自所加载配置文件中的 docker.mounts（见 configs/development.yaml 等）。
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mp_sched/internal/api"
	"mp_sched/internal/config"
	"mp_sched/internal/controller"
	"mp_sched/internal/database"
	"mp_sched/internal/model"
	"mp_sched/internal/pipeline"
	"mp_sched/internal/provider"
	"mp_sched/internal/provider/docker"
	"mp_sched/internal/provider/k8s"
	"mp_sched/internal/recordrepo"
	"mp_sched/internal/taskrepo"
	"mp_sched/internal/worker"

	"gorm.io/gorm/logger"
)

const (
	// 环境变量 E2E_DOCKER：未设置/ real / 1 / true 表示使用真实 Docker provider（默认）；stub / 0 / false 表示桩。
	e2eEnvDocker = "E2E_DOCKER"
	// 未设置 E2E_CONFIG 时尝试从模块根加载（go test ./e2e 工作目录一般为仓库根）
	e2eDefaultConfigPath = "configs/development.yaml"
)

func e2eUseStubDocker() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(e2eEnvDocker)))
	if v == "" {
		return false
	}
	return v == "stub" || v == "0" || v == "false" || v == "no" || v == "off"
}

// stubDocker 不连接 Docker Engine，不执行 pull/create/start；供 E2E_DOCKER=stub 使用。
type stubDocker struct{}

func (stubDocker) Name() string { return "docker" }

func (stubDocker) ResourceCheck(ctx context.Context, t *model.Task) (*provider.ResourceCheckResult, error) {
	_ = ctx
	_ = t
	return &provider.ResourceCheckResult{OK: true, Reason: "e2e stub"}, nil
}

func (stubDocker) Run(ctx context.Context, t *model.Task) (string, error) {
	_ = ctx
	return "e2e-stub-" + t.TaskID, nil
}

func (stubDocker) Status(ctx context.Context, t *model.Task) (*provider.RuntimeStatus, error) {
	_ = ctx
	_ = t
	return &provider.RuntimeStatus{Phase: provider.PhaseRunning, Message: "stub"}, nil
}

func (stubDocker) Stop(ctx context.Context, t *model.Task) error {
	_ = ctx
	_ = t
	return nil
}

var _ provider.Provider = stubDocker{}

func resolveE2EConfigPath(t *testing.T) string {
	t.Helper()
	if p := strings.TrimSpace(os.Getenv("E2E_CONFIG")); p != "" {
		return p
	}
	cands := []string{
		e2eDefaultConfigPath,
		filepath.Join("..", e2eDefaultConfigPath),
	}
	for _, c := range cands {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			if abs, err := filepath.Abs(c); err == nil {
				t.Logf("e2e: config file %s", abs)
			}
			return c
		}
	}
	t.Fatalf("e2e: 找不到配置文件（已尝试 %v）；请在仓库根执行 go test，或设置 E2E_CONFIG 为绝对/相对路径", cands)
	return ""
}

// e2eLoadAppConfig 加载与线上一致的完整 App 配置；E2E_DSN 非空时覆盖 database.dsn。
// 与共享开发库多次跑 e2e 时，易残留大量 running 任务占满 scheduler 槽位；未设 E2E_STRICT_SCHED=1 时略放宽 max，避免 Admit 长期失败。
func e2eLoadAppConfig(t *testing.T) *config.App {
	t.Helper()
	path := resolveE2EConfigPath(t)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("e2e: load config %s: %v", path, err)
	}
	if d := strings.TrimSpace(os.Getenv("E2E_DSN")); d != "" {
		cfg.Database.DSN = d
	}
	cfg.ApplyDefaults()
	if strings.TrimSpace(os.Getenv("E2E_STRICT_SCHED")) == "" {
		if cfg.Scheduler.MaxConcurrentRunning < 200 {
			cfg.Scheduler.MaxConcurrentRunning = 200
		}
		if cfg.Scheduler.MaxConcurrentSlow < 50 {
			cfg.Scheduler.MaxConcurrentSlow = 50
		}
	}
	if strings.TrimSpace(cfg.Database.DSN) == "" {
		t.Skip("e2e: database.dsn 为空；请在配置文件中设置 database.dsn，或 export E2E_DSN=...")
	}
	return cfg
}

// newHarness 注册 docker provider：默认真实 docker.New（走 Run / Status / Stop），E2E_DOCKER=stub 时换桩。
// 非 stub 时返回的 dck 可用于 Ping、及测试结束后 e2eStopTaskByRef 清容器；stub 时 dck 为 nil。
func newHarness(t *testing.T) (baseURL string, pl *pipeline.Pipeline, repo *taskrepo.Repo, cfg *config.App, dck *docker.Client, cleanup func()) {
	t.Helper()
	cfg = e2eLoadAppConfig(t)
	db, err := database.Open(&cfg.Database)
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	if err := database.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// worker 轮询时空队列会触发 ErrRecordNotFound，GORM 默认会打 SQL + record not found，干扰阅读；e2e 下设为 Silent
	db.Logger = logger.Default.LogMode(logger.Silent)
	repo = &taskrepo.Repo{DB: db}
	rec := &recordrepo.Repo{DB: db}
	var dockerProv provider.Provider
	if e2eUseStubDocker() {
		dockerProv = stubDocker{}
	} else {
		var derr error
		dck, derr = docker.New(&cfg.Docker)
		if derr != nil {
			t.Fatalf("docker client: %v", derr)
		}
		dockerProv = dck
	}
	reg := provider.Registry{
		"docker": dockerProv,
		"k8s":    k8s.New(),
	}
	pl = &pipeline.Pipeline{Cfg: cfg, Repo: repo, Reg: reg, Rec: rec, CB: nil}
	h := &controller.Handlers{Pl: pl, Reg: reg, Repo: repo}
	srv := httptest.NewServer((&api.Server{H: h}).NewHandler())
	cleanup = func() {
		srv.Close()
		x, _ := db.DB()
		_ = x.Close()
	}
	return srv.URL, pl, repo, cfg, dck, cleanup
}

// e2eStopTaskByRef 若存在 runtime_ref 则 Stop（删容器），避免真实 e2e 留下孤儿容器
func e2eStopTaskByRef(t *testing.T, dck *docker.Client, repo *taskrepo.Repo, taskID string) {
	t.Helper()
	if dck == nil || repo == nil || taskID == "" {
		return
	}
	row, err := repo.Get(taskID)
	if err != nil {
		return
	}
	if strings.TrimSpace(row.RuntimeRef) == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := dck.Stop(ctx, row); err != nil {
		t.Logf("e2e cleanup Stop(%s): %v", taskID, err)
	}
}

// logTaskRow 打印库中该 task 的主要字段（需 go test -v 才可见）
func logTaskRow(t *testing.T, repo *taskrepo.Repo, taskID string) {
	t.Helper()
	if repo == nil {
		return
	}
	row, err := repo.Get(taskID)
	if err != nil {
		t.Logf("e2e db Get(%q): %v", taskID, err)
		return
	}
	t.Logf("e2e db task: task_id=%s status=%s operation=%s provider=%s image=%q task_class=%s res_cpu=%q res_memory=%q res_gpu=%q runtime_ref=%q target_task_id=%q",
		row.TaskID, row.Status, row.Operation, row.Provider, row.Image, row.TaskClass,
		row.ResCPU, row.ResMemory, row.ResGPU, row.RuntimeRef, row.TargetTaskID)
	t.Logf("e2e db business: %s", string(row.Business))
}

// logRecentTasks 打印最近若干条 tasks 摘要，便于对照库内全貌
func logRecentTasks(t *testing.T, repo *taskrepo.Repo, limit int) {
	t.Helper()
	if repo == nil || limit <= 0 {
		return
	}
	items, total, err := repo.ListTasks(taskrepo.ListFilter{Limit: limit, Offset: 0})
	if err != nil {
		t.Logf("e2e db ListTasks: %v", err)
		return
	}
	t.Logf("e2e db: tasks total=%d (showing up to %d)", total, limit)
	for i := range items {
		x := &items[i]
		t.Logf("  [%d] task_id=%s status=%s op=%s provider=%s image=%q res_gpu=%q runtime_ref=%q",
			i, x.TaskID, x.Status, x.Operation, x.Provider, x.Image, x.ResGPU, x.RuntimeRef)
	}
}

// logDockerStartParamsPreview 打印与「即将 ContainerCreate」一致的参数快照（含 docker.mounts，仅来自 e2e 所加载配置）。
// SkipPull 时与 Run 的组参一致、仅不 pull；见 internal/provider/docker/startparams_test.go
func logDockerStartParamsPreview(t *testing.T, cfg *config.App, task *model.Task) {
	t.Helper()
	if cfg == nil || task == nil {
		return
	}
	if task.Provider != "docker" {
		return
	}
	client, err := docker.New(&cfg.Docker)
	if err != nil {
		t.Logf("e2e docker start params preview: client: %v", err)
		return
	}
	snap, err := client.PreviewStartParams(context.Background(), task, docker.PreviewStartParamsOptions{SkipPull: true})
	if err != nil {
		t.Logf("e2e docker start params preview: %v", err)
		return
	}
	b, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		t.Logf("e2e docker start params preview: json: %v", err)
		return
	}
	t.Logf("e2e docker start params (PreviewStartParams, SkipPull; 与 create 前一致、未 start):\n%s", string(b))
}

func TestE2EHealthz(t *testing.T) {
	base, _, _, _, _, cleanup := newHarness(t)
	defer cleanup()
	res, err := http.Get(base + api.RoutePrefixV1 + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
}

func TestE2EPostTasksReturnsServerTaskID(t *testing.T) {
	base, _, repo, cfg, _, cleanup := newHarness(t)
	defer cleanup()
	body := `{
		"operation": "start",
		"task_class": "fast",
		"provider": "docker",
		"image": "alpine:3.20",
		"res_cpu": "1",
		"res_memory": "64M",
		"business": {
			"type": "config",
			"config_mode": "app",
			"job_id": "job-100",
			"user_id": "user-9"
		}
	}`
	res, err := http.Post(base+api.RoutePrefixV1+"/tasks", "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("status %d: %s", res.StatusCode, b)
	}
	var out struct {
		OK     bool   `json:"ok"`
		TaskID string `json:"task_id"`
		Status string `json:"status"`
		Data   struct {
			TaskID   string          `json:"task_id"`
			Business json.RawMessage `json:"business"`
		} `json:"data"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out.TaskID == "" {
		t.Fatal("empty task_id in response")
	}
	if out.Data.TaskID != out.TaskID {
		t.Fatalf("data.task_id %q != task_id %q", out.Data.TaskID, out.TaskID)
	}
	if out.Status != model.TaskStatusPending {
		t.Fatalf("want pending, got %q", out.Status)
	}
	// 自定义字段原样在 business
	var biz map[string]any
	_ = json.Unmarshal(out.Data.Business, &biz)
	if biz["job_id"] != "job-100" || biz["user_id"] != "user-9" {
		t.Fatalf("business roundtrip: %+v", biz)
	}
	logTaskRow(t, repo, out.TaskID)
	if row, err := repo.Get(out.TaskID); err == nil {
		logDockerStartParamsPreview(t, cfg, row)
	}
}

func TestE2EWorkerPipelineReachesRunning(t *testing.T) {
	base, pl, repo, cfg, dck, cleanup := newHarness(t)
	defer cleanup()
	if !e2eUseStubDocker() {
		if dck == nil {
			t.Fatal("expected non-nil docker client in real mode")
		}
		pctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, perr := dck.Engine().Ping(pctx)
		cancel()
		if perr != nil {
			t.Skipf("docker engine 不可达: %v — 可设置 %s=stub 无 Docker 跑桩路径", perr, e2eEnvDocker)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go worker.Run(ctx, cfg, pl, repo)
	payload := `{
		"operation": "start",
		"task_class": "fast",
		"provider": "docker",
		"image": "alpine:3.20",
		"res_cpu": "0.1",
		"res_memory": "32M",
		"business": {"type": "config", "config_mode": "app"}
	}`
	res, err := http.Post(base+api.RoutePrefixV1+"/tasks", "application/json", bytes.NewBufferString(payload))
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("status %d: %s", res.StatusCode, b)
	}
	var enq struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal(b, &enq); err != nil || enq.TaskID == "" {
		t.Fatalf("parse: %v task_id=%q", err, enq.TaskID)
	}
	defer e2eStopTaskByRef(t, dck, repo, enq.TaskID)
	deadline := time.Now().Add(5 * time.Second)
	if !e2eUseStubDocker() {
		// 首次拉取 alpine 等可能较慢
		deadline = time.Now().Add(2 * time.Minute)
	}
	for time.Now().Before(deadline) {
		gr, err := http.Get(base + api.RoutePrefixV1 + "/tasks/" + enq.TaskID)
		if err != nil {
			t.Fatal(err)
		}
		gb, _ := io.ReadAll(gr.Body)
		_ = gr.Body.Close()
		if gr.StatusCode != http.StatusOK {
			t.Fatalf("get %d: %s", gr.StatusCode, gb)
		}
		var wrap struct {
			Data struct {
				Status     string `json:"status"`
				RuntimeRef string `json:"runtime_ref"`
			} `json:"data"`
		}
		_ = json.Unmarshal(gb, &wrap)
		if wrap.Data.Status == model.TaskStatusRunning && wrap.Data.RuntimeRef != "" {
			if e2eUseStubDocker() {
				if want := "e2e-stub-" + enq.TaskID; wrap.Data.RuntimeRef != want {
					t.Fatalf("runtime_ref %q, want %q", wrap.Data.RuntimeRef, want)
				}
			} else {
				if !strings.HasPrefix(wrap.Data.RuntimeRef, "e2e-stub-") {
					t.Logf("e2e: real container runtime_ref=%q", wrap.Data.RuntimeRef)
				} else {
					t.Fatalf("expected real engine ref, got stub pattern %q", wrap.Data.RuntimeRef)
				}
			}
			logTaskRow(t, repo, enq.TaskID)
			if row, err := repo.Get(enq.TaskID); err == nil {
				logDockerStartParamsPreview(t, cfg, row)
			}
			logRecentTasks(t, repo, 10)
			return
		}
		if wrap.Data.Status == model.TaskStatusFailed {
			row, _ := repo.Get(enq.TaskID)
			t.Fatalf("task %s: status failed (docker Run 未成功) row=%+v", enq.TaskID, row)
		}
		time.Sleep(80 * time.Millisecond)
	}
	if e2eUseStubDocker() {
		t.Fatal("timeout waiting for running + runtime_ref (stub docker)")
	}
	t.Fatal("timeout waiting for running + runtime_ref (真实 Docker：确认可拉 alpine:3.20、ResourceCheck 通过；无引擎则设 " + e2eEnvDocker + "=stub）")
}

// TestE2EResGPURoundTrip 校验 res_gpu 入库并在 GET 中原样返回（不经过 worker/Run 时与 GPU 本机能力无关）
func TestE2EResGPURoundTrip(t *testing.T) {
	base, _, repo, cfg, _, cleanup := newHarness(t)
	defer cleanup()
	body := `{
		"operation": "start",
		"task_class": "fast",
		"provider": "docker",
		"image": "alpine:3.20",
		"res_cpu": "1",
		"res_memory": "128M",
		"res_gpu": "true",
		"business": {"type": "config", "config_mode": "app"}
	}`
	res, err := http.Post(base+api.RoutePrefixV1+"/tasks", "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("status %d: %s", res.StatusCode, b)
	}
	var enq struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal(b, &enq); err != nil || enq.TaskID == "" {
		t.Fatalf("task_id: %v", err)
	}
	gr, err := http.Get(base + api.RoutePrefixV1 + "/tasks/" + enq.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	gb, _ := io.ReadAll(gr.Body)
	_ = gr.Body.Close()
	var wrap struct {
		Data struct {
			ResGPU string `json:"res_gpu"`
		} `json:"data"`
	}
	if err := json.Unmarshal(gb, &wrap); err != nil {
		t.Fatal(err)
	}
	if wrap.Data.ResGPU != "true" {
		t.Fatalf("res_gpu: want true, got %q", wrap.Data.ResGPU)
	}
	logTaskRow(t, repo, enq.TaskID)
	if row, err := repo.Get(enq.TaskID); err == nil {
		logDockerStartParamsPreview(t, cfg, row)
	}
	logRecentTasks(t, repo, 10)
}
