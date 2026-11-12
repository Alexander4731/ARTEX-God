package agent

import (
	"strings"
	"testing"
)

func TestWorkerFocusContractPreservesCustomPrompt(t *testing.T) {
	previous := PromptOverride
	defer func() { PromptOverride = previous }()
	PromptOverride = func(string) (string, bool) { return "custom specialist instructions", true }
	prompt := workerSystem("", "", "data", "work")
	if !strings.Contains(prompt, "custom specialist instructions") || !strings.Contains(prompt, workerFocusContract) {
		t.Fatal("missing customized prompt or runtime contract")
	}
	if !strings.Contains(plannerSystem("fixture goal", "data", "work"), plannerFocusContract) {
		t.Fatal("planner missing direction contract")
	}
	ctx := WithTaskObjective(t.Context(), "verify the fixture without modifying it")
	if !strings.Contains(taskObjectiveBlock(ctx), "verify the fixture without modifying it") {
		t.Fatal("objective not retained")
	}
	if taskObjectiveBlock(t.Context()) != "" {
		t.Fatal("invented objective")
	}
}
