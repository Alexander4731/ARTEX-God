package server

import (
	"net/http"

	"github.com/Autumn-27/artex/db"
)

func (s *Server) taskToolPolicy(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	taskID, ok := pathInt(r, "id")
	if !ok || taskID <= 0 {
		writeErr(w, http.StatusBadRequest, "bad task id")
		return
	}
	task, err := pg.GetTask(taskID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if task == nil {
		writeErr(w, http.StatusNotFound, "task not found")
		return
	}
	if r.Method == http.MethodPut {
		r.Body = http.MaxBytesReader(w, r.Body, 1024)
		var req struct {
			AllowAll *bool `json:"allow_all"`
		}
		if err := decode(r, &req); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if req.AllowAll == nil {
			writeErr(w, http.StatusBadRequest, "allow_all is required")
			return
		}
		if err := pg.SetTaskToolPolicy(r.Context(), taskID, db.TaskToolPolicy{AllowAll: *req.AllowAll}); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	policy, err := pg.GetTaskToolPolicy(r.Context(), taskID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, policy)
}
