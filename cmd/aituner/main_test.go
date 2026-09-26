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
	cmds := make(chan string, 4)
	go serveAppControl(inR, &lineWriter{w: outW}, link, stop, func(c string) { cmds <- c })

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
	if _, err := io.WriteString(inW, "check\nskip 0.2.0\n"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"check", "skip 0.2.0"} {
		select {
		case got := <-cmds:
			if got != want && got != "check" && got != "skip 0.2.0" {
				t.Fatalf("command %q", got)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("command %q not delivered", want)
		}
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
