package db

import "testing"

func TestUsageLedgerDeduplicatesAndOverridesActivity(t *testing.T) {
	d, _, _ := testSetup(t)
	defer d.Close()
	exp, err := d.CreateExploration("ledger-test", "fixture")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Exec("DELETE FROM explorations WHERE id=$1", exp)
	defer d.Exec("DELETE FROM llm_usage WHERE exploration_id=$1", exp)
	store := d.Exploration(exp)
	intent, err := store.AddIntent(map[string]any{"summary": "fixture"}, 1, nil, "planner")
	if err != nil {
		t.Fatal(err)
	}
	large := 1000
	if _, err := store.AppendActivity(Activity{Kind: "result", Worker: "worker", NodeID: &intent, InputTokens: &large}); err != nil {
		t.Fatal(err)
	}
	row := &LLMUsage{ExplorationID: exp, IntentID: intent, Worker: "worker", CallID: "ledger-test-call", AttemptID: 1, InputTokens: 7, OutputTokens: 3, CacheRead: 2, Status: "ok"}
	for range 2 {
		if err := d.InsertLLMUsage(row); err != nil {
			t.Fatal(err)
		}
	}
	row.AttemptID = 2
	row.InputTokens = 11
	if err := d.InsertLLMUsage(row); err != nil {
		t.Fatal(err)
	}
	total, err := store.TokenTotal()
	if err != nil {
		t.Fatal(err)
	}
	if total.InputTokens != 18 || total.OutputTokens != 6 || total.CacheReadTokens != 4 {
		t.Fatalf("ledger totals: %+v", total)
	}
	sessions, err := store.TokenStatsBySession()
	if err != nil || len(sessions) != 1 || sessions[0].InputTokens != 18 {
		t.Fatalf("session totals: %+v %v", sessions, err)
	}
	row.CallID = "foreign-intent"
	row.IntentID = intent + 1000000
	if err := d.InsertLLMUsage(row); err != nil {
		t.Fatal(err)
	}
	var missing bool
	if err := d.QueryRow("SELECT intent_id IS NULL FROM llm_usage WHERE call_id=$1", row.CallID).Scan(&missing); err != nil || !missing {
		t.Fatalf("invalid intent retained: %v", err)
	}
}
