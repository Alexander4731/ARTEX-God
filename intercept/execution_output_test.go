package intercept

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExecutionOutputSnapshotReportsStorageBoundary(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source")
	dst := filepath.Join(dir, "snapshot")
	if err := os.WriteFile(src, []byte("0123456789"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, limit := range []int64{6, 10, 11} {
		stored, total, err := snapshotExecutionOutput(src, dst, limit)
		if err != nil || stored != min(limit, 10) || total != 10 {
			t.Fatalf("stored=%d total=%d err=%v", stored, total, err)
		}
		raw, err := os.ReadFile(dst)
		if err != nil || int64(len(raw)) != stored {
			t.Fatalf("snapshot: %v", err)
		}
	}
}
