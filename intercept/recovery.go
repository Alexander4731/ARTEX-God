package intercept

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/transcript"
)

// RecoverExecutions repairs missed database writes from the SDK's existing
// transcripts. Only unresolved, exactly correlated calls are inspected.
func (i *Interceptor) RecoverExecutions(ctx context.Context, store *transcript.Store, taskID string) error {
	executions, err := i.db.UnsettledInterceptExecutions(ctx, taskID)
	if err != nil {
		return err
	}
	sessions := make(map[string][]db.UnsettledExecution)
	for _, execution := range executions {
		session := execution.Audit.SessionID
		if session == "" {
			// Older audits predate the explicit SDK session identity.
			if execution.OriginalCalls != 1 {
				continue
			}
			switch {
			case execution.ConversationID > 0:
				session = fmt.Sprintf("conv-%d", execution.ConversationID)
			case execution.ExplorationID > 0 && execution.Audit.IntentID > 0:
				session = fmt.Sprintf("exp%d-worker-i%d", execution.ExplorationID, execution.Audit.IntentID)
			case execution.ExplorationID > 0 && execution.AgentName == "planner":
				session = fmt.Sprintf("exp%d-planner", execution.ExplorationID)
			case execution.ExplorationID > 0 && execution.AgentName == "mainagent" && execution.MainSegment != nil:
				session = fmt.Sprintf("exp%d-main", execution.ExplorationID)
				if *execution.MainSegment > 0 {
					session = fmt.Sprintf("%s-s%d", session, *execution.MainSegment)
				}
			}
		}
		if session == "" || session == "." || session == ".." || filepath.Base(session) != session || strings.ContainsAny(session, `/\`) {
			continue
		}
		sessions[session] = append(sessions[session], execution)
	}
	for session, pending := range sessions {
		var legacyRecords []transcript.Record
		legacyLoaded := false
		pairs := make(map[int64][]transcript.Record)
		for _, execution := range pending {
			if err := ctx.Err(); err != nil {
				return err
			}
			var records []transcript.Record
			if execution.Audit.SessionID != "" {
				if execution.Audit.TranscriptOffset == nil {
					continue
				}
				offset := *execution.Audit.TranscriptOffset
				var loaded bool
				records, loaded = pairs[offset]
				if !loaded {
					records, err = executionPairAt(store.MainPath(session), offset)
					if err != nil && !errors.Is(err, io.EOF) && !os.IsNotExist(err) {
						log.Printf("[intercept] load execution evidence for %s: %v", session, err)
					}
					pairs[offset] = records
				}
			} else {
				if !legacyLoaded {
					legacyRecords, err = store.Load(store.MainPath(session))
					legacyLoaded = true
					if err != nil {
						log.Printf("[intercept] load execution evidence for %s: %v", session, err)
					}
				}
				records = legacyRecords
			}
			result, endedAt, ok := recordedExecution(records, session, execution)
			if !ok {
				continue
			}
			status := "succeeded"
			if result.IsError {
				status = "failed"
			}
			output, truncated := bounded(resultText(result.Content), outputLimit)
			if err := i.db.RecoverInterceptExecution(ctx, execution, status, output, truncated, endedAt); err != nil {
				return err
			}
		}
	}
	return nil
}

// The SDK appends one assistant message and its tool-result message after tool
// execution. Inspect exactly that position; a later Prompt cannot supply it.
func executionPairAt(path string, offset int64) ([]transcript.Record, error) {
	if offset < 0 {
		return nil, fmt.Errorf("invalid transcript offset")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(f)
	records := make([]transcript.Record, 2)
	for index := range records {
		if err := decoder.Decode(&records[index]); err != nil {
			return nil, err
		}
	}
	return records, nil
}

func resultText(blocks []llm.ContentBlock) string {
	var text []string
	for _, block := range blocks {
		if block.Type == llm.BlockText && block.Text != "" {
			text = append(text, block.Text)
		}
	}
	return strings.Join(text, "\n")
}

// SDK v0.4.3 records the original assistant/tool-result pair after emitting the
// tool events. Compaction subsequently copies that pair; those copies are not
// new execution evidence. Never infer a result from a reused call ID alone.
func recordedExecution(records []transcript.Record, session string, execution db.UnsettledExecution) (llm.ContentBlock, time.Time, bool) {
	audit := execution.Audit
	if audit.RunID == "" || audit.ToolUseID == "" || audit.InputDigest == "" || audit.CapturedAt.IsZero() {
		return llm.ContentBlock{}, time.Time{}, false
	}
	start, end := 0, len(records)
	if audit.SessionID != "" {
		if audit.TranscriptOffset == nil || len(records) != 2 || records[0].Message == nil || records[0].Message.Role != llm.RoleAssistant {
			return llm.ContentBlock{}, time.Time{}, false
		}
	}
	for index := start; index < end; index++ {
		record := records[index]
		stamp, err := time.Parse(time.RFC3339Nano, record.Timestamp)
		if err != nil {
			return llm.ContentBlock{}, time.Time{}, false
		}
		if record.Type == "boundary" {
			if stamp.After(audit.CapturedAt) {
				end = index
				break
			}
			start = index + 1
		}
		// Legacy audits have no recorded file position. Stop at later Prompt input;
		// the original SDK tool-result message contains tool_result blocks only.
		if audit.SessionID == "" && stamp.After(audit.CapturedAt) && record.SessionID == session && record.Message != nil && record.Message.Role == llm.RoleUser {
			for _, block := range record.Message.Content {
				if block.Type != llm.BlockToolResult {
					end = index
					break
				}
			}
		}
	}
	var call *transcript.Record
	var result llm.ContentBlock
	var endedAt time.Time
	uses, results := 0, 0
	for index := start; index < end; index++ {
		record := &records[index]
		stamp, err := time.Parse(time.RFC3339Nano, record.Timestamp)
		if err != nil {
			return llm.ContentBlock{}, time.Time{}, false
		}
		if record.SessionID != session || record.IsSidechain || record.AgentID != "" || record.Message == nil {
			continue
		}
		for _, block := range record.Message.Content {
			if block.Type == llm.BlockToolUse && block.ID == audit.ToolUseID {
				uses++
				if !stamp.Before(audit.CapturedAt) && record.Message.Role == llm.RoleAssistant && block.Name == execution.ToolName && digestInput(block.Input) == audit.InputDigest {
					call = record
				}
			}
			if block.Type == llm.BlockToolResult && block.ToolUseID == audit.ToolUseID {
				results++
				if call != nil && !stamp.Before(audit.CapturedAt) && record.Message.Role == llm.RoleUser && call.UUID != "" && record.ParentUUID == call.UUID {
					result, endedAt = block, stamp
				}
			}
		}
	}
	// This is a synthetic SDK cancellation result, not a completed tool report.
	if result.IsError && resultText(result.Content) == "tool execution interrupted" {
		return llm.ContentBlock{}, time.Time{}, false
	}
	return result, endedAt, uses == 1 && results == 1 && !endedAt.IsZero()
}
