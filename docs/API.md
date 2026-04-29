# HTTP API

Base URL：`http://<host>:<port>`，端口由 `controller.http.addr`（优先）或 `server.addr` 决定，默认 `:8080`。**所有业务接口**统一挂在路径前缀 **`/api/sched/v1`** 下（健康检查亦在此前缀内）。

`Content-Type: application/json`。所有时间字段为 RFC3339 UTC。任务 ID 由本服务在入队时生成（UUID），调用方不得指定。

---

## 1. 健康

### `GET /api/sched/v1/healthz`

200：

```json
{ "ok": true }
```

---

## 2. 任务

### 2.1 `POST /api/sched/v1/tasks`

创建一条 task，初始 `pending`。`task_id` 在响应中返回；后续查询、stop、restart 都用它做主键。

请求体字段：

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `operation` | string | 否 | `start`（默认）或 `stop` |
| `task_class` | string | 否 | `fast` / `slow`，影响调度槽与快慢配额 |
| `provider` | string | 是 | 必须在 Registry 中注册（如 `docker`、`k8s`） |
| `target_task_id` | string | `stop` 时必填 | 指向已存在的 start 任务 |
| `image` | string | docker 时二选一 | 容器镜像；不填则取 `business.image` |
| `res_cpu` | string | 否 | 例如 `"1"`、`"500m"` |
| `res_memory` | string | 否 | 例如 `"512Mi"`、`"2Gi"` |
| `res_gpu` | string | 否 | 是否使用 GPU：仅 `1` / `true` / `yes` / `on`（不区分大小写）为开。device id 仅来自 `docker.host_resources.gpu_ids`（去重后写入容器） |
| `max_runtime_seconds` | int | 否 | 运行时长上限（秒），见下表 |
| `business` | object | 否 | 业务自定义 JSON；缺省 `{}` |

`max_runtime_seconds` 取值：

| 值 | 含义 |
|----|------|
| `0`（或未传） | 使用 worker 的 `default_max_runtime_seconds`（典型 3600），为 0 表示不限时 |
| `>0` | 自进入 `running` 起最多运行 N 秒，超时则 worker 主动 `Stop`、状态 `failed`、推送 `event=timeout` |
| `<0` | 不在调度侧做运行超时（仍受 admitted 卡死回收等其它机制影响） |

Docker Engine 没有「容器最长存活时间」原生开关，运行超时由 worker `runtime_sweeper_interval_seconds` 定时扫描实现。

#### 2.1.1 docker provider 专属规则

