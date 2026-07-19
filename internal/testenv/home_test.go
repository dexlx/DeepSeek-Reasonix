package testenv

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsolateUserStateRedirectsAndRestoresCallerEnvironment(t *testing.T) {
	callerHome := t.TempDir()
	callerReasonixHome := filepath.Join(callerHome, "explicit-reasonix-home")
	t.Setenv("HOME", callerHome)
	t.Setenv("USERPROFILE", callerHome)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(callerHome, "caller-config"))
	t.Setenv("AppData", filepath.Join(callerHome, "caller-appdata"))
	t.Setenv("REASONIX_HOME", callerReasonixHome)
	t.Setenv("REASONIX_STATE_HOME", filepath.Join(callerHome, "caller-state"))
	t.Setenv("REASONIX_CACHE_HOME", filepath.Join(callerHome, "caller-cache"))

	cleanup, err := IsolateUserState()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	isolateHome := os.Getenv("HOME")
	if isolateHome == "" || isolateHome == callerHome {
		t.Fatalf("HOME = %q, want a disposable home distinct from caller %q", isolateHome, callerHome)
	}
	if rel, err := filepath.Rel(callerHome, isolateHome); err != nil || rel == ".." || filepath.IsAbs(rel) {
		t.Fatalf("isolated home %q is not contained by caller home %q", isolateHome, callerHome)
	}
	for _, key := range []string{"REASONIX_HOME", "REASONIX_STATE_HOME", "REASONIX_CACHE_HOME"} {
		if _, ok := os.LookupEnv(key); ok {
			t.Fatalf("%s remained set inside isolated test process", key)
		}
	}

	cleanup()
	if got := os.Getenv("HOME"); got != callerHome {
		t.Fatalf("HOME after cleanup = %q, want %q", got, callerHome)
	}
	if got := os.Getenv("REASONIX_HOME"); got != callerReasonixHome {
		t.Fatalf("REASONIX_HOME after cleanup = %q, want %q", got, callerReasonixHome)
	}
	if _, err := os.Stat(isolateHome); !os.IsNotExist(err) {
		t.Fatalf("isolated home still exists after cleanup: %v", err)
	}
}
