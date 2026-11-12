package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Autumn-27/artex/db"
)

func TestAutomaticRecoveryDoesNotPauseOtherWork(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	task, err := m.CreateTask("recovery isolation", "fixture", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	var taskNum int64
	fmt.Sscan(task.ID, &taskNum)
	defer m.pg.DeleteTask(taskNum)
	if err = m.pg.SetExecutionPolicy(t.Context(), taskNum, "auto"); err != nil {
		t.Fatal(err)
	}
	if err = m.pg.SetPaused(taskNum, false); err != nil {
		t.Fatal(err)
	}
	first, err := task.Store.AddIntent(map[string]any{"summary": "first"}, 0, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := task.Store.AddIntent(map[string]any{"summary": "second"}, 0, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.pg.Exec(`UPDATE exploration_nodes SET state='paused',blocked_reason=$2 WHERE id=$1`, first, db.IntentBlockedRecovery); err != nil {
		t.Fatal(err)
	}
	a := &db.InterceptAudit{RunID: "run", ToolUseID: "call", IntentID: first, InputDigest: "digest", Correlation: "exact", EffectiveAction: "allow", ExecutionStatus: "unknown"}
	id, err := m.pg.CreateDecidedIntercept(0, 0, task.ID, "worker", "Read", []byte(`{"file_path":"fixture"}`), "allowed", "fixture", a)
	if err != nil {
		t.Fatal(err)
	}
	if err = m.pg.PrepareToolExecution(t.Context(), id, "run", "call", t.TempDir(), true); err != nil {
		t.Fatal(err)
	}
	if err = m.pg.RecoverExecutionLedger(t.Context()); err != nil {
		t.Fatal(err)
	}
	s := newAdmissionTestServer(m, nil)
	if blocked, err := m.executionBlocksTask(t.Context(), task.ID); err != nil || blocked {
		t.Fatal("auto task resume blocked", err)
	}
	if blocked, err := m.executionBlocksIntent(t.Context(), task.ID, second); err != nil || blocked {
		t.Fatal("unrelated intent blocked", err)
	}
	s.recoverTaskExecutions(t.Context(), task)
	if s.engine.IsPaused(task.ID) {
		t.Fatal("recovery paused the whole task")
	}
	node, _ := task.Store.GetNode(first)
	if node.State != "open" {
		t.Fatalf("read recovery did not reopen its intent: %+v", node)
	}
	e, err := m.pg.GetExecution(t.Context(), task.ID, id)
	if err != nil || e.ExecutionState != "unknown" || e.RecoveryState != "resolved" {
		t.Fatalf("recovery fabricated execution result: %+v %v", e, err)
	}
	if _, err = m.pg.Exec(`UPDATE exploration_nodes SET state='paused',blocked_reason=NULL WHERE id=$1`, first); err != nil {
		t.Fatal(err)
	}
	s.recoverTaskExecutions(t.Context(), task)
	node, _ = task.Store.GetNode(first)
	if node.State != "paused" {
		t.Fatal("manual intent pause reopened")
	}
}

func TestExecutionRecoveryHTTPIsolation(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	first, err := m.pg.CreateTask("recovery API", "fixture", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer m.pg.DeleteTask(first.ID)
	second, err := m.pg.CreateTask("other API task", "fixture", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer m.pg.DeleteTask(second.ID)
	s := &Server{m: m, jwtKey: []byte("recovery-api-fixture")}
	token, _ := signJWT(s.jwtKey)
	handler := s.Handler()
	request := func(method, path, body string, auth bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if auth {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	base := fmt.Sprintf("/api/tasks/%d", first.ID)
	if w := request(http.MethodPut, base+"/execution-policy", `{"mode":"auto"}`, false); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := request(http.MethodPut, base+"/execution-policy", `{"mode":"auto"}`, true); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := request(http.MethodPut, base+"/execution-policy", `{"mode":"anything"}`, true); w.Code != 400 {
		t.Fatal(w.Code)
	}
	p, _ := m.pg.ExecutionPolicy(t.Context(), fmt.Sprint(second.ID))
	if p.Mode != "legacy" {
		t.Fatal("mode escaped task")
	}
	a := &db.InterceptAudit{RunID: "run", ToolUseID: "call", InputDigest: "digest", Correlation: "exact", EffectiveAction: "allow", ExecutionStatus: "unknown"}
	id, err := m.pg.CreateDecidedIntercept(0, 0, fmt.Sprint(first.ID), "worker", "Bash", []byte(`{}`), "allowed", "fixture", a)
	if err != nil {
		t.Fatal(err)
	}
	if err = m.pg.QueueExecutionRecoveries(t.Context(), fmt.Sprint(first.ID)); err != nil {
		t.Fatal(err)
	}
	rows, _ := m.pg.ListExecutionRecoveries(t.Context(), fmt.Sprint(first.ID))
	if len(rows) != 1 {
		t.Fatal(rows)
	}
	body, _ := json.Marshal(map[string]any{"version": rows[0].Version, "decision": "park", "note": "user checked the partial fixture"})
	wrong := fmt.Sprintf("/api/tasks/%d/execution-recoveries/%d/decision", second.ID, id)
	if w := request(http.MethodPost, wrong, string(body), true); w.Code != 404 {
		t.Fatal("cross-task decision", w.Code)
	}
	path := fmt.Sprintf("%s/execution-recoveries/%d/decision", base, id)
	if w := request(http.MethodPost, path, string(body), true); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := request(http.MethodPost, path, string(body), true); w.Code != 409 {
		t.Fatal("duplicate decision", w.Code)
	}
	if w := request(http.MethodPost, fmt.Sprintf("/api/intercept/pending/%d/resolve-execution", id), `{"status":"succeeded","note":"1"}`, true); w.Code != 409 {
		t.Fatal("legacy endpoint overwrote executor evidence", w.Code)
	}
}
