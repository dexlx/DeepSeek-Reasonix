package main

import "testing"

func TestDesktopStartupSettingsExposeSafeMode(t *testing.T) {
	t.Setenv("REASONIX_SAFE_MODE", "1")
	view := NewApp().DesktopStartupSettings()
	if !view.SafeMode {
		t.Fatalf("startup settings = %+v", view)
	}
}
