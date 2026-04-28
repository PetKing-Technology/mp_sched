package callback

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"time"

	"mp_sched/internal/config"
	"mp_sched/internal/model"
)

// 事件名约定，与 HTTP body 的 event 一致
const (
	EventPending    = "pending"
	EventProcessing = "processing"
	EventAdmitted   = "admitted"
	EventRunning    = "running"
	EventSucceeded  = "succeeded"
	EventFailed     = "failed"
	EventStopped    = "stopped"
	EventTimeout    = "timeout"
)

// Client 向业务 App 推送任务状态
type Client struct {
	cfg *config.Callback
	hc  *http.Client
}

func New(c *config.Callback) *Client {
	if c == nil {
		return &Client{}
	}
	secs := c.TimeoutSeconds
	if secs <= 0 {
		secs = 10
	}
	return &Client{
		cfg: c,
		hc:  &http.Client{Timeout: time.Duration(secs) * time.Second},
	}
}

// Fire 当 cfg.Enable 或 URL 空时无操作
func (c *Client) Fire(ctx context.Context, event string, t *model.Task) {
	if c == nil || c.cfg == nil || !c.cfg.Enable || c.cfg.URL == "" || t == nil {
		return
	}
	if !c.allows(event) {
		return
	}
	method := c.cfg.Method
	if method == "" {
		method = http.MethodPost
	}
	body := map[string]any{
		"event":   event,
		"task":    t,
		"task_id": t.TaskID,
		"status":  t.Status,
	}
	b, err := json.Marshal(body)
	if err != nil {
		return
	}
	req, err := http.NewRequestWithContext(ctx, method, c.cfg.URL, bytes.NewReader(b))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range c.cfg.Headers {
		req.Header.Set(k, v)
	}
	_, _ = c.hc.Do(req)
}

func (c *Client) allows(event string) bool {
	if len(c.cfg.Events) == 0 {
		return true
	}
	for _, e := range c.cfg.Events {
		if e == event {
			return true
		}
	}
	return false
}
