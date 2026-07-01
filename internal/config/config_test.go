package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInitCreatesDefaultConfigAndDirectories(t *testing.T) {
	home := t.TempDir()

	cfg, err := Init(home)
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	if cfg.DBPath != filepath.Join(home, "workspace.db") {
		t.Fatalf("DBPath = %q", cfg.DBPath)
	}
	if cfg.WorkDir != filepath.Join(home, "work") {
		t.Fatalf("WorkDir = %q", cfg.WorkDir)
	}
	if cfg.ReleaseDir != filepath.Join(home, "work", "releases") {
		t.Fatalf("ReleaseDir = %q", cfg.ReleaseDir)
	}

	for _, path := range []string{
		filepath.Join(home, "config.yaml"),
		filepath.Join(home, "workspace.db"),
		filepath.Join(home, "work"),
		filepath.Join(home, "work", "repos"),
		filepath.Join(home, "work", "requirements"),
		filepath.Join(home, "work", "releases"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected %s to exist: %v", path, err)
		}
	}

	loaded, err := Load(home)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.Tools.Codex[0] != "codex" || loaded.Tools.Claude[0] != "claude" {
		t.Fatalf("unexpected tool defaults: %+v", loaded.Tools)
	}
	if loaded.Tools.VSCode[0] != "code" || loaded.Tools.Cursor[0] != "cursor" || loaded.Tools.Zed[0] != "zed" {
		t.Fatalf("unexpected IDE defaults: %+v", loaded.Tools)
	}
}

func TestLoadCustomIDEToolCommands(t *testing.T) {
	home := t.TempDir()
	if _, err := Init(home); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	configPath := filepath.Join(home, "config.yaml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	updated := string(data) + `  vscode: "code-insiders"
  cursor: "cursor-nightly"
  zed: "zed-preview"
`
	if err := os.WriteFile(configPath, []byte(updated), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	loaded, err := Load(home)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.Tools.VSCode[0] != "code-insiders" {
		t.Fatalf("VSCode = %v", loaded.Tools.VSCode)
	}
	if loaded.Tools.Cursor[0] != "cursor-nightly" {
		t.Fatalf("Cursor = %v", loaded.Tools.Cursor)
	}
	if loaded.Tools.Zed[0] != "zed-preview" {
		t.Fatalf("Zed = %v", loaded.Tools.Zed)
	}
}
