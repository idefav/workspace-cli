package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestBranchInUseDetectsCheckedOutWorktreeBranch(t *testing.T) {
	remote := seedGitRemote(t)
	barePath := filepath.Join(t.TempDir(), "repo.git")
	manager := NewManager(ExecRunner{})

	if err := manager.CloneBare(remote, barePath); err != nil {
		t.Fatalf("CloneBare() error = %v", err)
	}
	if err := manager.Fetch(barePath, "origin"); err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	worktreePath := filepath.Join(t.TempDir(), "feature-worktree")
	if err := manager.CreateWorktree(barePath, worktreePath, "feature/pay-flow", "origin/main"); err != nil {
		t.Fatalf("CreateWorktree() error = %v", err)
	}

	if !manager.BranchInUse(barePath, "feature/pay-flow") {
		t.Fatal("BranchInUse() = false, want true for checked out worktree branch")
	}
	if manager.BranchInUse(barePath, "feature/other") {
		t.Fatal("BranchInUse() = true, want false for branch with no worktree")
	}
}

func seedGitRemote(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	runGitCmd(t, "", "git", "init", "--bare", remote)
	seed := filepath.Join(root, "seed")
	runGitCmd(t, "", "git", "clone", remote, seed)
	runGitCmd(t, seed, "git", "checkout", "-b", "main")
	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("# seed\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	runGitCmd(t, seed, "git", "add", "README.md")
	runGitCmd(t, seed, "git", "-c", "user.name=Workspace Test", "-c", "user.email=workspace@example.com", "commit", "-m", "init")
	runGitCmd(t, seed, "git", "push", "origin", "main")
	return remote
}

func runGitCmd(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v in %s failed: %v\n%s", name, args, dir, err, out)
	}
}
