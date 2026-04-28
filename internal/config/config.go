package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
)

// App 全局配置，对应 YAML 根节点。
type App struct {
	Server     Server     `mapstructure:"server" yaml:"server"`
	Database   Database   `mapstructure:"database" yaml:"database"`
	Scheduler  Scheduler  `mapstructure:"scheduler" yaml:"scheduler"`
	Controller Controller `mapstructure:"controller" yaml:"controller"`
	Callback   Callback   `mapstructure:"callback" yaml:"callback"`
	Worker     Worker     `mapstructure:"worker" yaml:"worker"`
	Reconciler Reconciler `mapstructure:"reconciler" yaml:"reconciler"`
	// ClickHouse 可选：调度/容器日志与资源采样落库（默认关闭）
	ClickHouse ClickHouse `mapstructure:"clickhouse" yaml:"clickhouse"`
	Telemetry  Telemetry  `mapstructure:"telemetry" yaml:"telemetry"`
	// Docker 专用于 docker provider：默认镜像、全局挂载、S3 兼容 OSS 与 business 整段 JSON 注入的默认 env 名
	Docker Docker `mapstructure:"docker" yaml:"docker"`
}

// ClickHouse 原生协议（默认 9000）；http 8123 请用 clickhouse-go 的 HTTP 模式或前面加代理，此处走默认 Open。
type ClickHouse struct {
	Enable   bool   `mapstructure:"enable" yaml:"enable"`
	Address  string `mapstructure:"address" yaml:"address"`
	Database string `mapstructure:"database" yaml:"database"`
	User     string `mapstructure:"user" yaml:"user"`
	Password string `mapstructure:"password" yaml:"password"`
	TLS      bool   `mapstructure:"tls" yaml:"tls"`
}

// Telemetry slog 与 worker 侧 Docker 采集间隔
type Telemetry struct {
	// SlogToClickHouse 为 true 且 clickhouse.enable 时，将 slog 批量写入 scheduler_logs
	SlogToClickHouse bool `mapstructure:"slog_to_clickhouse" yaml:"slog_to_clickhouse"`
	// DockerStatsIntervalSeconds worker 采容器 CPU/内存 间隔；0 关闭
	DockerStatsIntervalSeconds int `mapstructure:"docker_stats_interval_seconds" yaml:"docker_stats_interval_seconds"`
	// DockerLogIntervalSeconds 拉容器日志间隔；0 关闭
	DockerLogIntervalSeconds int `mapstructure:"docker_log_interval_seconds" yaml:"docker_log_interval_seconds"`
	// DockerLogTailLines 首次拉取每容器最近 N 行（之后按时间增量）
	DockerLogTailLines int `mapstructure:"docker_log_tail_lines" yaml:"docker_log_tail_lines"`
	LogBatchSize       int `mapstructure:"log_batch_size" yaml:"log_batch_size"`
	LogBatchFlushMS    int `mapstructure:"log_batch_flush_ms" yaml:"log_batch_flush_ms"`
}

type Server struct {
	Addr     string `mapstructure:"addr" yaml:"addr"`
	LogLevel string `mapstructure:"log_level" yaml:"log_level"`
}

type Database struct {
	DSN        string `mapstructure:"dsn" yaml:"dsn"`
	LogQueries bool   `mapstructure:"log_queries" yaml:"log_queries"`
}

// Scheduler 快慢、总并发；排队在 DB 中不设上限
type Scheduler struct {
	MaxConcurrentRunning int `mapstructure:"max_concurrent_running" yaml:"max_concurrent_running"`
	MaxConcurrentSlow    int `mapstructure:"max_concurrent_slow" yaml:"max_concurrent_slow"`
	MinFreeSlotsForFast  int `mapstructure:"min_free_slots_for_fast" yaml:"min_free_slots_for_fast"`
}

// Controller 仅接 HTTP，校验后入 pending；任务由独立 worker 消费
type Controller struct {
	HTTP HTTPCtrl `mapstructure:"http" yaml:"http"`
}

