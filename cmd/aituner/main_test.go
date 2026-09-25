package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"testing"
	"time"
)

func TestServeAppControl(t *testing.T) {
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	n := 0
	link := func() string { n++; return fmt.Sprintf("http://127.0.0.1:1/?t=%d", n) }
	go serveAppControl(inR, outW, link, stop)

	lines := bufio.NewScanner(outR)
	next := func() string {
		if !lines.Scan() {
			t.Fatalf("no line: %v", lines.Err())
		}
		return lines.Text()
	}
	if got := next(); got != "link http://127.0.0.1:1/?t=1" {
		t.Fatalf("first line = %q", got)
	}
	if _, err := io.WriteString(inW, "\n"); err != nil {
		t.Fatal(err)
	}
	if got := next(); got != "link http://127.0.0.1:1/?t=2" {
		t.Fatalf("second line = %q", got)
	}
	if ctx.Err() != nil {
		t.Fatal("stopped while stdin is open")
	}
	inW.Close() // the app quit or was killed
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("did not stop when stdin closed")
	}
}
