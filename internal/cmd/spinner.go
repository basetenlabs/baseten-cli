package cmd

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// spinner shows a labeled progress line on stderr while a long step runs:
// animated with the elapsed time on a terminal, label line plus a 30s
// heartbeat otherwise, so piped logs still show the step is alive.
type spinner struct {
	ctx      *CommandContext
	label    string
	started  time.Time
	animated bool
	lineLen  int
	done     chan struct{}
	stopOnce sync.Once
}

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// startSpinner begins showing the label. The caller must call Stop, also on
// the error path, or the line keeps animating.
func startSpinner(ctx *CommandContext, label string) *spinner {
	s := &spinner{
		ctx:      ctx,
		label:    label,
		started:  time.Now(),
		animated: isTerminalWriter(ctx.Stderr),
		done:     make(chan struct{}),
	}
	s.ctx.Logf("%s\n", label)
	go s.run()
	return s
}

func (s *spinner) run() {
	if s.animated {
		tick := time.NewTicker(200 * time.Millisecond)
		defer tick.Stop()
		frame := 0
		for {
			select {
			case <-s.done:
				return
			case <-tick.C:
				line := fmt.Sprintf("\r%s %s (%ds)", spinnerFrames[frame%len(spinnerFrames)], s.label, int(time.Since(s.started).Seconds()))
				s.ctx.Log(line)
				s.lineLen = len(line)
				frame++
			}
		}
	}
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-tick.C:
			s.ctx.Logf("%s (%ds elapsed)\n", s.label, int(time.Since(s.started).Seconds()))
		}
	}
}

// Stop clears the animated line and prints finalLine with its elapsed time;
// "" just clears. Only the first call does anything.
func (s *spinner) Stop(finalLine string) {
	s.stopOnce.Do(func() { s.stop(finalLine) })
}

func (s *spinner) stop(finalLine string) {
	close(s.done)
	if s.animated {
		s.ctx.Log("\r" + strings.Repeat(" ", s.lineLen+2) + "\r")
	}
	if finalLine == "" {
		return
	}
	elapsed := int(time.Since(s.started).Seconds())
	s.ctx.Logf("%s (%ds)\n", finalLine, elapsed)
}
