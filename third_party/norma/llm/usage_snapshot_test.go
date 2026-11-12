package llm

import "testing"

func TestCumulativeUsageAndCustomDeltas(t *testing.T) {
	acc := NewAccumulator()
	frames := []string{
		`{"type":"message_start","message":{"usage":{"input_tokens":10,"cache_read_input_tokens":20,"output_tokens":1}}}`,
		`{"type":"message_delta","usage":{"input_tokens":10,"cache_read_input_tokens":20,"output_tokens":4}}`,
		`{"type":"message_delta","usage":{"input_tokens":10,"cache_read_input_tokens":20,"output_tokens":9}}`,
	}
	for _, f := range frames {
		ev, ok, err := parseAnthropicFrame(f)
		if err != nil || !ok {
			t.Fatal(err)
		}
		acc.Add(ev)
	}
	if acc.Usage != (Usage{InputTokens: 30, CacheReadTokens: 20, OutputTokens: 9}) {
		t.Fatalf("echo: %+v", acc.Usage)
	}
	acc = NewAccumulator()
	ev, _, _ := parseAnthropicFrame(`{"type":"message_delta","usage":{"input_tokens":42,"output_tokens":7}}`)
	acc.Add(ev)
	if acc.Usage.InputTokens != 42 {
		t.Fatal("delta-only usage lost")
	}
	acc = NewAccumulator()
	for range 2 {
		acc.Add(StreamEvent{Type: SEMessageDelta, Usage: Usage{OutputTokens: 4}})
	}
	if acc.Usage.OutputTokens != 8 {
		t.Fatal("custom delta semantics changed")
	}
}

func TestParallelToolInputAndSignatureIndices(t *testing.T) {
	acc := NewAccumulator()
	started := map[int]bool{}
	frames := []string{
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c0","function":{"name":"probe","arguments":"{\"x\":"}},{"index":1,"id":"c1","function":{"name":"probe","arguments":"{\"x\":"}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"1}"},"extra_content":{"google":{"thought_signature":"sig0"}}},{"index":1,"function":{"arguments":"2}"},"extra_content":{"google":{"thought_signature":"sig1"}}}]}}]}`,
	}
	for _, f := range frames {
		evs, _, _, _ := parseOpenAIFrame(f, started)
		for _, ev := range evs {
			acc.Add(ev)
		}
	}
	b := acc.Message().Content
	if len(b) != 2 || string(b[0].Input) != `{"x":1}` || string(b[1].Input) != `{"x":2}` || b[0].ThoughtSignature != "sig0" || b[1].ThoughtSignature != "sig1" {
		t.Fatalf("interleaved tools: %+v", b)
	}
}
