# mp_sched

基于 PostgreSQL 的轻量任务调度器：HTTP **controller** 只负责入队，**worker** 从库中抢占并执行（Docker 或 K8s 等 `Provider`）。两个进程共享同一份数据库与配置，进程间无消息队列、无内存共享，状态通过 `tasks` 行字段流转。

## 双进程模型

| 进程 | 入口 | 主要职责 |
|------|------|----------|
| `mp-controller` | `cmd/mp-controller` | HTTP 入队、查询、便捷 stop / restart |
| `mp-worker` | `cmd/mp-worker` | 抢占 `pending`、调度、执行、回调、对账、运行超时回收 |

排队深度由 `tasks` 表中 `status='pending'` 行数体现，配置层不设队列长度上限。

## 快速开始（本机）

1. 启动依赖（PostgreSQL，可选 ClickHouse），需要本机已装 Docker：

   ```bash
   make up
   ```

2. 终端 A 启动 API 服务（首次会自动 `AutoMigrate` 表结构）：

   ```bash
   make run-controller
   ```

3. 终端 B 启动 Worker（消费 pending、运行超时扫描、对账、回调）：

   ```bash
   make run-worker
   ```

4. 健康检查与演示请求：

   ```bash
   make smoke
   ```

5. 停止本地依赖：

   ```bash
   make down
   ```

`configs/development.yaml` 默认指向 compose 中 PostgreSQL `127.0.0.1:5433/mp_sched`。

## 容器部署

`mp-controller` 与 `mp-worker` 是两个独立二进制；通常做法是构建一个镜像，按 `command` 区分进程，再在编排里跑两个 service：

```bash
make build   # 输出 bin/mp-controller、bin/mp-worker
```

部署要点：

- **同一份配置**：两进程都读 `-config <path>` 指定的 YAML/TOML，整文件反序列化；不需要的段保留为空或注释即可。也可以为两者各挂载一份不同文件（例如 controller 关闭 `[reconciler]` 与 `[telemetry]`，worker 完整启用）。
- **HTTP 端口**：由 `controller.http.addr`（优先）或 `server.addr` 决定，例如 `":8080"`；compose / k8s 里再做端口映射。
- **HTTP 仅在 controller**：worker 不监听 HTTP，但需要能访问 PG / Docker daemon / 业务 callback。
- **无状态扩展**：可起多个 `mp-worker`，通过 `worker.allowed_providers` 做 provider 分片；`mp-controller` 也可水平扩展（无内存状态）。
- **Docker provider**：worker 容器需挂 `/var/run/docker.sock` 才能驱动宿主 Docker；要采集容器 stats / 日志请额外开 `[clickhouse]` 与 `[telemetry]`。

## 项目结构

| 路径 | 说明 |
|------|------|
| `cmd/mp-controller` | HTTP 入队与查询 |
| `cmd/mp-worker` | 拉取、调度、执行、对账、运行超时扫描 |
| `internal/api` | go-chi 路由、HTTP 视图 |
| `internal/controller` | 入参校验、入队、stop / restart 编排 |
| `internal/pipeline` | 状态机与 Provider 调用 |
| `internal/scheduler` | 纯函数 `Admit`（无 I/O） |
| `internal/taskrepo` | GORM Repo、`SKIP LOCKED` 抢占、计数 |
| `internal/provider/{docker,k8s}` | Provider 实现 |
| `internal/reconciler` | 周期 `Status` 对账与孤儿回收 |
| `internal/worker` | worker 主循环、admitted/runtime sweeper |
| `internal/callback` | 事件回调客户端 |
| `internal/telemetry` | ClickHouse 落库（slog、docker stats / logs） |
| `docs/` | 设计、配置、API、OpenAPI |

## 文档索引

- [体系结构与数据流](docs/ARCHITECTURE.md)
- [配置项说明](docs/CONFIG.md)
- [HTTP API](docs/API.md)
- [OpenAPI 3 片段](docs/openapi.yaml)（可导入 Postman / Swagger UI）
- 配置示例：[configs/config.example.yaml](configs/config.example.yaml)、[configs/development.yaml](configs/development.yaml)

## 构建与测试

```bash
go test ./...
make build
make e2e        # Postgres-only e2e
make e2e-full   # Postgres + ClickHouse 探针
```
