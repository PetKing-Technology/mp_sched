package callback

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

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
}

func New(c *config.Callback) *Client {
	if c == nil {
		return &Client{}
	}
	secs := c.TimeoutSeconds
	if secs <= 0 {
		secs = 10
	}
	return &Client{cfg: c, hc: &http.Client{Timeout: time.Duration(secs) * time.Second}}
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
	_, _ = c.hc.Do(req)
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
