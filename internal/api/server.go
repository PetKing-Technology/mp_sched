package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"gorm.io/gorm"

	"mp_sched/internal/controller"
	"mp_sched/internal/taskrepo"
)

// Server HTTP API（go-chi）
type Server struct {
	H *controller.Handlers
}

// NewHandler 注册路由
func (s *Server) NewHandler() http.Handler {
	if s == nil || s.H == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "unconfigured", http.StatusServiceUnavailable)
		})
	}
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
	r.Route("/v1", func(r chi.Router) {
		r.Post("/tasks", s.postTasks)
		r.Get("/tasks", s.getTasks)
		r.Get("/tasks/{taskID}", s.getTask)
		r.Post("/tasks/{taskID}/stop", s.postStop)
		r.Post("/tasks/{taskID}/restart", s.postRestart)
		r.Route("/telemetry", func(r chi.Router) {
			r.Get("/scheduler-logs", s.getSchedulerLogs)
			r.Get("/controller-logs", s.getControllerLogs)
			r.Get("/docker-stats", s.getDockerStats)
			r.Get("/docker-log-lines", s.getDockerLogLines)
		})
	})
	return r
}

// Start 监听并随 ctx 关闭
func (s *Server) Start(ctx context.Context, addr string) *http.Server {
	if addr == "" {
		addr = ":8080"
	}
	hs := &http.Server{Addr: addr, Handler: s.NewHandler()}
	go func() {
		<-ctx.Done()
		sh, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = hs.Shutdown(sh)
	}()
	go func() {
		if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("api listen", "err", err.Error())
		}
	}()
	return hs
}

func (s *Server) postTasks(w http.ResponseWriter, r *http.Request) {
	var req controller.Request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	t, err := s.H.Enqueue(r.Context(), &req)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"ok":      true,
		"task_id": t.TaskID,
		"status":  t.Status,
		"data":    taskToView(t),
	})
}

func (s *Server) getTasks(w http.ResponseWriter, r *http.Request) {
	if s.H.Repo == nil {
		writeErr(w, http.StatusServiceUnavailable, "no repo")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	f := taskrepo.ListFilter{
		Status:   r.URL.Query().Get("status"),
		Provider: r.URL.Query().Get("provider"),
		Limit:    limit,
		Offset:   offset,
	}
	items, total, err := s.H.Repo.ListTasks(f)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]taskView, 0, len(items))
	for i := range items {
		out = append(out, taskToView(&items[i]))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":     true,
		"total":  total,
		"limit":  limit,
		"offset": offset,
		"items":  out,
	})
}

func (s *Server) getTask(w http.ResponseWriter, r *http.Request) {
	if s.H.Repo == nil {
		writeErr(w, http.StatusServiceUnavailable, "no repo")
		return
	}
	id := chi.URLParam(r, "taskID")
	t, err := s.H.Repo.Get(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "data": taskToView(t)})
}

func (s *Server) postStop(w http.ResponseWriter, r *http.Request) {
	tid := chi.URLParam(r, "taskID")
	if tid == "" {
		writeErr(w, http.StatusBadRequest, "missing task id")
		return
	}
	t, err := s.H.EnqueueStopForTarget(r.Context(), tid)
	if err != nil {
		st := http.StatusBadRequest
		if errors.Is(err, gorm.ErrRecordNotFound) {
			st = http.StatusNotFound
		}
		writeErr(w, st, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"ok":         true,
		"task_id":    t.TaskID,
		"status":     t.Status,
		"target_id":  tid,
		"data":       taskToView(t),
	})
}
