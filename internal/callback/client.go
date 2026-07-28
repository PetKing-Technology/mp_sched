package callback

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"mp_sched/internal/config"
	"mp_sched/internal/model"
)

const (
	EventPending    = "pending"
	EventProcessing = "processing"
	EventAdmitted   = "admitted"
	EventRunning    = "running"
	EventSucceeded  = "succeeded"
	EventFailed     = "failed"
	EventStopped    = "stopped"
	EventTimeout    = "timeout"

	protocolVersion       = "mp_sched_callback/v2"
	headerVersion         = "X-MP-Sched-Callback-Version"
	headerKeyID           = "X-MP-Sched-Key-Id"
	headerDeliveryID      = "X-MP-Sched-Delivery-Id"
	headerOccurredAt      = "X-MP-Sched-Occurred-At"
	headerContentSHA256   = "X-MP-Sched-Content-SHA256"
	headerSignature       = "X-MP-Sched-Signature"
)

// Client sends scheduler task-state callbacks to the configured application.
type Client struct {
	cfg *config.Callback
	hc  *http.Client
	db  *gorm.DB
}

func New(c *config.Callback, databases ...*gorm.DB) *Client {
	if c == nil {
		return &Client{}
	}
	secs := c.TimeoutSeconds
	if secs <= 0 {
		secs = 10
	}
	var db *gorm.DB
	if len(databases) > 0 {
		db = databases[0]
	}
	return &Client{cfg: c, hc: &http.Client{Timeout: time.Duration(secs) * time.Second}, db: db}
}

// Fire does nothing when callbacks are disabled or the event is filtered out.
// An auth-absent callback follows the legacy body/header path exactly.
func (c *Client) Fire(ctx context.Context, event string, t *model.Task) {
	if c == nil || c.cfg == nil || !c.cfg.Enable || c.cfg.URL == "" || t == nil || !c.allows(event) {
		return
	}
	method := c.cfg.Method
	if method == "" {
		method = http.MethodPost
	}
	body := map[string]any{"event": event, "task": t, "task_id": t.TaskID, "status": t.Status}
	var meta callbackMeta
	if c.cfg.Auth.Enabled() {
		meta = callbackMeta{DeliveryID: uuid.NewString(), OccurredAt: time.Now().UTC().Format(time.RFC3339Nano)}
		body["protocol_version"] = protocolVersion
		body["delivery_id"] = meta.DeliveryID
		body["occurred_at"] = meta.OccurredAt
		c.addControlledAuthority(body, event, t)
		c.addArtifactBinding(body, event, t)
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return
	}
	req, err := http.NewRequestWithContext(ctx, method, c.cfg.URL, bytes.NewReader(encoded))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range c.cfg.Headers {
		req.Header.Set(k, v)
	}
	if c.cfg.Auth.Enabled() {
		for k, values := range c.v2Headers(meta, encoded) {
			for _, value := range values {
				req.Header.Set(k, value)
			}
		}
	}
	if c.cfg.Auth.Enabled() && terminal(event) && c.db != nil {
		c.enqueue(ctx, event, t.TaskID, req)
		return
	}
	c.send(ctx, req)
}

// addControlledAuthority copies the non-secret controlled-run correlation set
// out of the persisted scheduler business document.  The signed callback is
// still verified and cross-checked against Runtime's durable run registry;
// this echo prevents an otherwise valid callback for one protected attempt
// from being silently interpreted as another attempt.
func (c *Client) addControlledAuthority(body map[string]any, event string, task *model.Task) {
	if task == nil {
		return
	}
	var business struct {
		AgentRT struct {
			ControlledCallback *struct {
				TenantID string `json:"tenant_id"`
				Generation string `json:"generation"`
				TaskID string `json:"task_id"`
				WorkUnitID string `json:"work_unit_id"`
				RunID string `json:"run_id"`
				Attempt int `json:"attempt"`
				FencingToken string `json:"fencing_token"`
				ArtifactContract string `json:"artifact_contract"`
			} `json:"controlled_callback"`
		} `json:"agent_rt"`
	}
	if json.Unmarshal(task.Business, &business) != nil || business.AgentRT.ControlledCallback == nil {
		return
	}
	value := business.AgentRT.ControlledCallback
	if value.TenantID == "" || value.Generation == "" || value.TaskID == "" ||
		value.WorkUnitID == "" || value.RunID == "" || value.Attempt < 1 ||
		value.FencingToken == "" || value.ArtifactContract != "sequence_bundle/v1" {
		return
	}
	body["tenant_id"] = value.TenantID
	body["generation"] = value.Generation
	body["task_id"] = value.TaskID
	body["work_unit_id"] = value.WorkUnitID
	body["run_id"] = value.RunID
	body["attempt"] = value.Attempt
	body["fencing_token"] = value.FencingToken
	body["scheduler_job_id"] = task.TaskID
	body["terminal_status"] = event
}

func terminal(event string) bool {
	return event == EventSucceeded || event == EventFailed || event == EventStopped || event == EventTimeout
}

