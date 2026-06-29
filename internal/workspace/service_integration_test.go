package workspace

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"workspace-cli/internal/config"
	gitx "workspace-cli/internal/git"
	"workspace-cli/internal/store"
)

func TestCreateAndFinishRequirementAcrossGitRepo(t *testing.T) {
	ctx := context.Background()
	remote := seedRemote(t)
	home := t.TempDir()

	cfg, err := config.Init(home)
	if err != nil {
		t.Fatalf("config.Init() error = %v", err)
	}
	db, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	svc := NewService(cfg, db, gitx.NewManager(gitx.ExecRunner{}))
	if _, err := svc.AddRepo(ctx, AddRepoParams{Name: "backend", URL: remote, Remote: "origin", BaseBranch: "main"}); err != nil {
		t.Fatalf("AddRepo() error = %v", err)
	}

	req, err := svc.CreateRequirement(ctx, CreateRequirementParams{
		Title:     "Payment Flow",
		Key:       "pay-flow",
		RepoNames: []string{"backend"},
	})
	if err != nil {
		t.Fatalf("CreateRequirement() error = %v", err)
	}

	worktree := filepath.Join(req.WorkspacePath, "backend")
	runGit(t, worktree, "config", "user.name", "Workspace Test")
	runGit(t, worktree, "config", "user.email", "workspace@example.com")
	if err := os.WriteFile(filepath.Join(worktree, "feature.txt"), []byte("done\n"), 0o644); err != nil {
		t.Fatalf("write feature file: %v", err)
	}

	if err := svc.FinishRequirement(ctx, "pay-flow", "feat: finish pay-flow"); err != nil {
		t.Fatalf("FinishRequirement() error = %v", err)
	}

	if _, err := os.Stat(worktree); !os.IsNotExist(err) {
		t.Fatalf("worktree should be removed, stat error = %v", err)
	}

	branchHead := runGitOutput(t, remote, "rev-parse", "refs/heads/feature/pay-flow")
	if branchHead == "" {
		t.Fatal("expected remote feature branch to exist")
	}

	finished, err := db.GetRequirement(ctx, "pay-flow")
	if err != nil {
		t.Fatalf("GetRequirement() error = %v", err)
	}
	if finished.Status != store.RequirementStatusCompleted || !finished.ArchivedAt.Valid {
		t.Fatalf("requirement status = %q", finished.Status)
	}
}

func TestAddRepoDetectsMainWhenRemoteHEADIsStale(t *testing.T) {
	ctx := context.Background()
	remote := seedRemote(t)
	home := t.TempDir()

	cfg, err := config.Init(home)
	if err != nil {
		t.Fatalf("config.Init() error = %v", err)
	}
	db, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	svc := NewService(cfg, db, gitx.NewManager(gitx.ExecRunner{}))
	repo, err := svc.AddRepo(ctx, AddRepoParams{Name: "backend", URL: remote, Remote: "origin"})
	if err != nil {
		t.Fatalf("AddRepo() error = %v", err)
	}
	if repo.BaseBranch != "main" {
		t.Fatalf("BaseBranch = %q, want main", repo.BaseBranch)
	}
}

func TestCreateRequirementWithDetectedBaseBranch(t *testing.T) {
	ctx := context.Background()
	remote := seedRemote(t)
	home := t.TempDir()

	cfg, err := config.Init(home)
	if err != nil {
		t.Fatalf("config.Init() error = %v", err)
	}
	db, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	svc := NewService(cfg, db, gitx.NewManager(gitx.ExecRunner{}))
	if _, err := svc.AddRepo(ctx, AddRepoParams{Name: "backend", URL: remote, Remote: "origin"}); err != nil {
		t.Fatalf("AddRepo() error = %v", err)
	}
	if _, err := svc.CreateRequirement(ctx, CreateRequirementParams{
		Title:     "Payment Flow",
		Key:       "pay-flow",
		RepoNames: []string{"backend"},
	}); err != nil {
		t.Fatalf("CreateRequirement() with detected base error = %v", err)
	}
}

