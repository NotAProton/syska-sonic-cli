//go:build unix

package jobs

import (
	"os"
	"path/filepath"
	"syscall"

	"github.com/notaproton/syska-sonic-cli/internal/clierrors"
)

func withLock(path string, fn func() (*Job, error)) (*Job, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, clierrors.New(clierrors.CodeDevice, "create ledger dir").WithCause(err)
	}
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, clierrors.New(clierrors.CodeDevice, "open ledger lock %s", path+".lock").WithCause(err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return nil, clierrors.New(clierrors.CodeDevice, "lock ledger").WithCause(err)
	}
	defer func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }()
	return fn()
}

// pidAlive checks /proc-style existence: signal 0 = permission probe only.
func pidAlive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}
