# 配置说明

`mp-controller` 与 `mp-worker` 共用一种格式：**YAML（.yaml / .yml）**。其它扩展名（如 `.toml`、`.json`）会被 `config.Load` 直接拒绝，避免静默回退到非预期格式。

```bash
go run ./cmd/mp-controller -config configs/development.yaml
go run ./cmd/mp-worker     -config configs/development.yaml
```

不带 `-config` 时，两个二进制都默认读取仓库内 **`configs/default.yaml`**（`cmd/mp-*/main.go` 的 `-config` 缺省值）。**`make run-controller` / `make run-worker`** 显式传入 **`configs/development.yaml`**，与 e2e 默认 `E2E_CONFIG` 一致，便于 `make up` 后直接联调。

两个进程整文件反序列化为同一份 `App` 结构（`internal/config/config.go`），未涉及的段保留默认或注释；也可以为两进程分别挂载不同文件（例如 controller 关闭 `reconciler` 与 `telemetry`，worker 启用）。`ApplyDefaults` 会补全部分缺省，未写明字段为类型零值，**注意数字 `0` 的语义**（见下表）。

## 1. 根字段一览

| 节 | 进程 | 用途 |
|----|------|------|
| `server` | 共用 | 默认监听地址、日志等级；`controller.http.addr` 为空时回退使用 `server.addr` |
| `database` | 共用 | DSN、是否打印 SQL |
| `scheduler` | 共用 | 并发槽与快慢任务策略 |
| `controller` | controller | HTTP 是否开启、监听地址 |
| `callback` | worker（reconciler、sweeper、pipeline） | 向业务方推送状态 |
| `worker` | worker | 抢占轮询、admitted 卡死回收、运行超时扫描、provider 分片 |
| `reconciler` | worker | 周期 `Provider.Status` 对账与孤儿回收 |
| `docker` | worker（可选 controller） | docker provider 行为：挂载、镜像拉取、OSS、本机 `host_resources` |
| `clickhouse` | 共用 | 可选遥测落库；关闭时 controller / worker 都无依赖 |
| `telemetry` | 共用 | slog 落库、Docker stats / 日志拉取（仅 worker 触发） |

## 2. server

| 键 | 含义 |
|----|------|
| `addr` | 默认 HTTP 监听地址，例如 `:8080`；当 `controller.http.addr` 为空时被回退使用 |
| `log_level` | slog 等级：`debug` / `info` / `warn` / `error` |

## 3. database

| 键 | 含义 |
|----|------|
| `dsn` | PostgreSQL 连接串；为空时 `ApplyDefaults` 写入开发默认 |
| `log_queries` | true 时打印 GORM SQL（调试用） |

## 4. scheduler

| 键 | 含义 |
|----|------|
| `max_concurrent_running` | `admitted + running` 的 start 任务总数上限 |
| `max_concurrent_slow` | 其中 `task_class=slow` 的上限 |
| `min_free_slots_for_fast` | 接慢任务后剩余空槽下限，为快任务保留余量 |

## 5. controller.http

| 键 | 含义 |
|----|------|
| `enable` | 是否启动 HTTP 服务 |
| `addr` | 监听地址，例如 `:8080`；可空，空则回退到 `server.addr` |

**路由**：进程内挂载在 **`/api/sched/v1`** 下（健康检查、任务与遥测 HTTP 皆为此前缀；代码常量 `internal/api.RoutePrefixV1`）。

部署提示：容器化时通常配置 `controller.http.addr: ":8080"`，再由编排做端口映射。

## 6. callback

| 键 | 含义 |
|----|------|
| `enable` + `url` | 都成立才会 POST |
| `method` | 默认 `POST` |
| `timeout_seconds` | 客户端超时；`<=0` 时 `ApplyDefaults` 补默认 |
| `headers` | 额外头（鉴权等） |
| `events` | 非空时仅推白名单事件；事件名见下 |

事件名（`internal/callback/client.go`）：

`pending`、`processing`、`admitted`、`running`、`succeeded`、`failed`、`stopped`、`timeout`。

`timeout` 是运行超时扫描强杀语义，独立于 `failed`，需要业务区分时请把它显式加进 `events`。

### callback.auth (protocol v2)

`callback.auth` is opt-in. Protocol v2 is active only when both `key_id` and
`hmac_secret` are non-empty. `hmac_secret` is signing material: supply it only
through protected deployment configuration; never commit, print, or put it in
static callback headers. An absent or partial `auth` block preserves the legacy
callback body and headers.

| Key | Meaning |
|-----|---------|
| `key_id` | Public verifier key identifier emitted in the v2 metadata. |
| `hmac_secret` | In-process HMAC-SHA-256 key; never echoed by mp_sched. |
| `artifact_root` | Optional server-owned root for controlled `sequence_bundle/v1` success binding. |

