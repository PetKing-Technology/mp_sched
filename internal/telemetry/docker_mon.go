package telemetry

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	"mp_sched/internal/config"
	"mp_sched/internal/model"
	"mp_sched/internal/taskrepo"
)

const maxLogLineRunes = 65535

const maxTerminalDockerLogTail = 1_000_000

func effectiveTerminalFlushTail(app *config.App) int {
	if app == nil {
		return 20000
	}
	n := app.Telemetry.DockerLogTerminalFlushLines
	if n < 0 {
		return 0
	}
	if n == 0 {
		n = 20000
	}
	if n > maxTerminalDockerLogTail {
		n = maxTerminalDockerLogTail
	}
	return n
}

func dockerLogRowsFromBuffers(taskID, containerID string, ts time.Time, stdout, stderr *bytes.Buffer) []DockerLogLineRow {
	var rows []DockerLogLineRow
	appendDockerLogStreamLines(&rows, taskID, containerID, "stdout", stdout, ts)
	appendDockerLogStreamLines(&rows, taskID, containerID, "stderr", stderr, ts)
	return rows
}

func appendDockerLogStreamLines(rows *[]DockerLogLineRow, taskID, containerID, stream string, buf *bytes.Buffer, ts time.Time) {
	sc := bufio.NewScanner(buf)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := trimLine(sc.Text())
		if line == "" {
			continue
		}
		*rows = append(*rows, DockerLogLineRow{
			TS:          ts,
			TaskID:      taskID,
			ContainerID: containerID,
			Stream:      stream,
			Line:        line,
		})
	}
}

// FinalFlushDockerLogs 在任务已停或终态同步时尽最大努力再拉一次容器日志（仅 Tail）；若仍开启 docker_log_interval_seconds，可能与周期采集写入重复行（无去重）。
func FinalFlushDockerLogs(ctx context.Context, app *config.App, eng *client.Client, task *model.Task) {
	if app == nil || !app.ClickHouse.Enable || eng == nil || task == nil {
		return
	}
	if task.Provider != "docker" {
		return
	}
	cid := strings.TrimSpace(task.RuntimeRef)
	if cid == "" {
		return
	}
	tailN := effectiveTerminalFlushTail(app)
	if tailN <= 0 {
		return
	}
	conn, err := OpenClickHouse(&app.ClickHouse)
	if err != nil {
		slog.Debug("telemetry: terminal log flush: clickhouse", "err", err.Error())
		return
	}
	defer func() { _ = conn.Close() }()
	db := app.ClickHouse.Database
	if db == "" {
		db = "default"
	}
	sctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	err = EnsureSchema(sctx, conn, db)
	cancel()
	if err != nil {
		slog.Debug("telemetry: terminal log flush: schema", "err", err.Error())
		return
	}
	cctx, ccancel := context.WithTimeout(ctx, 60*time.Second)
	defer ccancel()
	opts := container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Timestamps: true,
		Tail:       strconv.Itoa(tailN),
	}
	rc, err := eng.ContainerLogs(cctx, cid, opts)
	if err != nil {
		slog.Debug("telemetry: terminal log flush: container logs", "task_id", task.TaskID, "err", err.Error())
		return
	}
	var outBuf, errBuf bytes.Buffer
	_, _ = stdcopy.StdCopy(&outBuf, &errBuf, rc)
	_ = rc.Close()
	now := time.Now().UTC()
	rows := dockerLogRowsFromBuffers(task.TaskID, cid, now, &outBuf, &errBuf)
	if len(rows) == 0 {
		return
	}
	ictx, icancel := context.WithTimeout(ctx, 30*time.Second)
	defer icancel()
	if err := InsertDockerLogLines(ictx, conn, db, rows); err != nil {
		slog.Debug("telemetry: terminal log flush: insert", "err", err.Error())
	}
}

// StartDockerMonitors 在 worker 上拉取 running 容器的 stats 与日志；需 ClickHouse 已启用。
func StartDockerMonitors(ctx context.Context, app *config.App, eng *client.Client, repo *taskrepo.Repo) {
	if app == nil || !app.ClickHouse.Enable || eng == nil || repo == nil {
		return
	}
	statsSec := app.Telemetry.DockerStatsIntervalSeconds
	logSec := app.Telemetry.DockerLogIntervalSeconds
	if statsSec <= 0 && logSec <= 0 {
		return
	}
	conn, err := OpenClickHouse(&app.ClickHouse)
	if err != nil {
		slog.Error("telemetry: docker monitors: clickhouse", "err", err.Error())
		return
	}
	schemaCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	err = EnsureSchema(schemaCtx, conn, app.ClickHouse.Database)
	cancel()
	if err != nil {
		slog.Error("telemetry: docker monitors: schema", "err", err.Error())
		_ = conn.Close()
		return
	}
	db := app.ClickHouse.Database
	if db == "" {
		db = "default"
	}
	tail := app.Telemetry.DockerLogTailLines
	if tail <= 0 {
		tail = 500
	}
	go func() {
		defer func() { _ = conn.Close() }()
		var wg sync.WaitGroup
		if statsSec > 0 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				runStatsLoop(ctx, eng, repo, conn, db, time.Duration(statsSec)*time.Second)
			}()
		}
		if logSec > 0 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				runLogLoop(ctx, eng, repo, conn, db, time.Duration(logSec)*time.Second, tail)
			}()
		}
		wg.Wait()
	}()
}

