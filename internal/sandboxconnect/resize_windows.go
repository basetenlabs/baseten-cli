//go:build windows

package sandboxconnect

import "context"

// watchResize is a no-op on Windows: SIGWINCH does not exist there, and the
// window size was sent at dial time.
func (t *Terminal) watchResize(context.Context) {}
