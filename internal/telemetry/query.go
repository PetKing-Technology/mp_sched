package telemetry

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// SchedulerLogView CH scheduler_logs 一行（SELECT 均为字符串便于 JSON 输出）。
type SchedulerLogView struct {
	TS      string `json:"ts"`
	Service string `json:"service"`
	Level   string `json:"level"`
	Msg     string `json:"msg"`
	Attrs   string `json:"attrs"`
}

// DockerStatView CH docker_container_stats 一行。
type DockerStatView struct {
	TS          string  `json:"ts"`
	TaskID      string  `json:"task_id"`
	ContainerID string  `json:"container_id"`
	CPUPercent  float64 `json:"cpu_percent"`
	MemUsage    uint64  `json:"mem_usage_bytes"`
	MemLimit    uint64  `json:"mem_limit_bytes"`
	Pids        uint32  `json:"pids"`
}

// DockerLogLineView CH docker_log_lines 一行。
type DockerLogLineView struct {
	TS          string `json:"ts"`
	TaskID      string `json:"task_id"`
	ContainerID string `json:"container_id"`
	Stream      string `json:"stream"`
	Line        string `json:"line"`
}

// QueryLimits 通用分页/条数。
type QueryLimits struct {
	Limit int // 上限 500，默认 100
}

func (q QueryLimits) limit() int {
	if q.Limit <= 0 {
		return 100
	}
	if q.Limit > 500 {
		return 500
	}
	return q.Limit
}

// SchedulerLogQuery 条件均为 AND；空则不过滤。
type SchedulerLogQuery struct {
	QueryLimits
	Service     string
	Level       string
	MsgContains string // 子串匹配（CH positionCaseInsensitive）
	SinceRFC    string // RFC3339，过滤 ts >=
}

// ListSchedulerLogs 查调度器/全服务 slog 落库。
func ListSchedulerLogs(ctx context.Context, conn driver.Conn, db string, q SchedulerLogQuery) ([]SchedulerLogView, error) {
	if conn == nil {
		return nil, fmt.Errorf("telemetry: nil conn")
	}
	if db == "" {
		db = "default"
	}
	tbl := ident(db) + ".scheduler_logs"
	var conds []string
	var args []any
	if s := strings.TrimSpace(q.Service); s != "" {
		conds = append(conds, "service = ?")
		args = append(args, s)
	}
	if s := strings.TrimSpace(q.Level); s != "" {
		conds = append(conds, "level = ?")
		args = append(args, s)
	}
	if s := strings.TrimSpace(q.MsgContains); s != "" {
		conds = append(conds, "positionCaseInsensitive(msg, ?) > 0")
		args = append(args, s)
	}
	if s := strings.TrimSpace(q.SinceRFC); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return nil, fmt.Errorf("since: %w", err)
		}
		conds = append(conds, "ts >= ?")
		args = append(args, t.UTC())
	}
	where := "1"
	if len(conds) > 0 {
		where = strings.Join(conds, " AND ")
	}
	sql := fmt.Sprintf(`SELECT toString(ts), service, level, msg, attrs FROM %s WHERE %s ORDER BY ts DESC LIMIT %d`, tbl, where, q.limit())
	rows, err := conn.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SchedulerLogView
	for rows.Next() {
		var v SchedulerLogView
		if err := rows.Scan(&v.TS, &v.Service, &v.Level, &v.Msg, &v.Attrs); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// DockerStatQuery 条件。
type DockerStatQuery struct {
	QueryLimits
	TaskID      string
	ContainerID string
}

// ListDockerStats 查容器资源采样。
func ListDockerStats(ctx context.Context, conn driver.Conn, db string, q DockerStatQuery) ([]DockerStatView, error) {
	if conn == nil {
		return nil, fmt.Errorf("telemetry: nil conn")
	}
	if db == "" {
		db = "default"
	}
	tbl := ident(db) + ".docker_container_stats"
	var conds []string
	var args []any
	if s := strings.TrimSpace(q.TaskID); s != "" {
		conds = append(conds, "task_id = ?")
		args = append(args, s)
	}
	if s := strings.TrimSpace(q.ContainerID); s != "" {
		conds = append(conds, "container_id = ?")
		args = append(args, s)
	}
	where := "1"
	if len(conds) > 0 {
		where = strings.Join(conds, " AND ")
	}
	sql := fmt.Sprintf(`SELECT toString(ts), task_id, container_id, cpu_percent, mem_usage_bytes, mem_limit_bytes, pids FROM %s WHERE %s ORDER BY ts DESC LIMIT %d`, tbl, where, q.limit())
	rows, err := conn.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DockerStatView
	for rows.Next() {
		var v DockerStatView
		if err := rows.Scan(&v.TS, &v.TaskID, &v.ContainerID, &v.CPUPercent, &v.MemUsage, &v.MemLimit, &v.Pids); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// DockerLogLineQuery 条件。
type DockerLogLineQuery struct {
	QueryLimits
	TaskID      string
	ContainerID string
	Stream      string
}

// ListDockerLogLines 查容器标准输出/错误日志行。
func ListDockerLogLines(ctx context.Context, conn driver.Conn, db string, q DockerLogLineQuery) ([]DockerLogLineView, error) {
	if conn == nil {
		return nil, fmt.Errorf("telemetry: nil conn")
	}
	if db == "" {
		db = "default"
	}
	tbl := ident(db) + ".docker_log_lines"
	var conds []string
	var args []any
	if s := strings.TrimSpace(q.TaskID); s != "" {
		conds = append(conds, "task_id = ?")
		args = append(args, s)
	}
	if s := strings.TrimSpace(q.ContainerID); s != "" {
		conds = append(conds, "container_id = ?")
		args = append(args, s)
	}
	if s := strings.TrimSpace(q.Stream); s != "" {
		conds = append(conds, "stream = ?")
		args = append(args, s)
	}
	where := "1"
	if len(conds) > 0 {
		where = strings.Join(conds, " AND ")
	}
	sql := fmt.Sprintf(`SELECT toString(ts), task_id, container_id, stream, line FROM %s WHERE %s ORDER BY ts DESC LIMIT %d`, tbl, where, q.limit())
	rows, err := conn.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DockerLogLineView
	for rows.Next() {
		var v DockerLogLineView
		if err := rows.Scan(&v.TS, &v.TaskID, &v.ContainerID, &v.Stream, &v.Line); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func ident(db string) string {
	// 仅允许字母数字下划线，防注入
	b := []byte(strings.TrimSpace(db))
	if len(b) == 0 {
		return "default"
	}
	for _, c := range b {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' {
			continue
		}
		return "default"
	}
	return string(b)
}
