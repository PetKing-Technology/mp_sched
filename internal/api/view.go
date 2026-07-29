package api

import (
	"encoding/json"
	"net/http"
	"time"

	"mp_sched/internal/model"
)

// taskView API 出参，business 为 JSON
type taskView struct {
	TaskID            string          `json:"task_id"`
	Business          json.RawMessage `json:"business"`
	TaskClass         string          `json:"task_class"`
	Provider          string          `json:"provider"`
	Operation         string          `json:"operation"`
	TargetTaskID      string          `json:"target_task_id,omitempty"`
	ResCPU            string          `json:"res_cpu,omitempty"`
	ResMemory         string          `json:"res_memory,omitempty"`
	ResGPU            string          `json:"res_gpu,omitempty"`
	MaxRuntimeSeconds int             `json:"max_runtime_seconds,omitempty"`
	Image             string          `json:"image,omitempty"`
	Status            string          `json:"status"`
	PendingReason     string          `json:"pending_reason,omitempty"`
	NextScheduleAt    string          `json:"next_schedule_at,omitempty"`
	ScheduleAttempts  int             `json:"schedule_attempts,omitempty"`
	RunningAt         string          `json:"running_at,omitempty"`
	RuntimeRef        string          `json:"runtime_ref,omitempty"`
	CreatedAt         string          `json:"created_at"`
	UpdatedAt         string          `json:"updated_at"`
}

func taskToView(t *model.Task) taskView {
	if t == nil {
		return taskView{}
	}
	bz := []byte(t.Business)
	if len(bz) == 0 {
		bz = []byte(`{}`)
	}
	out := taskView{
		TaskID:            t.TaskID,
		Business:          bz,
		TaskClass:         t.TaskClass,
		Provider:          t.Provider,
		Operation:         t.Operation,
		TargetTaskID:      t.TargetTaskID,
		ResCPU:            t.ResCPU,
		ResMemory:         t.ResMemory,
		ResGPU:            t.ResGPU,
		MaxRuntimeSeconds: t.MaxRuntimeSeconds,
		Image:             t.Image,
		Status:            t.Status,
		PendingReason:     t.PendingReason,
		ScheduleAttempts:  t.ScheduleAttempts,
		RuntimeRef:        t.RuntimeRef,
		CreatedAt:         t.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:         t.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if t.RunningAt != nil {
		out.RunningAt = t.RunningAt.UTC().Format(time.RFC3339)
	}
	if t.NextScheduleAt != nil {
		out.NextScheduleAt = t.NextScheduleAt.UTC().Format(time.RFC3339)
	}
	return out
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"ok": false, "error": msg})
}