func runStatsLoop(ctx context.Context, eng *client.Client, repo *taskrepo.Repo, conn driver.Conn, db string, every time.Duration) {
	tk := time.NewTicker(every)
	defer tk.Stop()
	flush := func() {
		tasks, err := repo.ListDockerRunning()
		if err != nil {
			slog.Debug("telemetry: list docker running", "err", err.Error())
			return
		}
		var rows []DockerStatRow
		now := time.Now().UTC()
		cctx, cancel := context.WithTimeout(ctx, 25*time.Second)
		defer cancel()
		for i := range tasks {
			t := &tasks[i]
			id := strings.TrimSpace(t.RuntimeRef)
			if id == "" {
				continue
			}
			st, err := eng.ContainerStatsOneShot(cctx, id)
			if err != nil {
				continue
			}
			b := st.Body
			var sj types.StatsJSON
			if err := json.NewDecoder(b).Decode(&sj); err != nil {
				_ = b.Close()
				continue
			}
			_ = b.Close()
			rows = append(rows, DockerStatRow{
				TS:            now,
				TaskID:        t.TaskID,
				ContainerID:   id,
				CPUPercent:    cpuPercent(&sj),
				MemUsage:      sj.MemoryStats.Usage,
				MemLimit:      sj.MemoryStats.Limit,
				Pids:          uint32(sj.PidsStats.Current),
			})
		}
		if len(rows) == 0 {
			return
		}
		ictx, icancel := context.WithTimeout(ctx, 15*time.Second)
		if err := InsertDockerStats(ictx, conn, db, rows); err != nil {
			slog.Debug("telemetry: insert docker stats", "err", err.Error())
		}
		icancel()
	}
	flush()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
			flush()
		}
	}
}

func runLogLoop(ctx context.Context, eng *client.Client, repo *taskrepo.Repo, conn driver.Conn, db string, every time.Duration, tail int) {
	var sinceMu sync.Mutex
	sinceByID := map[string]time.Time{} // 已有 key 时下一次用 Since 增量；否则用 Tail 拉最近 N 行
	tk := time.NewTicker(every)
	defer tk.Stop()
	flush := func() {
		tasks, err := repo.ListDockerRunning()
		if err != nil {
			slog.Debug("telemetry: list docker running (logs)", "err", err.Error())
			return
		}
		now := time.Now().UTC()
		var rows []DockerLogLineRow
		cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		for i := range tasks {
			t := &tasks[i]
			cid := strings.TrimSpace(t.RuntimeRef)
			if cid == "" {
				continue
			}
			sinceMu.Lock()
			prior, hasSince := sinceByID[cid]
			sinceMu.Unlock()
			opts := container.LogsOptions{
				ShowStdout: true,
				ShowStderr: true,
				Timestamps: true,
			}
			if hasSince {
				opts.Since = prior.Format(time.RFC3339Nano)
			} else {
				opts.Tail = strconv.Itoa(tail)
			}
			rc, err := eng.ContainerLogs(cctx, cid, opts)
			if err != nil {
				continue
			}
			var outBuf, errBuf bytes.Buffer
			_, _ = stdcopy.StdCopy(&outBuf, &errBuf, rc)
			_ = rc.Close()
			sinceMu.Lock()
			sinceByID[cid] = now
			sinceMu.Unlock()
			rows = append(rows, dockerLogRowsFromBuffers(t.TaskID, cid, now, &outBuf, &errBuf)...)
		}
		cancel()
		if len(rows) == 0 {
			return
		}
		ictx, icancel := context.WithTimeout(ctx, 30*time.Second)
		if err := InsertDockerLogLines(ictx, conn, db, rows); err != nil {
			slog.Debug("telemetry: insert docker logs", "err", err.Error())
		}
		icancel()
	}
	flush()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
			flush()
		}
	}
}

func trimLine(s string) string {
	if utf8.RuneCountInString(s) > maxLogLineRunes {
		return string([]rune(s)[:maxLogLineRunes])
	}
	return s
}

func cpuPercent(v *types.StatsJSON) float64 {
	if v == nil {
		return 0
	}
	cpuDelta := float64(v.CPUStats.CPUUsage.TotalUsage) - float64(v.PreCPUStats.CPUUsage.TotalUsage)
	sysDelta := float64(v.CPUStats.SystemUsage) - float64(v.PreCPUStats.SystemUsage)
	online := float64(v.CPUStats.OnlineCPUs)
	if online == 0 {
		online = float64(len(v.CPUStats.CPUUsage.PercpuUsage))
		if online == 0 {
			online = 1
		}
	}
	if sysDelta > 0 && cpuDelta > 0 {
		return (cpuDelta / sysDelta) * online * 100.0
	}
	return 0
}
