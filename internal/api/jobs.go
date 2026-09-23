package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/tonynv/aituner/internal/bench"
)

var ErrBusy = errors.New("another operation is already running")

const ringSize = 1000

// SSEEvent is one line of job output.
type SSEEvent struct {
	Seq   int         `json:"seq"`
	Time  int64       `json:"time"`
	Kind  string      `json:"kind"` // job kind: benchmark | tune | revert
	Event bench.Event `json:"event"`
}

type JobInfo struct {
	Kind     string  `json:"kind"`
	Running  bool    `json:"running"`
	Started  int64   `json:"started"`
	Finished int64   `json:"finished,omitempty"`
	Error    string  `json:"error,omitempty"`
	Progress float64 `json:"progress"`
}

// jobs serialises long operations (only one at a time) and fans their events out to SSE clients,
// keeping a ring buffer so a page refresh replays recent output.
type jobs struct {
	mu     sync.Mutex
	cur    *JobInfo
	cancel context.CancelFunc
	ring   []SSEEvent
	seq    int
	subs   map[chan SSEEvent]struct{}
}

func newJobs() *jobs { return &jobs{subs: map[chan SSEEvent]struct{}{}} }

func (j *jobs) info() *JobInfo {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.cur == nil {
		return nil
	}
	c := *j.cur
	return &c
}

func (j *jobs) emit(kind string, e bench.Event) {
	j.mu.Lock()
	j.seq++
	ev := SSEEvent{Seq: j.seq, Time: time.Now().UnixMilli(), Kind: kind, Event: e}
	j.ring = append(j.ring, ev)
	if len(j.ring) > ringSize {
		j.ring = j.ring[len(j.ring)-ringSize:]
	}
	if j.cur != nil && e.Progress > 0 {
		j.cur.Progress = e.Progress
	}
	for ch := range j.subs {
		select {
		case ch <- ev:
		default: // slow client: drop rather than block the job
		}
	}
	j.mu.Unlock()
}

// start runs fn as the single active job. done is called (still holding no locks) with fn's error.
func (j *jobs) start(parent context.Context, kind string, fn func(ctx context.Context, emit bench.Emit) error, done func(err error)) error {
	j.mu.Lock()
	if j.cur != nil && j.cur.Running {
		j.mu.Unlock()
		return ErrBusy
	}
	ctx, cancel := context.WithCancel(parent)
	j.cancel = cancel
	j.cur = &JobInfo{Kind: kind, Running: true, Started: time.Now().UnixMilli()}
	j.mu.Unlock()
	go func() {
		defer cancel()
		err := fn(ctx, func(e bench.Event) { j.emit(kind, e) })
		j.mu.Lock()
		j.cur.Running = false
		j.cur.Finished = time.Now().UnixMilli()
		if err != nil {
			j.cur.Error = err.Error()
		} else {
			j.cur.Progress = 1
		}
		j.mu.Unlock()
		if err != nil {
			j.emit(kind, bench.Event{Level: "error", Message: err.Error()})
		}
		if done != nil {
			done(err)
		}
	}()
	return nil
}

func (j *jobs) stop() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.cur != nil && j.cur.Running && j.cancel != nil {
		j.cancel()
		return true
	}
	return false
}

func (j *jobs) subscribe(after int) (replay []SSEEvent, ch chan SSEEvent, unsub func()) {
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, e := range j.ring {
		if e.Seq > after {
			replay = append(replay, e)
		}
	}
	ch = make(chan SSEEvent, 256)
	j.subs[ch] = struct{}{}
	return replay, ch, func() {
		j.mu.Lock()
		delete(j.subs, ch)
		j.mu.Unlock()
	}
}

// handleEvents streams job output as Server-Sent Events.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, 500, "no_streaming", "streaming unsupported")
		return
	}
	after, _ := strconv.Atoi(r.Header.Get("Last-Event-ID"))
	if q := r.URL.Query().Get("after"); q != "" {
		after, _ = strconv.Atoi(q)
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	replay, ch, unsub := s.jobs.subscribe(after)
	defer unsub()
	send := func(e SSEEvent) {
		b, _ := json.Marshal(e)
		fmt.Fprintf(w, "id: %d\ndata: %s\n\n", e.Seq, b)
	}
	for _, e := range replay {
		send(e)
	}
	fl.Flush()
	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case e := <-ch:
			send(e)
			fl.Flush()
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			fl.Flush()
		}
	}
}
