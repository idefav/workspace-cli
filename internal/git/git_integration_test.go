package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

func TestForcePushBranchUsesExplicitExpectedSHA(t *testing.T) {
	remote := seedGitRemote(t)
	first := filepath.Join(t.TempDir(), "first")
	second := filepath.Join(t.TempDir(), "second")
	manager := NewManager(ExecRunner{})

	runGitCmd(t, "", "git", "clone", remote, first)
	runGitCmd(t, first, "git", "checkout", "-B", "release/test", "origin/main")
	if err := os.WriteFile(filepath.Join(first, "release.txt"), []byte("first\n"), 0o644); err != nil {
		t.Fatalf("write first release: %v", err)
	}
	runGitCmd(t, first, "git", "add", "release.txt")
	runGitCmd(t, first, "git", "-c", "user.name=Workspace Test", "-c", "user.email=workspace@example.com", "commit", "-m", "first release")
	runGitCmd(t, first, "git", "push", "origin", "HEAD:refs/heads/release/test")
	expectedOldSHA := strings.TrimSpace(gitOutput(t, first, "rev-parse", "HEAD"))

	runGitCmd(t, "", "git", "clone", remote, second)
	runGitCmd(t, second, "git", "checkout", "-B", "release/test", "origin/release/test")
	if err := os.WriteFile(filepath.Join(second, "release.txt"), []byte("second\n"), 0o644); err != nil {
		t.Fatalf("write second release: %v", err)
	}
	runGitCmd(t, second, "git", "add", "release.txt")
	runGitCmd(t, second, "git", "-c", "user.name=Workspace Test", "-c", "user.email=workspace@example.com", "commit", "-m", "second release")
	runGitCmd(t, second, "git", "push", "origin", "HEAD:refs/heads/release/test")

	runGitCmd(t, first, "git", "fetch", "origin")
	if err := os.WriteFile(filepath.Join(first, "release.txt"), []byte("third\n"), 0o644); err != nil {
		t.Fatalf("write third release: %v", err)
	}
	runGitCmd(t, first, "git", "add", "release.txt")
	runGitCmd(t, first, "git", "-c", "user.name=Workspace Test", "-c", "user.email=workspace@example.com", "commit", "-m", "third release")

	if err := manager.ForcePushBranch(first, "origin", "release/test", expectedOldSHA); err == nil {
		t.Fatal("ForcePushBranch() succeeded with stale expected SHA, want lease failure")
	}
}

