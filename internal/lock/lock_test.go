//go:build unix

package lock

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestSecondAcquireFailsUntilReleased(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.lock")
	a, err := Acquire(p)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Acquire(p)
	if !errors.Is(err, ErrHeld) || !strings.Contains(err.Error(), strconv.Itoa(os.Getpid())) {
		t.Fatalf("want ErrHeld with pid, got %v", err)
	}
	a.Release()
	b, err := Acquire(p)
	if err != nil {
		t.Fatalf("after release: %v", err)
	}
	b.Release()
}

func TestLockFileIsPrivate(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.lock")
	l, _ := Acquire(p)
	defer l.Release()
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", fi.Mode().Perm())
	}
}
