//go:build unix

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// acquireLock takes an exclusive, non-blocking lock so two agents (say, a
// watcher and a scheduled run) never make roster moves at the same time.
// The OS releases it automatically if the process dies.
func acquireLock(path string) (release func(), err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("another ffagent is already making changes (lock %s held)", path)
	}
	return func() { syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }, nil
}
