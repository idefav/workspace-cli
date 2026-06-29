package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	Home     string
	DBPath   string
	WorkDir  string
	ReposDir string
	ReqDir   string
	Tools    Tools
}

type Tools struct {
	Codex  []string
	Claude []string
}

func Default(home string) Config {
	workDir := filepath.Join(home, "work")
	return Config{
		Home:     home,
		DBPath:   filepath.Join(home, "workspace.db"),
		WorkDir:  workDir,
		ReposDir: filepath.Join(workDir, "repos"),
		ReqDir:   filepath.Join(workDir, "requirements"),
		Tools: Tools{
			Codex:  []string{"codex"},
			Claude: []string{"claude"},
		},
	}
}

func Init(home string) (Config, error) {
	cfg := Default(home)
	for _, dir := range []string{cfg.Home, cfg.WorkDir, cfg.ReposDir, cfg.ReqDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return Config{}, fmt.Errorf("create %s: %w", dir, err)
		}
	}
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte(defaultYAML(cfg)), 0o644); err != nil {
		return Config{}, fmt.Errorf("write config: %w", err)
	}
	db, err := os.OpenFile(cfg.DBPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return Config{}, fmt.Errorf("create database file: %w", err)
	}
	if err := db.Close(); err != nil {
		return Config{}, fmt.Errorf("close database file: %w", err)
	}
	return cfg, nil
}

func Load(home string) (Config, error) {
	cfg := Default(home)
	data, err := os.ReadFile(filepath.Join(home, "config.yaml"))
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.Trim(strings.TrimSpace(parts[1]), `"`)
		switch key {
		case "db_path":
			cfg.DBPath = value
		case "work_dir":
			cfg.WorkDir = value
		case "repos_dir":
			cfg.ReposDir = value
		case "requirements_dir":
			cfg.ReqDir = value
		case "codex":
			if value != "" {
				cfg.Tools.Codex = []string{value}
			}
		case "claude":
			if value != "" {
				cfg.Tools.Claude = []string{value}
			}
		}
	}
	return cfg, nil
}

func defaultYAML(cfg Config) string {
	return fmt.Sprintf(`db_path: "%s"
work_dir: "%s"
repos_dir: "%s"
requirements_dir: "%s"
tools:
  codex: "%s"
  claude: "%s"
`, cfg.DBPath, cfg.WorkDir, cfg.ReposDir, cfg.ReqDir, cfg.Tools.Codex[0], cfg.Tools.Claude[0])
}
