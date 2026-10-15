package ui

import (
	"os"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/JGoterris/orbit/internal/core/logs"
	"github.com/JGoterris/orbit/internal/core/supervisor"
)

// Run opens the interface and blocks until the user quits. It returns the final model's error, if any.
func Run(opts Options) error {
	m := New(opts)
	// the palettes are #rrggbb: without this, terminals that do not announce COLORTERM (WSL, tmux, ssh) get them
	// cut down to the 256-colour cube and every theme looks washed out
	if _, plain := os.LookupEnv("NO_COLOR"); !plain {
		lipgloss.SetColorProfile(termenv.TrueColor)
	}
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	var mu sync.Mutex
	stop := Attach(p, opts)
	defer func() { mu.Lock(); stop(); mu.Unlock() }()
	// after a project switch the events to follow are the new supervisor's
	opts.Rebind = func(sup supervisor.Like) {
		mu.Lock()
		defer mu.Unlock()
		stop()
		o := opts
		o.Sup = sup
		stop = Attach(p, o)
	}
	m.opts.Rebind = opts.Rebind
	if opts.Started != nil {
		opts.Started(p)
	}
	_, err := p.Run()
	return err
}

// Attach forwards the supervisor's events to the program: state changes as they come, and a throttled refresh for
// the rest (logs, metrics), so a flood of output does not mean a flood of redraws.
func Attach(p *tea.Program, opts Options) (stop func()) {
	sup := opts.Sup
	var mu sync.Mutex
	var timer *time.Timer
	bump := func() {
		mu.Lock()
		defer mu.Unlock()
		if timer != nil {
			return
		}
		timer = time.AfterFunc(50*time.Millisecond, func() {
			mu.Lock()
			timer = nil
			mu.Unlock()
			p.Send(refreshMsg{})
		})
	}
	// state changes go through a queue: a slow UI must never hold up the supervisor
	queue := make(chan string, 1024)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			case name := <-queue:
				p.Send(stateMsg{name: name})
			}
		}
	}()
	cancels := []func(){
		sup.OnChange(func(name string) {
			if name != "" {
				select {
				case queue <- name:
				default:
				}
			}
			bump()
		}),
		sup.LogStore().OnLine(func(logs.Line) { bump() }),
		sup.OnConfigPending(func(*supervisor.PendingConfig) { bump() }),
	}
	return func() {
		for _, c := range cancels {
			c()
		}
		close(done)
		mu.Lock()
		if timer != nil {
			timer.Stop()
		}
		mu.Unlock()
	}
}
