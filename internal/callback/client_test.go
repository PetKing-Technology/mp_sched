package callback

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"mp_sched/internal/config"
	"mp_sched/internal/database"
	"mp_sched/internal/model"
)

func TestAuthenticatedTerminalDeliveryPersistsAndRetriesAfterRestart(t *testing.T) {
	dsn := os.Getenv("MP_SCHED_OUTBOX_TEST_DSN")
	if dsn == "" {
		t.Skip("MP_SCHED_OUTBOX_TEST_DSN is not configured")
	}
	db, err := database.Open(&config.Database{DSN: dsn})
	if err != nil { t.Fatal(err) }
	if err := database.Migrate(db); err != nil { t.Fatal(err) }
	if err := db.Exec("DELETE FROM callback_deliveries").Error; err != nil { t.Fatal(err) }
	var calls []capturedRequest
	status := http.StatusServiceUnavailable
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		calls = append(calls, capturedRequest{header: r.Header.Clone(), body: body})
		w.WriteHeader(status)
	}))
	defer srv.Close()
	cfg := &config.Callback{Enable: true, URL: srv.URL, Auth: config.CallbackAuth{KeyID: "fixture-k1", HMACSecret: "fixture-secret"}}
	first := New(cfg, db)
	first.Fire(t.Context(), EventFailed, testTask())
	var pending model.CallbackDelivery
	if err := db.Where("task_id = ? AND event = ?", "scheduler-job-1", EventFailed).First(&pending).Error; err != nil { t.Fatal(err) }
	if pending.Status != "pending" || pending.Attempts != 1 { t.Fatalf("pending row = %#v", pending) }
	if len(calls) != 1 { t.Fatalf("calls = %d", len(calls)) }
	if err := db.Model(&model.CallbackDelivery{}).Where("delivery_id = ?", pending.DeliveryID).Updates(map[string]any{"status": "delivering", "next_attempt_at": time.Now().UTC()}).Error; err != nil { t.Fatal(err) }
	status = http.StatusNoContent
	New(cfg, db).Drain(t.Context())
	var delivered model.CallbackDelivery
	if err := db.Where("delivery_id = ?", pending.DeliveryID).First(&delivered).Error; err != nil { t.Fatal(err) }
	if delivered.Status != "delivered" || delivered.Attempts != 2 { t.Fatalf("delivered row = %#v", delivered) }
	if len(calls) != 2 || calls[0].header.Get(headerDeliveryID) != calls[1].header.Get(headerDeliveryID) || string(calls[0].body) != string(calls[1].body) { t.Fatal("restart retry changed signed delivery") }
	for _, call := range calls {
		bodyHash := sha256.Sum256(call.body)
		bodySHA := hex.EncodeToString(bodyHash[:])
		if call.header.Get(headerContentSHA256) != bodySHA {
			t.Fatalf("persisted delivery digest does not match sent body")
		}
		want := testSignature("fixture-secret", call.header.Get(headerKeyID), call.header.Get(headerDeliveryID), call.header.Get(headerOccurredAt), bodySHA)
		if !hmac.Equal([]byte(call.header.Get(headerSignature)), []byte(want)) {
			t.Fatalf("persisted delivery signature does not match sent body")
		}
	}
}

type capturedRequest struct {
	header http.Header
	body   []byte
}

func captureServer(t *testing.T) (*httptest.Server, func() capturedRequest) {
	t.Helper()
	var mu sync.Mutex
	var got capturedRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read callback: %v", err)
		}
		mu.Lock()
		got = capturedRequest{header: r.Header.Clone(), body: body}
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	return srv, func() capturedRequest {
		mu.Lock()
		defer mu.Unlock()
		return got
	}
}

func testTask() *model.Task {
	return &model.Task{
		TaskID: "scheduler-job-1", TaskClass: model.TaskClassFast,
		Provider: "docker", Operation: model.OperationStart,
		Status: model.TaskStatusSucceeded,
	}
}

