package server

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/transcript"
)

func executionRecoveryFixture(t *testing.T, m *Manager, status string, legacy bool, mainSegment ...int) (*Task, int64, db.UnsettledExecution) {
	t.Helper()
	task, err := m.CreateTask("execution recovery", "verify persisted tool result", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	taskID, _ := strconv.ParseInt(task.ID, 10, 64)
	t.Cleanup(func() {
		_, _ = m.pg.Exec(`DELETE FROM intercept_pending WHERE task_id=$1`, task.ID)
		_ = m.pg.DeleteTask(taskID)
	})
	intent, err := task.Store.AddIntent(map[string]any{"summary": "fixture"}, 0, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	input := json.RawMessage(`{"path":"fixture.txt"}`)
	session := agent.WorkerSessionID(task.ExpID, intent)
	agentName := "work#1"
	if len(mainSegment) > 0 {
		agentName = "mainagent"
		intent = 0
		session = fmt.Sprintf("exp%d-main", task.ExpID)
		if mainSegment[0] > 0 {
			session += fmt.Sprintf("-s%d", mainSegment[0])
		}
	}
	activity := db.Activity{Worker: agentName, Kind: "tool_use", Tool: "Read", ToolUseID: "fixture-call"}
	if len(mainSegment) > 0 {
		activity.MainSeg = &mainSegment[0]
	}
	if _, err := task.Store.AppendActivity(activity); err != nil {
		t.Fatal(err)
	}
	audit := db.InterceptAudit{IntentID: intent, RunID: "fixture-run", SessionID: session,
		ToolUseID: "fixture-call", Correlation: "exact", InputDigest: fmt.Sprintf("%x", sha256.Sum256(input)),
		CapturedAt: time.Now().UTC(), EffectiveAction: "allow", ExecutionStatus: status}
	if legacy {
		audit.SessionID = ""
	} else {
		offset := int64(0)
		audit.TranscriptOffset = &offset
	}
	id, err := m.pg.CreateDecidedIntercept(0, 0, task.ID, agentName, "Read", input, "allowed", "fixture", &audit)
	if err != nil {
		t.Fatal(err)
	}
	store := transcript.NewStore(filepath.Join(m.dir, "transcripts"))
	writer := store.NewWriter(session, "")
	writer.RecordMessage(llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{Type: llm.BlockToolUse, ID: audit.ToolUseID, Name: "Read", Input: input}}}, llm.Usage{})
	writer.RecordMessage(llm.Message{Role: llm.RoleUser, Content: []llm.ContentBlock{llm.ToolResultText(audit.ToolUseID, "Error: context canceled", true)}}, llm.Usage{})
	if err := writer.Err(); err != nil {
		t.Fatal(err)
	}
	return task, id, db.UnsettledExecution{ID: id, Audit: audit}
}

func TestRecoveryOfMainAgentAwaitingResult(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		for _, segment := range []int{0, 2} {
			t.Run(fmt.Sprintf("legacy=%v/segment=%d", legacy, segment), func(t *testing.T) {
				m, err := NewManager(t.TempDir(), "")
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = m.Close() })
				task, id, _ := executionRecoveryFixture(t, m, "awaiting_result", legacy, segment)
				if unknown, err := m.hasUnknownTaskExecution(task.ID); err != nil || unknown {
					t.Fatal(unknown, err)
				}
				detail, err := m.pg.GetInterceptDetail(id)
				if err != nil || detail.Audit.ExecutionStatus != "failed" {
					t.Fatalf("main-agent result not recovered: %+v %v", detail, err)
				}
			})
		}
	}
}

