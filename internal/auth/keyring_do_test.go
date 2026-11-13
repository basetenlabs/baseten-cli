package auth

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestKeyringDo_TimeoutWithoutCallerDeadline(t *testing.T) {
	saved := keyringOpTimeout
	keyringOpTimeout = 50 * time.Millisecond
	t.Cleanup(func() { keyringOpTimeout = saved })

	blocked := make(chan struct{})
	t.Cleanup(func() { close(blocked) })

	start := time.Now()
	err := keyringDo(context.Background(), func() error { <-blocked; return nil })
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Less(t, time.Since(start), time.Second)
}
