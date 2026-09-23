package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/tonynv/aituner/internal/api"
)

type tickMsg time.Time
type eventMsg api.SSEEvent

type model struct {
	srv      *api.Server
	url      string
	open     func()
	cancel   context.CancelFunc
	phase    string
	job      *api.JobInfo
	lines    []string
	events   <-chan api.SSEEvent
	width    int
	quitting bool
}

var (
	title = lipgloss.NewStyle().Bold(true)
	dim   = lipgloss.NewStyle().Faint(true)
)

func runTUI(ctx context.Context, stop context.CancelFunc, srv *api.Server, url string, open func()) error {
	replay, ch, unsub := srv.Subscribe()
	defer unsub()
	m := model{srv: srv, url: url, open: open, cancel: stop, events: ch}
	for _, e := range replay {
		m.addLine(e)
	}
	m.phase, m.job = srv.Status()
	p := tea.NewProgram(m, tea.WithContext(ctx))
	_, err := p.Run()
	if err != nil && ctx.Err() != nil {
		return nil // interrupted by signal
	}
	return err
}

func (m *model) addLine(e api.SSEEvent) {
	if e.Event.Message == "" {
		return
	}
	prefix := "  "
	switch e.Event.Level {
	case "error":
		prefix = "! "
	case "warn":
		prefix = "~ "
	}
	m.lines = append(m.lines, prefix+e.Event.Message)
	if len(m.lines) > 14 {
		m.lines = m.lines[len(m.lines)-14:]
	}
}

func waitEvent(ch <-chan api.SSEEvent) tea.Cmd {
	return func() tea.Msg {
		e, ok := <-ch
		if !ok {
			return nil
		}
		return eventMsg(e)
	}
}

func tick() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m model) Init() tea.Cmd { return tea.Batch(tick(), waitEvent(m.events)) }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
	case tickMsg:
		m.phase, m.job = m.srv.Status()
		return m, tick()
	case eventMsg:
		m.addLine(api.SSEEvent(msg))
		return m, waitEvent(m.events)
	case tea.KeyPressMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			m.quitting = true
			m.cancel()
			return m, tea.Quit
		case "o":
			m.open()
		}
	}
	return m, nil
}

var phaseLabel = map[string]string{
	"detected":         "1 Hardware detected",
	"baseline_running": "2 Benchmark running",
	"baseline_done":    "3 Baseline done, review tuning",
	"tune_reviewed":    "4 Tuning reviewed, re-run benchmark",
	"tuned_running":    "4 Re-benchmark running",
	"tuned_done":       "5 Recommendations unlocked",
}

func (m model) View() tea.View {
	var b strings.Builder
	b.WriteString(title.Render("aituner") + dim.Render("  local AI benchmark and tuner") + "\n\n")
	b.WriteString("Web UI  " + m.url + "\n")
	b.WriteString("Step    " + phaseLabel[m.phase] + "\n")
	if m.job != nil && m.job.Running {
		b.WriteString(fmt.Sprintf("Job     %s  %.0f%%\n", m.job.Kind, m.job.Progress*100))
	}
	b.WriteString("\n" + dim.Render("log") + "\n")
	for _, l := range m.lines {
		b.WriteString(l + "\n")
	}
	b.WriteString("\n" + dim.Render("o open browser   q quit") + "\n")
	return tea.NewView(b.String())
}
