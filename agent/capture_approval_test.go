package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/guard"
	"github.com/Autumn-27/artex/intercept"
	"github.com/Autumn-27/norma/agentcore"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/permission"
	"github.com/Autumn-27/norma/tool"
	"github.com/Autumn-27/norma/transcript"
)

// Exercise the actual SDK event -> hook -> execution -> result path. No real
// model or command is used; the probe tool only returns a fixed string.
func TestCaptureApprovalLifecycle(t *testing.T) {
	dsn, _, err := db.DSN()
	if err != nil {
		t.Skip("no test database configured")
	}
	d, err := db.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	ic := intercept.New(d)
	priorTools, err := ic.GetEnabledTools()
	if err != nil {
		t.Fatal(err)
	}
	priorConfig := ic.GetJudgeConfig()
	t.Cleanup(func() { _ = ic.SetEnabledTools(priorTools); _ = ic.SetJudgeConfig(priorConfig) })
	if err := ic.SetEnabledTools([]string{"ApprovalAuditProbe"}); err != nil {
		t.Fatal(err)
	}
	if err := ic.SetJudgeConfig(intercept.JudgeConfig{Enabled: true, AskTimeoutSeconds: 1, AskTimeoutAction: "allow"}); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name, action, status, execution          string
		manual, approve, toolError, writeFailure bool
	}{
		{"model_fallback", "invalid", "denied", "not_executed", true, false, false, false},
		{"model_allow", "allow", "allowed", "succeeded", false, false, false, false},
		{"model_deny", "deny", "denied", "not_executed", false, false, false, false},
		{"human_allow_tool_error", "ask", "allowed", "failed", true, true, true, false},
		{"human_deny", "ask", "denied", "not_executed", true, false, false, false},
		{"timeout_allow", "ask", "timeout", "succeeded", false, false, false, false},
		{"write_failure_success", "allow", "allowed", "succeeded", false, false, false, true},
		{"write_failure_error", "allow", "allowed", "failed", false, false, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ic.SetReviewer(func(context.Context, int64, string, intercept.ReviewInput) (intercept.Decision, error) {
				return intercept.Decision{Action: tc.action, Message: "probe review", ProfileID: 7}, nil
			})
			g := guard.NewWithInterceptor(ic)
			taskID := "approval-lifecycle-" + tc.name
			t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM intercept_pending WHERE task_id=$1`, taskID) })
			if tc.writeFailure {
				if _, err := d.Exec(fmt.Sprintf(`CREATE FUNCTION approval_result_failure() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'injected result write failure'; END $$;
CREATE TRIGGER approval_result_failure BEFORE UPDATE ON intercept_pending FOR EACH ROW
WHEN (NEW.task_id='%s' AND NEW.audit->>'execution_status' IN ('succeeded','failed'))
EXECUTE FUNCTION approval_result_failure()`, taskID)); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					_, _ = d.Exec(`DROP TRIGGER IF EXISTS approval_result_failure ON intercept_pending; DROP FUNCTION IF EXISTS approval_result_failure()`)
				})
			}
			ctx := intercept.WithTaskContext(t.Context(), taskID, "test-agent", func(a db.Activity) {
				if a.Kind != "intercept_request" || !tc.manual {
					return
				}
				var detail struct {
					ID int64 `json:"pending_id"`
				}
				if err := json.Unmarshal([]byte(a.Detail), &detail); err != nil {
					t.Error(err)
					return
				}
				if err := ic.Decide(detail.ID, tc.approve); err != nil {
					t.Error(err)
				}
			})
			ctx = intercept.WithIntent(ctx, 36)
			turn, executions := 0, 0
			provider := captureUsageProvider{stream: func(_ context.Context, yield func(llm.StreamEvent, error) bool) {
				turn++
				events := []llm.StreamEvent{{Type: llm.SETextDelta, Text: "done"}, {Type: llm.SEMessageDelta, StopReason: "end_turn"}}
				if turn == 1 {
					events = []llm.StreamEvent{
						{Type: llm.SEToolUseStart, ToolID: "probe-call", ToolName: "ApprovalAuditProbe"},
						{Type: llm.SEToolInputJSON, Text: `{}`}, {Type: llm.SEMessageDelta, StopReason: "tool_use"},
					}
				}
				for _, event := range events {
					if !yield(event, nil) {
						return
					}
				}
			}}
			probe := tool.Build(tool.Spec{Name: "ApprovalAuditProbe", Schema: map[string]any{"type": "object"},
				Run: func(context.Context, json.RawMessage, *tool.ToolContext) (tool.Result, error) {
					executions++
					if tc.toolError {
						return tool.Errorf("probe failed"), nil
					}
					return tool.Text("probe succeeded"), nil
				},
			})
			store := transcript.NewStore(t.TempDir())
			sessionID := "approval-" + tc.name
			_, _, err := captureRun(ctx, agentcore.Options{Provider: provider, Tools: []tool.CoreTool{probe}, Hooks: g.Hooks(),
				Transcript: store, SessionID: sessionID,
				PermissionMode: permission.ModeBypass, WorkingDir: t.TempDir(), MaxTurns: 2}, "record this review", nil)
			if err != nil {
				t.Fatal(err)
			}
			rows, err := d.ListTaskIntercepts(taskID)
			if err != nil || len(rows) != 1 {
				t.Fatalf("rows=%d err=%v", len(rows), err)
			}
			if tc.writeFailure {
				before, err := d.GetInterceptDetail(rows[0].ID)
				if err != nil || before.Audit.ExecutionStatus != "awaiting_result" {
					t.Fatalf("result-write fault was not reproduced: %+v %v", before, err)
				}
				if _, err := d.Exec(`DROP TRIGGER approval_result_failure ON intercept_pending; DROP FUNCTION approval_result_failure()`); err != nil {
					t.Fatal(err)
				}
				if err := d.FinishIntentExecutionAudit(taskID, 36); err != nil {
					t.Fatal(err)
				}
				// A cold resume and a later Prompt must preserve the original pair
				// and the SDK's parent chain, without supplying a different result.
				resumed := agentcore.NewSession(agentcore.Options{Provider: provider, Transcript: store, SessionID: sessionID, MaxTurns: 1})
				defer resumed.Close()
				if err := resumed.Resume(sessionID); err != nil {
					t.Fatal(err)
				}
				if _, _, err := captureRunSession(ctx, resumed, store, "next prompt", nil); err != nil {
					t.Fatal(err)
				}
				records, err := store.Load(store.MainPath(sessionID))
				if err != nil {
					t.Fatal(err)
				}
				for index := 1; index < len(records); index++ {
					if records[index].ParentUUID != records[index-1].UUID {
						t.Fatal("SDK transcript parent chain changed")
					}
				}
				if err := ic.RecoverExecutions(t.Context(), store, taskID); err != nil {
					t.Fatal(err)
				}
				if unknown, err := d.HasUnknownTaskExecution(taskID); err != nil || unknown {
					t.Fatalf("persisted result still blocks resume: %v %v", unknown, err)
				}
			}
			detail, err := d.GetInterceptDetail(rows[0].ID)
			if err != nil {
				t.Fatal(err)
			}
			a := detail.Audit
			if a.SessionID != sessionID || a.TranscriptOffset == nil {
				t.Fatalf("SDK transcript session was not recorded: %q", a.SessionID)
			}
			initialAction := tc.action
			if tc.action == "invalid" {
				initialAction = "ask"
			}
			wantUserMessage := "record this review"
			if initialAction == "allow" {
				// Routine automatic allows keep decision/execution metadata but
				// intentionally omit the bulky replay context from the audit row.
				wantUserMessage = ""
			}
			if detail.Status != tc.status || detail.DecisionSource != "model" || a == nil || a.ExecutionStatus != tc.execution || a.ToolUseID != "probe-call" || a.UserMessage != wantUserMessage || a.InitialAction != initialAction || a.ModelFallback != (tc.action == "invalid") || a.ProfileID != 7 {
				t.Fatalf("unexpected review: row=%+v audit=%+v", detail.InterceptApprovalRow, a)
			}
			if initialAction == "allow" && (len(a.Context) != 0 || a.UserTruncated || a.ContextTruncated) {
				t.Fatalf("routine allow retained replay context: %+v", a)
			}
			wantCalls := 1
			if tc.execution == "not_executed" {
				wantCalls = 0
			}
			if executions != wantCalls {
				t.Fatalf("tool ran %d times, wanted %d", executions, wantCalls)
			}
		})
	}
}
