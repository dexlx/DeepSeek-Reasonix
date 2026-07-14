//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package repair

import (
	"os"

	"golang.org/x/sys/unix"
)

// lockStartupStateFile takes an exclusive cross-process lock guarding the
// startup-state read-modify-write cycle. The critical section is a handful of
// small file operations, so a blocking lock cannot stall startup meaningfully.
func lockStartupStateFile(path string) (func(), error) {
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, err
	}
	return func() {
		_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
		_ = f.Close()
	}, nil
}
