package intercept

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/transcript"
)

func TestRecordedExecutionRequiresOriginalExactPair(t *testing.T) {
	now := time.Now().UTC()
	input := json.RawMessage(`{"path":"fixture.txt"}`)
	execution := db.UnsettledExecution{ToolName: "Read", Audit: db.InterceptAudit{
		RunID: "run", ToolUseID: "call", InputDigest: digestInput(input), CapturedAt: now,
	}}
	for _, tc := range []struct {
		name   string
		change func([]transcript.Record) []transcript.Record
		want   bool
	}{
		{"success", func(r []transcript.Record) []transcript.Record { return r }, true},
		{"failure", func(r []transcript.Record) []transcript.Record {
			r[1].Message.Content[0].IsError = true
			return r
		}, true},
		{"missing result", func(r []transcript.Record) []transcript.Record { return r[:1] }, false},
		{"different input", func(r []transcript.Record) []transcript.Record {
			r[0].Message.Content[0].Input = json.RawMessage(`{"path":"other.txt"}`)
			return r
		}, false},
		{"different session", func(r []transcript.Record) []transcript.Record {
			r[1].SessionID = "other"
			return r
		}, false},
		{"wrong parent", func(r []transcript.Record) []transcript.Record {
			r[1].ParentUUID = "other-call"
			return r
		}, false},
		{"reused call ID", func(r []transcript.Record) []transcript.Record {
			return append(r, r[0], r[1])
		}, false},
		{"ID conflict before capture", func(r []transcript.Record) []transcript.Record {
			old := r[0]
			old.Timestamp = now.Add(-time.Second).Format(time.RFC3339Nano)
			return append([]transcript.Record{old}, r...)
		}, false},
		{"later prompt reused ID", func(r []transcript.Record) []transcript.Record {
			return append([]transcript.Record{{SessionID: "session", Timestamp: now.Add(time.Millisecond).Format(time.RFC3339Nano), Message: &llm.Message{Role: llm.RoleUser, Content: []llm.ContentBlock{llm.TextBlock("next prompt")}}}}, r...)
		}, false},
		{"old result", func(r []transcript.Record) []transcript.Record {
			r[0].Timestamp = now.Add(-time.Second).Format(time.RFC3339Nano)
			return r
		}, false},
		{"synthetic interruption", func(r []transcript.Record) []transcript.Record {
			r[1].Message.Content[0] = llm.ToolResultText("call", "tool execution interrupted", true)
			return r
		}, false},
		{"compaction replay", func(r []transcript.Record) []transcript.Record {
			return append(r, transcript.Record{Type: "boundary", Timestamp: now.Add(3 * time.Second).Format(time.RFC3339Nano)}, r[0], r[1])
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			records := []transcript.Record{
				{UUID: "assistant", SessionID: "session", Timestamp: now.Add(time.Second).Format(time.RFC3339Nano), Type: "message",
					Message: &llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{Type: llm.BlockToolUse, ID: "call", Name: "Read", Input: input}}}},
				{UUID: "result", ParentUUID: "assistant", SessionID: "session", Timestamp: now.Add(2 * time.Second).Format(time.RFC3339Nano), Type: "message",
					Message: &llm.Message{Role: llm.RoleUser, Content: []llm.ContentBlock{llm.ToolResultText("call", "recorded output", false)}}},
			}
			result, _, ok := recordedExecution(tc.change(records), "session", execution)
			if ok != tc.want || (ok && result.ToolUseID != "call") {
				t.Fatalf("recovered=%v, want=%v, result=%+v", ok, tc.want, result)
			}
		})
	}
}

func TestRecordedExecutionUsesExactTranscriptPosition(t *testing.T) {
	now := time.Now().UTC()
	input := json.RawMessage(`{}`)
	offset := int64(0)
	execution := db.UnsettledExecution{ToolName: "Read", Audit: db.InterceptAudit{
		RunID: "run", SessionID: "session", ToolUseID: "call", InputDigest: digestInput(input), CapturedAt: now,
		TranscriptOffset: &offset,
	}}
	pair := []transcript.Record{
		{UUID: "assistant", SessionID: "session", Timestamp: now.Add(time.Second).Format(time.RFC3339Nano), Message: &llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{Type: llm.BlockToolUse, ID: "call", Name: "Read", Input: input}}}},
		{ParentUUID: "assistant", SessionID: "session", Timestamp: now.Add(2 * time.Second).Format(time.RFC3339Nano), Message: &llm.Message{Role: llm.RoleUser, Content: []llm.ContentBlock{llm.ToolResultText("call", "output", false)}}},
	}
	store := transcript.NewStore(t.TempDir())
	path := store.MainPath("session")
	if err := store.Append(path, pair...); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Append(path, transcript.Record{SessionID: "session", Timestamp: now.Add(3 * time.Second).Format(time.RFC3339Nano), Message: &llm.Message{Role: llm.RoleUser, Content: []llm.ContentBlock{llm.TextBlock("next prompt")}}}); err != nil {
		t.Fatal(err)
	}
	if err := store.Append(path, pair...); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		offset int64
		want   bool
	}{
		{"original pair despite later reused ID", 0, true},
		{"new prompt cannot supply missing old result", info.Size(), false},
		{"partial record", 1, false},
		{"missing position", info.Size() + 100000, false},
		{"negative position", -1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			records, err := executionPairAt(path, tc.offset)
			_, _, ok := recordedExecution(records, "session", execution)
			if ok != tc.want || (tc.want && err != nil) {
				t.Fatalf("recovered=%v, want=%v, err=%v", ok, tc.want, err)
			}
		})
	}
	execution.Audit.TranscriptOffset = nil
	if _, _, ok := recordedExecution(pair, "session", execution); ok {
		t.Fatal("missing position was inferred")
	}
}
