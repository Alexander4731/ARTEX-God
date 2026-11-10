package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/intercept"
)

func TestActionableApprovalsSurviveHistoryPagination(t *testing.T) {
	dsn, _, err := db.DSN()
	if err != nil {
		t.Skip("no test database configured")
	}
	d, err := db.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	scope := "actionable-" + t.Name()
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM intercept_pending WHERE task_id IN ($1,$2)`, scope, scope+"-other") })
	add := func(task, status, execution string) int64 {
		t.Helper()
		id, err := d.CreateDecidedIntercept(0, 0, task, "fixture", "Probe", []byte(`{}`), status, "fixture", &db.InterceptAudit{ExecutionStatus: execution})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	unknown := add(scope, "allowed", "unknown")
	pending := add(scope, "pending", "not_started")
	add(scope+"-other", "allowed", "unknown")
	for range 65 {
		add(scope, "allowed", "succeeded")
	}
	add(scope, "allowed", "awaiting_result") // live work needs no human decision
	s := &Server{m: &Manager{pg: d, interceptor: intercept.New(d)}, jwtKey: []byte("actionable-test-key")}
	h := s.Handler()
	token, err := signJWT(s.jwtKey)
	if err != nil {
		t.Fatal(err)
	}
	get := func(auth bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/api/intercept/actionable?task_id="+scope+"&status=denied&page=99", nil)
		if auth {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := get(false); w.Code != 401 {
		t.Fatalf("unprotected queue: %d", w.Code)
	}
	check := func(ids ...int64) {
		t.Helper()
		w := get(true)
		var body struct {
			Items []db.InterceptApprovalRow `json:"items"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &body) != nil {
			t.Fatalf("queue response: %d %s", w.Code, w.Body.String())
		}
		if len(body.Items) != len(ids) {
			t.Fatalf("hidden or extraneous blockers: %+v", body.Items)
		}
		for i, id := range ids {
			if body.Items[i].ID != id {
				t.Fatalf("queue=%+v want=%v", body.Items, ids)
			}
		}
	}
	check(unknown, pending)
	if won, err := d.ResolveUnknownExecution(t.Context(), unknown, "not_executed", "local fixture confirmed"); err != nil || !won {
		t.Fatal(won, err)
	}
	check(pending)
}
