package api

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"

	"mp_sched/internal/controller"
)

// POST /api/sched/v1/tasks/{taskID}/restart
func (s *Server) postRestart(w http.ResponseWriter, r *http.Request) {
	if s.H == nil {
		writeErr(w, http.StatusServiceUnavailable, "unconfigured")
		return
	}
	id := chi.URLParam(r, "taskID")
	if id == "" {
		writeErr(w, http.StatusBadRequest, "missing task id")
		return
	}
	stopT, newT, err := s.H.RestartStartWorkload(r.Context(), id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
		if errors.Is(err, controller.ErrRestartConflict) {
			writeErr(w, http.StatusConflict, err.Error())
			return
		}
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	out := map[string]any{
		"ok":          true,
		"new_task_id": newT.TaskID,
		"data":        taskToView(newT),
	}
	if stopT != nil {
		out["stop_task_id"] = stopT.TaskID
		out["stop_data"] = taskToView(stopT)
	}
	writeJSON(w, http.StatusCreated, out)
}
