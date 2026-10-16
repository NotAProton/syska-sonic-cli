//go:build windows

package jobs

// Best-effort locking on Windows: single-user CLI, atomic renames already
// keep writes sane.

func withLock(path string, fn func() (*Job, error)) (*Job, error) { return fn() }

// pidAlive: no repair on Windows (cannot probe cheaply).
func pidAlive(int) bool { return true }