- 镜像：`image` 与 `business.image` 至少一个有值。
- 私有仓库拉取：在 `[docker.image_pull]` 配置 `enable=true` 与 `username` / `token`；`enable=false` 时 pull 不带鉴权。
- 路径挂载：通过 `[[docker.mounts]]` 全局配置（`host_path` → `mount_path`），不能由任务传参覆盖。
- 容器 Label：`mp_sched.task_id`、`mp_sched.provider`、`vendor=mova` 等。
- GPU 槽位（多重集语义）：见 `[docker.host_resources].gpu_ids`，每一项 = 一个槽，同一 device id 重复 = 多槽；当前占用（含自身）超过槽位时在 **admit 前**拒绝，任务退回 **`pending`** 待下一轮抢占，不会因此直接 `failed`。详见 [`CONFIG.md`](CONFIG.md#92-dockerhost_resources)。

#### 2.1.2 `business.type` 分支

| `type` | 行为 |
|--------|------|
| `config` 或 `file` 或省略 | 走「配置文件」分支：可选用下面 **OSS 相关** `config_*` 字段，以及 `business.env`（`KEY=VALUE` 字符串数组）等 |
| `env` | 注入一对环境变量：`env_key` + `env_value`；可与 `business.env` 数组并存（先合并后追加） |

**`config_*` 与 OSS（对象存储）**：`config_oss_key`、`config_mode`、`config_container_path` **只在与 S3/OSS 等对象存储中的配置文件配合时使用**：`config_oss_key` 是 bucket 内的对象 key（一任务一 key 的约定）；`config_mode` 表示由 **mp-worker** 从 worker 侧已配置的 `docker.oss` 拉取后 **bind 进容器**（`worker`），还是由 **容器内应用** 自行持凭证从 OSS 拉取（`app`）；`config_container_path` 仅在 `config_mode=worker` 时生效（容器内只读挂载路径，省略则用 `docker.config_file_container_path`）。

**无需 OSS 时**：若配置来自**镜像内文件**、**全局** `[[docker.mounts]]` 挂好的**主机本地路径**，或任务**根本不读外部配置文件**，则 **不要填** `config_oss_key` / `config_mode` / `config_container_path`，在 `business` 里只写 `command`、`entrypoint`、`env`、`workdir` 等即可。

`type=config` 时，**仅在使用 OSS 配置**（在 `business` 内）常用字段如下：

| 字段 | 含义 |
|------|------|
| `config_oss_key` | 该任务对应配置文件在 **OSS** 中的对象 key |
| `config_container_path` | 仅 `config_mode=worker`：容器内只读挂载的绝对路径；省略则用 `docker.config_file_container_path` |
| `config_mode` | `worker`：mp-worker 从 OSS 下载后 bind（需 `docker.oss`）；`app`：容器内自拉 OSS |
| `env` | 字符串数组，作为容器环境变量（与是否用 OSS 无关，可任意搭配） |

`type=env` 时（在 `business` 内）：

| 字段 | 含义 |
|------|------|
| `env_key` | 必填 |
| `env_value` | 可选（可空字符串） |

`business` 也可携带任意应用侧自定义键（如 `job_id`、`user_id`），本服务仅原样存储与回显，不解析这些键。

#### 2.1.3 请求示例

start，`type=config` 且 **从 OSS 取配置**（示例：`config_mode=app`，由容器内自拉 `config_oss_key`）：

```json
{
  "operation": "start",
  "task_class": "fast",
  "provider": "docker",
  "image": "alpine:3.20",
  "res_cpu": "1",
  "res_memory": "512Mi",
  "res_gpu": "true",
  "max_runtime_seconds": 7200,
  "business": {
    "type": "config",
    "config_oss_key": "configs/app.yaml",
    "config_mode": "app",
    "command": ["sleep", "60"]
  }
}
```

start，`type=config` 但 **不用 OSS**（配置在镜像内或已由全局 `mounts` 挂入，仅下指令）：

```json
{
  "operation": "start",
  "provider": "docker",
  "image": "myapp:1.0",
  "task_class": "fast",
  "business": {
    "command": ["./server", "--config", "/etc/myapp/config.yaml"]
  }
}
```

start，`type=env`：

```json
{
  "operation": "start",
  "provider": "docker",
  "image": "alpine:3.20",
  "res_cpu": "0.5",
  "res_memory": "256Mi",
  "business": {
    "type": "env",
    "env_key": "APP_OPTIONS",
    "env_value": "{\"mode\":\"dry-run\"}"
  }
}
```

stop（也可使用下方便捷接口）：

```json
{
  "operation": "stop",
  "provider": "docker",
  "target_task_id": "目标 start 任务的 task_id"
}
```

#### 2.1.4 响应

`201`：

```json
{
  "ok": true,
  "task_id": "uuid",
  "status": "pending",
  "data": { /* TaskView */ }
}
```

`400`：

```json
{ "ok": false, "error": "provider required" }
```

### 2.2 `GET /api/sched/v1/tasks/{taskID}`

`200` 含 `data: TaskView`；`404` 不存在。

### 2.3 `GET /api/sched/v1/tasks`

查询参数：

| 名 | 默认 | 上限 | 说明 |
|----|------|------|------|
| `status` | 空 | - | 精确过滤 |
| `provider` | 空 | - | 精确过滤 |
| `limit` | 50 | 200 | 返回条数 |
| `offset` | 0 | - | 翻页起点 |

`200`：

```json
{
  "ok": true,
  "total": 0,
  "limit": 50,
  "offset": 0,
  "items": [ /* TaskView[] */ ]
}
```

### 2.4 `POST /api/sched/v1/tasks/{taskID}/stop`

对 `taskID` 指向的 start 工作负载入队一条 stop（`provider` 从目标继承）。请求体可空。

`201`：

```json
{
  "ok": true,
  "task_id": "uuid_of_stop_request",
  "status": "pending",
  "target_id": "uuid_of_start_workload",
  "data": { /* TaskView */ }
}
```

`400`：目标不是合法 workload；`404`：目标不存在。

### 2.5 `POST /api/sched/v1/tasks/{taskID}/restart`

对 start 工作负载克隆一条新 start：

| 当前状态 | 行为 |
|----------|------|
| `succeeded` / `failed` / `stopped` | 仅入队新 start |
| `running` / `admitted` | 先入队 stop（`stop_task_id`），再入队新 start（`new_task_id`）；多 worker 时执行顺序不保证，生产建议先手动 stop 再调用本接口或限单 worker |
| `pending` / `processing` | `409` ErrRestartConflict |

`201`：

```json
{
  "ok": true,
  "new_task_id": "uuid_new",
  "data": { /* TaskView */ },
  "stop_task_id": "uuid_stop_optional",
  "stop_data": { /* TaskView, 仅在派生 stop 时存在 */ }
}
```

错误码：`400` 非合法 workload；`404` 不存在；`409` 仍在 pending/processing；`503` 未配置。

---

## 3. TaskView 字段

| 字段 | 类型 | 说明 |
|------|------|------|
| `task_id` | string | 主键，UUID |
| `business` | object | 原始 JSON（不存在时 `{}`） |
| `task_class` | string | `fast` / `slow` 等 |
| `provider` | string | docker / k8s |
| `operation` | string | `start` / `stop` |
| `target_task_id` | string | stop 行才有；指向 start |
| `res_cpu` / `res_memory` / `res_gpu` | string | 任务请求的算力 |
| `max_runtime_seconds` | int | 任务级运行上限（>0 限时；0 用默认；<0 不限） |
| `image` | string | 容器镜像 |
| `status` | string | 见状态表 |
| `running_at` | string | 进入 `running` 的 UTC 时间（RFC3339）；运行超时扫描以此为起点 |
| `runtime_ref` | string | provider 返回的引用（容器 ID 等） |
| `created_at` / `updated_at` | string | RFC3339 UTC |

---

## 4. 状态与事件

任务状态：`pending` → `processing` → `admitted` → `running` → `succeeded` / `failed` / `stopped`。详见 [`ARCHITECTURE.md`](ARCHITECTURE.md#3-任务状态机start-工作负载)。

callback 事件名：

| 事件 | 触发 |
|------|------|
| `pending` | controller 入队 |
| `processing` | worker 抢占 |
| `admitted` | 通过 `Admit` |
| `running` | `Run` 成功 |
| `succeeded` / `failed` / `stopped` | pipeline、reconciler |
| `timeout` | 运行超时扫描强杀，独立于 `failed` |

`callback.events` 非空时仅推白名单内事件；要区分超时与一般失败请显式包含 `timeout`。

---

## 5. ClickHouse 遥测查询

需配置 `[clickhouse].enable=true` 且进程能连上 ClickHouse（默认账号见 `internal/config/clickhouse_defaults.go` 与 `docker-compose`）。首次请求会 `EnsureSchema` 建表。所有接口为 `GET`，`limit` 默认 100、最大 500，失败 `503` / `502`（未启用或连不上）。

### 5.1 `GET /api/sched/v1/telemetry/scheduler-logs`

全量调度 / 服务 slog（表 `scheduler_logs`）。

查询参数：`service`、`level`、`msg`（子串匹配）、`since`（RFC3339）、`limit`。

### 5.2 `GET /api/sched/v1/telemetry/controller-logs`

同 5.1，固定 `service=mp-controller`，便于查 `enqueue` / `stop` / `restart` 等行。`level` / `msg` / `since` / `limit` 仍可用。

### 5.3 `GET /api/sched/v1/telemetry/docker-stats`

表 `docker_container_stats`：`task_id`、`container_id`（均可选）、`limit`。

### 5.4 `GET /api/sched/v1/telemetry/docker-log-lines`

表 `docker_log_lines`。查询参数：

| 参数 | 说明 |
|------|------|
| `task_id` / `container_id` / `stream` | 可选；`stream` 为 `stdout` 或 `stderr` |
| `limit` | 条数，默认 100，最大 500 |
| `offset` | 分页偏移，默认 0，最大 10000 |
| `order` | 默认 **`asc`**（按采集时间正序，早的在前）；`desc` 或 `newest` 为倒序（新的在前） |

**终态补拉与重复行**：启用 `docker_log_terminal_flush_lines`（非 -1）时，worker 在把任务打到终态或停止容器并仍持有 `runtime_ref` 时会再执行一次 Tail 写入。若周期拉日志仍开启，重叠区间的 stderr/stdout 行可能在 `docker_log_lines` 中出现多条内容相同、采集时间不同的记录；查询侧可按 `task_id`+`line` 去重或接受冗余，存储侧未建唯一约束。

**采集侧说明**（与「Tail 不足 500 行就丢吗」）：`docker_log_tail_lines` 的 **Tail 是「最多」行数**，不足 500 会整段拉回；只要在任务仍为 `running` 时至少完成一次成功的 `ContainerLogs` 写入，这些行就会落库。之后容器退出不会把已写入行删掉。**容易丢**的是：从未在 `running` 期间被周期采集扫到（或 `ContainerLogs` 失败），与「第一次是否满 500 行」无关；终态补拉用于缓解「任务太快结束从未被周期扫到」的情况。

---

## 6. 错误体

业务错误统一：

```json
{ "ok": false, "error": "..." }
```

主要状态码：

| Code | 场景 |
|------|------|
| 200 | 查询成功 |
| 201 | 入队成功（含 stop / restart） |
| 400 | 参数错误、不合法 workload、`Validate` 失败 |
| 404 | 目标 task 不存在 |
| 409 | `restart` 时仍在 `pending` / `processing` |
| 502 | ClickHouse 连接 / schema 失败 |
| 503 | ClickHouse 未启用、controller `unconfigured` |

---

## 7. 本地 e2e

```bash
make up         # 启动 PostgreSQL（与可选 ClickHouse）
make e2e        # Postgres-only e2e
make e2e-full   # Postgres + ClickHouse 探针
```

变量：`E2E_DSN` 覆盖 DSN；`E2E_CH_ADDR` 覆盖 CH 地址；`E2E_DOCKER=stub` 不连真实 docker；`E2E_CONFIG` 改用其它配置文件（默认 `configs/development.yaml`）。

机器可读规格见 [`openapi.yaml`](openapi.yaml)。
