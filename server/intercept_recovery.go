package server

import (
	"net/http"
	"strings"
)

func (s *Server) interceptResolveExecution(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	id, ok := pathInt(r, "id")
	if !ok {
		writeErr(w, 400, "bad approval id")
		return
	}
	var request struct {
		Status string `json:"status"`
		Note   string `json:"note"`
	}
	if err := decode(r, &request); err != nil {
		writeErr(w, 400, "请求格式错误")
		return
	}
	if request.Status != "succeeded" && request.Status != "failed" && request.Status != "not_executed" {
		writeErr(w, 400, "核验结果无效")
		return
	}
	if strings.TrimSpace(request.Note) == "" {
		writeErr(w, 400, "请填写核验依据")
		return
	}
	won, err := pg.ResolveUnknownExecution(r.Context(), id, request.Status, request.Note)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if !won {
		writeErr(w, 409, "记录不再需要核验，请刷新")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
