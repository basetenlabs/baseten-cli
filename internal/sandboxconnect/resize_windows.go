//go:build windows

package sandboxconnect

import "context"

// NotifyResize never calls resized on Windows, which has no resize signal, so
// the window keeps the size it had when the session started.
func (t *osTerminal) NotifyResize(context.Context, func()) {}
