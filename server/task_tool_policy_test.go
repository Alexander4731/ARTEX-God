package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/guard"
	"github.com/Autumn-27/artex/intercept"
)

func TestTaskToolPolicyHTTPIsolation(t *testing.T) {
	dsn, _, err := db.DSN()
	if err != nil {
		t.Skip("test PostgreSQL not configured")
	}
	d, err := db.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	first, err := d.CreateTask("tool policy first", "local test", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	second, err := d.CreateTask("tool policy second", "local test", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteTask(first.ID)
	defer d.DeleteTask(second.ID)
	ic := intercept.New(d)
	beforeScope, err := ic.GetEnabledTools()
	if err != nil {
		t.Fatal(err)
	}
	beforeRules, err := d.ListInterceptRules()
	if err != nil {
		t.Fatal(err)
	}
	rule, err := d.CreateInterceptRule("task policy test", "tool_input", "string", "task-policy-probe", "deny", "fixture blocked", 10000, true, false, 0, "deny")
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteInterceptRule(rule.ID)
	s := &Server{m: &Manager{pg: d, interceptor: ic}, jwtKey: []byte("task-tool-policy-test")}
	token, err := signJWT(s.jwtKey)
	if err != nil {
		t.Fatal(err)
	}
	h := s.Handler()
	request := func(method, id, body string, auth bool) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, "/api/tasks/"+id+"/tool-policy", strings.NewReader(body))
		if auth {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	firstID, secondID := fmt.Sprint(first.ID), fmt.Sprint(second.ID)
	for _, method := range []string{http.MethodGet, http.MethodPut} {
		if got := request(method, firstID, `{"allow_all":true}`, false); got.Code != 401 {
			t.Fatalf("unprotected %s: %d", method, got.Code)
		}
	}
	for _, body := range []string{`{}`, `{"allow_all":null}`, `{"allow_all":"true"}`} {
		if got := request(http.MethodPut, firstID, body, true); got.Code != 400 {
			t.Fatalf("invalid body accepted: %s %d", body, got.Code)
		}
	}
	if got := request(http.MethodPut, "-1", `{"allow_all":true}`, true); got.Code != 400 {
		t.Fatalf("bad ID: %d", got.Code)
	}
	if got := request(http.MethodPut, "9223372036854775807", `{"allow_all":true}`, true); got.Code != 404 {
		t.Fatalf("unknown task: %d", got.Code)
	}
	get := func(id string, want bool) {
		t.Helper()
		rec := request(http.MethodGet, id, "", true)
		var policy db.TaskToolPolicy
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &policy) != nil || policy.AllowAll != want {
			t.Fatalf("GET policy: %d %s", rec.Code, rec.Body.String())
		}
	}
	set := func(value bool) {
		t.Helper()
		rec := request(http.MethodPut, firstID, fmt.Sprintf(`{"allow_all":%t}`, value), true)
		if rec.Code != 200 {
			t.Fatalf("PUT policy: %d %s", rec.Code, rec.Body.String())
		}
		get(firstID, value)
	}
	g := guard.NewWithInterceptor(ic)
	check := func(id string, wantBlocked bool) {
		t.Helper()
		ctx := context.Background()
		if id != "" {
			ctx = intercept.WithTaskContext(ctx, id, "policy-test", nil)
		}
		blocked, reason, _ := g.Hooks().PreToolUse(ctx, "Bash", []byte(`{"command":"echo task-policy-probe"}`))
		if blocked != wantBlocked {
			t.Fatalf("task %q blocked=%v want=%v reason=%s", id, blocked, wantBlocked, reason)
		}
	}
	get(firstID, false)
	get(secondID, false)
	check(firstID, true)
	set(true)
	check(firstID, false)
	check(secondID, true)
	check("", true)
	get(secondID, false)
	// A fresh interceptor observes the persisted override too.
	g = guard.NewWithInterceptor(intercept.New(d))
	check(firstID, false)
	var logged int
	if err := d.QueryRow(`SELECT count(*) FROM intercept_pending WHERE task_id=$1 AND status='allowed' AND reason='任务策略：直接放行所有工具'`, firstID).Scan(&logged); err != nil || logged == 0 {
		t.Fatalf("missing allow audit: count=%d err=%v", logged, err)
	}
	set(false)
	check(firstID, true)
	// Deleting a task removes the exception; it cannot leak to a new task.
	set(true)
	if _, err := d.Exec(`DELETE FROM tasks WHERE id=$1`, first.ID); err != nil {
		t.Fatal(err)
	}
	policy, err := d.GetTaskToolPolicy(t.Context(), first.ID)
	if err != nil || policy.AllowAll {
		t.Fatalf("deleted task retained policy: %+v %v", policy, err)
	}
	if err := d.DeleteInterceptRule(rule.ID); err != nil {
		t.Fatal(err)
	}
	afterScope, err := ic.GetEnabledTools()
	if err != nil {
		t.Fatal(err)
	}
	afterRules, err := d.ListInterceptRules()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(beforeScope, afterScope) || !reflect.DeepEqual(beforeRules, afterRules) {
		t.Fatal("task policy changed global settings")
	}
}
