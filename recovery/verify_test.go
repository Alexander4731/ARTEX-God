package recovery

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/llm"
)

func TestVerifierChecksEvidenceAndBudget(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "result.txt"), []byte("18 rows completed; 2 unfinished"), 0600)
	e := db.ExecutionRecord{ID: 1, EvidenceVersion: 3, ToolName: "Bash", Input: json.RawMessage(`{"command":"fixture"}`), Process: json.RawMessage(`{}`), ExecutionState: "unknown"}
	calls := 0
	complete := func(_ context.Context, req llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
		calls++
		if len(req.Tools) > 0 {
			t.Fatal("verifier has unrestricted tools")
		}
		text := `{"execution_id":1,"evidence_version":3,"effect_state":"indeterminate","decision":"inspect","reason":"检查输出","evidence_ids":[],"checks":["result.txt","../outside.txt"]}`
		if calls == 2 {
			text = `{"execution_id":1,"evidence_version":3,"effect_state":"partial","decision":"continue_with_partial","reason":"产物包含部分完成结果，保留原执行未知状态","evidence_ids":["file-1"]}`
		}
		return llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{llm.TextBlock(text)}}, "", llm.Usage{}, nil
	}
	d, evidence, err := Review(t.Context(), complete, dir, "parse 20 rows", e)
	if err != nil || calls != 2 || d.Decision != "continue_with_partial" {
		t.Fatal(d, calls, err)
	}
	if evidence[len(evidence)-1].Kind != "unavailable" {
		t.Fatal("outside path accepted")
	}
	for _, verdict := range []Decision{
		{Decision: "continue", EffectState: "achieved", Reason: "model feels certain", EvidenceIDs: []string{"missing"}},
		{Decision: "continue", EffectState: "achieved", Reason: "model feels certain", EvidenceIDs: []string{"execution"}},
		{Decision: "continue", EffectState: "indeterminate", Reason: "model feels certain", EvidenceIDs: []string{"file-1"}},
	} {
		if ValidateDecision(verdict, evidence) == nil {
			t.Fatalf("invalid verdict accepted: %+v", verdict)
		}
	}
}

func TestVerifierDoesNotLoopOnInventedChecks(t *testing.T) {
	calls := 0
	complete := func(context.Context, llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
		calls++
		return llm.Message{Content: []llm.ContentBlock{llm.TextBlock(`{"execution_id":1,"evidence_version":0,"effect_state":"indeterminate","decision":"inspect","reason":"need more","checks":["missing.txt"]}`)}}, "", llm.Usage{}, nil
	}
	_, _, err := Review(t.Context(), complete, t.TempDir(), "fixture", db.ExecutionRecord{ID: 1, Input: json.RawMessage(`{}`), Process: json.RawMessage(`{}`)})
	if err == nil || calls != 2 {
		t.Fatal("unbounded verifier", calls, err)
	}
}
