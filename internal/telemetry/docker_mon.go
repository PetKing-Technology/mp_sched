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
	"mp_sched/internal/taskrepo"
)

const maxLogLineRunes = 65535

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
			appendLines := func(stream string, buf *bytes.Buffer) {
				sc := bufio.NewScanner(buf)
				sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
				for sc.Scan() {
					line := trimLine(sc.Text())
					if line == "" {
						continue
					}
					rows = append(rows, DockerLogLineRow{
						TS:          now,
						TaskID:      t.TaskID,
						ContainerID: cid,
						Stream:      stream,
						Line:        line,
					})
				}
			}
			appendLines("stdout", &outBuf)
			appendLines("stderr", &errBuf)
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