func TestFireWithoutAuthKeepsLegacyBodyAndHeaders(t *testing.T) {
	srv, captured := captureServer(t)
	defer srv.Close()
	c := New(&config.Callback{
		Enable: true, URL: srv.URL, Method: http.MethodPost,
		Headers: map[string]string{"X-Legacy": "present"},
	})
	c.Fire(t.Context(), EventSucceeded, testTask())
	got := captured()
	if got.header.Get("X-Legacy") != "present" {
		t.Fatalf("legacy header = %q", got.header.Get("X-Legacy"))
	}
	for _, name := range []string{
		"X-MP-Sched-Callback-Version", "X-MP-Sched-Key-Id",
		"X-MP-Sched-Delivery-Id", "X-MP-Sched-Signature",
	} {
		if got.header.Get(name) != "" {
			t.Fatalf("legacy callback unexpectedly added %s", name)
		}
	}
	var payload map[string]any
	if err := json.Unmarshal(got.body, &payload); err != nil {
		t.Fatalf("legacy JSON: %v", err)
	}
	if len(payload) != 4 || payload["event"] != EventSucceeded || payload["status"] != model.TaskStatusSucceeded || payload["task_id"] != "scheduler-job-1" {
		t.Fatalf("legacy payload changed: %s", got.body)
	}
}

func TestV2DeliverySignsExactBodyAndUsesFreshReceipt(t *testing.T) {
	srv, captured := captureServer(t)
	defer srv.Close()
	c := New(&config.Callback{
		Enable: true, URL: srv.URL,
		Auth: config.CallbackAuth{KeyID: "fixture-k1", HMACSecret: "fixture-secret"},
	})
	c.Fire(t.Context(), EventFailed, testTask())
	first := captured()
	c.Fire(t.Context(), EventFailed, testTask())
	second := captured()
	for _, got := range []capturedRequest{first, second} {
		if got.header.Get("X-MP-Sched-Callback-Version") != "2" {
			t.Fatalf("version = %q", got.header.Get("X-MP-Sched-Callback-Version"))
		}
		if got.header.Get("X-MP-Sched-Key-Id") != "fixture-k1" {
			t.Fatalf("key id = %q", got.header.Get("X-MP-Sched-Key-Id"))
		}
		if !strings.HasPrefix(got.header.Get("X-MP-Sched-Occurred-At"), "20") {
			t.Fatalf("occurred-at = %q", got.header.Get("X-MP-Sched-Occurred-At"))
		}
		if _, err := time.Parse(time.RFC3339Nano, got.header.Get("X-MP-Sched-Occurred-At")); err != nil {
			t.Fatalf("occurred-at: %v", err)
		}
		bodyHash := sha256.Sum256(got.body)
		if got.header.Get("X-MP-Sched-Content-SHA256") != hex.EncodeToString(bodyHash[:]) {
			t.Fatalf("body digest does not match exact body")
		}
		expected := testSignature("fixture-secret", got.header.Get("X-MP-Sched-Key-Id"), got.header.Get("X-MP-Sched-Delivery-Id"), got.header.Get("X-MP-Sched-Occurred-At"), got.header.Get("X-MP-Sched-Content-SHA256"))
		if !hmac.Equal([]byte(expected), []byte(got.header.Get("X-MP-Sched-Signature"))) {
			t.Fatalf("signature does not verify exact body")
		}
		var payload map[string]any
		if err := json.Unmarshal(got.body, &payload); err != nil {
			t.Fatalf("v2 JSON: %v", err)
		}
		if payload["protocol_version"] != "mp_sched_callback/v2" || payload["delivery_id"] != got.header.Get("X-MP-Sched-Delivery-Id") || payload["occurred_at"] != got.header.Get("X-MP-Sched-Occurred-At") {
			t.Fatalf("body/header authority mismatch: %s", got.body)
		}
	}
	if first.header.Get("X-MP-Sched-Delivery-Id") == second.header.Get("X-MP-Sched-Delivery-Id") {
		t.Fatal("two delivery attempts reused the same delivery id")
	}
}

