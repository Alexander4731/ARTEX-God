package testutil

import (
	"os"
	"runtime"
	"testing"
	"time"
)

// TempDir also handles Windows directory entries that remain pending deletion
// after their handles close. testing.TempDir retries sharing violations, but
// not ERROR_DIR_NOT_EMPTY. Resources must still be closed before cleanup runs.
func TempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if runtime.GOOS == "windows" {
		t.Cleanup(func() {
			deadline := time.Now().Add(2 * time.Second)
			for {
				err := os.RemoveAll(dir)
				if !os.IsExist(err) || time.Now().After(deadline) {
					if err != nil {
						t.Errorf("remove test fixture: %v", err)
					}
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
		})
	}
	return dir
}