func (c *Client) send(ctx context.Context, req *http.Request) bool {
	resp, err := c.hc.Do(req.WithContext(ctx))
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

func (c *Client) enqueue(ctx context.Context, event, taskID string, req *http.Request) {
	body, _ := req.GetBody()
	if body == nil {
		return
	}
	encoded, err := io.ReadAll(body)
	if err != nil {
		return
	}
	headers, err := json.Marshal(req.Header)
	if err != nil {
		return
	}
	now := time.Now().UTC()
	row := model.CallbackDelivery{DeliveryID: req.Header.Get(headerDeliveryID), TaskID: taskID, Event: event, Method: req.Method, URL: req.URL.String(), Body: datatypes.JSON(encoded), Headers: datatypes.JSON(headers), Status: "pending", NextAttemptAt: now}
	if err := c.db.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "task_id"}, {Name: "event"}}, DoNothing: true}).Create(&row).Error; err != nil {
		return
	}
	if c.db.WithContext(ctx).Where("task_id = ? AND event = ?", taskID, event).First(&row).Error != nil {
		return
	}
	if row.Status != "pending" {
		return
	}
	if c.claim(ctx, &row) {
		c.deliver(ctx, &row)
	}
}

func (c *Client) claim(ctx context.Context, row *model.CallbackDelivery) bool {
	now := time.Now().UTC()
	result := c.db.WithContext(ctx).Model(&model.CallbackDelivery{}).
		Where("delivery_id = ? AND status IN ? AND next_attempt_at <= ?", row.DeliveryID, []string{"pending", "delivering"}, now).
		Updates(map[string]any{"status": "delivering", "next_attempt_at": now.Add(30 * time.Second)})
	return result.Error == nil && result.RowsAffected == 1
}

func (c *Client) deliver(ctx context.Context, row *model.CallbackDelivery) {
	var headers http.Header
	if json.Unmarshal(row.Headers, &headers) != nil {
		return
	}
	req, err := http.NewRequestWithContext(ctx, row.Method, row.URL, bytes.NewReader(row.Body))
	if err != nil {
		return
	}
	req.Header = headers
	now := time.Now().UTC()
	updates := map[string]any{"attempts": row.Attempts + 1, "status": "pending", "next_attempt_at": now.Add(5 * time.Second)}
	if c.send(ctx, req) {
		updates["status"] = "delivered"
		updates["delivered_at"] = now
	}
	_ = c.db.WithContext(ctx).Model(&model.CallbackDelivery{}).Where("delivery_id = ? AND status = ?", row.DeliveryID, "delivering").Updates(updates).Error
}

// Drain retries exact persisted authenticated terminal deliveries after restart.
func (c *Client) Drain(ctx context.Context) {
	if c == nil || c.db == nil || c.cfg == nil || !c.cfg.Auth.Enabled() {
		return
	}
	var rows []model.CallbackDelivery
	if c.db.WithContext(ctx).Where("status IN ? AND next_attempt_at <= ?", []string{"pending", "delivering"}, time.Now().UTC()).Limit(100).Find(&rows).Error != nil {
		return
	}
	for i := range rows {
		if c.claim(ctx, &rows[i]) {
			c.deliver(ctx, &rows[i])
		}
	}
}

func (c *Client) RunLoop(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	c.Drain(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.Drain(ctx)
		}
	}
}

func (c *Client) addArtifactBinding(body map[string]any, event string, task *model.Task) {
	if event != EventSucceeded {
		return
	}
	binding, err := collectSequenceBundle(c.cfg.Auth.ArtifactRoot, task)
	if err == nil {
		body["artifact_binding"] = map[string]string{
			"mode": "scheduler_digest/v1", "contract": binding.Contract, "run_id": binding.RunID,
		}
		body["artifact_manifest_digest"] = binding.ManifestDigest
		return
	}
	if !errors.Is(err, errNoControlledBinding) {
		body["artifact_binding_error"] = "unavailable"
	}
}

func (c *Client) allows(event string) bool {
	if len(c.cfg.Events) == 0 {
		return true
	}
	for _, allowed := range c.cfg.Events {
		if allowed == event {
			return true
		}
	}
	return false
}

type callbackMeta struct{ DeliveryID, OccurredAt string }

func (c *Client) v2Headers(meta callbackMeta, body []byte) http.Header {
	digest := sha256.Sum256(body)
	bodySHA := hex.EncodeToString(digest[:])
	headers := make(http.Header)
	headers.Set(headerVersion, "2")
	headers.Set(headerKeyID, c.cfg.Auth.KeyID)
	headers.Set(headerDeliveryID, meta.DeliveryID)
	headers.Set(headerOccurredAt, meta.OccurredAt)
	headers.Set(headerContentSHA256, bodySHA)
	headers.Set(headerSignature, sign(c.cfg.Auth.HMACSecret, c.cfg.Auth.KeyID, meta.DeliveryID, meta.OccurredAt, bodySHA))
	return headers
}

func sign(secret, keyID, deliveryID, occurredAt, bodySHA string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte("mp_sched_callback_v2\n" + lengthField(keyID) + "\n" + lengthField(deliveryID) + "\n" + lengthField(occurredAt) + "\n" + lengthField(bodySHA) + "\n"))
	return hex.EncodeToString(mac.Sum(nil))
}

func lengthField(value string) string { return strconv.Itoa(len(value)) + ":" + value }
