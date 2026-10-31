package db

import "testing"

func TestAdvisoryLockNamespacesAreDistinct(t *testing.T) {
	locks := map[string]int64{
		"schema migration":         schemaMigrationLockKey,
		"cross-package test suite": 7337741002,
		"company scope mutation":   companyScopeMutationLock,
		"finding evidence":         findingEvidenceLockKey,
		"runtime ownership":        runtimeLockKey,
		"finding retest":           7337741010,
	}
	seen := make(map[int64]string, len(locks))
	for name, key := range locks {
		if previous, exists := seen[key]; exists {
			t.Errorf("%s and %s reuse advisory lock key %d", previous, name, key)
		}
		seen[key] = name
	}
}