func TestUpdateRepoURLRemoteAndBaseBranch(t *testing.T) {
	ctx := context.Background()
	remote := seedRemote(t)
	newRemote := seedRemote(t)
	mirrorRemote := seedRemote(t)
	home := t.TempDir()

	cfg, err := config.Init(home)
	if err != nil {
		t.Fatalf("config.Init() error = %v", err)
	}
	db, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	svc := NewService(cfg, db, gitx.NewManager(gitx.ExecRunner{}))
	repo, err := svc.AddRepo(ctx, AddRepoParams{Name: "backend", URL: remote, Remote: "origin", BaseBranch: "main"})
	if err != nil {
		t.Fatalf("AddRepo() error = %v", err)
	}
	updated, err := svc.UpdateRepo(ctx, UpdateRepoParams{Name: "backend", URL: newRemote})
	if err != nil {
		t.Fatalf("UpdateRepo(url) error = %v", err)
	}
	if updated.URL != newRemote || updated.Remote != "origin" {
		t.Fatalf("url update = %+v", updated)
	}
	if got := runGitOutput(t, repo.BarePath, "remote", "get-url", "origin"); got != newRemote+"\n" {
		t.Fatalf("origin url = %q, want %q", got, newRemote+"\n")
	}

	updated, err = svc.UpdateRepo(ctx, UpdateRepoParams{Name: "backend", Remote: "upstream", BaseBranch: "develop"})
	if err != nil {
		t.Fatalf("UpdateRepo(remote/base) error = %v", err)
	}
	if updated.Remote != "upstream" || updated.URL != newRemote || updated.BaseBranch != "develop" {
		t.Fatalf("remote/base update = %+v", updated)
	}
	if got := runGitOutput(t, repo.BarePath, "remote", "get-url", "upstream"); got != newRemote+"\n" {
		t.Fatalf("upstream url = %q, want %q", got, newRemote+"\n")
	}

	updated, err = svc.UpdateRepo(ctx, UpdateRepoParams{Name: "backend", URL: mirrorRemote, Remote: "mirror"})
	if err != nil {
		t.Fatalf("UpdateRepo(remote+url) error = %v", err)
	}
	if updated.Remote != "mirror" || updated.URL != mirrorRemote || updated.BaseBranch != "develop" {
		t.Fatalf("remote+url update = %+v", updated)
	}
	if got := runGitOutput(t, repo.BarePath, "remote", "get-url", "mirror"); got != mirrorRemote+"\n" {
		t.Fatalf("mirror url = %q, want %q", got, mirrorRemote+"\n")
	}
	if _, err := runGitOutputAllowError(repo.BarePath, "remote", "get-url", "upstream"); err == nil {
		t.Fatal("upstream remote still exists after remote+url update, want renamed remote")
	}
}

func TestRepoUpdateAndRemoveRejectActiveRequirementReference(t *testing.T) {
	ctx := context.Background()
	_, db, svc, _ := createRequirementWithRepo(t, ctx)

	if _, err := svc.UpdateRepo(ctx, UpdateRepoParams{Name: "backend", BaseBranch: "develop"}); err == nil {
		t.Fatal("UpdateRepo() succeeded for repo referenced by active requirement, want error")
	}
	if err := svc.RemoveRepo(ctx, "backend"); err == nil {
		t.Fatal("RemoveRepo() succeeded for repo referenced by active requirement, want error")
	}
	repos, err := db.ListRepos(ctx, false)
	if err != nil {
		t.Fatalf("ListRepos() error = %v", err)
	}
	if len(repos) != 1 {
		t.Fatalf("repo should not be removed, repos = %+v", repos)
	}
}

func TestRemoveRepoSoftDeletesUnreferencedRepo(t *testing.T) {
	ctx := context.Background()
	remote := seedRemote(t)
	home := t.TempDir()

	cfg, err := config.Init(home)
	if err != nil {
		t.Fatalf("config.Init() error = %v", err)
	}
	db, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	svc := NewService(cfg, db, gitx.NewManager(gitx.ExecRunner{}))
	if _, err := svc.AddRepo(ctx, AddRepoParams{Name: "backend", URL: remote, Remote: "origin", BaseBranch: "main"}); err != nil {
		t.Fatalf("AddRepo() error = %v", err)
	}
	if err := svc.RemoveRepo(ctx, "backend"); err != nil {
		t.Fatalf("RemoveRepo() error = %v", err)
	}
	visible, err := db.ListRepos(ctx, false)
	if err != nil {
		t.Fatalf("ListRepos(false) error = %v", err)
	}
	if len(visible) != 0 {
		t.Fatalf("soft deleted repo should be hidden, got %+v", visible)
	}
	all, err := db.ListRepos(ctx, true)
	if err != nil {
		t.Fatalf("ListRepos(true) error = %v", err)
	}
	if len(all) != 1 || !all[0].DeletedAt.Valid {
		t.Fatalf("soft deleted repo missing from --all: %+v", all)
	}
}

func TestFinishCleanupPendingSelfHealsMissingWorktree(t *testing.T) {
	ctx := context.Background()
	_, db, svc, req := createRequirementWithRepo(t, ctx)
	rels, err := db.ListRequirementRepos(ctx, req.ID)
	if err != nil {
		t.Fatalf("ListRequirementRepos() error = %v", err)
	}
	if err := db.UpdateRequirementRepoStatus(ctx, rels[0].ID, store.RequirementRepoStatusPushed); err != nil {
		t.Fatalf("UpdateRequirementRepoStatus() error = %v", err)
	}
	if err := os.RemoveAll(rels[0].WorktreePath); err != nil {
		t.Fatalf("remove worktree: %v", err)
	}

	if err := svc.FinishRequirement(ctx, req.Key, "finish retry"); err != nil {
		t.Fatalf("FinishRequirement() cleanup retry error = %v", err)
	}

	finished, err := db.GetRequirement(ctx, req.Key)
	if err != nil {
		t.Fatalf("GetRequirement() error = %v", err)
	}
	if finished.Status != store.RequirementStatusCompleted || !finished.ArchivedAt.Valid {
		t.Fatalf("requirement after self-heal = %+v", finished)
	}
}

