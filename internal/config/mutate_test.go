package config

import (
	"fmt"
	"sync"
	"testing"
)

// TestLockUserConfigEditsSerializesRMW drives concurrent load-modify-save
// cycles through the edit lock and checks no writer's change is dropped.
// Without the lock, two editors load the same base config, each append their
// own connection, and the second save silently erases the first one's entry —
// the bot auto-session persistence vs. settings-save race this lock exists for.
func TestLockUserConfigEditsSerializesRMW(t *testing.T) {
	// Point the user config at a temp home: SaveTo renders bot connections only
	// for user-scope paths (project configs save incrementally without them).
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	path := UserConfigPath()
	if path == "" {
		t.Fatal("UserConfigPath is empty with REASONIX_HOME set")
	}

	const writers = 8
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			unlock := LockUserConfigEdits()
			defer unlock()
			cfg := LoadForEdit(path)
			cfg.Bot.Connections = append(cfg.Bot.Connections, BotConnectionConfig{
				ID:       fmt.Sprintf("conn-%d", n),
				Provider: "qq",
				Enabled:  true,
			})
			if err := cfg.SaveTo(path); err != nil {
				t.Errorf("save: %v", err)
			}
		}(i)
	}
	wg.Wait()

	cfg := LoadForEdit(path)
	if got := len(cfg.Bot.Connections); got != writers {
		t.Fatalf("connections = %d, want %d (concurrent read-modify-write dropped updates)", got, writers)
	}
}
