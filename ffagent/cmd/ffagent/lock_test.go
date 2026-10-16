//go:build unix

package main

import (
	"path/filepath"
	"testing"
)

func TestLockIsExclusive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ffagent.lock")
	release, err := acquireLock(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := acquireLock(path); err == nil {
		t.Fatal("second agent got the lock while the first held it")
	}
	release()
	r2, err := acquireLock(path)
	if err != nil {
		t.Fatalf("lock not released: %v", err)
	}
	r2()
}
