package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"sync"

	"github.com/Autumn-27/artex/agent"
)

type failedToolAttempt struct{ failures, warnings int }

// Per-intent, shared across model retries, bounded memory, no command text kept.
// Success clears the corresponding failure streak; ordinary polling is unaffected.
type workerFailureGuard struct {
	mu      sync.Mutex
	entries map[[32]byte]failedToolAttempt
	order   [][32]byte
	cancel  context.CancelCauseFunc
}

func newWorkerFailureGuard(cancel context.CancelCauseFunc) *workerFailureGuard {
	return &workerFailureGuard{entries: map[[32]byte]failedToolAttempt{}, cancel: cancel}
}

func toolAttemptKey(name string, input []byte) [32]byte {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.UseNumber()
	if decoder.Decode(&value) == nil {
		if canonical, err := json.Marshal(value); err == nil {
			input = canonical
		}
	}
	h := sha256.New()
	h.Write([]byte(name))
	h.Write([]byte{0})
	h.Write(input)
	var key [32]byte
	copy(key[:], h.Sum(nil))
	return key
}

func (g *workerFailureGuard) before(name string, input []byte) (bool, string) {
	key := toolAttemptKey(name, input)
	g.mu.Lock()
	entry := g.entries[key]
	if entry.failures < 3 {
		g.mu.Unlock()
		return false, ""
	}
	entry.warnings++
	g.entries[key] = entry
	stop := entry.warnings >= 3
	g.mu.Unlock()
	if stop && g.cancel != nil {
		g.cancel(agent.AbortRepeatedToolFailure)
	}
	return true, "【重复失败纠偏】同一工具及参数已经失败三次，本次未执行。请读取已有结果、验证缺失前提、采用不同方法，或写回真实失败依据并交回规划者；不要重复同一操作。"
}

func (g *workerFailureGuard) after(name string, input []byte, isError bool) {
	key := toolAttemptKey(name, input)
	g.mu.Lock()
	defer g.mu.Unlock()
	entry, exists := g.entries[key]
	if !isError {
		// Retain its bounded slot but clear counts, so a success permits later calls.
		if exists {
			g.entries[key] = failedToolAttempt{}
		}
		return
	}
	if !exists {
		if len(g.order) == 32 {
			delete(g.entries, g.order[0])
			g.order = g.order[1:]
		}
		g.order = append(g.order, key)
	}
	entry.failures++
	g.entries[key] = entry
}
