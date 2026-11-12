package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	pgdb "github.com/Autumn-27/artex/db"
	"io"
	"os"
	"path/filepath"
	"strconv"
)

// The manifest ties each file to its exact executor identity.
func archiveExecutionFiles(payload string, s *pgdb.TaskArchiveSnapshot, verify bool) error {
	var receipts []struct {
		ID   int64  `json:"approval_id"`
		Run  string `json:"run_id"`
		Call string `json:"tool_use_id"`
	}
	if err := json.Unmarshal(s.Tables["tool_execution_receipts"], &receipts); err != nil {
		return err
	}
	owners := make(map[int64][2]string, len(receipts))
	for _, r := range receipts {
		owners[r.ID] = [2]string{r.Run, r.Call}
	}
	expected := make(map[string]pgdb.ArchiveExecutionFile, len(s.ExecutionFiles))
	for _, f := range s.ExecutionFiles {
		if f.Name != filepath.Base(f.Name) || !filepath.IsLocal(f.Name) {
			return fmt.Errorf("invalid execution filename")
		}
		key := strconv.FormatInt(f.ApprovalID, 10) + "/" + f.Name
		if _, ok := expected[key]; ok {
			return fmt.Errorf("duplicate execution file")
		}
		expected[key] = f
	}
	dirs, err := os.ReadDir(filepath.Join(payload, "executions"))
	if os.IsNotExist(err) {
		if verify && len(expected) != 0 {
			return fmt.Errorf("missing execution files")
		}
		return nil
	}
	if err != nil {
		return err
	}
	var found []pgdb.ArchiveExecutionFile
	for _, dir := range dirs {
		id, err := strconv.ParseInt(dir.Name(), 10, 64)
		identity, ok := owners[id]
		if err != nil || !ok || strconv.FormatInt(id, 10) != dir.Name() || !dir.IsDir() {
			return fmt.Errorf("invalid execution folder %q", dir.Name())
		}
		entries, err := os.ReadDir(filepath.Join(payload, "executions", dir.Name()))
		if err != nil {
			return err
		}
		for _, entry := range entries {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("non-regular execution file")
			}
			f, err := os.Open(filepath.Join(payload, "executions", dir.Name(), entry.Name()))
			if err != nil {
				return err
			}
			h := sha256.New()
			n, err := io.Copy(h, f)
			closeErr := f.Close()
			if err != nil {
				return err
			}
			if closeErr != nil {
				return closeErr
			}
			item := pgdb.ArchiveExecutionFile{ApprovalID: id, RunID: identity[0], ToolUseID: identity[1], Name: entry.Name(), Size: n, SHA256: hex.EncodeToString(h.Sum(nil))}
			if verify {
				key := dir.Name() + "/" + entry.Name()
				want, ok := expected[key]
				if !ok || want != item {
					return fmt.Errorf("execution file identity/hash mismatch: %s", key)
				}
				delete(expected, key)
			}
			found = append(found, item)
		}
	}
	if verify && len(expected) > 0 {
		return fmt.Errorf("missing execution files")
	}
	if !verify {
		s.ExecutionFiles = found
	}
	return nil
}
