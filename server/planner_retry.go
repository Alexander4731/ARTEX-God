package server

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"time"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/norma/harness"
	"github.com/Autumn-27/norma/llm"
)

// Only known model/transport failures may wait indefinitely. Configuration,
// transcript and graph errors need an explicit action instead of a retry loop.
func retryablePlannerModelError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || isQuotaExhaustedError(err) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, agent.ErrRetryBudget) || errors.Is(err, harness.ErrEmptyResponse) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var status *llm.HTTPStatusError
	if errors.As(err, &status) {
		return status.Status == 408 || status.Status == 429 || status.Status == 500 || status.Status == 502 || status.Status == 503 || status.Status == 504
	}
	var network net.Error
	if errors.As(err, &network) {
		var operation *net.OpError
		return network.Timeout() || errors.As(err, &operation)
	}
	text := strings.ToLower(err.Error())
	// Providers can also deliver protocol errors inside an otherwise successful SSE.
	for _, marker := range []string{"status 408", "status 429", "status 500", "status 502", "status 503", "status 504", "overloaded_error", "server_error", "connection reset", "unexpected eof", "broken pipe"} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func plannerModelBackoff(failures int) time.Duration {
	return min(time.Second*time.Duration(1<<min(max(failures, 1), 6)), time.Minute)
}