V2 writes scheduler-owned `X-MP-Sched-*` headers after static `headers`, so a
static value cannot override signed metadata. It signs a length-delimited tuple
of key id, delivery id, occurred-at timestamp, and SHA-256 of the exact JSON
body. For controlled `succeeded`, the collector emits raw-byte
`sha256:<hex>` for exactly
`<artifact_root>/<run_id>/output/sequence_bundle.json`; missing or unsafe
output yields only `artifact_binding_error: unavailable`. Non-success terminal
events never claim a success digest.

## 7. worker

| 键 | 含义 | 缺省 |
|----|------|------|
| `poll_ms` | 无 `pending` 时轮询休眠（毫秒） | 200 |
| `not_admitted_retry_ms` | 准入失败任务再次参与扫描的最短间隔；冷却期间继续回填后续 pending 任务 | 2000 |
| `admitted_timeout_seconds` | 大于 0 时把长期 `admitted` 且 `updated_at` 太旧的标 `failed`；0 = 关闭 | 0 |
| `allowed_providers` | 非空如 `["docker"]` 时只抢占对应 provider 的 pending（多 worker 分片） | 空 |
| `default_max_runtime_seconds` | 任务 `max_runtime_seconds=0` 时使用的运行时长上限（秒，自进入 `running` 起算）；0 表示默认不限时（任务仍可显式 `>0` 限时或 `-1` 跳过） | 3600 |
| `runtime_sweeper_interval_seconds` | 运行超时扫描周期；`<=0` 且未禁用时 `ApplyDefaults` 补 300 | 300 |
| `disable_runtime_sweeper` | true 时不启动运行超时协程 | false |

注意：Docker Engine 没有「容器最长存活时间」原生开关，必须由调度侧实现这条 sweeper。

## 8. reconciler

| 键 | 含义 |
|----|------|
| `enable` | 周期对 `admitted` / `running` 任务调 `Status`，把容器终态写回库 |
| `interval_seconds` | 上述对账与下方孤儿列表扫描共用的周期 |
| `orphan_reap_enable` | 扫描库已终态、`runtime_ref` 仍非空的 docker 任务；首次 `Status` 仍 running 时按 `orphan_reap_waits_s` 多次再观测；最终仍 running 则 `Stop` 并清空 `runtime_ref`（不改 `status`、不重复 callback） |
| `orphan_reap_waits_s` | 三次再观测的间隔（秒），缺省 `[5, 15, 30]` |

## 9. docker

仅 docker provider 读取；`mp-worker` 必须有正确的 docker 段才能调度起容器。`mp-controller` 也会建立 docker 客户端用于校验 provider 已注册，但启动时不 ping 引擎，没装 Docker 也能跑 controller。

| 键 | 含义 |
|----|------|
| `data_dir` | 调度器在宿主机的工作目录（如 worker 暂存 OSS 配置）；空则 `ApplyDefaults` 落到 `os.TempDir()/mp_sched-docker` |
| `config_file_container_path` | 当 `business.config_container_path` 未填、且 `config_mode=worker` 时，容器内 bind 配置文件的默认路径，缺省 `/etc/mp_sched/config.json` |

### 9.1 `docker.image_pull`

| 键 | 含义 |
|----|------|
| `enable` | true 时 `ImagePull` 会带 `RegistryAuth`；false 时不带鉴权 |
| `username` | registry 用户名 |
| `token` | 作 registry 密码（与 `docker login` 的 token 一致） |

### 9.2 `docker.gpu_admission`

机会式模式不为单个任务分配固定显存，也不把容器 GPU 挂载、`admitted/running` 状态或历史单卡分配当成显存占用。启动条件仅为 `memory.free >= min_start_free_memory_mb`；默认即真实空闲显存至少 20480 MiB。

| 键 | 含义 | 缺省 |
|----|------|------|
| `enable` | 开启 nvidia-smi 真实显存准入；查询失败时保持 pending | false（示例配置开启） |
| `min_start_free_memory_mb` | 新任务准入所需的实时空闲显存 | 20480 |
| `reserve_memory_mb` | 兼容旧 YAML；free-memory-only 模式忽略 | 0 |
| `launch_guard_seconds` | 兼容旧 YAML；free-memory-only 模式忽略 | 0 |
| `query_timeout_seconds` | nvidia-smi 查询超时 | 3 |

开启后，`host_resources.gpu_ids` 表示允许调度的 GPU 集合，重复项去重；关闭后保留原多重集槽位语义。完整设计见 [OPPORTUNISTIC_GPU_SCHEDULING.md](OPPORTUNISTIC_GPU_SCHEDULING.md)。

### 9.3 `docker.host_resources`

