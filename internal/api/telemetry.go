package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	"mp_sched/internal/telemetry"
)

// chConn 连 ClickHouse 并 EnsureSchema；调用方必须 Close(conn)。未启用或失败时写 HTTP 并返回 false。
func (s *Server) chConn(w http.ResponseWriter, r *http.Request) (conn driver.Conn, db string, ok bool) {
	if s == nil || s.H == nil || s.H.Pl == nil || s.H.Pl.Cfg == nil || !s.H.Pl.Cfg.ClickHouse.Enable {
		writeErr(w, http.StatusServiceUnavailable, "clickhouse disabled; set [clickhouse].enable=true and credentials")
		return nil, "", false
	}
	cfg := &s.H.Pl.Cfg.ClickHouse
	c, err := telemetry.OpenClickHouse(cfg)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "clickhouse: "+err.Error())
		return nil, "", false
	}
	d := cfg.Database
	if d == "" {
		d = "default"
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	if err := telemetry.EnsureSchema(ctx, c, d); err != nil {
		_ = c.Close()
		writeErr(w, http.StatusBadGateway, "clickhouse schema: "+err.Error())
		return nil, "", false
	}
	return c, d, true
}

func parseLimit(q string) int {
	n, _ := strconv.Atoi(q)
	if n <= 0 {
		return 100
	}
	if n > 500 {
		return 500
	}
	return n
}

func parseLogOffset(q string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(q))
	if n < 0 {
		return 0
	}
	if n > 10000 {
		return 10000
	}
	return n
}

func parseOrderDesc(q string) bool {
	switch strings.ToLower(strings.TrimSpace(q)) {
	case "desc", "newest":
		return true
	default:
		return false
	}
}

// GET /api/sched/v1/telemetry/scheduler-logs
func (s *Server) getSchedulerLogs(w http.ResponseWriter, r *http.Request) {
	conn, db, ok := s.chConn(w, r)
	if !ok {
		return
	}
	defer func() { _ = conn.Close() }()
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	rows, err := telemetry.ListSchedulerLogs(ctx, conn, db, telemetry.SchedulerLogQuery{
		QueryLimits: telemetry.QueryLimits{Limit: parseLimit(r.URL.Query().Get("limit"))},
		Service:     r.URL.Query().Get("service"),
		Level:       r.URL.Query().Get("level"),
		MsgContains: r.URL.Query().Get("msg"),
		SinceRFC:    r.URL.Query().Get("since"),
	})
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "items": rows})
}

// GET /api/sched/v1/telemetry/controller-logs  —  等同 service=mp-controller 的调度日志
func (s *Server) getControllerLogs(w http.ResponseWriter, r *http.Request) {
	conn, db, ok := s.chConn(w, r)
	if !ok {
		return
	}
	defer func() { _ = conn.Close() }()
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	rows, err := telemetry.ListSchedulerLogs(ctx, conn, db, telemetry.SchedulerLogQuery{
		QueryLimits: telemetry.QueryLimits{Limit: parseLimit(r.URL.Query().Get("limit"))},
		Service:     telemetry.ServiceController,
		Level:       r.URL.Query().Get("level"),
		MsgContains: r.URL.Query().Get("msg"),
		SinceRFC:    r.URL.Query().Get("since"),
	})
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "items": rows, "service": telemetry.ServiceController})
}

// GET /api/sched/v1/telemetry/docker-stats
func (s *Server) getDockerStats(w http.ResponseWriter, r *http.Request) {
	conn, db, ok := s.chConn(w, r)
	if !ok {
		return
	}
	defer func() { _ = conn.Close() }()
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	rows, err := telemetry.ListDockerStats(ctx, conn, db, telemetry.DockerStatQuery{
		QueryLimits: telemetry.QueryLimits{Limit: parseLimit(r.URL.Query().Get("limit"))},
		TaskID:      r.URL.Query().Get("task_id"),
		ContainerID: r.URL.Query().Get("container_id"),
	})
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "items": rows})
}

// GET /api/sched/v1/telemetry/docker-log-lines
func (s *Server) getDockerLogLines(w http.ResponseWriter, r *http.Request) {
	conn, db, ok := s.chConn(w, r)
	if !ok {
		return
	}
	defer func() { _ = conn.Close() }()
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	rows, err := telemetry.ListDockerLogLines(ctx, conn, db, telemetry.DockerLogLineQuery{
		QueryLimits: telemetry.QueryLimits{Limit: parseLimit(r.URL.Query().Get("limit"))},
		TaskID:      r.URL.Query().Get("task_id"),
		ContainerID: r.URL.Query().Get("container_id"),
		Stream:      r.URL.Query().Get("stream"),
		Offset:      parseLogOffset(r.URL.Query().Get("offset")),
		OrderDesc:   parseOrderDesc(r.URL.Query().Get("order")),
	})
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	if rows == nil {
		rows = []telemetry.DockerLogLineView{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "items": rows})
}