type HTTPCtrl struct {
	Enable bool   `mapstructure:"enable" yaml:"enable"`
	Addr   string `mapstructure:"addr" yaml:"addr"`
}

// Callback 调度侧（worker/对账等）向业务 App 推状态
type Callback struct {
	Enable         bool              `mapstructure:"enable" yaml:"enable"`
	URL            string            `mapstructure:"url" yaml:"url"`
	Method         string            `mapstructure:"method" yaml:"method"`
	TimeoutSeconds int               `mapstructure:"timeout_seconds" yaml:"timeout_seconds"`
	Headers        map[string]string `mapstructure:"headers" yaml:"headers"`
	// Events 空表示全部；否则仅推送列出的
	Events []string `mapstructure:"events" yaml:"events"`
}

// Worker 从 DB 拉取 pending 的进程
type Worker struct {
	// PollMS 无任务时休眠
	PollMS int `mapstructure:"poll_ms" yaml:"poll_ms"`
	// AdmittedTimeoutSeconds admitted 后长期未进 running 则失败回收；0 关闭
	AdmittedTimeoutSeconds int `mapstructure:"admitted_timeout_seconds" yaml:"admitted_timeout_seconds"`
	// AllowedProviders 非空时只抢占这些 provider 的 pending（多 worker 分片）；空=全部
	AllowedProviders []string `mapstructure:"allowed_providers" yaml:"allowed_providers"`
	// DefaultMaxRuntimeSeconds 任务 max_runtime_seconds 为 0 时的默认运行上限（自 running 起算）；0 表示默认不限（仅任务侧显式正数仍限时）。典型 3600。
	DefaultMaxRuntimeSeconds int `mapstructure:"default_max_runtime_seconds" yaml:"default_max_runtime_seconds"`
	// RuntimeSweeperIntervalSeconds 检查 running 超时并强杀的周期（秒）；≤0 时 ApplyDefaults 补默认 300。
	RuntimeSweeperIntervalSeconds int `mapstructure:"runtime_sweeper_interval_seconds" yaml:"runtime_sweeper_interval_seconds"`
	// DisableRuntimeSweeper 为 true 时不启动运行超时扫描
	DisableRuntimeSweeper bool `mapstructure:"disable_runtime_sweeper" yaml:"disable_runtime_sweeper"`
}

// Reconciler 对账
type Reconciler struct {
	Enable          bool `mapstructure:"enable" yaml:"enable"`
	IntervalSeconds int  `mapstructure:"interval_seconds" yaml:"interval_seconds"`
	// OrphanReapEnable 为 true 时，扫描「库中已是终态但 runtime_ref 仍非空」的 docker 任务，与 Provider.Status 对账
	// 若工作负荷仍在跑，经 OrphanReapWaitsS 多次间隔后仍 running 则 Stop 并清空 runtime_ref（不重复发 callback）
	OrphanReapEnable bool  `mapstructure:"orphan_reap_enable" yaml:"orphan_reap_enable"`
	OrphanReapWaitsS []int `mapstructure:"orphan_reap_waits_s" yaml:"orphan_reap_waits_s"`
}

// Docker 仅被 docker provider 读取；[[mounts]] 为调度侧固定；config_file_container_path 为每任务在 business 未写 config_container_path 时的回退
type Docker struct {
	DataDir                 string              `mapstructure:"data_dir" yaml:"data_dir"`
	ConfigFileContainerPath string              `mapstructure:"config_file_container_path" yaml:"config_file_container_path"`
	ImagePull               DockerImagePull     `mapstructure:"image_pull" yaml:"image_pull"`
	HostResources           DockerHostResources `mapstructure:"host_resources" yaml:"host_resources"`
	Mounts                  []DockerMount       `mapstructure:"mounts" yaml:"mounts"`
	OSS                     DockerOSS           `mapstructure:"oss" yaml:"oss"`
}

