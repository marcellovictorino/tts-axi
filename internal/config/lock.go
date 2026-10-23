package config

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// LockRender takes the cross-process lock that lets one render run at a time,
// waiting up to wait for another render to finish, and returns the function
// that releases it.
//
// Every render loads a speech model; on a Mac already short of memory, three
// agents speaking at once would load three. flock(2) is the operating
// system's advisory file lock: it is released automatically if the process
// dies, so a crash never leaves the lock held. onWait is called once if the
// lock is busy, so a person at a terminal can be told why nothing happens.
func LockRender(ctx context.Context, p Paths, wait time.Duration, onWait func()) (unlock func(), err error) {
	if err := os.MkdirAll(p.RunDir(), 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(p.RunDir(), "render.lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(wait)
	told := false
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() {
				_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
				_ = f.Close()
			}, nil
		}
		if err != syscall.EWOULDBLOCK {
			_ = f.Close()
			return nil, err
		}
		if !told && onWait != nil {
			onWait()
			told = true
		}
		if time.Now().After(deadline) {
			_ = f.Close()
			return nil, fmt.Errorf("another render is still running after %s (lock %s)", wait, path)
		}
		select {
		case <-ctx.Done():
			_ = f.Close()
			return nil, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}