func TestV2ControlledDeliveryEchoesAuthorityInsideSignedBody(t *testing.T) {
	srv, captured := captureServer(t)
	defer srv.Close()
	task := testTask()
	task.Business = []byte(`{"agent_rt":{"controlled_callback":{"tenant_id":"tenant-controlled","generation":"laccm_zymctrl_v2_001","task_id":"task-controlled","work_unit_id":"s1-zymctrl","run_id":"run-controlled","attempt":1,"fencing_token":"fence-controlled","artifact_contract":"sequence_bundle/v1"}}}`)
	c := New(&config.Callback{Enable: true, URL: srv.URL, Auth: config.CallbackAuth{KeyID: "fixture-k1", HMACSecret: "fixture-secret"}})
	c.Fire(t.Context(), EventSucceeded, task)
	got := captured()
	var payload map[string]any
	if err := json.Unmarshal(got.body, &payload); err != nil { t.Fatal(err) }
	for key, want := range map[string]any{
		"tenant_id":"tenant-controlled", "generation":"laccm_zymctrl_v2_001", "task_id":"task-controlled", "work_unit_id":"s1-zymctrl", "run_id":"run-controlled", "attempt":float64(1), "fencing_token":"fence-controlled", "provider_task_ref":"scheduler-job-1", "scheduler_job_id":"scheduler-job-1", "terminal_status":EventSucceeded,
	} {
		if payload[key] != want { t.Fatalf("%s = %#v, want %#v", key, payload[key], want) }
	}
	bodyHash := sha256.Sum256(got.body)
	if got.header.Get("X-MP-Sched-Content-SHA256") != hex.EncodeToString(bodyHash[:]) { t.Fatal("authority echo was not signed") }
}

func TestV2ReservedHeadersOverrideStaticValues(t *testing.T) {
	srv, captured := captureServer(t)
	defer srv.Close()
	c := New(&config.Callback{Enable: true, URL: srv.URL,
		Headers: map[string]string{
			"X-MP-Sched-Callback-Version": "forged",
			"X-MP-Sched-Key-Id":           "forged",
			"X-MP-Sched-Signature":        "forged",
		},
		Auth: config.CallbackAuth{KeyID: "fixture-k1", HMACSecret: "fixture-secret"},
	})
	c.Fire(t.Context(), EventFailed, testTask())
	got := captured()
	if got.header.Get("X-MP-Sched-Callback-Version") != "2" || got.header.Get("X-MP-Sched-Key-Id") != "fixture-k1" || got.header.Get("X-MP-Sched-Signature") == "forged" {
		t.Fatalf("reserved header was not scheduler-owned: %#v", got.header)
	}
}

func TestV2SignatureRejectsBodyTampering(t *testing.T) {
	body := []byte(`{"event":"failed"}`)
	original := sha256.Sum256(body)
	signature := testSignature("fixture-secret", "fixture-k1", "delivery-1", "2026-07-27T00:00:00Z", hex.EncodeToString(original[:]))
	tampered := append([]byte(nil), body...)
	tampered[len(tampered)-2] = 'd'
	tamperedDigest := sha256.Sum256(tampered)
	if hmac.Equal([]byte(signature), []byte(testSignature("fixture-secret", "fixture-k1", "delivery-1", "2026-07-27T00:00:00Z", hex.EncodeToString(tamperedDigest[:])))) {
		t.Fatal("signature accepted tampered body")
	}
}

func testSignature(secret, keyID, deliveryID, occurredAt, bodySHA string) string {
	canonical := "mp_sched_callback_v2\n" +
		testLengthField(keyID) + "\n" +
		testLengthField(deliveryID) + "\n" +
		testLengthField(occurredAt) + "\n" +
		testLengthField(bodySHA) + "\n"
	h := hmac.New(sha256.New, []byte(secret))
	_, _ = h.Write([]byte(canonical))
	return hex.EncodeToString(h.Sum(nil))
}

func testLengthField(value string) string {
	return strconv.Itoa(len(value)) + ":" + value
}
