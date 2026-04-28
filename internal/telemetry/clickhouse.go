package telemetry

import (
	"context"
	"crypto/tls"
	"fmt"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	"mp_sched/internal/config"
)

// ClickHouseDialOptions 与 OpenClickHouse 使用同一套字段，e2e/工具与业务共用。
func ClickHouseDialOptions(cfg *config.ClickHouse) (*clickhouse.Options, error) {
	if cfg == nil {
		return nil, fmt.Errorf("telemetry: nil clickhouse config")
	}
	if !cfg.Enable {
		return nil, fmt.Errorf("telemetry: clickhouse disabled")
	}
	addr := cfg.Address
	if addr == "" {
		addr = "127.0.0.1:9000"
	}
	db := cfg.Database
	if db == "" {
		db = "default"
	}
	user := cfg.User
	if user == "" {
		user = config.DefaultClickHouseUser
	}
	opts := &clickhouse.Options{
		Addr: []string{addr},
		Auth: clickhouse.Auth{
			Database: db,
			Username: user,
			Password: cfg.Password,
		},
		DialTimeout: 5 * time.Second,
	}
	if cfg.TLS {
		opts.TLS = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	return opts, nil
}

// OpenClickHouse 建立原生协议连接（默认 9000 端口），内部使用 ClickHouseDialOptions + Ping。
func OpenClickHouse(cfg *config.ClickHouse) (driver.Conn, error) {
	opts, err := ClickHouseDialOptions(cfg)
	if err != nil {
		return nil, err
	}
	conn, err := clickhouse.Open(opts)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := conn.Ping(ctx); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

// EnsureSchema 建表（MergeTree，可重复执行）。
func EnsureSchema(ctx context.Context, conn driver.Conn, db string) error {
	if conn == nil {
		return nil
	}
	if db == "" {
		db = "default"
	}
	stmts := []string{
		fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS %s.scheduler_logs (
  ts DateTime64(3, 'UTC'),
  service LowCardinality(String),
  level LowCardinality(String),
  msg String,
  attrs String
) ENGINE = MergeTree()
ORDER BY (ts, service)
TTL ts + toIntervalDay(90)`, db),
		fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS %s.docker_container_stats (
  ts DateTime64(3, 'UTC'),
  task_id String,
  container_id String,
  cpu_percent Float64,
  mem_usage_bytes UInt64,
  mem_limit_bytes UInt64,
  pids UInt32
) ENGINE = MergeTree()
ORDER BY (ts, task_id)
TTL ts + toIntervalDay(60)`, db),
		fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS %s.docker_log_lines (
  ts DateTime64(3, 'UTC'),
  task_id String,
  container_id String,
  stream LowCardinality(String),
  line String
) ENGINE = MergeTree()
ORDER BY (ts, task_id, container_id)
TTL ts + toIntervalDay(30)`, db),
	}
	for _, q := range stmts {
		if err := conn.Exec(ctx, q); err != nil {
			return fmt.Errorf("clickhouse schema: %w", err)
		}
	}
	return nil
}

// InsertSchedulerLogs 批量写入调度器 slog。
func InsertSchedulerLogs(ctx context.Context, conn driver.Conn, db string, rows []SchedulerLogRow) error {
	if conn == nil || len(rows) == 0 {
		return nil
	}
	if db == "" {
		db = "default"
	}
	batch, err := conn.PrepareBatch(ctx, fmt.Sprintf(
		`INSERT INTO %s.scheduler_logs (ts, service, level, msg, attrs)`, db))
	if err != nil {
		return err
	}
	for _, r := range rows {
		if err := batch.Append(r.TS, r.Service, r.Level, r.Msg, r.Attrs); err != nil {
			return err
		}
	}
	return batch.Send()
}

// InsertDockerStats 批量写入容器资源采样。
func InsertDockerStats(ctx context.Context, conn driver.Conn, db string, rows []DockerStatRow) error {
	if conn == nil || len(rows) == 0 {
		return nil
	}
	if db == "" {
		db = "default"
	}
	batch, err := conn.PrepareBatch(ctx, fmt.Sprintf(
		`INSERT INTO %s.docker_container_stats (ts, task_id, container_id, cpu_percent, mem_usage_bytes, mem_limit_bytes, pids)`, db))
	if err != nil {
		return err
	}
	for _, r := range rows {
		if err := batch.Append(r.TS, r.TaskID, r.ContainerID, r.CPUPercent, r.MemUsage, r.MemLimit, r.Pids); err != nil {
			return err
		}
	}
	return batch.Send()
}

// InsertDockerLogLines 批量写入容器日志行。
func InsertDockerLogLines(ctx context.Context, conn driver.Conn, db string, rows []DockerLogLineRow) error {
	if conn == nil || len(rows) == 0 {
		return nil
	}
	if db == "" {
		db = "default"
	}
	batch, err := conn.PrepareBatch(ctx, fmt.Sprintf(
		`INSERT INTO %s.docker_log_lines (ts, task_id, container_id, stream, line)`, db))
	if err != nil {
		return err
	}
	for _, r := range rows {
		if err := batch.Append(r.TS, r.TaskID, r.ContainerID, r.Stream, r.Line); err != nil {
			return err
		}
	}
	return batch.Send()
}

// SchedulerLogRow slog 落库行。
type SchedulerLogRow struct {
	TS      time.Time
	Service string
	Level   string
	Msg     string
	Attrs   string
}

// DockerStatRow 单次 stats 采样。
type DockerStatRow struct {
	TS          time.Time
	TaskID      string
	ContainerID string
	CPUPercent  float64
	MemUsage    uint64
	MemLimit    uint64
	Pids        uint32
}

// DockerLogLineRow 单行容器日志。
type DockerLogLineRow struct {
	TS          time.Time
	TaskID      string
	ContainerID string
	Stream      string
	Line        string
}
