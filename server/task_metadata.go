package server

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/Autumn-27/artex/db"
)

const (
	maxTaskNameRunes           = 200
	maxTaskMetadataRequestSize = 256 << 10
	maxTaskTextRunes           = 16000
)

// Editing content does not restart a terminal task or mutate its evidence graph.
func (s *Server) updateTaskMetadata(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	if _, ok := s.m.Task(taskID); !ok {
		writeErr(w, http.StatusNotFound, "task not found")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxTaskMetadataRequestSize)
	var request struct {
		Name        *string `json:"name"`
		Pinned      *bool   `json:"pinned"`
		Description *string `json:"description"`
		Goal        *string `json:"goal"`
	}
	if err := decode(r, &request); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeErr(w, http.StatusRequestEntityTooLarge, "请求正文过大")
		} else {
			writeErr(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	if request.Name == nil && request.Pinned == nil && request.Description == nil && request.Goal == nil {
		writeErr(w, http.StatusBadRequest, "至少需要提供 name、pinned、description 或 goal")
		return
	}
	if request.Name != nil {
		name := strings.TrimSpace(*request.Name)
		if name == "" {
			writeErr(w, http.StatusBadRequest, "任务名称不能为空")
			return
		}
		if utf8.RuneCountInString(name) > maxTaskNameRunes {
			writeErr(w, http.StatusBadRequest, fmt.Sprintf("任务名称最多 %d 个字符", maxTaskNameRunes))
			return
		}
		request.Name = &name
	}
	for _, field := range []struct {
		name  string
		value **string
	}{{"描述", &request.Description}, {"目的", &request.Goal}} {
		if *field.value == nil {
			continue
		}
		text := strings.TrimSpace(**field.value)
		if text == "" || utf8.RuneCountInString(text) > maxTaskTextRunes {
			writeErr(w, 400, fmt.Sprintf("%s不能为空且最多 %d 个字符", field.name, maxTaskTextRunes))
			return
		}
		*field.value = &text
	}
	s.concMu.Lock()
	defer s.concMu.Unlock()
	if !s.engine.beginTaskOperation(taskID) {
		writeErr(w, http.StatusConflict, "任务正在删除，请刷新")
		return
	}
	defer s.engine.decInflight(taskID)
	task, err := s.m.UpdateTaskMetadata(taskID, db.TaskPatch{Name: request.Name, Pinned: request.Pinned, Description: request.Description, Goal: request.Goal})
	if err != nil {
		if errors.Is(err, db.ErrTaskContentRunning) {
			writeErr(w, http.StatusConflict, err.Error())
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if task == nil {
		writeErr(w, http.StatusNotFound, "task not found")
		return
	}
	writeJSON(w, http.StatusOK, taskDTO(task, s.resolvedTaskStatus(task)))
}
