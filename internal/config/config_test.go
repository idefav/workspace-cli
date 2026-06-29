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

	for _, path := range []string{
		filepath.Join(home, "config.yaml"),
		filepath.Join(home, "workspace.db"),
		filepath.Join(home, "work"),
		filepath.Join(home, "work", "repos"),
		filepath.Join(home, "work", "requirements"),
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
}
