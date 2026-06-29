package git

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type ExecRunner struct{}

func (ExecRunner) Run(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("%s %v: %w\n%s", name, args, err, out.String())
	}
	return out.String(), nil
}

type Manager struct {
	runner ExecRunner
}

func NewManager(runner ExecRunner) *Manager {
	return &Manager{runner: runner}
}

func (m *Manager) CloneBare(url, barePath string) error {
	if err := os.MkdirAll(filepath.Dir(barePath), 0o755); err != nil {
		return fmt.Errorf("create bare repo parent: %w", err)
	}
	if _, err := os.Stat(barePath); err == nil {
		return nil
	}
	if _, err := m.runner.Run("", "git", "clone", "--bare", url, barePath); err != nil {
		return fmt.Errorf("clone bare %s: %w", url, err)
	}
	return nil
}

func (m *Manager) Fetch(barePath, remote string) error {
	_, err := m.runner.Run("", "git", "--git-dir="+barePath, "fetch", remote, "+refs/heads/*:refs/remotes/"+remote+"/*")
	if err != nil {
		return fmt.Errorf("fetch %s: %w", barePath, err)
	}
	return nil
}

func (m *Manager) DefaultBranch(barePath, remote string) (string, error) {
	out, err := m.runner.Run("", "git", "--git-dir="+barePath, "ls-remote", "--symref", remote, "HEAD")
	if err != nil {
		return "", fmt.Errorf("detect default branch: %w", err)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "ref: refs/heads/") && strings.Contains(line, "\tHEAD") {
			branch := strings.TrimPrefix(strings.Fields(line)[1], "refs/heads/")
			if branch != "" {
				return branch, nil
			}
		}
	}
	return "", fmt.Errorf("default branch not found for remote %s", remote)
}

func (m *Manager) SetRemoteURL(barePath, remote, url string) error {
	if _, err := m.runner.Run("", "git", "--git-dir="+barePath, "remote", "set-url", remote, url); err != nil {
		return fmt.Errorf("set remote url: %w", err)
	}
	return nil
}

func (m *Manager) RenameRemote(barePath, oldRemote, newRemote string) error {
	if oldRemote == newRemote {
		return nil
	}
	if _, err := m.runner.Run("", "git", "--git-dir="+barePath, "remote", "rename", oldRemote, newRemote); err != nil {
		return fmt.Errorf("rename remote: %w", err)
	}
	return nil
}

func (m *Manager) CreateWorktree(barePath, worktreePath, branch, baseRef string) error {
	if _, err := os.Stat(worktreePath); err == nil {
		return fmt.Errorf("worktree path already exists: %s", worktreePath)
	}
	if err := os.MkdirAll(filepath.Dir(worktreePath), 0o755); err != nil {
		return fmt.Errorf("create worktree parent: %w", err)
	}
	if m.LocalBranchExists(barePath, branch) {
		if m.BranchInUse(barePath, branch) {
			return fmt.Errorf("branch %s is already used by another worktree", branch)
		}
		if _, err := m.runner.Run("", "git", "--git-dir="+barePath, "worktree", "add", worktreePath, branch); err != nil {
			return fmt.Errorf("add worktree from branch %s: %w", branch, err)
		}
		return nil
	}
	if _, err := m.runner.Run("", "git", "--git-dir="+barePath, "worktree", "add", "-b", branch, worktreePath, baseRef); err != nil {
		return fmt.Errorf("add worktree %s from %s: %w", branch, baseRef, err)
	}
	return nil
}

func (m *Manager) LocalBranchExists(barePath, branch string) bool {
	_, err := m.runner.Run("", "git", "--git-dir="+barePath, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil
}

func (m *Manager) RemoteBranchExists(barePath, remote, branch string) bool {
	_, err := m.runner.Run("", "git", "--git-dir="+barePath, "show-ref", "--verify", "--quiet", "refs/remotes/"+remote+"/"+branch)
	return err == nil
}

func (m *Manager) BranchInUse(barePath, branch string) bool {
	out, err := m.runner.Run("", "git", "--git-dir="+barePath, "worktree", "list", "--porcelain")
	if err != nil {
		return false
	}
	return strings.Contains(out, "\nbranch refs/heads/"+branch+"\n")
}

func (m *Manager) HasChanges(worktreePath string) (bool, error) {
	out, err := m.runner.Run(worktreePath, "git", "status", "--porcelain")
	if err != nil {
		return false, fmt.Errorf("git status: %w", err)
	}
	return strings.TrimSpace(out) != "", nil
}

func (m *Manager) CommitIdentity(worktreePath string) error {
	name, nameErr := m.runner.Run(worktreePath, "git", "config", "--get", "user.name")
	email, emailErr := m.runner.Run(worktreePath, "git", "config", "--get", "user.email")
	if nameErr != nil || emailErr != nil || strings.TrimSpace(name) == "" || strings.TrimSpace(email) == "" {
		return fmt.Errorf("missing git user.name or user.email in %s", worktreePath)
	}
	return nil
}

func (m *Manager) CommitAll(worktreePath, message string) error {
	if _, err := m.runner.Run(worktreePath, "git", "add", "-A"); err != nil {
		return fmt.Errorf("git add: %w", err)
	}
	if _, err := m.runner.Run(worktreePath, "git", "commit", "-m", message); err != nil {
		return fmt.Errorf("git commit: %w", err)
	}
	return nil
}

func (m *Manager) PushBranch(worktreePath, remote, branch string) error {
	if _, err := m.runner.Run(worktreePath, "git", "push", remote, "HEAD:refs/heads/"+branch); err != nil {
		return fmt.Errorf("git push: %w", err)
	}
	return nil
}

func (m *Manager) RemoveWorktree(barePath, worktreePath string) error {
	if _, err := os.Stat(worktreePath); os.IsNotExist(err) {
		return nil
	}
	if _, err := m.runner.Run("", "git", "--git-dir="+barePath, "worktree", "remove", worktreePath); err != nil {
		return fmt.Errorf("remove worktree: %w", err)
	}
	return nil
}