// DockerHostResources 本机上限；GPU 槽位在 pipeline 与 HostGPUSlotCapacity 中按 gpu_ids 重复次数统计。全空则不做对应校验
type DockerHostResources struct {
	MaxCPU    string `mapstructure:"max_cpu" yaml:"max_cpu"`
	MaxMemory string `mapstructure:"max_memory" yaml:"max_memory"`
	// GPUIDs 每个元素是一槽；同一 device id 写 n 次表示该卡上至多 n 路并发「需要 GPU」的任务。
	// 任务开启 GPU 时，容器 DeviceRequests 使用本列表按首次出现顺序去重后的 device id（每个 id 在容器里挂一次；槽位仍按多重集计数）。
	GPUIDs []string `mapstructure:"gpu_ids" yaml:"gpu_ids"`
}

// DockerImagePull 拉取镜像时的仓库鉴权；enable=false 时不向 ImagePull 传 RegistryAuth
type DockerImagePull struct {
	Enable   bool   `mapstructure:"enable" yaml:"enable"`
	Username string `mapstructure:"username" yaml:"username"`
	// Token 作为 registry 的 password 传入（与 docker login 的 token 一致）
	Token string `mapstructure:"token" yaml:"token"`
}

// DockerMount 静态 bind 挂载；Name 作运维标识，不传给 Docker
type DockerMount struct {
	Name      string `mapstructure:"name" yaml:"name"`
	HostPath  string `mapstructure:"host_path" yaml:"host_path"`
	MountPath string `mapstructure:"mount_path" yaml:"mount_path"`
}

// DockerOSS S3 兼容（阿里云 OSS、MinIO 等）；仅 business.config_mode=worker 拉取配置时用到
type DockerOSS struct {
	Enable    bool   `mapstructure:"enable" yaml:"enable"`
	Endpoint  string `mapstructure:"endpoint" yaml:"endpoint"`
	Region    string `mapstructure:"region" yaml:"region"`
	Bucket    string `mapstructure:"bucket" yaml:"bucket"`
	AccessKey string `mapstructure:"access_key" yaml:"access_key"`
	SecretKey string `mapstructure:"secret_key" yaml:"secret_key"`
	UseSSL    bool   `mapstructure:"use_ssl" yaml:"use_ssl"`
}

// Load 仅接受 YAML（.yaml / .yml）；其它扩展名直接报错以避免 TOML / JSON 等回退路径。
func Load(path string) (*App, error) {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))
	if ext != "yaml" && ext != "yml" && ext != "" {
		return nil, fmt.Errorf("config: only YAML is supported (.yaml/.yml), got %q", ext)
	}
	v := viper.New()
	v.SetConfigFile(path)
	v.SetConfigType("yaml")
	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var c App
	if err := v.Unmarshal(&c); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}
	return &c, nil
}

func Default() *App {
	return &App{
		Server:   Server{Addr: ":8080", LogLevel: "info"},
		Database: Database{DSN: "postgres://postgres:postgres@127.0.0.1:5438/mp_sched?sslmode=disable", LogQueries: false},
		Scheduler: Scheduler{
			MaxConcurrentRunning: 20,
			MaxConcurrentSlow:    3,
			MinFreeSlotsForFast:  4,
		},
		Controller: Controller{HTTP: HTTPCtrl{Enable: false, Addr: ""}},
		Callback:   Callback{Enable: false, Method: "POST", TimeoutSeconds: 10, Headers: nil},
		Worker:     Worker{PollMS: 200, AdmittedTimeoutSeconds: 0, DefaultMaxRuntimeSeconds: 3600, RuntimeSweeperIntervalSeconds: 300},
		Reconciler: Reconciler{Enable: false, IntervalSeconds: 10, OrphanReapWaitsS: []int{5, 15, 30}},
		ClickHouse: ClickHouse{
			Enable: false, Address: "127.0.0.1:9000", Database: "default",
			User: DefaultClickHouseUser, Password: DefaultClickHousePassword,
		},
		Telemetry: Telemetry{
			SlogToClickHouse:           false,
			DockerStatsIntervalSeconds: 0,
			DockerLogIntervalSeconds:   0,
			DockerLogTailLines:         500,
			LogBatchSize:               200,
			LogBatchFlushMS:            2000,
		},
		Docker: Docker{Mounts: nil},
	}
}

