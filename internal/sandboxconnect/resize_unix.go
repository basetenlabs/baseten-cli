//go:build !windows

package sandboxconnect

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

func (t *osTerminal) NotifyResize(ctx context.Context, resized func()) {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGWINCH)
	go func() {
		defer signal.Stop(signals)
		for {
			select {
			case <-ctx.Done():
				return
			case <-signals:
				resized()
			}
		}
	}()
}
