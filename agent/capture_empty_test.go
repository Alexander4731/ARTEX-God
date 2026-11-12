package agent

import (
	"context"
	"testing"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/agentcore"
	"github.com/Autumn-27/norma/harness"
	"github.com/Autumn-27/norma/llm"
)

func TestEmptyCompletionIsNotSuccessfulWork(t *testing.T) {
	for _, nonStreaming := range []bool{false, true} {
		provider := captureUsageProvider{stream: func(_ context.Context, yield func(llm.StreamEvent, error) bool) {
			for _, event := range []llm.StreamEvent{{Type: llm.SEThinkingDelta, Text: "considering"}, {Type: llm.SEMessageDelta, StopReason: "end_turn"}, {Type: llm.SEMessageStop}} {
				if !yield(event, nil) {
					return
				}
			}
		}}
		var result db.Activity
		_, reason, err := captureRun(t.Context(), agentcore.Options{Provider: provider, NonStreaming: nonStreaming, MaxTurns: 1}, "test", func(a db.Activity) {
			if a.Kind == "result" {
				result = a
			}
		})
		if err == nil || reason == harness.ReasonCompleted || !result.IsError {
			t.Fatalf("nonStreaming=%v reason=%s err=%v result error=%v", nonStreaming, reason, err, result.IsError)
		}
	}
}

func TestStandaloneModelClientHasDeadline(t *testing.T) {
	client, err := quotaAwareHTTPClient(Config{})
	if err != nil {
		t.Fatal(err)
	}
	if client.Timeout != ModelRequestTimeout || client.Timeout <= 0 {
		t.Fatalf("model HTTP timeout=%s", client.Timeout)
	}
}
