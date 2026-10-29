//go:build !windows

package auth

import (
	"os"
	"path/filepath"
	"syscall"
)

// gridTokenLockFileName is the lock file next to auth.json that serializes
// Grid token creation across CLI processes.
const gridTokenLockFileName = "grid-token.lock"

// LockGridTokens takes an exclusive lock on the Grid token cache in dir, so
// several harnesses starting at once mint one token between them. The
// returned function releases it.
func LockGridTokens(dir string) (func(), error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, gridTokenLockFileName), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