func TestResumeKeepsUnverifiableExecutionBlocked(t *testing.T) {
	for _, damage := range []string{"missing", "corrupt", "missing result"} {
		t.Run(damage, func(t *testing.T) {
			m, err := NewManager(t.TempDir(), "")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = m.Close() })
			task, id, audit := executionRecoveryFixture(t, m, "unknown", false)
			store := transcript.NewStore(filepath.Join(m.dir, "transcripts"))
			path := store.MainPath(audit.Audit.SessionID)
			switch damage {
			case "missing":
				err = os.Remove(path)
			case "corrupt":
				err = os.WriteFile(path, []byte("incomplete json\n"), 0600)
			case "missing result":
				var records []transcript.Record
				records, err = store.Load(path)
				if err == nil {
					err = os.Remove(path)
				}
				if err == nil {
					err = store.Append(path, records[:len(records)-1]...)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := m.ApplyTaskPause(task.ID); err != nil {
				t.Fatal(err)
			}
			s := newAdmissionTestServer(m, func(*Task) bool { return false })
			if _, err := s.admitPausedTask(task); err == nil {
				t.Fatal("unverified execution admitted")
			}
			detail, err := m.pg.GetInterceptDetail(id)
			if err != nil || detail.Audit.ExecutionStatus != "unknown" {
				t.Fatal("unverified result was rewritten", err)
			}
		})
	}
}

func TestResumeRecoversRecordedExecutionFailure(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	task, id, stale := executionRecoveryFixture(t, m, "unknown", false)
	if err := m.ApplyTaskPause(task.ID); err != nil {
		t.Fatal(err)
	}
	s := newAdmissionTestServer(m, func(*Task) bool { return false })
	if _, err := s.admitPausedTask(task); err != nil {
		t.Fatalf("recorded tool result blocked resume: %v", err)
	}
	detail, err := m.pg.GetInterceptDetail(id)
	if err != nil || detail.Audit.ExecutionStatus != "failed" || detail.Audit.Output != "Error: context canceled" {
		t.Fatalf("wrong recovered result: %+v %v", detail, err)
	}
	if err := m.pg.RecoverInterceptExecution(t.Context(), stale, "succeeded", "late duplicate", false, time.Now()); err != nil {
		t.Fatal(err)
	}
	detail, _ = m.pg.GetInterceptDetail(id)
	if detail.Audit.ExecutionStatus != "failed" {
		t.Fatal("late recovery overwrote a settled result")
	}
}

func TestStartupRecoversLegacyExecutionBeforePausingTask(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	task, id, _ := executionRecoveryFixture(t, m, "awaiting_result", true)
	lock, err := m.AcquireRuntime(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	detail, err := m.pg.GetInterceptDetail(id)
	if err != nil || detail.Audit.ExecutionStatus != "failed" {
		t.Fatalf("legacy result was not recovered: %+v %v", detail, err)
	}
	live, err := m.pg.GetTask(mustTaskID(t, task.ID))
	if err != nil || live.Paused {
		t.Fatalf("verified execution unnecessarily paused task: %+v %v", live, err)
	}
}

func TestRecoveryPreservesHumanVerification(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	_, id, stale := executionRecoveryFixture(t, m, "unknown", false)
	if won, err := m.pg.ResolveUnknownExecution(t.Context(), id, "not_executed", "checked actual target"); err != nil || !won {
		t.Fatal(won, err)
	}
	if err := m.pg.RecoverInterceptExecution(t.Context(), stale, "failed", "stale result", false, time.Now()); err != nil {
		t.Fatal(err)
	}
	detail, _ := m.pg.GetInterceptDetail(id)
	if detail.Audit.ExecutionStatus != "not_executed" || detail.Audit.Output != "checked actual target" {
		t.Fatal("recovery overwrote human verification")
	}
}

func TestLegacyMainAgentAmbiguousSegmentStaysUnknown(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	task, id, _ := executionRecoveryFixture(t, m, "unknown", true, 0)
	segment := 1
	if _, err := task.Store.AppendActivity(db.Activity{Worker: "mainagent", Kind: "tool_use", Tool: "Read", ToolUseID: "fixture-call", MainSeg: &segment}); err != nil {
		t.Fatal(err)
	}
	if unknown, err := m.hasUnknownTaskExecution(task.ID); err != nil || !unknown {
		t.Fatal("ambiguous legacy session recovered", unknown, err)
	}
	detail, err := m.pg.GetInterceptDetail(id)
	if err != nil || detail.Audit.ExecutionStatus != "unknown" {
		t.Fatal("ambiguous result rewritten", err)
	}
}
