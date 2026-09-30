//go:build !windows

package sandboxconnect

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/term"
)

// watchResize sends the new window size whenever the terminal is resized.
func (t *Terminal) watchResize(ctx context.Context) {
	resized := make(chan os.Signal, 1)
	signal.Notify(resized, syscall.SIGWINCH)
	go func() {
		defer signal.Stop(resized)
		for {
			select {
			case <-ctx.Done():
				return
			case <-resized:
				t.sendResize()
			}
		}
	}()
}

func (t *Terminal) sendResize() {
	cols, rows, err := term.GetSize(int(t.stdout.Fd()))
	if err != nil {
		return
	}
	_ = t.send(Message{Type: "resize", Cols: cols, Rows: rows})
}