func TestHasNewCommitsSinceUsesCommitGraph(t *testing.T) {
	remote := seedGitRemote(t)
	barePath := filepath.Join(t.TempDir(), "repo.git")
	worktree := filepath.Join(t.TempDir(), "worktree")
	manager := NewManager(ExecRunner{})

	if err := manager.CloneBare(remote, barePath); err != nil {
		t.Fatalf("CloneBare() error = %v", err)
	}
	if err := manager.Fetch(barePath, "origin"); err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	baseSHA, err := manager.RevParseBare(barePath, "origin/main")
	if err != nil {
		t.Fatalf("RevParseBare(base) error = %v", err)
	}
	hasNew, err := manager.HasNewCommitsSince(barePath, "origin", "main", baseSHA)
	if err != nil {
		t.Fatalf("HasNewCommitsSince(base) error = %v", err)
	}
	if hasNew {
		t.Fatal("HasNewCommitsSince() = true for unchanged branch, want false")
	}

	runGitCmd(t, "", "git", "clone", remote, worktree)
	runGitCmd(t, worktree, "git", "checkout", "-B", "main", "origin/main")
	if err := os.WriteFile(filepath.Join(worktree, "new.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatalf("write new commit: %v", err)
	}
	runGitCmd(t, worktree, "git", "add", "new.txt")
	runGitCmd(t, worktree, "git", "-c", "user.name=Workspace Test", "-c", "user.email=workspace@example.com", "commit", "-m", "new commit")
	runGitCmd(t, worktree, "git", "push", "origin", "HEAD:main")
	if err := manager.Fetch(barePath, "origin"); err != nil {
		t.Fatalf("Fetch(after new commit) error = %v", err)
	}
	hasNew, err = manager.HasNewCommitsSince(barePath, "origin", "main", baseSHA)
	if err != nil {
		t.Fatalf("HasNewCommitsSince(new commit) error = %v", err)
	}
	if !hasNew {
		t.Fatal("HasNewCommitsSince() = false after descendant commit, want true")
	}

	runGitCmd(t, worktree, "git", "push", "--force", "origin", baseSHA+":main")
	if err := manager.Fetch(barePath, "origin"); err != nil {
		t.Fatalf("Fetch(after rewind) error = %v", err)
	}
	hasNew, err = manager.HasNewCommitsSince(barePath, "origin", "main", baseSHA)
	if err != nil {
		t.Fatalf("HasNewCommitsSince(rewind) error = %v", err)
	}
	if hasNew {
		t.Fatal("HasNewCommitsSince() = true after branch rewind to old SHA, want false")
	}
}

func TestCommitHasParentBareDetectsMergeParent(t *testing.T) {
	remote := seedGitRemote(t)
	worktree := filepath.Join(t.TempDir(), "worktree")
	barePath := filepath.Join(t.TempDir(), "repo.git")
	manager := NewManager(ExecRunner{})

	runGitCmd(t, "", "git", "clone", remote, worktree)
	runGitCmd(t, worktree, "git", "checkout", "-B", "main", "origin/main")
	baseSHA := strings.TrimSpace(gitOutput(t, worktree, "rev-parse", "HEAD"))
	runGitCmd(t, worktree, "git", "checkout", "-b", "release/test")
	if err := os.WriteFile(filepath.Join(worktree, "release.txt"), []byte("release\n"), 0o644); err != nil {
		t.Fatalf("write release file: %v", err)
	}
	runGitCmd(t, worktree, "git", "add", "release.txt")
	runGitCmd(t, worktree, "git", "-c", "user.name=Workspace Test", "-c", "user.email=workspace@example.com", "commit", "-m", "release")
	releaseSHA := strings.TrimSpace(gitOutput(t, worktree, "rev-parse", "HEAD"))
	runGitCmd(t, worktree, "git", "checkout", "main")
	runGitCmd(t, worktree, "git", "-c", "user.name=Workspace Test", "-c", "user.email=workspace@example.com", "merge", "--no-ff", "-m", "publish release", "release/test")
	mergeSHA := strings.TrimSpace(gitOutput(t, worktree, "rev-parse", "HEAD"))
	runGitCmd(t, worktree, "git", "push", "origin", "HEAD:main")

	if err := manager.CloneBare(remote, barePath); err != nil {
		t.Fatalf("CloneBare() error = %v", err)
	}
	if err := manager.Fetch(barePath, "origin"); err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	hasParent, err := manager.CommitHasParentBare(barePath, mergeSHA, releaseSHA)
	if err != nil {
		t.Fatalf("CommitHasParentBare(release parent) error = %v", err)
	}
	if !hasParent {
		t.Fatal("CommitHasParentBare() = false for release merge parent, want true")
	}
	hasParent, err = manager.CommitHasParentBare(barePath, mergeSHA, baseSHA)
	if err != nil {
		t.Fatalf("CommitHasParentBare(base parent) error = %v", err)
	}
	if !hasParent {
		t.Fatal("CommitHasParentBare() = false for base merge parent, want true")
	}
	hasParent, err = manager.CommitHasParentBare(barePath, releaseSHA, baseSHA)
	if err != nil {
		t.Fatalf("CommitHasParentBare(non-merge parent) error = %v", err)
	}
	if !hasParent {
		t.Fatal("CommitHasParentBare() = false for direct parent, want true")
	}
	hasParent, err = manager.CommitHasParentBare(barePath, baseSHA, releaseSHA)
	if err != nil {
		t.Fatalf("CommitHasParentBare(non-parent) error = %v", err)
	}
	if hasParent {
		t.Fatal("CommitHasParentBare() = true for non-parent, want false")
	}
}

func TestCheckoutAndResetHard(t *testing.T) {
	remote := seedGitRemote(t)
	worktree := filepath.Join(t.TempDir(), "worktree")
	manager := NewManager(ExecRunner{})

	runGitCmd(t, "", "git", "clone", remote, worktree)
	runGitCmd(t, worktree, "git", "checkout", "-B", "main", "origin/main")
	baseSHA := strings.TrimSpace(gitOutput(t, worktree, "rev-parse", "HEAD"))
	runGitCmd(t, worktree, "git", "checkout", "-b", "feature/test")
	if err := os.WriteFile(filepath.Join(worktree, "feature.txt"), []byte("feature\n"), 0o644); err != nil {
		t.Fatalf("write feature file: %v", err)
	}
	runGitCmd(t, worktree, "git", "add", "feature.txt")
	runGitCmd(t, worktree, "git", "-c", "user.name=Workspace Test", "-c", "user.email=workspace@example.com", "commit", "-m", "feature")

	if err := manager.Checkout(worktree, "main"); err != nil {
		t.Fatalf("Checkout() error = %v", err)
	}
	if got := strings.TrimSpace(gitOutput(t, worktree, "branch", "--show-current")); got != "main" {
		t.Fatalf("current branch = %q, want main", got)
	}
	if err := os.WriteFile(filepath.Join(worktree, "README.md"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatalf("write dirty tracked file: %v", err)
	}
	if err := manager.ResetHard(worktree, baseSHA); err != nil {
		t.Fatalf("ResetHard() error = %v", err)
	}
	if hasChanges, err := manager.HasChanges(worktree); err != nil {
		t.Fatalf("HasChanges() error = %v", err)
	} else if hasChanges {
		t.Fatal("ResetHard() left tracked changes, want clean worktree")
	}
	if got := strings.TrimSpace(gitOutput(t, worktree, "rev-parse", "HEAD")); got != baseSHA {
		t.Fatalf("HEAD = %s, want %s", got, baseSHA)
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

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v in %s failed: %v", args, dir, err)
	}
	return string(out)
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
