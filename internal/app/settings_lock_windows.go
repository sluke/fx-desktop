//go:build windows

package app

// The packaged macOS app is the supported target today. Windows keeps the
// same atomic replacement and simply skips the POSIX advisory lock; a future
// Windows port should replace this with LockFileEx.
func lockSettingsFile(string) (func(), error) {
	return func() {}, nil
}
