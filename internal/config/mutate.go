package config

import "sync"

// userEditMu serializes in-process read-modify-write cycles on the user config
// file. LoadForEdit+SaveTo is not atomic: two concurrent editors each load,
// mutate their own copy, and save — the second save silently drops the first
// writer's fields (e.g. bot auto-session mappings vs. a settings-page save).
// Cross-process writers still race; every in-process editor must take this.
var userEditMu sync.Mutex

// LockUserConfigEdits acquires the process-wide user-config edit lock and
// returns the unlock. Hold it across the full LoadForEdit→mutate→SaveTo
// cycle; do not hold it across controller rebuilds or other slow non-config
// work, and never call another LockUserConfigEdits taker while holding it.
func LockUserConfigEdits() func() {
	userEditMu.Lock()
	return userEditMu.Unlock
}
