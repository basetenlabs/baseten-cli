package auth

// LockGridTokens is a no-op on Windows, where harness commands are not
// supported. The returned function releases nothing.
func LockGridTokens(string) (func(), error) {
	return func() {}, nil
}
