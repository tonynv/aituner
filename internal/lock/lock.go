//go:build unix

// Package lock provides a single-instance guard: two aituners sharing one datastore would race on the
// same run and both drive the benchmark and tuning steps.
package lock

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"syscall"
)

var ErrHeld = errors.New("another aituner is already running")

type Lock struct{ f *os.File }

// Acquire takes an exclusive, non-blocking advisory lock on path and records our PID in it. The kernel
// releases the lock if the process dies, so a crash never leaves a stale lock behind.
func Acquire(path string) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		var pid [32]byte
		n, _ := f.ReadAt(pid[:], 0)
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("%w (pid %s)", ErrHeld, trimPID(pid[:n]))
		}
		return nil, err
	}
	_ = f.Truncate(0)
	_, _ = f.WriteAt([]byte(strconv.Itoa(os.Getpid())), 0)
	return &Lock{f: f}, nil
}

func trimPID(b []byte) string {
	for i, c := range b {
		if c < '0' || c > '9' {
			return string(b[:i])
		}
	}
	return string(b)
}

func (l *Lock) Release() {
	if l == nil || l.f == nil {
		return
	}
	_ = syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	l.f.Close()
}
