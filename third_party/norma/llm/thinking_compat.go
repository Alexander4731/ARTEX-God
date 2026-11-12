package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"strings"
)

// HTTPStatusError retains structured rejection data for narrowly scoped fallbacks.
type HTTPStatusError struct {
	Provider string
	Status   int
	Body     string
}

func (e *HTTPStatusError) Error() string {
	return fmt.Sprintf("%s: status %d: %s", e.Provider, e.Status, e.Body)
}

func thinkingParameterRejected(err error) bool {
	var status *HTTPStatusError
	if !errors.As(err, &status) || (status.Status != 400 && status.Status != 422) {
		return false
	}
	var body struct {
		Error struct {
			Param   string `json:"param"`
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(status.Body), &body) != nil {
		return false
	}
	param := strings.ToLower(body.Error.Param)
	switch param {
	case "thinking", "thinking.type", "reasoning_effort", "reasoning.effort", "reasoning", "output_config.effort":
	default:
		return false
	}
	code := strings.ToLower(body.Error.Code)
	return code == "unsupported_parameter" || code == "unknown_parameter" || code == "unrecognized_request_argument"
}

type thinkingFallbackProvider struct {
	inner Provider
	cfg   Config
}

func (p *thinkingFallbackProvider) fallback(req CompletionRequest, err error) (Provider, CompletionRequest, bool) {
	if !thinkingParameterRejected(err) || (req.Thinking == "" && p.cfg.ThinkingType == "" && p.cfg.ReasoningEffort == "") {
		return nil, req, false
	}
	cfg := p.cfg
	cfg.ThinkingType = ""
	cfg.ReasoningEffort = ""
	req.Thinking = ""
	// Preserve the client's headers, transport and shared limiter. These copies are
	// local to this call, so concurrent calls and saved profiles are unaffected.
	switch cfg.Format {
	case FormatAnthropic:
		return &anthropicProvider{cfg: cfg}, req, true
	case FormatOpenAI:
		return &openaiProvider{cfg: cfg}, req, true
	case FormatOpenAIResponses:
		return &openaiResponsesProvider{cfg: cfg}, req, true
	}
	return nil, req, false
}
func (p *thinkingFallbackProvider) Stream(ctx context.Context, req CompletionRequest) iter.Seq2[StreamEvent, error] {
	return func(yield func(StreamEvent, error) bool) {
		visible := false
		for ev, err := range p.inner.Stream(ctx, req) {
			if err != nil && !visible && ctx.Err() == nil {
				if fallback, next, ok := p.fallback(req, err); ok {
					for ev, err := range fallback.Stream(ctx, next) {
						if !yield(ev, err) {
							return
						}
					}
					return
				}
			}
			switch ev.Type {
			case SETextDelta, SEThinkingDelta, SEToolUseStart:
				visible = true
			}
			if !yield(ev, err) {
				return
			}
		}
	}
}
func (p *thinkingFallbackProvider) Complete(ctx context.Context, req CompletionRequest) (Message, string, Usage, error) {
	msg, stop, usage, err := p.inner.Complete(ctx, req)
	if err != nil && len(msg.Content) == 0 && ctx.Err() == nil {
		if fallback, next, ok := p.fallback(req, err); ok {
			return fallback.Complete(ctx, next)
		}
	}
	return msg, stop, usage, err
}
