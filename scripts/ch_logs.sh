#!/usr/bin/env bash
# 查询 ClickHouse 里 mp_sched 写入的表（需 compose 中 clickhouse 已启动，且 worker/controller 已开过 clickhouse.enable）。
#
# 用法：
#   ./scripts/ch_logs.sh                    # 最近 50 条 scheduler_logs（结构化 slog）
#   ./scripts/ch_logs.sh scheduler 100      # 最近 100 条调度日志
#   ./scripts/ch_logs.sh stats 30           # 最近 30 条 docker_container_stats
#   ./scripts/ch_logs.sh lines 30           # 最近 30 条 docker_log_lines（容器 stdout/stderr 行）
#
# 环境变量（与 docker-compose 默认一致）：
#   CLICKHOUSE_USER（默认 mp_sched）
#   CLICKHOUSE_PASSWORD（默认 mp_sched_dev）
#   CLICKHOUSE_DB（默认 default）
#
# 交互式进 client（任意 SQL）：
#   make clickhouse-client
#
# 本机已装 clickhouse-client 时也可直连（不经 Docker）：
#   clickhouse-client -h 127.0.0.1 --port 9000 -u mp_sched --password mp_sched_dev -d default -q "SELECT count() FROM default.scheduler_logs"

set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

USER="${CLICKHOUSE_USER:-mp_sched}"
PASS="${CLICKHOUSE_PASSWORD:-mp_sched_dev}"
DB="${CLICKHOUSE_DB:-default}"

KIND="${1:-scheduler}"
LIMIT="${2:-50}"

if ! docker compose exec -T clickhouse true >/dev/null 2>&1; then
  echo "无法在 clickhouse 容器中执行命令。先执行: make up 或 make clickhouse-up" >&2
  exit 1
fi

chq() {
  docker compose exec -T clickhouse clickhouse-client \
    --user "$USER" \
    --password "$PASS" \
    --database "$DB" \
    --query "$1"
}

case "$KIND" in
  scheduler|scheduler_logs|logs)
    chq "SELECT formatDateTime(ts, '%F %T') AS ts_utc, service, level, substring(msg, 1, 240) AS msg, substring(attrs, 1, 160) AS attrs
FROM ${DB}.scheduler_logs
ORDER BY ts DESC
LIMIT ${LIMIT}
FORMAT PrettyCompact"
    ;;
  stats|docker_stats|docker_container_stats)
    chq "SELECT formatDateTime(ts, '%F %T') AS ts_utc, task_id, substring(container_id, 1, 14) AS cid, cpu_percent, mem_usage_bytes, pids
FROM ${DB}.docker_container_stats
ORDER BY ts DESC
LIMIT ${LIMIT}
FORMAT PrettyCompact"
    ;;
  lines|docker_log_lines|docker_logs)
    chq "SELECT formatDateTime(ts, '%F %T') AS ts_utc, task_id, stream, substring(line, 1, 240) AS line
FROM ${DB}.docker_log_lines
ORDER BY ts DESC
LIMIT ${LIMIT}
FORMAT PrettyCompact"
    ;;
  tables)
    chq "SHOW TABLES FROM ${DB}"
    ;;
  count)
    chq "SELECT
  (SELECT count() FROM ${DB}.scheduler_logs) AS scheduler_logs,
  (SELECT count() FROM ${DB}.docker_container_stats) AS docker_container_stats,
  (SELECT count() FROM ${DB}.docker_log_lines) AS docker_log_lines
FORMAT PrettyCompact"
    ;;
  *)
    echo "未知类型: $KIND" >&2
    echo "可选: scheduler | stats | lines | tables | count" >&2
    echo "示例: $0 scheduler 50" >&2
    exit 1
    ;;
esac