CPU / 内存通过 `Provider.ResourceCheck` 比对；GPU 槽位在 `executeStart` **事务内**由 `docker.CheckDockerGPUOccupancyWithCandidate` 比对（已 `admitted|running` 的 docker start 任务 **加上** 当前待 admit 任务）。超槽位时返回 `ErrNotAdmitted`，任务保持/退回 `pending`，与全局并发满一致。

| 键 | 含义 |
|----|------|
| `max_cpu` | 字符串如 `"32"` 或 `"32000m"`；任务 `res_cpu` 不得超过本值。空 = 不校验 |
| `max_memory` | 字符串；任务 `res_memory` 不得超过本值。空 = 不校验。支持与 `github.com/docker/go-units` / `RAMInBytes` 相同的写法（如 `512GiB`、`512gb`、`512g`），并**兼容 Kubernetes 二进制写法** `Mi` / `Gi` / `Ti`（无尾字母 `B`） |
| `gpu_ids` | 字符串数组；**同一语义两重作用**：① **多重集槽位** — 每项 1 槽，同一 device id 出现 n 次表示该卡上至多 n 路并发「需要 GPU」的任务；② **挂载列表** — 任务 `res_gpu` 为开时，对列表按**首次出现顺序去重**后写入容器 `DeviceRequests`（每个 id 在容器内挂一次）。**不在请求里传 GPU 编号** |

GPU 行为补充：

- 任务字段 `res_gpu`：开 — **`1` / `true` / `yes` / `on`**（不区分大小写）；其余均为关。
- 单任务槽位占用 = 去重后的每个 distinct id **各计 1**（同一任务不会在单一 id 上因 `gpu_ids` 重复而多占槽；`gpu_ids` 的重复只抬高**并发路数**上限）。
- 任一 id 总占用 > 槽位数时：在 admit **事务内**拒绝，**退回 `pending`**，下一轮 `ClaimNext` 可再试（与 `scheduler.Admit` 反压一致）；不会仅因 GPU 满而标 `failed`。
- 旁路读取剩余槽位：`docker.RemainingGPUSlots(hostCap, used)`。

### 9.4 `docker.mounts`

数组，每项三字段，所有任务都会带上：

| 键 | 含义 |
|----|------|
| `name` | 仅运维标识，不传给 Docker |
| `host_path` | 宿主路径，必须存在；否则 `Run` 失败 |
| `mount_path` | 容器内路径 |

### 9.4 `docker.oss`（S3 兼容）

仅 `business.config_mode=worker` 拉配置时使用：

| 键 | 含义 |
|----|------|
| `enable` | 启用 OSS 子模块 |
| `endpoint` | 例如 `oss-cn-hangzhou.aliyuncs.com` |
| `region` | 例如 `cn-hangzhou` |
| `bucket` | bucket 名 |
| `access_key` / `secret_key` | 凭证 |
| `use_ssl` | 默认 true |

## 10. clickhouse

| 键 | 含义 |
|----|------|
| `enable` | 关闭时 controller / worker 都无 ClickHouse 依赖 |
| `address` | native 协议地址，例如 `127.0.0.1:9000` |
| `database` / `user` / `password` | 库与凭证；空字段由 `ApplyDefaults` 用开发默认补全 |
| `tls` | 启用 TLS |

## 11. telemetry

| 键 | 含义 |
|----|------|
| `slog_to_clickhouse` | true 且 `clickhouse.enable` 时把 slog 批量写入 `scheduler_logs` |
| `docker_stats_interval_seconds` | worker 采容器 CPU / 内存 间隔（秒），0 = 关闭 |
| `docker_log_interval_seconds` | worker 拉容器日志间隔（秒），0 = 关闭 |
| `docker_log_tail_lines` | 首次拉取每容器最近 N 行（之后按时间增量），缺省 500 |
| `docker_log_terminal_flush_lines` | 任务终态 / 主动 Stop / 孤儿回收 / 运行超时停容器**之前**再 Tail 补拉日志的最大行数；0=默认 20000，上限约 1e6，**-1=关闭**。若与 `docker_log_interval_seconds` 同时开启，同一行可能被写多次（插入时间 `ts` 不同，表内**不去重**） |
| `log_batch_size` | slog 批写阈值，缺省 200 |
| `log_batch_flush_ms` | slog 强制刷批间隔（毫秒），缺省 2000 |

## 12. 与 docker-compose 对齐

`configs/development.yaml` 默认与 `docker-compose.yaml` 中 PostgreSQL `127.0.0.1:5438/mp_sched`、ClickHouse 默认账号一致，便于 `make up && make run-controller` 直接联调。

## 13. 配置共享与多份配置

- 推荐做法是同一份配置文件给两个进程，未用到的段保留为空或注释。
- 当 controller 与 worker 部署在不同主机时，可分别挂载两份文件，但请保证：
  - `database`、`scheduler` 一致；
  - `docker`、`callback`、`worker`、`reconciler` 子段在 worker 侧完整；
  - `controller.http` 在 controller 侧完整。
