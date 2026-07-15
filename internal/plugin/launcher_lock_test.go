package plugin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"reasonix/internal/mcptrust"
)

func TestStoredNPXLauncherLockUsesExactOfflinePackage(t *testing.T) {
	manager := mcptrust.NewManager(filepath.Join(t.TempDir(), mcptrust.StateFilename), "/workspace")
	lock := mcptrust.LauncherLock{
		Server: "search", Locator: digestText("@scope/server"), ResolvedVersion: "@scope/server@1.2.3", ContentSHA256: digestText("integrity"),
	}
	if err := manager.PutLauncherLock(lock); err != nil {
		t.Fatal(err)
	}
	spec := Spec{Name: "search", Command: "npx", Args: []string{"-y", "@scope/server", "--stdio"}, TrustManager: manager}
	locked, err := applyStoredLauncherLock(spec)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"-y", "--offline", "@scope/server@1.2.3", "--stdio"}
	if !reflect.DeepEqual(locked.LaunchArgs, want) {
		t.Fatalf("launch args = %v, want %v", locked.LaunchArgs, want)
	}
	if locked.LauncherDigest == "" {
		t.Fatal("launcher digest is empty")
	}
	if SpecFingerprint(locked) != SpecFingerprint(spec) {
		t.Fatal("host-local launcher lock changed provider cache fingerprint")
	}
}

func TestMutableLauncherRejectsAmbiguousFlagValue(t *testing.T) {
	locator, mutable := mutableLauncherLocator(Spec{Command: "npx", Args: []string{"--node-options", "--inspect", "server"}})
	if !mutable || locator.value != "" {
		t.Fatalf("ambiguous locator = %+v, mutable=%v", locator, mutable)
	}
}

func TestOfficialLauncherRequiresImmutablePackageLocator(t *testing.T) {
	commit := "0123456789abcdef0123456789abcdef01234567"
	cases := []struct {
		name string
		spec Spec
		ok   bool
	}{
		{name: "exact npm", spec: Spec{Command: "npx", Args: []string{"-y", "@scope/server@1.2.3"}}, ok: true},
		{name: "npm tag", spec: Spec{Command: "npx", Args: []string{"server@latest"}}},
		{name: "npm range", spec: Spec{Command: "bunx", Args: []string{"server@^1.2.3"}}},
		{name: "exact pypi", spec: Spec{Command: "uvx", Args: []string{"server==2.4.1"}}, ok: true},
		{name: "floating pypi", spec: Spec{Command: "uvx", Args: []string{"server"}}},
		{name: "exact git", spec: Spec{Command: "npx", Args: []string{"git+https://example.test/server.git@" + commit}}, ok: true},
		{name: "git branch", spec: Spec{Command: "npx", Args: []string{"git+https://example.test/server.git@main"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.spec.Name = "official"
			tc.spec.OfficialCatalogEntryID = "official@1"
			err := validateOfficialLauncher(tc.spec)
			if tc.ok && err != nil {
				t.Fatalf("immutable official launcher rejected: %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatal("mutable official launcher accepted")
			}
		})
	}
}

func TestResolvePyPIPackagePinsVersionAndFileDigests(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/demo/json" {
			t.Fatalf("request path = %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"info":{"version":"2.4.1"},"urls":[{"digests":{"sha256":"bbb"}},{"digests":{"sha256":"aaa"}}]}`))
	}))
	defer server.Close()
	oldBase := pypiBaseURL
	pypiBaseURL = server.URL
	defer func() { pypiBaseURL = oldBase }()
	resolved, digest, err := resolvePyPIPackage(context.Background(), "demo")
	if err != nil {
		t.Fatal(err)
	}
	if resolved != "demo==2.4.1" || digest != digestText("aaa\nbbb") {
		t.Fatalf("resolution = %q %q", resolved, digest)
	}
}

func TestResolveExactGitLocatorDoesNotNeedNetwork(t *testing.T) {
	commit := "0123456789abcdef0123456789abcdef01234567"
	locator := "git+https://example.invalid/server.git@" + commit
	resolved, digest, err := resolveGitLocator(context.Background(), Spec{}, locator)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != commit || digest != digestText(commit) {
		t.Fatalf("resolution = %q %q", resolved, digest)
	}
}

func TestGitLauncherLockDoesNotPersistCredentialedLocator(t *testing.T) {
	home := t.TempDir()
	manager := mcptrust.NewManager(filepath.Join(home, mcptrust.StateFilename), "/workspace")
	locator := "git+https://user:secret-token@example.test/server.git@main"
	commit := "0123456789abcdef0123456789abcdef01234567"
	lock := mcptrust.LauncherLock{
		Server: "git-server", Locator: digestText(locator), ResolvedVersion: commit, ContentSHA256: digestText(commit),
	}
	if err := manager.PutLauncherLock(lock); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(manager.Path())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "secret-token") || strings.Contains(string(body), "user:") {
		t.Fatal("launcher security state persisted URL credentials")
	}
	spec := Spec{Name: "git-server", Command: "npx", Args: []string{locator}, TrustManager: manager}
	got, err := applyStoredLauncherLock(spec)
	if err != nil {
		t.Fatal(err)
	}
	want := "git+https://user:secret-token@example.test/server.git@" + commit
	if len(got.LaunchArgs) != 2 || got.LaunchArgs[1] != want || got.LaunchArgs[0] != "--offline" {
		t.Fatalf("reconstructed git launch args = %v, want offline + exact original locator", got.LaunchArgs)
	}
}

func TestNPMPackageName(t *testing.T) {
	cases := map[string]string{
		"server":              "server",
		"server@^1":           "server",
		"@scope/server":       "@scope/server",
		"@scope/server@1.2.3": "@scope/server",
		"file:../server":      "",
		"github/acme":         "",
	}
	for input, want := range cases {
		if got := npmPackageName(input); got != want {
			t.Errorf("npmPackageName(%q) = %q, want %q", input, got, want)
		}
	}
}
