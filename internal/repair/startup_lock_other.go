//go:build !windows && !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris

package repair

// lockStartupStateFile is a no-op on platforms without file locking; the
// tracker then behaves as before the cross-process serialization was added.
func lockStartupStateFile(string) (func(), error) {
	return func() {}, nil
}
