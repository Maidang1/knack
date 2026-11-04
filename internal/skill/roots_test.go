package skill

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDiscoverGoose(t *testing.T) {
	for _, tc := range []struct {
		name, platform string
		currentHome    bool
		appData        string
		wantNative     bool
		wantRedirected bool
	}{
		{name: "Windows default", platform: "windows", currentHome: true, wantNative: true},
		{name: "Windows redirected", platform: "windows", currentHome: true, appData: "external", wantRedirected: true},
		{name: "alternate home ignores APPDATA", platform: "windows", appData: "external", wantNative: true},
		{name: "relative APPDATA ignored", platform: "windows", currentHome: true, appData: "relative", wantNative: true},
		{name: "non-Windows keeps config only", platform: "linux", currentHome: true, appData: "external"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			compat := filepath.Join(home, ".config", "goose", "skills")
			native := filepath.Join(home, "AppData", "Roaming", "Block", "goose", "config", "skills")
			externalConfig := t.TempDir()
			redirected := filepath.Join(externalConfig, "Block", "goose", "config", "skills")
			for _, dir := range []string{compat, native, redirected} {
				writeSkill(t, filepath.Join(dir, "direct"), "Goose skill")
				writeSkill(t, filepath.Join(dir, "nested", "child"), "outside shallow boundary")
			}
			currentHome := filepath.Join(home, "another-home")
			if tc.currentHome {
				currentHome = home
			}
			appData := ""
			switch tc.appData {
			case "external":
				appData = externalConfig
			case "relative":
				appData = "relative-appdata"
			}
			roots := discover(home, tc.platform, currentHome, appData)
			wantCount := 1
			if tc.wantNative || tc.wantRedirected {
				wantCount++
			}
			if len(roots) != wantCount || !hasRoot(roots, compat, RoleAgent) ||
				hasRoot(roots, native, RoleAgent) != tc.wantNative ||
				hasRoot(roots, redirected, RoleAgent) != tc.wantRedirected {
				t.Fatalf("discovered unexpected roots: %+v", roots)
			}
			for _, root := range roots {
				wantID := windowsGooseID
				if root.Path == compat {
					wantID = "config-goose-skills"
				}
				if root.ID != wantID || root.Name != "Goose" || root.Role != RoleAgent {
					t.Fatalf("unstable Goose identity: %+v", root)
				}
				if _, ok := ProjectRoot(home, root, filepath.Join(home, "project")); ok {
					t.Fatalf("Goose config root acquired a project folder: %+v", root)
				}
			}
			snap := Scan(roots, nil)
			for _, e := range snap.Entries {
				if e.Name == "child" {
					t.Fatalf("nested Goose skill scanned: %+v", e)
				}
			}
		})
	}
}

func TestDiscoverTemporaryHomeIgnoresAPPDATA(t *testing.T) {
	home := t.TempDir()
	externalConfig := t.TempDir()
	writeSkill(t, filepath.Join(externalConfig, "Block", "goose", "config", "skills", "external"), "outside home")
	t.Setenv("APPDATA", externalConfig)
	if roots := Discover(home); len(roots) != 0 {
		t.Fatalf("temporary home read external APPDATA: %+v", roots)
	}
}

func TestDiscoverCurrentWindowsHomeUsesAPPDATA(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows config discovery")
	}
	home := t.TempDir()
	config := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", config)
	path := filepath.Join(config, "Block", "goose", "config", "skills")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	roots := Discover(home)
	if len(roots) != 1 || roots[0].ID != windowsGooseID || !hasRoot(roots, path, RoleAgent) {
		t.Fatalf("redirected APPDATA missing: %+v", roots)
	}
}