func (a *App) ApplyDefaults() {
	if a == nil {
		return
	}
	d := Default()
	if a.Server.Addr == "" {
		a.Server.Addr = d.Server.Addr
	}
	if a.Server.LogLevel == "" {
		a.Server.LogLevel = d.Server.LogLevel
	}
	if a.Database.DSN == "" {
		a.Database = d.Database
	}
	if a.Scheduler.MaxConcurrentRunning <= 0 {
		a.Scheduler.MaxConcurrentRunning = d.Scheduler.MaxConcurrentRunning
	}
	if a.Scheduler.MaxConcurrentSlow < 0 {
		a.Scheduler.MaxConcurrentSlow = d.Scheduler.MaxConcurrentSlow
	}
	if a.Scheduler.MinFreeSlotsForFast < 0 {
		a.Scheduler.MinFreeSlotsForFast = d.Scheduler.MinFreeSlotsForFast
	}
	if a.Reconciler.IntervalSeconds <= 0 {
		a.Reconciler.IntervalSeconds = d.Reconciler.IntervalSeconds
	}
	if len(a.Reconciler.OrphanReapWaitsS) == 0 {
		a.Reconciler.OrphanReapWaitsS = d.Reconciler.OrphanReapWaitsS
	}
	if a.Worker.PollMS <= 0 {
		a.Worker.PollMS = d.Worker.PollMS
	}
	if a.Worker.AdmittedTimeoutSeconds < 0 {
		a.Worker.AdmittedTimeoutSeconds = d.Worker.AdmittedTimeoutSeconds
	}
	if !a.Worker.DisableRuntimeSweeper && a.Worker.RuntimeSweeperIntervalSeconds <= 0 {
		a.Worker.RuntimeSweeperIntervalSeconds = d.Worker.RuntimeSweeperIntervalSeconds
	}
	if a.Callback.Method == "" {
		a.Callback.Method = d.Callback.Method
	}
	if a.Callback.TimeoutSeconds <= 0 {
		a.Callback.TimeoutSeconds = d.Callback.TimeoutSeconds
	}
	if a.Docker.DataDir == "" {
		a.Docker.DataDir = filepath.Join(os.TempDir(), "mp_sched-docker")
	}
	if strings.TrimSpace(a.Docker.ConfigFileContainerPath) == "" {
		a.Docker.ConfigFileContainerPath = "/etc/mp_sched/config.json"
	}
	if a.ClickHouse.Address == "" {
		a.ClickHouse.Address = d.ClickHouse.Address
	}
	if strings.TrimSpace(a.ClickHouse.Database) == "" {
		a.ClickHouse.Database = d.ClickHouse.Database
	}
	if strings.TrimSpace(a.ClickHouse.User) == "" {
		a.ClickHouse.User = d.ClickHouse.User
	}
	// 与 docker-compose 开发实例一致；生产请在配置中显式填写
	if strings.TrimSpace(a.ClickHouse.Password) == "" {
		a.ClickHouse.Password = d.ClickHouse.Password
	}
	if a.Telemetry.DockerLogTailLines <= 0 {
		a.Telemetry.DockerLogTailLines = d.Telemetry.DockerLogTailLines
	}
	if a.Telemetry.LogBatchSize <= 0 {
		a.Telemetry.LogBatchSize = d.Telemetry.LogBatchSize
	}
	if a.Telemetry.LogBatchFlushMS <= 0 {
		a.Telemetry.LogBatchFlushMS = d.Telemetry.LogBatchFlushMS
	}
}