func TestFinishCleanupPendingRejectsDirtyWorktree(t *testing.T) {
	ctx := context.Background()
	_, db, svc, req := createRequirementWithRepo(t, ctx)
	rels, err := db.ListRequirementRepos(ctx, req.ID)
	if err != nil {
		t.Fatalf("ListRequirementRepos() error = %v", err)
	}
	if err := db.UpdateRequirementRepoStatus(ctx, rels[0].ID, store.RequirementRepoStatusPushed); err != nil {
		t.Fatalf("UpdateRequirementRepoStatus() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(rels[0].WorktreePath, "dirty.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatalf("write dirty file: %v", err)
	}

	if err := svc.FinishRequirement(ctx, req.Key, "finish retry"); err == nil {
		t.Fatal("FinishRequirement() succeeded on dirty cleanup-pending worktree, want error")
	}
	if _, err := os.Stat(rels[0].WorktreePath); err != nil {
		t.Fatalf("dirty worktree should remain: %v", err)
	}
}

func TestFinishCleanupPendingDoesNotCommitOrPushActiveRelations(t *testing.T) {
	ctx := context.Background()
	remoteA := seedRemote(t)
	remoteB := seedRemote(t)
	home := t.TempDir()

	cfg, err := config.Init(home)
	if err != nil {
		t.Fatalf("config.Init() error = %v", err)
	}
	db, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	svc := NewService(cfg, db, gitx.NewManager(gitx.ExecRunner{}))
	if _, err := svc.AddRepo(ctx, AddRepoParams{Name: "backend", URL: remoteA, Remote: "origin", BaseBranch: "main"}); err != nil {
		t.Fatalf("AddRepo(backend) error = %v", err)
	}
	if _, err := svc.AddRepo(ctx, AddRepoParams{Name: "frontend", URL: remoteB, Remote: "origin", BaseBranch: "main"}); err != nil {
		t.Fatalf("AddRepo(frontend) error = %v", err)
	}
	req, err := svc.CreateRequirement(ctx, CreateRequirementParams{
		Title:     "Payment Flow",
		Key:       "pay-flow",
		RepoNames: []string{"backend", "frontend"},
	})
	if err != nil {
		t.Fatalf("CreateRequirement() error = %v", err)
	}
	rels, err := db.ListRequirementRepos(ctx, req.ID)
	if err != nil {
		t.Fatalf("ListRequirementRepos() error = %v", err)
	}
	if err := db.UpdateRequirementRepoStatus(ctx, rels[0].ID, store.RequirementRepoStatusPushed); err != nil {
		t.Fatalf("UpdateRequirementRepoStatus() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(req.WorkspacePath, "frontend", "should-not-push.txt"), []byte("no push\n"), 0o644); err != nil {
		t.Fatalf("write frontend change: %v", err)
	}

	if err := svc.FinishRequirement(ctx, req.Key, "finish retry"); err == nil {
		t.Fatal("FinishRequirement() completed mixed cleanup-pending requirement, want error")
	}
	if _, err := runGitOutputAllowError(remoteB, "rev-parse", "refs/heads/feature/pay-flow"); err == nil {
		t.Fatal("cleanup-pending retry pushed active relation, want no remote feature branch")
	}
	current, err := db.GetRequirement(ctx, req.Key)
	if err != nil {
		t.Fatalf("GetRequirement() error = %v", err)
	}
	if current.Status != store.RequirementStatusActive || current.ArchivedAt.Valid {
		t.Fatalf("mixed cleanup-pending requirement should remain active, got %+v", current)
	}
	rels, err = db.ListRequirementRepos(ctx, req.ID)
	if err != nil {
		t.Fatalf("ListRequirementRepos() after finish error = %v", err)
	}
	if rels[1].Status != store.RequirementRepoStatusActive {
		t.Fatalf("active relation status = %s, want active", rels[1].Status)
	}
	if rels[0].Status != store.RequirementRepoStatusPushed {
		t.Fatalf("cleanup-pending relation status = %s, want pushed", rels[0].Status)
	}
}

func TestFinishCleanupPendingStopsWhenStatusCheckFails(t *testing.T) {
	ctx := context.Background()
	_, db, svc, req := createRequirementWithRepo(t, ctx)
	rels, err := db.ListRequirementRepos(ctx, req.ID)
	if err != nil {
		t.Fatalf("ListRequirementRepos() error = %v", err)
	}
	rel := rels[0]
	if err := db.UpdateRequirementRepoStatus(ctx, rel.ID, store.RequirementRepoStatusPushed); err != nil {
		t.Fatalf("UpdateRequirementRepoStatus() error = %v", err)
	}
	gitFile := filepath.Join(rel.WorktreePath, ".git")
	if err := os.Remove(gitFile); err != nil {
		t.Fatalf("remove .git file: %v", err)
	}

	if err := svc.FinishRequirement(ctx, req.Key, "finish retry"); err == nil {
		t.Fatal("FinishRequirement() succeeded when cleanup status check fails, want error")
	}
	if _, err := os.Stat(rel.WorktreePath); err != nil {
		t.Fatalf("worktree should remain after status check failure: %v", err)
	}
	rels, err = db.ListRequirementRepos(ctx, req.ID)
	if err != nil {
		t.Fatalf("ListRequirementRepos() after finish error = %v", err)
	}
	if rels[0].Status != store.RequirementRepoStatusCleanupFailed {
		t.Fatalf("relation status = %s, want cleanup_failed", rels[0].Status)
	}
	current, err := db.GetRequirement(ctx, req.Key)
	if err != nil {
		t.Fatalf("GetRequirement() error = %v", err)
	}
	if current.Status != store.RequirementStatusActive || current.ArchivedAt.Valid {
		t.Fatalf("requirement should remain active after cleanup status failure, got %+v", current)
	}
}

func TestFinishPushFailureDoesNotPersistPushedStatus(t *testing.T) {
	ctx := context.Background()
	remoteA := seedRemote(t)
	remoteB := seedRemote(t)
	home := t.TempDir()

	cfg, err := config.Init(home)
	if err != nil {
		t.Fatalf("config.Init() error = %v", err)
	}
	db, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	svc := NewService(cfg, db, gitx.NewManager(gitx.ExecRunner{}))
	if _, err := svc.AddRepo(ctx, AddRepoParams{Name: "backend", URL: remoteA, Remote: "origin", BaseBranch: "main"}); err != nil {
		t.Fatalf("AddRepo(backend) error = %v", err)
	}
	if _, err := svc.AddRepo(ctx, AddRepoParams{Name: "frontend", URL: remoteB, Remote: "origin", BaseBranch: "main"}); err != nil {
		t.Fatalf("AddRepo(frontend) error = %v", err)
	}
	req, err := svc.CreateRequirement(ctx, CreateRequirementParams{
		Title:     "Payment Flow",
		Key:       "pay-flow",
		RepoNames: []string{"backend", "frontend"},
	})
	if err != nil {
		t.Fatalf("CreateRequirement() error = %v", err)
	}
	runGit(t, filepath.Join(req.WorkspacePath, "frontend"), "remote", "set-url", "origin", filepath.Join(t.TempDir(), "missing.git"))

	if err := svc.FinishRequirement(ctx, req.Key, "finish retry"); err == nil {
		t.Fatal("FinishRequirement() succeeded with broken second remote, want error")
	}

	rels, err := db.ListRequirementRepos(ctx, req.ID)
	if err != nil {
		t.Fatalf("ListRequirementRepos() error = %v", err)
	}
	for _, rel := range rels {
		if rel.Status != store.RequirementRepoStatusActive {
			t.Fatalf("relation %s status = %s, want active after push failure", rel.RepoName, rel.Status)
		}
	}
	logs, err := db.ListOperationLogs(ctx, req.ID)
	if err != nil {
		t.Fatalf("ListOperationLogs() error = %v", err)
	}
	if len(logs) == 0 || logs[len(logs)-1].Operation != "push" || logs[len(logs)-1].Status != store.OperationStatusFailed {
		t.Fatalf("expected failed push operation log, got %+v", logs)
	}
}

func TestCreateRequirementLogsFetchFailure(t *testing.T) {
	ctx := context.Background()
	remote := seedRemote(t)
	home := t.TempDir()

	cfg, err := config.Init(home)
	if err != nil {
		t.Fatalf("config.Init() error = %v", err)
	}
	db, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	svc := NewService(cfg, db, gitx.NewManager(gitx.ExecRunner{}))
	repo, err := svc.AddRepo(ctx, AddRepoParams{Name: "backend", URL: remote, Remote: "origin", BaseBranch: "main"})
	if err != nil {
		t.Fatalf("AddRepo() error = %v", err)
	}
	if err := db.UpdateRepo(ctx, repo.ID, repo.URL, "missing", repo.BaseBranch); err != nil {
		t.Fatalf("UpdateRepo(db) error = %v", err)
	}

	if _, err := svc.CreateRequirement(ctx, CreateRequirementParams{
		Title:     "Payment Flow",
		Key:       "pay-flow",
		RepoNames: []string{"backend"},
	}); err == nil {
		t.Fatal("CreateRequirement() succeeded with missing remote, want error")
	}
	log := latestFailedOperationLog(t, cfg.DBPath, "fetch")
	assertFailedOperationLogHasRepoAndMessage(t, log, repo.ID)
}

func TestCreateRequirementFetchFailureDoesNotLeaveRequirement(t *testing.T) {
	ctx := context.Background()
	remote := seedRemote(t)
	home := t.TempDir()

	cfg, err := config.Init(home)
	if err != nil {
		t.Fatalf("config.Init() error = %v", err)
	}
	db, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	svc := NewService(cfg, db, gitx.NewManager(gitx.ExecRunner{}))
	repo, err := svc.AddRepo(ctx, AddRepoParams{Name: "backend", URL: remote, Remote: "origin", BaseBranch: "main"})
	if err != nil {
		t.Fatalf("AddRepo() error = %v", err)
	}
	if err := db.UpdateRepo(ctx, repo.ID, repo.URL, "missing", repo.BaseBranch); err != nil {
		t.Fatalf("UpdateRepo(db) error = %v", err)
	}

	if _, err := svc.CreateRequirement(ctx, CreateRequirementParams{
		Title:     "Payment Flow",
		Key:       "pay-flow",
		RepoNames: []string{"backend"},
	}); err == nil {
		t.Fatal("CreateRequirement() succeeded with missing remote, want error")
	}
	if _, err := db.GetRequirement(ctx, "pay-flow"); err == nil {
		t.Fatal("failed CreateRequirement() left requirement row behind")
	}
	if countOperationLogs(t, cfg.DBPath, "fetch") == 0 {
		t.Fatal("failed CreateRequirement() should retain fetch failure operation log")
	}

	if err := db.UpdateRepo(ctx, repo.ID, repo.URL, "origin", repo.BaseBranch); err != nil {
		t.Fatalf("restore repo remote: %v", err)
	}
	if _, err := svc.CreateRequirement(ctx, CreateRequirementParams{
		Title:     "Payment Flow",
		Key:       "pay-flow",
		RepoNames: []string{"backend"},
	}); err != nil {
		t.Fatalf("CreateRequirement() retry with same key error = %v", err)
	}
}

func TestCreateRequirementLogsWorktreeFailure(t *testing.T) {
	ctx := context.Background()
	remote := seedRemote(t)
	home := t.TempDir()

	cfg, err := config.Init(home)
	if err != nil {
		t.Fatalf("config.Init() error = %v", err)
	}
	db, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	svc := NewService(cfg, db, gitx.NewManager(gitx.ExecRunner{}))
	repo, err := svc.AddRepo(ctx, AddRepoParams{Name: "backend", URL: remote, Remote: "origin", BaseBranch: "main"})
	if err != nil {
		t.Fatalf("AddRepo() error = %v", err)
	}
	blockingPath := filepath.Join(cfg.ReqDir, "pay-flow", "backend")
	if err := os.MkdirAll(blockingPath, 0o755); err != nil {
		t.Fatalf("create blocking worktree path: %v", err)
	}

	if _, err := svc.CreateRequirement(ctx, CreateRequirementParams{
		Title:     "Payment Flow",
		Key:       "pay-flow",
		RepoNames: []string{"backend"},
	}); err == nil {
		t.Fatal("CreateRequirement() succeeded with existing worktree path, want error")
	}
	log := latestFailedOperationLog(t, cfg.DBPath, "worktree")
	assertFailedOperationLogHasRepoAndMessage(t, log, repo.ID)
}

func TestCreateRequirementWorktreeFailureCleansPartialRepoState(t *testing.T) {
	ctx := context.Background()
	remoteA := seedRemote(t)
	remoteB := seedRemote(t)
	home := t.TempDir()

	cfg, err := config.Init(home)
	if err != nil {
		t.Fatalf("config.Init() error = %v", err)
	}
	db, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	svc := NewService(cfg, db, gitx.NewManager(gitx.ExecRunner{}))
	if _, err := svc.AddRepo(ctx, AddRepoParams{Name: "backend", URL: remoteA, Remote: "origin", BaseBranch: "main"}); err != nil {
		t.Fatalf("AddRepo(backend) error = %v", err)
	}
	if _, err := svc.AddRepo(ctx, AddRepoParams{Name: "frontend", URL: remoteB, Remote: "origin", BaseBranch: "main"}); err != nil {
		t.Fatalf("AddRepo(frontend) error = %v", err)
	}
	backendWorktree := filepath.Join(cfg.ReqDir, "pay-flow", "backend")
	frontendBlockingPath := filepath.Join(cfg.ReqDir, "pay-flow", "frontend")
	if err := os.MkdirAll(frontendBlockingPath, 0o755); err != nil {
		t.Fatalf("create blocking frontend path: %v", err)
	}

	if _, err := svc.CreateRequirement(ctx, CreateRequirementParams{
		Title:     "Payment Flow",
		Key:       "pay-flow",
		RepoNames: []string{"backend", "frontend"},
	}); err == nil {
		t.Fatal("CreateRequirement() succeeded with existing second worktree path, want error")
	}
	if _, err := db.GetRequirement(ctx, "pay-flow"); err == nil {
		t.Fatal("failed multi-repo CreateRequirement() left requirement row behind")
	}
	if _, err := os.Stat(backendWorktree); !os.IsNotExist(err) {
		t.Fatalf("partial backend worktree should be removed, stat error = %v", err)
	}
	if _, err := os.Stat(frontendBlockingPath); err != nil {
		t.Fatalf("user-created blocking path should remain: %v", err)
	}
}

func TestCreateRequirementRelationFailureCleansPartialRepoState(t *testing.T) {
	ctx := context.Background()
	remoteA := seedRemote(t)
	remoteB := seedRemote(t)
	home := t.TempDir()

	cfg, err := config.Init(home)
	if err != nil {
		t.Fatalf("config.Init() error = %v", err)
	}
	db, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	svc := NewService(cfg, db, gitx.NewManager(gitx.ExecRunner{}))
	if _, err := svc.AddRepo(ctx, AddRepoParams{Name: "backend", URL: remoteA, Remote: "origin", BaseBranch: "main"}); err != nil {
		t.Fatalf("AddRepo(backend) error = %v", err)
	}
	frontend, err := svc.AddRepo(ctx, AddRepoParams{Name: "frontend", URL: remoteB, Remote: "origin", BaseBranch: "main"})
	if err != nil {
		t.Fatalf("AddRepo(frontend) error = %v", err)
	}
	createFailingRequirementRepoTrigger(t, cfg.DBPath, frontend.ID)
	backendWorktree := filepath.Join(cfg.ReqDir, "pay-flow", "backend")
	frontendWorktree := filepath.Join(cfg.ReqDir, "pay-flow", "frontend")

	if _, err := svc.CreateRequirement(ctx, CreateRequirementParams{
		Title:     "Payment Flow",
		Key:       "pay-flow",
		RepoNames: []string{"backend", "frontend"},
	}); err == nil {
		t.Fatal("CreateRequirement() succeeded with relation insert failure, want error")
	}
	if _, err := db.GetRequirement(ctx, "pay-flow"); err == nil {
		t.Fatal("failed relation CreateRequirement() left requirement row behind")
	}
	if got := countRequirementRepos(t, cfg.DBPath); got != 0 {
		t.Fatalf("failed relation CreateRequirement() left %d requirement_repos rows, want 0", got)
	}
	if _, err := os.Stat(backendWorktree); !os.IsNotExist(err) {
		t.Fatalf("partial backend worktree should be removed, stat error = %v", err)
	}
	if _, err := os.Stat(frontendWorktree); !os.IsNotExist(err) {
		t.Fatalf("failed frontend worktree should be removed, stat error = %v", err)
	}
	log := latestFailedOperationLog(t, cfg.DBPath, "relation")
	assertFailedOperationLogHasRepoAndMessage(t, log, frontend.ID)
}

func TestAddRepoToRequirementLogsFetchFailure(t *testing.T) {
	ctx := context.Background()
	remoteA := seedRemote(t)
	remoteB := seedRemote(t)
	home := t.TempDir()

	cfg, err := config.Init(home)
	if err != nil {
		t.Fatalf("config.Init() error = %v", err)
	}
	db, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	svc := NewService(cfg, db, gitx.NewManager(gitx.ExecRunner{}))
	if _, err := svc.AddRepo(ctx, AddRepoParams{Name: "backend", URL: remoteA, Remote: "origin", BaseBranch: "main"}); err != nil {
		t.Fatalf("AddRepo(backend) error = %v", err)
	}
	frontend, err := svc.AddRepo(ctx, AddRepoParams{Name: "frontend", URL: remoteB, Remote: "origin", BaseBranch: "main"})
	if err != nil {
		t.Fatalf("AddRepo(frontend) error = %v", err)
	}
	req, err := svc.CreateRequirement(ctx, CreateRequirementParams{
		Title:     "Payment Flow",
		Key:       "pay-flow",
		RepoNames: []string{"backend"},
	})
	if err != nil {
		t.Fatalf("CreateRequirement() error = %v", err)
	}
	if err := db.UpdateRepo(ctx, frontend.ID, frontend.URL, "missing", frontend.BaseBranch); err != nil {
		t.Fatalf("UpdateRepo(db) error = %v", err)
	}

	if _, err := svc.AddRepoToRequirement(ctx, req.Key, "frontend"); err == nil {
		t.Fatal("AddRepoToRequirement() succeeded with missing remote, want error")
	}
	logs, err := db.ListOperationLogs(ctx, req.ID)
	if err != nil {
		t.Fatalf("ListOperationLogs() error = %v", err)
	}
	if len(logs) == 0 || logs[len(logs)-1].Operation != "fetch" || logs[len(logs)-1].Status != store.OperationStatusFailed {
		t.Fatalf("expected failed add-repo fetch operation log, got %+v", logs)
	}
	assertFailedOperationLogFields(t, logs[len(logs)-1], req.ID, frontend.ID)
}

func TestAddRepoToRequirementRelationFailureRemovesCreatedWorktree(t *testing.T) {
	ctx := context.Background()
	remoteA := seedRemote(t)
	remoteB := seedRemote(t)
	home := t.TempDir()

	cfg, err := config.Init(home)
	if err != nil {
		t.Fatalf("config.Init() error = %v", err)
	}
	db, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	svc := NewService(cfg, db, gitx.NewManager(gitx.ExecRunner{}))
	if _, err := svc.AddRepo(ctx, AddRepoParams{Name: "backend", URL: remoteA, Remote: "origin", BaseBranch: "main"}); err != nil {
		t.Fatalf("AddRepo(backend) error = %v", err)
	}
	frontend, err := svc.AddRepo(ctx, AddRepoParams{Name: "frontend", URL: remoteB, Remote: "origin", BaseBranch: "main"})
	if err != nil {
		t.Fatalf("AddRepo(frontend) error = %v", err)
	}
	req, err := svc.CreateRequirement(ctx, CreateRequirementParams{
		Title:     "Payment Flow",
		Key:       "pay-flow",
		RepoNames: []string{"backend"},
	})
	if err != nil {
		t.Fatalf("CreateRequirement() error = %v", err)
	}
	createFailingRequirementRepoTrigger(t, cfg.DBPath, frontend.ID)
	frontendWorktree := filepath.Join(req.WorkspacePath, "frontend")

	if _, err := svc.AddRepoToRequirement(ctx, req.Key, "frontend"); err == nil {
		t.Fatal("AddRepoToRequirement() succeeded with relation insert failure, want error")
	}
	if _, err := os.Stat(frontendWorktree); !os.IsNotExist(err) {
		t.Fatalf("created frontend worktree should be removed after relation failure, stat error = %v", err)
	}
	current, err := db.GetRequirement(ctx, req.Key)
	if err != nil {
		t.Fatalf("existing requirement should remain after add-repo failure: %v", err)
	}
	if current.Status != store.RequirementStatusActive {
		t.Fatalf("requirement status = %s, want active", current.Status)
	}
	logs, err := db.ListOperationLogs(ctx, req.ID)
	if err != nil {
		t.Fatalf("ListOperationLogs() error = %v", err)
	}
	if len(logs) == 0 || logs[len(logs)-1].Operation != "relation" || logs[len(logs)-1].Status != store.OperationStatusFailed {
		t.Fatalf("expected failed relation operation log, got %+v", logs)
	}
}

func TestAddRepoToRequirementLogsWorktreeFailure(t *testing.T) {
	ctx := context.Background()
	remoteA := seedRemote(t)
	remoteB := seedRemote(t)
	home := t.TempDir()

	cfg, err := config.Init(home)
	if err != nil {
		t.Fatalf("config.Init() error = %v", err)
	}
	db, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	svc := NewService(cfg, db, gitx.NewManager(gitx.ExecRunner{}))
	if _, err := svc.AddRepo(ctx, AddRepoParams{Name: "backend", URL: remoteA, Remote: "origin", BaseBranch: "main"}); err != nil {
		t.Fatalf("AddRepo(backend) error = %v", err)
	}
	frontend, err := svc.AddRepo(ctx, AddRepoParams{Name: "frontend", URL: remoteB, Remote: "origin", BaseBranch: "main"})
	if err != nil {
		t.Fatalf("AddRepo(frontend) error = %v", err)
	}
	req, err := svc.CreateRequirement(ctx, CreateRequirementParams{
		Title:     "Payment Flow",
		Key:       "pay-flow",
		RepoNames: []string{"backend"},
	})
	if err != nil {
		t.Fatalf("CreateRequirement() error = %v", err)
	}
	blockingPath := filepath.Join(req.WorkspacePath, "frontend")
	if err := os.MkdirAll(blockingPath, 0o755); err != nil {
		t.Fatalf("create blocking add-repo path: %v", err)
	}

	if _, err := svc.AddRepoToRequirement(ctx, req.Key, "frontend"); err == nil {
		t.Fatal("AddRepoToRequirement() succeeded with existing worktree path, want error")
	}
	logs, err := db.ListOperationLogs(ctx, req.ID)
	if err != nil {
		t.Fatalf("ListOperationLogs() error = %v", err)
	}
	if len(logs) == 0 || logs[len(logs)-1].Operation != "worktree" || logs[len(logs)-1].Status != store.OperationStatusFailed {
		t.Fatalf("expected failed add-repo worktree operation log, got %+v", logs)
	}
	assertFailedOperationLogFields(t, logs[len(logs)-1], req.ID, frontend.ID)
}

func TestCreateRequirementUsesRemoteFeatureBranchWhenPresent(t *testing.T) {
	ctx := context.Background()
	remote := seedRemote(t)
	seedRemoteFeatureBranch(t, remote, "feature/pay-flow")
	home := t.TempDir()

	cfg, err := config.Init(home)
	if err != nil {
		t.Fatalf("config.Init() error = %v", err)
	}
	db, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	svc := NewService(cfg, db, gitx.NewManager(gitx.ExecRunner{}))
	if _, err := svc.AddRepo(ctx, AddRepoParams{Name: "backend", URL: remote, Remote: "origin", BaseBranch: "main"}); err != nil {
		t.Fatalf("AddRepo() error = %v", err)
	}
	req, err := svc.CreateRequirement(ctx, CreateRequirementParams{
		Title:     "Payment Flow",
		Key:       "pay-flow",
		RepoNames: []string{"backend"},
	})
	if err != nil {
		t.Fatalf("CreateRequirement() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(req.WorkspacePath, "backend", "remote-feature.txt")); err != nil {
		t.Fatalf("expected worktree from remote feature branch: %v", err)
	}
}

func TestCreateRequirementUsesRemoteFeatureBranchCreatedAfterRepoAdd(t *testing.T) {
	ctx := context.Background()
	remote := seedRemote(t)
	home := t.TempDir()

	cfg, err := config.Init(home)
	if err != nil {
		t.Fatalf("config.Init() error = %v", err)
	}
	db, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	svc := NewService(cfg, db, gitx.NewManager(gitx.ExecRunner{}))
	if _, err := svc.AddRepo(ctx, AddRepoParams{Name: "backend", URL: remote, Remote: "origin", BaseBranch: "main"}); err != nil {
		t.Fatalf("AddRepo() error = %v", err)
	}
	seedRemoteFeatureBranch(t, remote, "feature/pay-flow")

	req, err := svc.CreateRequirement(ctx, CreateRequirementParams{
		Title:     "Payment Flow",
		Key:       "pay-flow",
		RepoNames: []string{"backend"},
	})
	if err != nil {
		t.Fatalf("CreateRequirement() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(req.WorkspacePath, "backend", "remote-feature.txt")); err != nil {
		t.Fatalf("expected worktree from remote feature branch created after repo add: %v", err)
	}
}

func seedRemote(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	run(t, "", "git", "init", "--bare", remote)

	seed := filepath.Join(root, "seed")
	run(t, "", "git", "clone", remote, seed)
	runGit(t, seed, "checkout", "-b", "main")
	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("# seed\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	runGit(t, seed, "add", "README.md")
	run(t, seed, "git", "-c", "user.name=Workspace Test", "-c", "user.email=workspace@example.com", "commit", "-m", "init")
	runGit(t, seed, "push", "origin", "main")

	return remote
}

func seedRemoteFeatureBranch(t *testing.T, remote, branch string) {
	t.Helper()
	root := t.TempDir()
	seed := filepath.Join(root, "feature-seed")
	run(t, "", "git", "clone", remote, seed)
	runGit(t, seed, "checkout", "-b", branch, "origin/main")
	if err := os.WriteFile(filepath.Join(seed, "remote-feature.txt"), []byte("from remote feature\n"), 0o644); err != nil {
		t.Fatalf("write remote feature file: %v", err)
	}
	runGit(t, seed, "add", "remote-feature.txt")
	run(t, seed, "git", "-c", "user.name=Workspace Test", "-c", "user.email=workspace@example.com", "commit", "-m", "feature seed")
	runGit(t, seed, "push", "origin", branch)
}

func createRequirementWithRepo(t *testing.T, ctx context.Context) (config.Config, *store.DB, *Service, store.Requirement) {
	t.Helper()
	remote := seedRemote(t)
	home := t.TempDir()

	cfg, err := config.Init(home)
	if err != nil {
		t.Fatalf("config.Init() error = %v", err)
	}
	db, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	svc := NewService(cfg, db, gitx.NewManager(gitx.ExecRunner{}))
	if _, err := svc.AddRepo(ctx, AddRepoParams{Name: "backend", URL: remote, Remote: "origin", BaseBranch: "main"}); err != nil {
		t.Fatalf("AddRepo() error = %v", err)
	}
	req, err := svc.CreateRequirement(ctx, CreateRequirementParams{
		Title:     "Payment Flow",
		Key:       "pay-flow",
		RepoNames: []string{"backend"},
	})
	if err != nil {
		t.Fatalf("CreateRequirement() error = %v", err)
	}
	return cfg, db, svc, req
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	run(t, dir, "git", args...)
}

func runGitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v in %s failed: %v", args, dir, err)
	}
	return string(out)
}

func runGitOutputAllowError(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	return string(out), err
}

func assertFailedOperationLogFields(t *testing.T, log store.OperationLog, requirementID, repoID int64) {
	t.Helper()
	if !log.RequirementID.Valid || log.RequirementID.Int64 != requirementID {
		t.Fatalf("operation log requirement_id = %+v, want %d", log.RequirementID, requirementID)
	}
	if !log.RepoID.Valid || log.RepoID.Int64 != repoID {
		t.Fatalf("operation log repo_id = %+v, want %d", log.RepoID, repoID)
	}
	if !log.Message.Valid || log.Message.String == "" {
		t.Fatalf("operation log message should be present, got %+v", log.Message)
	}
}

func assertFailedOperationLogHasRepoAndMessage(t *testing.T, log store.OperationLog, repoID int64) {
	t.Helper()
	if !log.RequirementID.Valid || log.RequirementID.Int64 == 0 {
		t.Fatalf("operation log requirement_id should be present, got %+v", log.RequirementID)
	}
	if !log.RepoID.Valid || log.RepoID.Int64 != repoID {
		t.Fatalf("operation log repo_id = %+v, want %d", log.RepoID, repoID)
	}
	if !log.Message.Valid || log.Message.String == "" {
		t.Fatalf("operation log message should be present, got %+v", log.Message)
	}
}

func latestFailedOperationLog(t *testing.T, dbPath, operation string) store.OperationLog {
	t.Helper()
	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open raw sqlite: %v", err)
	}
	defer raw.Close()
	var log store.OperationLog
	err = raw.QueryRow(`SELECT id, requirement_id, repo_id, operation, status, message, created_at
		FROM operation_logs
		WHERE operation = ? AND status = ?
		ORDER BY id DESC
		LIMIT 1`, operation, store.OperationStatusFailed).Scan(&log.ID, &log.RequirementID, &log.RepoID, &log.Operation, &log.Status, &log.Message, &log.CreatedAt)
	if err != nil {
		t.Fatalf("latest failed operation log %s: %v", operation, err)
	}
	return log
}

func countOperationLogs(t *testing.T, dbPath, operation string) int {
	t.Helper()
	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open raw sqlite: %v", err)
	}
	defer raw.Close()
	var count int
	if err := raw.QueryRow(`SELECT COUNT(*) FROM operation_logs WHERE operation = ? AND status = ?`, operation, store.OperationStatusFailed).Scan(&count); err != nil {
		t.Fatalf("count operation logs: %v", err)
	}
	return count
}

func countRequirementRepos(t *testing.T, dbPath string) int {
	t.Helper()
	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open raw sqlite: %v", err)
	}
	defer raw.Close()
	var count int
	if err := raw.QueryRow(`SELECT COUNT(*) FROM requirement_repos`).Scan(&count); err != nil {
		t.Fatalf("count requirement_repos: %v", err)
	}
	return count
}

func createFailingRequirementRepoTrigger(t *testing.T, dbPath string, repoID int64) {
	t.Helper()
	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open raw sqlite: %v", err)
	}
	defer raw.Close()
	stmt := fmt.Sprintf(`CREATE TRIGGER fail_requirement_repo_insert
		BEFORE INSERT ON requirement_repos
		WHEN NEW.repo_id = %d
		BEGIN
			SELECT RAISE(ABORT, 'test relation insert failure');
		END`, repoID)
	if _, err := raw.Exec(stmt); err != nil {
		t.Fatalf("create failing relation trigger: %v", err)
	}
}

func run(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v in %s failed: %v\n%s", name, args, dir, err, out)
	}
}
