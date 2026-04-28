package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	"mp_sched/internal/config"
	"mp_sched/internal/telemetry"
)

// 可选 ClickHouse 联调：不设置 E2E_CH_ADDR 时本文件内测试会 Skip。
// compose 默认用户/密码为 mp_sched / mp_sched_dev（见 internal/config.DefaultClickHouse*）。
// 启动：make up 或 make clickhouse-up；曾用无密码 default 的容器时请先 docker compose up -d --force-recreate clickhouse
//
//	E2E_CH_ADDR=127.0.0.1:9000 go test -v -count=1 ./e2e/... -run TestE2E_ClickHouse
//
// 覆盖凭据：E2E_CH_USER、E2E_CH_PASSWORD、E2E_CH_DATABASE、E2E_CH_TLS=1
// 与 E2E_DSN（Postgres）独立。

func e2eClickHouseConfig(t *testing.T) (cfg config.ClickHouse) {
	t.Helper()
	c, ok := config.ClickHouseFromE2EEnv()
	if !ok {
		t.Skip("跳过 ClickHouse：未设置 E2E_CH_ADDR（例: E2E_CH_ADDR=127.0.0.1:9000，需先 make clickhouse-up）")
	}
	return c
}

// TestE2E_ClickHouseTelemetryInsertAndQuery 用与线上一致的 OpenClickHouse + Insert* 写三表，再 SELECT 打日志。
func TestE2E_ClickHouseTelemetryInsertAndQuery(t *testing.T) {
	chCfg := e2eClickHouseConfig(t)
	ctx := context.Background()

	conn, err := telemetry.OpenClickHouse(&chCfg)
	if err != nil {
		t.Fatalf("OpenClickHouse（与进程内 telemetry 同路径）: %v", err)
	}
	defer func() { _ = conn.Close() }()

	dbName := chCfg.Database
	if dbName == "" {
		dbName = "default"
	}

	schemaCtx, sCancel := context.WithTimeout(ctx, 20*time.Second)
	if err := telemetry.EnsureSchema(schemaCtx, conn, dbName); err != nil {
		sCancel()
		t.Fatalf("ensure schema: %v", err)
	}
	sCancel()

	mark := fmt.Sprintf("e2e-probe-%d", time.Now().UnixNano())
	now := time.Now().UTC().Truncate(time.Millisecond)

	slogRow := []telemetry.SchedulerLogRow{{
		TS: now, Service: mark, Level: "INFO", Msg: "e2e hello", Attrs: `{"run":"` + mark + `","k":1}`,
	}}
	statsRow := []telemetry.DockerStatRow{{
		TS:          now,
		TaskID:      "task-" + mark,
		ContainerID: "e2e-container-1",
		CPUPercent:  1.5,
		MemUsage:    1024,
		MemLimit:    2048,
		Pids:        3,
	}}
	logLineRow := []telemetry.DockerLogLineRow{{
		TS:          now,
		TaskID:      "task-" + mark,
		ContainerID: "e2e-container-1",
		Stream:      "stdout",
		Line:        "e2e log line " + mark,
	}}

	insCtx, iCancel := context.WithTimeout(ctx, 15*time.Second)
	if err := telemetry.InsertSchedulerLogs(insCtx, conn, dbName, slogRow); err != nil {
		iCancel()
		t.Fatalf("insert scheduler_logs: %v", err)
	}
	if err := telemetry.InsertDockerStats(insCtx, conn, dbName, statsRow); err != nil {
		iCancel()
		t.Fatalf("insert docker_container_stats: %v", err)
	}
	if err := telemetry.InsertDockerLogLines(insCtx, conn, dbName, logLineRow); err != nil {
		iCancel()
		t.Fatalf("insert docker_log_lines: %v", err)
	}
	iCancel()

	qCtx, qCancel := context.WithTimeout(ctx, 10*time.Second)
	defer qCancel()
	dbQ := identQuote(dbName)

	t.Logf("e2e ClickHouse 探针 mark=%q database=%q user=%q（OpenClickHouse 与 worker/controller 一致）", mark, dbName, chCfg.User)

	if err := logTableSample5(t, qCtx, conn, dbQ+".scheduler_logs",
		"SELECT toString(ts), service, level, msg, attrs FROM %s WHERE service = ? ORDER BY ts DESC LIMIT 1", mark,
		"scheduler_logs（mp-controller/mp-worker 的 slog 落库，attrs 为 JSON 字符串）"); err != nil {
		t.Fatalf("query scheduler_logs: %v", err)
	}
	if err := logTableSample7(t, qCtx, conn, dbQ+".docker_container_stats",
		"SELECT toString(ts), task_id, container_id, toString(cpu_percent), toString(mem_usage_bytes), toString(mem_limit_bytes), toString(pids) FROM %s WHERE task_id = ? ORDER BY ts DESC LIMIT 1", "task-"+mark,
		"docker_container_stats（worker 定时采样）"); err != nil {
		t.Fatalf("query docker_container_stats: %v", err)
	}
	if err := logTableSample5(t, qCtx, conn, dbQ+".docker_log_lines",
		"SELECT toString(ts), task_id, container_id, stream, line FROM %s WHERE task_id = ? ORDER BY ts DESC LIMIT 1", "task-"+mark,
		"docker_log_lines（worker 拉取容器 stdout/stderr 分行）"); err != nil {
		t.Fatalf("query docker_log_lines: %v", err)
	}
}

func identQuote(db string) string {
	if db == "" {
		return "default"
	}
	return db
}

func logTableSample5(t *testing.T, ctx context.Context, conn driver.Conn, table string, query string, whereVal string, caption string) error {
	t.Helper()
	q := fmt.Sprintf(query, table)
	rows, err := conn.Query(ctx, q, whereVal)
	if err != nil {
		return err
	}
	defer rows.Close()
	if !rows.Next() {
		t.Logf("  — %s: 无行", caption)
		return rows.Err()
	}
	cols := rows.Columns()
	var a, b, c, d, e string
	if err := rows.Scan(&a, &b, &c, &d, &e); err != nil {
		return err
	}
	t.Logf("  — %s", caption)
	vals := []string{a, b, c, d, e}
	for i, name := range cols {
		if i < len(vals) {
			t.Logf("      %s = %q", name, vals[i])
		}
	}
	return rows.Err()
}

func logTableSample7(t *testing.T, ctx context.Context, conn driver.Conn, table string, query string, whereVal string, caption string) error {
	t.Helper()
	q := fmt.Sprintf(query, table)
	rows, err := conn.Query(ctx, q, whereVal)
	if err != nil {
		return err
	}
	defer rows.Close()
	if !rows.Next() {
		t.Logf("  — %s: 无行", caption)
		return rows.Err()
	}
	cols := rows.Columns()
	var a, b, c, d, e, f, g string
	if err := rows.Scan(&a, &b, &c, &d, &e, &f, &g); err != nil {
		return err
	}
	t.Logf("  — %s", caption)
	vals := []string{a, b, c, d, e, f, g}
	for i, name := range cols {
		if i < len(vals) {
			t.Logf("      %s = %q", name, vals[i])
		}
	}
	return rows.Err()
}
