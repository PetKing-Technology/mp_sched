# mp_sched 本地开发
.PHONY: up down test build e2e e2e-spec e2e-full help run-controller run-worker smoke clickhouse-up clickhouse-down clickhouse-client ch-logs

help:
	@echo "up              启动 docker-compose 中的依赖（PostgreSQL、ClickHouse）"
	@echo "down            停止并移除 compose 网络与容器"
	@echo "clickhouse-up   仅启动 ClickHouse（原生 :9000，HTTP :8123）"
	@echo "clickhouse-down 停止 ClickHouse 容器"
	@echo "clickhouse-client  进入本机 clickhouse 容器的 client（需已 clickhouse-up）"
	@echo "ch-logs         查 CH 最近数据；例: make ch-logs ARGS='scheduler 100'"
	@echo "test            go test"
	@echo "build           编译到 bin/"
	@echo "run-controller  本机起 API（需先 make up 且表已迁移）"
	@echo "run-worker     本机起 worker + reconciler"
	@echo "smoke          对 :8080 做简单 HTTP 检查（路径 /api/sched/v1；可设 BASE_URL / API_PREFIX）"
	@echo "e2e            加载 configs/development.yaml（E2E_CONFIG 可改）；E2E_DSN 覆盖 dsn；E2E_STRICT_SCHED=1 不放宽并发；E2E_DOCKER=stub 不连引擎"
	@echo "e2e-spec       同上，只跑会打印「docker create 前 spec」的用例；默认走真实 Run（worker 用例需本机 Docker）"
	@echo "e2e-full       Postgres + 本机 CH 探针（E2E_CH_ADDR 默认 127.0.0.1:9000；账号见 config/clickhouse_defaults）"

up:
	docker compose up -d

down:
	docker compose down

clickhouse-up:
	docker compose up -d clickhouse

clickhouse-down:
	docker compose stop clickhouse

# 交互式；查询示例：SHOW TABLES FROM default;
clickhouse-client:
	docker compose exec -it clickhouse clickhouse-client

ch-logs:
	bash scripts/ch_logs.sh $(ARGS)

test:
	go test ./...

# 默认 DSN 指向 make up 后的本机 Postgres；可 export E2E_DSN=... 覆盖
e2e:
	bash -c 'E2E_DSN=$${E2E_DSN:-postgres://postgres:postgres@127.0.0.1:5433/mp_sched?sslmode=disable} go test -count=1 -v -timeout=300s ./e2e/...'

# 会触发 logDockerStartParamsPreview；worker 用例在默认模式下走真实 Run（非 stub 时需可拉 alpine）
e2e-spec:
	bash -c 'E2E_DSN=$${E2E_DSN:-postgres://postgres:postgres@127.0.0.1:5433/mp_sched?sslmode=disable} go test -count=1 -v -timeout=300s ./e2e/ -run "TestE2E(PostTasksReturnsServerTaskID|WorkerPipelineReachesRunning|ResGPURoundTrip)"'

# 需已 make up（或至少 Postgres+CH 可达）。CH 与 docker-compose 一致时可不设 E2E_CH_USER/E2E_CH_PASSWORD
e2e-full:
	bash -c 'E2E_DSN=$${E2E_DSN:-postgres://postgres:postgres@127.0.0.1:5433/mp_sched?sslmode=disable} E2E_CH_ADDR=$${E2E_CH_ADDR:-127.0.0.1:9000} go test -count=1 -v -timeout=300s ./e2e/...'

build:
	@mkdir -p bin
	go build -o bin/mp-controller ./cmd/mp-controller
	go build -o bin/mp-worker ./cmd/mp-worker

run-controller:
	go run ./cmd/mp-controller -config configs/development.yaml

run-worker:
	go run ./cmd/mp-worker -config configs/development.yaml

smoke:
	@chmod +x scripts/smoke.sh
	@./scripts/smoke.sh
