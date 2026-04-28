package telemetry

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	"mp_sched/internal/config"
)

// InitLogger 配置 JSON 到 stdout 的全局 slog，可选将日志批量写入 ClickHouse。
// 返回的 stop 在进程退出前调用以刷新批次。
func InitLogger(ctx context.Context, app *config.App, serviceName string) (stop func()) {
	if app == nil {
		app = &config.App{}
	}
	lvl := parseLevel(app.Server.LogLevel)
	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl, AddSource: false})

	noop := func() {}
	if !app.ClickHouse.Enable || !app.Telemetry.SlogToClickHouse {
		slog.SetDefault(slog.New(h))
		return noop
	}
	conn, err := OpenClickHouse(&app.ClickHouse)
	if err != nil {
		slog.SetDefault(slog.New(h))
		slog.New(h).Error("telemetry: clickhouse unavailable for slog, stdout only", "err", err.Error())
		return noop
	}
	schemaCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := EnsureSchema(schemaCtx, conn, app.ClickHouse.Database); err != nil {
		_ = conn.Close()
		slog.SetDefault(slog.New(h))
		slog.New(h).Error("telemetry: clickhouse schema failed, stdout only", "err", err.Error())
		return noop
	}
	batchSize := app.Telemetry.LogBatchSize
	if batchSize <= 0 {
		batchSize = 200
	}
	flush := time.Duration(app.Telemetry.LogBatchFlushMS) * time.Millisecond
	if flush <= 0 {
		flush = 2 * time.Second
	}
	b := newLogBatcher(conn, &app.ClickHouse, serviceName, batchSize, flush)
	chH := newClickHouseHandler(b, serviceName, nil, lvl)
	multi := &multiHandler{handlers: []slog.Handler{h, chH}}
	slog.SetDefault(slog.New(multi))
	go b.runFlusher(ctx)
	return func() {
		b.drain()
		_ = conn.Close()
	}
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// multiHandler 将同一条记录交给多个 handler（stdout + CH）。
type multiHandler struct {
	handlers []slog.Handler
}

func (m *multiHandler) Enabled(ctx context.Context, level slog.Level) bool {
	if len(m.handlers) == 0 {
		return false
	}
	return m.handlers[0].Enabled(ctx, level)
}

func (m *multiHandler) Handle(ctx context.Context, r slog.Record) error {
	for _, h := range m.handlers {
		_ = h.Handle(ctx, r)
	}
	return nil
}

func (m *multiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := make([]slog.Handler, len(m.handlers))
	for i, h := range m.handlers {
		out[i] = h.WithAttrs(attrs)
	}
	return &multiHandler{handlers: out}
}

func (m *multiHandler) WithGroup(name string) slog.Handler {
	out := make([]slog.Handler, len(m.handlers))
	for i, h := range m.handlers {
		out[i] = h.WithGroup(name)
	}
	return &multiHandler{handlers: out}
}

type logBatcher struct {
	conn      driver.Conn
	db        *config.ClickHouse
	service   string
	batchSize int
	flush     time.Duration
	mu        sync.Mutex
	buf       []SchedulerLogRow
	closed    bool
}

func newLogBatcher(conn driver.Conn, db *config.ClickHouse, service string, batchSize int, flush time.Duration) *logBatcher {
	return &logBatcher{conn: conn, db: db, service: service, batchSize: batchSize, flush: flush}
}

func (b *logBatcher) addRow(row SchedulerLogRow) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.buf = append(b.buf, row)
	if len(b.buf) >= b.batchSize {
		_ = b.flushUnlocked()
	}
}

func (b *logBatcher) runFlusher(ctx context.Context) {
	if b == nil {
		return
	}
	tk := time.NewTicker(b.flush)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
			b.mu.Lock()
			_ = b.flushUnlocked()
			b.mu.Unlock()
		}
	}
}

func (b *logBatcher) drain() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	_ = b.flushUnlocked()
}

func (b *logBatcher) flushUnlocked() error {
	if len(b.buf) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	db := "default"
	if b.db != nil && b.db.Database != "" {
		db = b.db.Database
	}
	rows := b.buf
	b.buf = nil
	if err := InsertSchedulerLogs(ctx, b.conn, db, rows); err != nil {
		b.buf = append(rows, b.buf...)
		_, _ = io.WriteString(os.Stderr, "telemetry: flush scheduler_logs: "+err.Error()+"\n")
		return err
	}
	return nil
}

// clickHouseHandler 将记录写入 logBatcher；合并 Handler 上 WithAttrs 的默认属性与 Record 内属性。
type clickHouseHandler struct {
	b      *logBatcher
	svc    string
	def    []slog.Attr
	level  slog.Leveler
}

func newClickHouseHandler(b *logBatcher, serviceName string, def []slog.Attr, level slog.Leveler) *clickHouseHandler {
	return &clickHouseHandler{b: b, svc: serviceName, def: def, level: level}
}

func (h *clickHouseHandler) Enabled(_ context.Context, level slog.Level) bool {
	if h.level == nil {
		return true
	}
	return level >= h.level.Level()
}

func (h *clickHouseHandler) Handle(_ context.Context, r slog.Record) error {
	if h == nil || h.b == nil {
		return nil
	}
	if !h.Enabled(context.Background(), r.Level) {
		return nil
	}
	m := make(map[string]any)
	for _, a := range h.def {
		setAttr(m, a)
	}
	r.Attrs(func(a slog.Attr) bool {
		setAttr(m, a)
		return true
	})
	attrsJSON, _ := json.Marshal(m)
	h.b.addRow(SchedulerLogRow{
		TS:      r.Time,
		Service: h.svc,
		Level:   r.Level.String(),
		Msg:     r.Message,
		Attrs:   string(attrsJSON),
	})
	return nil
}

func (h *clickHouseHandler) WithAttrs(as []slog.Attr) slog.Handler {
	if len(as) == 0 {
		return h
	}
	return newClickHouseHandler(h.b, h.svc, append(append([]slog.Attr{}, h.def...), as...), h.level)
}

func (h *clickHouseHandler) WithGroup(name string) slog.Handler {
	// slog 在 Logger 上 WithGroup 时会把结构打进 Record，此处保持默认转发即可
	_ = name
	return h
}

func setAttr(m map[string]any, a slog.Attr) {
	if a.Value.Kind() == slog.KindGroup {
		inner := make(map[string]any)
		for _, ga := range a.Value.Group() {
			setAttr(inner, ga)
		}
		m[a.Key] = inner
		return
	}
	m[a.Key] = a.Value.Any()
}
