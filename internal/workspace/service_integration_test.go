package workspace

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	if finished.Status != store.RequirementStatusActive || !finished.ReadyAt.Valid || finished.ArchivedAt.Valid {
		t.Fatalf("requirement after finish = %+v", finished)
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

func TestCreateRequirementFetchesLatestBaseBranchBeforeWorktree(t *testing.T) {
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
	latest := seedRemoteBaseCommit(t, remote, "main", "latest-base.txt", "latest from base\n")

	req, err := svc.CreateRequirement(ctx, CreateRequirementParams{
		Title:     "Payment Flow",
		Key:       "pay-flow",
		RepoNames: []string{"backend"},
	})
	if err != nil {
		t.Fatalf("CreateRequirement() error = %v", err)
	}
	worktree := filepath.Join(req.WorkspacePath, "backend")
	data, err := os.ReadFile(filepath.Join(worktree, "latest-base.txt"))
	if err != nil {
		t.Fatalf("expected worktree to include latest base file: %v", err)
	}
	if string(data) != "latest from base\n" {
		t.Fatalf("latest base file = %q", data)
	}
	if got := strings.TrimSpace(runGitOutput(t, worktree, "rev-parse", "HEAD")); got != latest {
		t.Fatalf("worktree HEAD = %s, want latest base %s", got, latest)
	}
}

func TestSyncRepoFetchesLatestBaseBranch(t *testing.T) {
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
	latest := seedRemoteBaseCommit(t, remote, "main", "latest-sync.txt", "latest sync\n")
	if got := strings.TrimSpace(runGitOutput(t, repo.BarePath, "rev-parse", "refs/remotes/origin/main")); got == latest {
		t.Fatalf("test setup expected bare repo to be stale before sync, got %s", got)
	}

	if err := svc.SyncRepo(ctx, "backend"); err != nil {
		t.Fatalf("SyncRepo() error = %v", err)
	}

	if got := strings.TrimSpace(runGitOutput(t, repo.BarePath, "rev-parse", "refs/remotes/origin/main")); got != latest {
		t.Fatalf("refs/remotes/origin/main = %s, want %s", got, latest)
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
	if finished.Status != store.RequirementStatusActive || !finished.ReadyAt.Valid || finished.ArchivedAt.Valid {
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

func TestReopenReadyRequirementRestoresWorktreeAndClearsReady(t *testing.T) {
	ctx := context.Background()
	_, db, svc, req := createRequirementWithRepo(t, ctx)
	rels, err := db.ListRequirementRepos(ctx, req.ID)
	if err != nil {
		t.Fatalf("ListRequirementRepos() error = %v", err)
	}
	rel := rels[0]
	if err := svc.git.RemoveWorktree(rel.Repo.BarePath, rel.WorktreePath); err != nil {
		t.Fatalf("RemoveWorktree() error = %v", err)
	}
	if err := db.UpdateRequirementRepoStatus(ctx, rel.ID, store.RequirementRepoStatusCompleted); err != nil {
		t.Fatalf("UpdateRequirementRepoStatus() error = %v", err)
	}
	if err := db.MarkRequirementReady(ctx, req.ID); err != nil {
		t.Fatalf("MarkRequirementReady() error = %v", err)
	}

	if err := svc.ReopenRequirement(ctx, req.Key); err != nil {
		t.Fatalf("ReopenRequirement() error = %v", err)
	}

	if _, err := os.Stat(rel.WorktreePath); err != nil {
		t.Fatalf("reopened worktree missing: %v", err)
	}
	reopened, err := db.GetRequirement(ctx, req.Key)
	if err != nil {
		t.Fatalf("GetRequirement() error = %v", err)
	}
	if reopened.ReadyAt.Valid {
		t.Fatalf("ReadyAt still set after reopen: %+v", reopened)
	}
	rels, err = db.ListRequirementRepos(ctx, req.ID)
	if err != nil {
		t.Fatalf("ListRequirementRepos() after reopen error = %v", err)
	}
	if rels[0].Status != store.RequirementRepoStatusActive {
		t.Fatalf("relation status = %s, want active", rels[0].Status)
	}
}

func TestReopenReadyRequirementKeepsDraftReleaseDraft(t *testing.T) {
	ctx := context.Background()
	_, db, svc, req := createRequirementWithRepo(t, ctx)
	rels, err := db.ListRequirementRepos(ctx, req.ID)
	if err != nil {
		t.Fatalf("ListRequirementRepos() error = %v", err)
	}
	rel := rels[0]
	if err := svc.git.RemoveWorktree(rel.Repo.BarePath, rel.WorktreePath); err != nil {
		t.Fatalf("RemoveWorktree() error = %v", err)
	}
	if err := db.UpdateRequirementRepoStatus(ctx, rel.ID, store.RequirementRepoStatusCompleted); err != nil {
		t.Fatalf("UpdateRequirementRepoStatus() error = %v", err)
	}
	if err := db.MarkRequirementReady(ctx, req.ID); err != nil {
		t.Fatalf("MarkRequirementReady() error = %v", err)
	}
	release, err := svc.CreateRelease(ctx, CreateReleaseParams{
		Title:           "2026-07-01 Release",
		Key:             "2026-07-01",
		RequirementKeys: []string{req.Key},
	})
	if err != nil {
		t.Fatalf("CreateRelease() error = %v", err)
	}

	if err := svc.ReopenRequirement(ctx, req.Key); err != nil {
		t.Fatalf("ReopenRequirement() error = %v", err)
	}
	currentRelease, err := db.GetRelease(ctx, release.Key)
	if err != nil {
		t.Fatalf("GetRelease() error = %v", err)
	}
	if currentRelease.Status != store.ReleaseStatusDraft {
		t.Fatalf("release status after reopen = %s, want draft", currentRelease.Status)
	}
}

func TestReopenReadyRequirementCleansCreatedWorktreesWhenDBUpdateFails(t *testing.T) {
	ctx := context.Background()
	cfg, db, svc, req := createRequirementWithRepo(t, ctx)
	rels, err := db.ListRequirementRepos(ctx, req.ID)
	if err != nil {
		t.Fatalf("ListRequirementRepos() error = %v", err)
	}
	rel := rels[0]
	if err := svc.git.RemoveWorktree(rel.Repo.BarePath, rel.WorktreePath); err != nil {
		t.Fatalf("RemoveWorktree() error = %v", err)
	}
	if err := db.UpdateRequirementRepoStatus(ctx, rel.ID, store.RequirementRepoStatusCompleted); err != nil {
		t.Fatalf("UpdateRequirementRepoStatus() error = %v", err)
	}
	if err := db.MarkRequirementReady(ctx, req.ID); err != nil {
		t.Fatalf("MarkRequirementReady() error = %v", err)
	}
	raw, err := sql.Open("sqlite", cfg.DBPath)
	if err != nil {
		t.Fatalf("open raw sqlite: %v", err)
	}
	defer raw.Close()
	if _, err := raw.Exec(`CREATE TRIGGER fail_reopen_relation_update
		BEFORE UPDATE OF status ON requirement_repos
		WHEN NEW.status = 'active'
		BEGIN
			SELECT RAISE(ABORT, 'forced reopen relation update failure');
		END`); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}

	if err := svc.ReopenRequirement(ctx, req.Key); err == nil {
		t.Fatal("ReopenRequirement() succeeded with failing relation update, want error")
	}
	if _, err := os.Stat(rel.WorktreePath); !os.IsNotExist(err) {
		t.Fatalf("reopen failure left worktree at %s, stat error = %v", rel.WorktreePath, err)
	}
	current, err := db.GetRequirement(ctx, req.Key)
	if err != nil {
		t.Fatalf("GetRequirement() error = %v", err)
	}
	if !current.ReadyAt.Valid {
		t.Fatalf("ReadyAt was cleared after failed reopen: %+v", current)
	}
	rels, err = db.ListRequirementRepos(ctx, req.ID)
	if err != nil {
		t.Fatalf("ListRequirementRepos() after reopen error = %v", err)
	}
	if rels[0].Status != store.RequirementRepoStatusCompleted {
		t.Fatalf("relation status after failed reopen = %s, want completed", rels[0].Status)
	}
}

func TestIntegrateReleaseMergesReadyRequirementsIntoReleaseBranch(t *testing.T) {
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
	reqA := createReadyFeatureRequirement(t, ctx, db, svc, "pay-flow", "pay.txt", "pay\n")
	reqB := createReadyFeatureRequirement(t, ctx, db, svc, "user-center", "user.txt", "user\n")
	release, err := svc.CreateRelease(ctx, CreateReleaseParams{
		Title:           "2026-07-01 Release",
		Key:             "2026-07-01",
		RequirementKeys: []string{reqA.Key, reqB.Key},
	})
	if err != nil {
		t.Fatalf("CreateRelease() error = %v", err)
	}

	if err := svc.IntegrateRelease(ctx, release.Key, false); err != nil {
		t.Fatalf("IntegrateRelease() error = %v", err)
	}

	releaseWorktree := filepath.Join(cfg.ReleaseDir, release.Slug, "backend")
	for _, filename := range []string{"pay.txt", "user.txt"} {
		if _, err := os.Stat(filepath.Join(releaseWorktree, filename)); err != nil {
			t.Fatalf("release worktree missing %s: %v", filename, err)
		}
	}
	if got := strings.TrimSpace(runGitOutput(t, remote, "rev-parse", "refs/heads/release/2026-07-01")); got == "" {
		t.Fatal("remote release branch was not pushed")
	}
	integrated, err := db.GetRelease(ctx, release.Key)
	if err != nil {
		t.Fatalf("GetRelease() error = %v", err)
	}
	if integrated.Status != store.ReleaseStatusIntegrated {
		t.Fatalf("release status = %s, want integrated", integrated.Status)
	}
	releaseRepos, err := db.ListReleaseRepos(ctx, release.ID)
	if err != nil {
		t.Fatalf("ListReleaseRepos() error = %v", err)
	}
	if len(releaseRepos) != 1 || releaseRepos[0].ReleaseSHA == "" || releaseRepos[0].IntegratedBaseSHA == "" {
		t.Fatalf("release repo snapshots = %+v", releaseRepos)
	}
}

func TestIntegrateReleaseRemovesObsoleteRepoWorktreeAfterRequirementRemoved(t *testing.T) {
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
	reqA := createReadyFeatureRequirementForRepo(t, ctx, db, svc, "backend", "pay-flow", "pay.txt", "pay\n")
	reqB := createReadyFeatureRequirementForRepo(t, ctx, db, svc, "frontend", "ui-flow", "ui.txt", "ui\n")
	release, err := svc.CreateRelease(ctx, CreateReleaseParams{
		Title:           "2026-07-01 Release",
		Key:             "2026-07-01",
		RequirementKeys: []string{reqA.Key, reqB.Key},
	})
	if err != nil {
		t.Fatalf("CreateRelease() error = %v", err)
	}
	if err := svc.IntegrateRelease(ctx, release.Key, false); err != nil {
		t.Fatalf("IntegrateRelease() error = %v", err)
	}
	frontendReleaseWorktree := filepath.Join(cfg.ReleaseDir, release.Slug, "frontend")
	if _, err := os.Stat(frontendReleaseWorktree); err != nil {
		t.Fatalf("frontend release worktree should exist after first integrate: %v", err)
	}

	if err := svc.RemoveRequirementFromRelease(ctx, release.Key, reqB.Key); err != nil {
		t.Fatalf("RemoveRequirementFromRelease() error = %v", err)
	}
	if err := svc.IntegrateRelease(ctx, release.Key, false); err != nil {
		t.Fatalf("IntegrateRelease() after remove error = %v", err)
	}
	releaseRepos, err := db.ListReleaseRepos(ctx, release.ID)
	if err != nil {
		t.Fatalf("ListReleaseRepos() error = %v", err)
	}
	if len(releaseRepos) != 1 || releaseRepos[0].Repo.Name != "backend" {
		t.Fatalf("release repos after remove = %+v, want only backend", releaseRepos)
	}
	if _, err := os.Stat(frontendReleaseWorktree); !os.IsNotExist(err) {
		t.Fatalf("obsolete frontend release worktree should be removed, stat error = %v", err)
	}
}

func TestPublishReleaseMergesReleaseBranchToBaseAndCompletesRequirements(t *testing.T) {
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
	reqA := createReadyFeatureRequirement(t, ctx, db, svc, "pay-flow", "pay.txt", "pay\n")
	reqB := createReadyFeatureRequirement(t, ctx, db, svc, "user-center", "user.txt", "user\n")
	release, err := svc.CreateRelease(ctx, CreateReleaseParams{
		Title:           "2026-07-01 Release",
		Key:             "2026-07-01",
		RequirementKeys: []string{reqA.Key, reqB.Key},
	})
	if err != nil {
		t.Fatalf("CreateRelease() error = %v", err)
	}
	if err := svc.IntegrateRelease(ctx, release.Key, false); err != nil {
		t.Fatalf("IntegrateRelease() error = %v", err)
	}

	releaseMessage := "release: 2026-07-01"
	if err := svc.PublishRelease(ctx, release.Key, true, releaseMessage); err != nil {
		t.Fatalf("PublishRelease() error = %v", err)
	}

	mainCheck := filepath.Join(t.TempDir(), "main-check")
	run(t, "", "git", "clone", remote, mainCheck)
	runGit(t, mainCheck, "checkout", "main")
	if got := strings.TrimSpace(runGitOutput(t, mainCheck, "log", "-1", "--pretty=%s")); got != releaseMessage {
		t.Fatalf("published merge subject = %q, want %q", got, releaseMessage)
	}
	for _, filename := range []string{"pay.txt", "user.txt"} {
		if _, err := os.Stat(filepath.Join(mainCheck, filename)); err != nil {
			t.Fatalf("published main missing %s: %v", filename, err)
		}
	}
	for _, key := range []string{reqA.Key, reqB.Key} {
		req, err := db.GetRequirement(ctx, key)
		if err != nil {
			t.Fatalf("GetRequirement(%s) error = %v", key, err)
		}
		if req.Status != store.RequirementStatusCompleted || !req.CompletedAt.Valid || !req.ArchivedAt.Valid {
			t.Fatalf("published requirement %s state = %+v", key, req)
		}
	}
	published, err := db.GetRelease(ctx, release.Key)
	if err != nil {
		t.Fatalf("GetRelease() error = %v", err)
	}
	if published.Status != store.ReleaseStatusPublished || !published.PublishedAt.Valid {
		t.Fatalf("release after publish = %+v", published)
	}
}

func TestPublishReleaseDoesNotCompleteRemovedRequirement(t *testing.T) {
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
	reqA := createReadyFeatureRequirement(t, ctx, db, svc, "pay-flow", "pay.txt", "pay\n")
	reqB := createReadyFeatureRequirement(t, ctx, db, svc, "user-center", "user.txt", "user\n")
	release, err := svc.CreateRelease(ctx, CreateReleaseParams{
		Title:           "2026-07-01 Release",
		Key:             "2026-07-01",
		RequirementKeys: []string{reqA.Key, reqB.Key},
	})
	if err != nil {
		t.Fatalf("CreateRelease() error = %v", err)
	}
	if err := svc.RemoveRequirementFromRelease(ctx, release.Key, reqB.Key); err != nil {
		t.Fatalf("RemoveRequirementFromRelease() error = %v", err)
	}
	if err := svc.IntegrateRelease(ctx, release.Key, false); err != nil {
		t.Fatalf("IntegrateRelease() error = %v", err)
	}
	if err := svc.PublishRelease(ctx, release.Key, true, "release: 2026-07-01"); err != nil {
		t.Fatalf("PublishRelease() error = %v", err)
	}

	mainCheck := filepath.Join(t.TempDir(), "main-check")
	run(t, "", "git", "clone", remote, mainCheck)
	runGit(t, mainCheck, "checkout", "main")
	if _, err := os.Stat(filepath.Join(mainCheck, "pay.txt")); err != nil {
		t.Fatalf("published main missing active requirement file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(mainCheck, "user.txt")); err == nil {
		t.Fatal("published main contains removed requirement file")
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat removed requirement file on main: %v", err)
	}
	activeReq, err := db.GetRequirement(ctx, reqA.Key)
	if err != nil {
		t.Fatalf("GetRequirement(active) error = %v", err)
	}
	if activeReq.Status != store.RequirementStatusCompleted || !activeReq.ArchivedAt.Valid {
		t.Fatalf("active requirement after publish = %+v, want completed archived", activeReq)
	}
	removedReq, err := db.GetRequirement(ctx, reqB.Key)
	if err != nil {
		t.Fatalf("GetRequirement(removed) error = %v", err)
	}
	if removedReq.Status != store.RequirementStatusActive || !removedReq.ReadyAt.Valid || removedReq.CompletedAt.Valid || removedReq.ArchivedAt.Valid {
		t.Fatalf("removed requirement after publish = %+v, want ready active", removedReq)
	}
	completion, err := svc.RequirementCompletion(ctx, removedReq)
	if err != nil {
		t.Fatalf("RequirementCompletion(removed) error = %v", err)
	}
	if completion != "" {
		t.Fatalf("removed active requirement completion = %q, want empty", completion)
	}
}

func TestIntegrateReleaseMergeConflictMarksFailedAndKeepsWorktree(t *testing.T) {
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
	reqA := createReadyFeatureRequirement(t, ctx, db, svc, "pay-flow", "README.md", "# pay\n")
	reqB := createReadyFeatureRequirement(t, ctx, db, svc, "user-center", "README.md", "# user\n")
	release, err := svc.CreateRelease(ctx, CreateReleaseParams{
		Title:           "2026-07-01 Release",
		Key:             "2026-07-01",
		RequirementKeys: []string{reqA.Key, reqB.Key},
	})
	if err != nil {
		t.Fatalf("CreateRelease() error = %v", err)
	}

	if err := svc.IntegrateRelease(ctx, release.Key, false); err == nil {
		t.Fatal("IntegrateRelease() succeeded with conflicting requirements, want merge conflict")
	}
	failed, err := db.GetRelease(ctx, release.Key)
	if err != nil {
		t.Fatalf("GetRelease() error = %v", err)
	}
	if failed.Status != store.ReleaseStatusFailed {
		t.Fatalf("release status = %s, want failed", failed.Status)
	}
	releaseWorktree := filepath.Join(cfg.ReleaseDir, release.Slug, "backend")
	conflicted, err := os.ReadFile(filepath.Join(releaseWorktree, "README.md"))
	if err != nil {
		t.Fatalf("read conflicted README from release worktree: %v", err)
	}
	if !strings.Contains(string(conflicted), "<<<<<<<") {
		t.Fatalf("release worktree did not preserve conflict markers:\n%s", conflicted)
	}
}

func TestPublishReleaseRejectsNonWorktreePublishPathWithoutFailingRelease(t *testing.T) {
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
	req := createReadyFeatureRequirement(t, ctx, db, svc, "pay-flow", "pay.txt", "pay\n")
	release, err := svc.CreateRelease(ctx, CreateReleaseParams{
		Title:           "2026-07-01 Release",
		Key:             "2026-07-01",
		RequirementKeys: []string{req.Key},
	})
	if err != nil {
		t.Fatalf("CreateRelease() error = %v", err)
	}
	if err := svc.IntegrateRelease(ctx, release.Key, false); err != nil {
		t.Fatalf("IntegrateRelease() error = %v", err)
	}
	releaseRepos, err := db.ListReleaseRepos(ctx, release.ID)
	if err != nil {
		t.Fatalf("ListReleaseRepos() error = %v", err)
	}
	if len(releaseRepos) != 1 {
		t.Fatalf("release repos = %+v, want one repo", releaseRepos)
	}
	if err := os.MkdirAll(releaseRepos[0].PublishWorktreePath, 0o755); err != nil {
		t.Fatalf("create non-worktree publish path: %v", err)
	}

	err = svc.PublishRelease(ctx, release.Key, true, "release: 2026-07-01")
	if err == nil || !strings.Contains(err.Error(), "publish path exists but is not a git worktree") {
		t.Fatalf("PublishRelease() error = %v, want non-worktree path error", err)
	}
	stillIntegrated, err := db.GetRelease(ctx, release.Key)
	if err != nil {
		t.Fatalf("GetRelease() error = %v", err)
	}
	if stillIntegrated.Status != store.ReleaseStatusIntegrated {
		t.Fatalf("release status after non-worktree publish path = %s, want integrated", stillIntegrated.Status)
	}
}

func TestPublishReleaseRejectsDirtyPublishWorktreeWithoutPushing(t *testing.T) {
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
	req := createReadyFeatureRequirement(t, ctx, db, svc, "pay-flow", "pay.txt", "pay\n")
	release, err := svc.CreateRelease(ctx, CreateReleaseParams{
		Title:           "2026-07-01 Release",
		Key:             "2026-07-01",
		RequirementKeys: []string{req.Key},
	})
	if err != nil {
		t.Fatalf("CreateRelease() error = %v", err)
	}
	if err := svc.IntegrateRelease(ctx, release.Key, false); err != nil {
		t.Fatalf("IntegrateRelease() error = %v", err)
	}
	releaseRepos, err := db.ListReleaseRepos(ctx, release.ID)
	if err != nil {
		t.Fatalf("ListReleaseRepos() error = %v", err)
	}
	if len(releaseRepos) != 1 {
		t.Fatalf("release repos = %+v, want one repo", releaseRepos)
	}
	remoteBase := releaseRepos[0].Repo.Remote + "/" + releaseRepos[0].TargetBranch
	if err := svc.git.CreateDetachedWorktree(releaseRepos[0].Repo.BarePath, releaseRepos[0].PublishWorktreePath, remoteBase); err != nil {
		t.Fatalf("CreateDetachedWorktree() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(releaseRepos[0].PublishWorktreePath, "dirty.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatalf("write dirty publish worktree file: %v", err)
	}

	err = svc.PublishRelease(ctx, release.Key, true, "release: 2026-07-01")
	if err == nil || !strings.Contains(err.Error(), "publish worktree has uncommitted changes") {
		t.Fatalf("PublishRelease() error = %v, want dirty publish worktree error", err)
	}
	checkout := filepath.Join(t.TempDir(), "main-check")
	run(t, "", "git", "clone", remote, checkout)
	runGit(t, checkout, "checkout", "main")
	if _, err := os.Stat(filepath.Join(checkout, "pay.txt")); !os.IsNotExist(err) {
		t.Fatalf("dirty publish worktree should prevent target push, stat error = %v", err)
	}
	stillIntegrated, err := db.GetRelease(ctx, release.Key)
	if err != nil {
		t.Fatalf("GetRelease() error = %v", err)
	}
	if stillIntegrated.Status != store.ReleaseStatusIntegrated {
		t.Fatalf("release status after dirty publish worktree = %s, want integrated", stillIntegrated.Status)
	}
}

func TestPublishReleaseAcceptsExistingCleanPublishWorktree(t *testing.T) {
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
	req := createReadyFeatureRequirement(t, ctx, db, svc, "pay-flow", "pay.txt", "pay\n")
	release, err := svc.CreateRelease(ctx, CreateReleaseParams{
		Title:           "2026-07-01 Release",
		Key:             "2026-07-01",
		RequirementKeys: []string{req.Key},
	})
	if err != nil {
		t.Fatalf("CreateRelease() error = %v", err)
	}
	if err := svc.IntegrateRelease(ctx, release.Key, false); err != nil {
		t.Fatalf("IntegrateRelease() error = %v", err)
	}
	releaseRepos, err := db.ListReleaseRepos(ctx, release.ID)
	if err != nil {
		t.Fatalf("ListReleaseRepos() error = %v", err)
	}
	if len(releaseRepos) != 1 {
		t.Fatalf("release repos = %+v, want one repo", releaseRepos)
	}
	remoteBase := releaseRepos[0].Repo.Remote + "/" + releaseRepos[0].TargetBranch
	if err := svc.git.CreateDetachedWorktree(releaseRepos[0].Repo.BarePath, releaseRepos[0].PublishWorktreePath, remoteBase); err != nil {
		t.Fatalf("CreateDetachedWorktree() error = %v", err)
	}

	if err := svc.PublishRelease(ctx, release.Key, true, "release: 2026-07-01"); err != nil {
		t.Fatalf("PublishRelease() with existing clean publish worktree error = %v", err)
	}
	checkout := filepath.Join(t.TempDir(), "main-check")
	run(t, "", "git", "clone", remote, checkout)
	runGit(t, checkout, "checkout", "main")
	if _, err := os.Stat(filepath.Join(checkout, "pay.txt")); err != nil {
		t.Fatalf("published main missing pay.txt: %v", err)
	}
}

func TestPublishReleaseResetsExistingCleanPublishWorktreeInPlace(t *testing.T) {
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
	req := createReadyFeatureRequirement(t, ctx, db, svc, "pay-flow", "pay.txt", "pay\n")
	release, err := svc.CreateRelease(ctx, CreateReleaseParams{
		Title:           "2026-07-01 Release",
		Key:             "2026-07-01",
		RequirementKeys: []string{req.Key},
	})
	if err != nil {
		t.Fatalf("CreateRelease() error = %v", err)
	}
	if err := svc.IntegrateRelease(ctx, release.Key, false); err != nil {
		t.Fatalf("IntegrateRelease() error = %v", err)
	}
	releaseRepos, err := db.ListReleaseRepos(ctx, release.ID)
	if err != nil {
		t.Fatalf("ListReleaseRepos() error = %v", err)
	}
	if len(releaseRepos) != 1 {
		t.Fatalf("release repos = %+v, want one repo", releaseRepos)
	}
	publishBranch := "publish-main"
	remoteBase := releaseRepos[0].Repo.Remote + "/" + releaseRepos[0].TargetBranch
	run(t, "", "git", "--git-dir="+releaseRepos[0].Repo.BarePath, "worktree", "add", "-b", publishBranch, releaseRepos[0].PublishWorktreePath, remoteBase)

	if err := svc.PublishRelease(ctx, release.Key, true, "release: 2026-07-01"); err != nil {
		t.Fatalf("PublishRelease() with existing clean publish worktree error = %v", err)
	}
	if got := strings.TrimSpace(runGitOutput(t, releaseRepos[0].PublishWorktreePath, "branch", "--show-current")); got != publishBranch {
		t.Fatalf("publish worktree current branch = %q, want existing branch %q to be reset in place", got, publishBranch)
	}
}

func TestPublishReleaseRejectsChangedFeatureAfterIntegrate(t *testing.T) {
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
	req := createReadyFeatureRequirement(t, ctx, db, svc, "pay-flow", "pay.txt", "pay\n")
	release, err := svc.CreateRelease(ctx, CreateReleaseParams{
		Title:           "2026-07-01 Release",
		Key:             "2026-07-01",
		RequirementKeys: []string{req.Key},
	})
	if err != nil {
		t.Fatalf("CreateRelease() error = %v", err)
	}
	if err := svc.IntegrateRelease(ctx, release.Key, false); err != nil {
		t.Fatalf("IntegrateRelease() error = %v", err)
	}
	pushFeatureUpdate(t, remote, req.FeatureBranch, "pay-late.txt", "late\n")

	if err := svc.PublishRelease(ctx, release.Key, true, "release: 2026-07-01"); err == nil {
		t.Fatal("PublishRelease() succeeded after feature branch changed, want stale error")
	}
	stale, err := db.GetRelease(ctx, release.Key)
	if err != nil {
		t.Fatalf("GetRelease() error = %v", err)
	}
	if stale.Status != store.ReleaseStatusStale {
		t.Fatalf("release status = %s, want stale", stale.Status)
	}
}

func TestPublishReleaseAllowsTargetBranchRewindWithoutNewCommits(t *testing.T) {
	ctx := context.Background()
	remote := seedRemote(t)
	oldBaseSHA := strings.TrimSpace(runGitOutput(t, "", "--git-dir="+remote, "rev-parse", "refs/heads/main"))
	seedRemoteBaseCommit(t, remote, "main", "base-extra.txt", "base\n")
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
	req := createReadyFeatureRequirement(t, ctx, db, svc, "pay-flow", "pay.txt", "pay\n")
	release, err := svc.CreateRelease(ctx, CreateReleaseParams{
		Title:           "2026-07-01 Release",
		Key:             "2026-07-01",
		RequirementKeys: []string{req.Key},
	})
	if err != nil {
		t.Fatalf("CreateRelease() error = %v", err)
	}
	if err := svc.IntegrateRelease(ctx, release.Key, false); err != nil {
		t.Fatalf("IntegrateRelease() error = %v", err)
	}
	run(t, "", "git", "--git-dir="+remote, "update-ref", "refs/heads/main", oldBaseSHA)

	if err := svc.PublishRelease(ctx, release.Key, true, "release: 2026-07-01"); err != nil {
		t.Fatalf("PublishRelease() should allow target branch rewind without new commits, got error = %v", err)
	}
	published, err := db.GetRelease(ctx, release.Key)
	if err != nil {
		t.Fatalf("GetRelease() error = %v", err)
	}
	if published.Status != store.ReleaseStatusPublished {
		t.Fatalf("release status = %s, want published", published.Status)
	}
}

func TestPublishReleaseRejectsChangedReleaseBranchAfterIntegration(t *testing.T) {
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
	req := createReadyFeatureRequirement(t, ctx, db, svc, "pay-flow", "pay.txt", "pay\n")
	release, err := svc.CreateRelease(ctx, CreateReleaseParams{
		Title:           "2026-07-01 Release",
		Key:             "2026-07-01",
		RequirementKeys: []string{req.Key},
	})
	if err != nil {
		t.Fatalf("CreateRelease() error = %v", err)
	}
	if err := svc.IntegrateRelease(ctx, release.Key, false); err != nil {
		t.Fatalf("IntegrateRelease() error = %v", err)
	}

	untested := filepath.Join(t.TempDir(), "untested-release")
	run(t, "", "git", "clone", remote, untested)
	runGit(t, untested, "checkout", release.BranchName)
	if err := os.WriteFile(filepath.Join(untested, "untested.txt"), []byte("untested release change\n"), 0o644); err != nil {
		t.Fatalf("write untested release file: %v", err)
	}
	runGit(t, untested, "add", "untested.txt")
	run(t, untested, "git", "-c", "user.name=Workspace Test", "-c", "user.email=workspace@example.com", "commit", "-m", "untested release change")
	runGit(t, untested, "push", "--force", "origin", "HEAD:refs/heads/"+release.BranchName)

	err = svc.PublishRelease(ctx, release.Key, true, "release: 2026-07-01")
	if err == nil {
		t.Fatal("PublishRelease() succeeded after release branch changed, want stale error")
	}
	if !strings.Contains(err.Error(), "release branch") || !strings.Contains(err.Error(), "reintegrate") {
		t.Fatalf("PublishRelease() error = %v, want release branch reintegrate error", err)
	}
	stale, err := db.GetRelease(ctx, release.Key)
	if err != nil {
		t.Fatalf("GetRelease() error = %v", err)
	}
	if stale.Status != store.ReleaseStatusStale {
		t.Fatalf("release status = %s, want stale", stale.Status)
	}
	mainCheck := filepath.Join(t.TempDir(), "main-check")
	run(t, "", "git", "clone", remote, mainCheck)
	runGit(t, mainCheck, "checkout", "main")
	if _, err := os.Stat(filepath.Join(mainCheck, "untested.txt")); !os.IsNotExist(err) {
		t.Fatalf("publish should not push changed release branch to main, stat error = %v", err)
	}
}

func TestPublishReleaseFirstRepoFailureCanRetry(t *testing.T) {
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
	req := createReadyFeatureRequirement(t, ctx, db, svc, "pay-flow", "pay.txt", "pay\n")
	release, err := svc.CreateRelease(ctx, CreateReleaseParams{
		Title:           "2026-07-01 Release",
		Key:             "2026-07-01",
		RequirementKeys: []string{req.Key},
	})
	if err != nil {
		t.Fatalf("CreateRelease() error = %v", err)
	}
	if err := svc.IntegrateRelease(ctx, release.Key, false); err != nil {
		t.Fatalf("IntegrateRelease() error = %v", err)
	}

	hookPath := filepath.Join(remote, "hooks", "pre-receive")
	if err := os.WriteFile(hookPath, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("write rejecting hook: %v", err)
	}
	if err := svc.PublishRelease(ctx, release.Key, true, "release: 2026-07-01"); err == nil {
		t.Fatal("PublishRelease() with broken first repo succeeded, want error")
	}
	failed, err := db.GetRelease(ctx, release.Key)
	if err != nil {
		t.Fatalf("GetRelease() error = %v", err)
	}
	if failed.Status != store.ReleaseStatusIntegrated {
		t.Fatalf("release status after first repo publish failure = %s, want integrated retryable state", failed.Status)
	}
	releaseRepos, err := db.ListReleaseRepos(ctx, release.ID)
	if err != nil {
		t.Fatalf("ListReleaseRepos() after first repo failure error = %v", err)
	}
	if len(releaseRepos) != 1 || releaseRepos[0].Status != store.ReleaseRepoStatusFailed {
		t.Fatalf("release repo after first repo failure = %+v, want failed", releaseRepos)
	}

	if err := os.Remove(hookPath); err != nil {
		t.Fatalf("remove rejecting hook: %v", err)
	}
	if err := svc.PublishRelease(ctx, release.Key, true, "release: 2026-07-01"); err != nil {
		t.Fatalf("PublishRelease() retry after first repo failure error = %v", err)
	}
	published, err := db.GetRelease(ctx, release.Key)
	if err != nil {
		t.Fatalf("GetRelease() after retry error = %v", err)
	}
	if published.Status != store.ReleaseStatusPublished {
		t.Fatalf("release status after retry = %s, want published", published.Status)
	}
}

func TestPublishReleaseSelfHealsWhenRemoteWasPushedButPublishedStateFailed(t *testing.T) {
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
	req := createReadyFeatureRequirement(t, ctx, db, svc, "pay-flow", "pay.txt", "pay\n")
	release, err := svc.CreateRelease(ctx, CreateReleaseParams{
		Title:           "2026-07-01 Release",
		Key:             "2026-07-01",
		RequirementKeys: []string{req.Key},
	})
	if err != nil {
		t.Fatalf("CreateRelease() error = %v", err)
	}
	if err := svc.IntegrateRelease(ctx, release.Key, false); err != nil {
		t.Fatalf("IntegrateRelease() error = %v", err)
	}

	raw, err := sql.Open("sqlite", cfg.DBPath)
	if err != nil {
		t.Fatalf("open raw sqlite: %v", err)
	}
	defer raw.Close()
	if _, err := raw.Exec(`CREATE TRIGGER fail_release_repo_published_update
		BEFORE UPDATE OF status ON release_repos
		WHEN NEW.status = 'published'
		BEGIN
			SELECT RAISE(ABORT, 'forced release repo published update failure');
		END`); err != nil {
		t.Fatalf("create publish failure trigger: %v", err)
	}

	if err := svc.PublishRelease(ctx, release.Key, true, "release: 2026-07-01"); err == nil {
		t.Fatal("PublishRelease() succeeded with failing published state update, want error")
	}
	mainCheck := filepath.Join(t.TempDir(), "main-check")
	run(t, "", "git", "clone", remote, mainCheck)
	runGit(t, mainCheck, "checkout", "main")
	if _, err := os.Stat(filepath.Join(mainCheck, "pay.txt")); err != nil {
		t.Fatalf("first PublishRelease() should have pushed remote main before DB failure: %v", err)
	}
	releaseRepos, err := db.ListReleaseRepos(ctx, release.ID)
	if err != nil {
		t.Fatalf("ListReleaseRepos() after failed state update error = %v", err)
	}
	if len(releaseRepos) != 1 || releaseRepos[0].Status != store.ReleaseRepoStatusIntegrated {
		t.Fatalf("release repo after failed state update = %+v, want integrated before self-heal", releaseRepos)
	}

	if _, err := raw.Exec(`DROP TRIGGER fail_release_repo_published_update`); err != nil {
		t.Fatalf("drop publish failure trigger: %v", err)
	}
	if err := svc.PublishRelease(ctx, release.Key, true, "release: 2026-07-01"); err != nil {
		t.Fatalf("PublishRelease() retry should self-heal already pushed remote, got error = %v", err)
	}
	published, err := db.GetRelease(ctx, release.Key)
	if err != nil {
		t.Fatalf("GetRelease() after self-heal error = %v", err)
	}
	if published.Status != store.ReleaseStatusPublished {
		t.Fatalf("release status after self-heal = %s, want published", published.Status)
	}
	releaseRepos, err = db.ListReleaseRepos(ctx, release.ID)
	if err != nil {
		t.Fatalf("ListReleaseRepos() after self-heal error = %v", err)
	}
	if len(releaseRepos) != 1 || releaseRepos[0].Status != store.ReleaseRepoStatusPublished || !releaseRepos[0].PublishedSHA.Valid {
		t.Fatalf("release repo after self-heal = %+v, want published with published_sha", releaseRepos)
	}
	completed, err := db.GetRequirement(ctx, req.Key)
	if err != nil {
		t.Fatalf("GetRequirement() after self-heal error = %v", err)
	}
	if completed.Status != store.RequirementStatusCompleted || !completed.ArchivedAt.Valid {
		t.Fatalf("requirement after self-heal = %+v, want completed archived", completed)
	}
}

func TestPublishReleaseSelfHealThenTargetChangeRequiresManualHandling(t *testing.T) {
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
	writeAndPushRequirementRepoFeature(t, ctx, db, svc, req, "backend", "backend.txt", "backend\n")
	writeAndPushRequirementRepoFeature(t, ctx, db, svc, req, "frontend", "frontend.txt", "frontend\n")
	if err := db.MarkRequirementReady(ctx, req.ID); err != nil {
		t.Fatalf("MarkRequirementReady() error = %v", err)
	}
	ready, err := db.GetRequirement(ctx, req.Key)
	if err != nil {
		t.Fatalf("GetRequirement() error = %v", err)
	}
	release, err := svc.CreateRelease(ctx, CreateReleaseParams{
		Title:           "2026-07-01 Release",
		Key:             "2026-07-01",
		RequirementKeys: []string{ready.Key},
	})
	if err != nil {
		t.Fatalf("CreateRelease() error = %v", err)
	}
	if err := svc.IntegrateRelease(ctx, release.Key, false); err != nil {
		t.Fatalf("IntegrateRelease() error = %v", err)
	}
	releaseRepos, err := db.ListReleaseRepos(ctx, release.ID)
	if err != nil {
		t.Fatalf("ListReleaseRepos() error = %v", err)
	}
	var backendReleaseRepoID int64
	for _, releaseRepo := range releaseRepos {
		if releaseRepo.Repo.Name == "backend" {
			backendReleaseRepoID = releaseRepo.ID
		}
	}
	if backendReleaseRepoID == 0 {
		t.Fatalf("backend release repo not found: %+v", releaseRepos)
	}

	raw, err := sql.Open("sqlite", cfg.DBPath)
	if err != nil {
		t.Fatalf("open raw sqlite: %v", err)
	}
	defer raw.Close()
	if _, err := raw.Exec(`CREATE TRIGGER fail_backend_release_repo_published_update
		BEFORE UPDATE OF status ON release_repos
		WHEN NEW.status = 'published' AND OLD.id = ` + fmt.Sprint(backendReleaseRepoID) + `
		BEGIN
			SELECT RAISE(ABORT, 'forced backend release repo published update failure');
		END`); err != nil {
		t.Fatalf("create publish failure trigger: %v", err)
	}
	if err := svc.PublishRelease(ctx, release.Key, true, "release: 2026-07-01"); err == nil {
		t.Fatal("PublishRelease() succeeded with failing backend published state update, want error")
	}
	if _, err := raw.Exec(`DROP TRIGGER fail_backend_release_repo_published_update`); err != nil {
		t.Fatalf("drop publish failure trigger: %v", err)
	}
	seedRemoteBaseCommit(t, remoteB, "main", "external.txt", "external\n")

	err = svc.PublishRelease(ctx, release.Key, true, "release: 2026-07-01")
	if err == nil {
		t.Fatal("PublishRelease() retry succeeded after self-heal plus target change, want manual handling error")
	}
	if !strings.Contains(err.Error(), "publish-in-progress") || !strings.Contains(err.Error(), "manual handling") {
		t.Fatalf("PublishRelease() error = %v, want publish-in-progress manual handling", err)
	}
	if strings.Contains(err.Error(), "reintegrate") {
		t.Fatalf("PublishRelease() error = %v, should not suggest reintegrate after self-heal made release publish-in-progress", err)
	}
	unchanged, err := db.GetRelease(ctx, release.Key)
	if err != nil {
		t.Fatalf("GetRelease() error = %v", err)
	}
	if unchanged.Status == store.ReleaseStatusStale {
		t.Fatalf("release status = %s, want non-stale manual handling state", unchanged.Status)
	}
}

func TestReleaseStatusMarksIntegratedReleaseStaleWhenFeatureChanges(t *testing.T) {
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
	req := createReadyFeatureRequirement(t, ctx, db, svc, "pay-flow", "pay.txt", "pay\n")
	release, err := svc.CreateRelease(ctx, CreateReleaseParams{
		Title:           "2026-07-01 Release",
		Key:             "2026-07-01",
		RequirementKeys: []string{req.Key},
	})
	if err != nil {
		t.Fatalf("CreateRelease() error = %v", err)
	}
	if err := svc.IntegrateRelease(ctx, release.Key, false); err != nil {
		t.Fatalf("IntegrateRelease() error = %v", err)
	}
	pushFeatureUpdate(t, remote, req.FeatureBranch, "late.txt", "late\n")

	refreshed, err := svc.RefreshReleaseStatus(ctx, release.Key)
	if err != nil {
		t.Fatalf("RefreshReleaseStatus() error = %v", err)
	}
	if refreshed.Status != store.ReleaseStatusStale {
		t.Fatalf("release status = %s, want stale", refreshed.Status)
	}
}

func TestReleaseStatusMarksIntegratedReleaseStaleWhenReleaseBranchChanges(t *testing.T) {
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
	req := createReadyFeatureRequirement(t, ctx, db, svc, "pay-flow", "pay.txt", "pay\n")
	release, err := svc.CreateRelease(ctx, CreateReleaseParams{
		Title:           "2026-07-01 Release",
		Key:             "2026-07-01",
		RequirementKeys: []string{req.Key},
	})
	if err != nil {
		t.Fatalf("CreateRelease() error = %v", err)
	}
	if err := svc.IntegrateRelease(ctx, release.Key, false); err != nil {
		t.Fatalf("IntegrateRelease() error = %v", err)
	}
	untested := filepath.Join(t.TempDir(), "untested-release")
	run(t, "", "git", "clone", remote, untested)
	runGit(t, untested, "checkout", release.BranchName)
	if err := os.WriteFile(filepath.Join(untested, "untested-status.txt"), []byte("untested\n"), 0o644); err != nil {
		t.Fatalf("write untested status file: %v", err)
	}
	runGit(t, untested, "add", "untested-status.txt")
	run(t, untested, "git", "-c", "user.name=Workspace Test", "-c", "user.email=workspace@example.com", "commit", "-m", "untested status release change")
	runGit(t, untested, "push", "--force", "origin", "HEAD:refs/heads/"+release.BranchName)

	refreshed, err := svc.RefreshReleaseStatus(ctx, release.Key)
	if err != nil {
		t.Fatalf("RefreshReleaseStatus() error = %v", err)
	}
	if refreshed.Status != store.ReleaseStatusStale {
		t.Fatalf("release status = %s, want stale", refreshed.Status)
	}
	diagnostics, err := svc.ReleaseDiagnostics(ctx, refreshed)
	if err != nil {
		t.Fatalf("ReleaseDiagnostics() error = %v", err)
	}
	var found bool
	for _, reason := range diagnostics.StaleReasons {
		if strings.Contains(reason, "release branch") {
			found = true
		}
	}
	if !found {
		t.Fatalf("stale reasons = %v, want release branch reason", diagnostics.StaleReasons)
	}
}

func TestReleaseStatusMarksIntegratedReleaseStaleWhenRequirementNoLongerReady(t *testing.T) {
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
	req := createReadyFeatureRequirement(t, ctx, db, svc, "pay-flow", "pay.txt", "pay\n")
	first, err := svc.CreateRelease(ctx, CreateReleaseParams{
		Title:           "2026-07-01 Release",
		Key:             "2026-07-01",
		RequirementKeys: []string{req.Key},
	})
	if err != nil {
		t.Fatalf("CreateRelease(first) error = %v", err)
	}
	second, err := svc.CreateRelease(ctx, CreateReleaseParams{
		Title:           "2026-07-02 Release",
		Key:             "2026-07-02",
		RequirementKeys: []string{req.Key},
	})
	if err != nil {
		t.Fatalf("CreateRelease(second) error = %v", err)
	}
	if err := svc.IntegrateRelease(ctx, second.Key, false); err != nil {
		t.Fatalf("IntegrateRelease(second) error = %v", err)
	}
	if err := svc.IntegrateRelease(ctx, first.Key, false); err != nil {
		t.Fatalf("IntegrateRelease(first) error = %v", err)
	}
	if err := svc.PublishRelease(ctx, first.Key, true, "release: 2026-07-01"); err != nil {
		t.Fatalf("PublishRelease(first) error = %v", err)
	}

	refreshed, err := svc.RefreshReleaseStatus(ctx, second.Key)
	if err != nil {
		t.Fatalf("RefreshReleaseStatus(second) error = %v", err)
	}
	if refreshed.Status != store.ReleaseStatusStale {
		t.Fatalf("second release status = %s, want stale", refreshed.Status)
	}
	diagnostics, err := svc.ReleaseDiagnostics(ctx, refreshed)
	if err != nil {
		t.Fatalf("ReleaseDiagnostics(second) error = %v", err)
	}
	var found bool
	for _, reason := range diagnostics.StaleReasons {
		if strings.Contains(reason, "not ready") {
			found = true
		}
	}
	if !found {
		t.Fatalf("stale reasons = %v, want not ready reason", diagnostics.StaleReasons)
	}
}

func TestPublishReleasePartialSuccessCanRetryRemainingRepos(t *testing.T) {
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
	writeAndPushRequirementRepoFeature(t, ctx, db, svc, req, "backend", "backend.txt", "backend\n")
	writeAndPushRequirementRepoFeature(t, ctx, db, svc, req, "frontend", "frontend.txt", "frontend\n")
	if err := db.MarkRequirementReady(ctx, req.ID); err != nil {
		t.Fatalf("MarkRequirementReady() error = %v", err)
	}
	ready, err := db.GetRequirement(ctx, req.Key)
	if err != nil {
		t.Fatalf("GetRequirement() error = %v", err)
	}
	extra := createReadyFeatureRequirement(t, ctx, db, svc, "extra-flow", "extra.txt", "extra\n")
	release, err := svc.CreateRelease(ctx, CreateReleaseParams{
		Title:           "2026-07-01 Release",
		Key:             "2026-07-01",
		RequirementKeys: []string{ready.Key},
	})
	if err != nil {
		t.Fatalf("CreateRelease() error = %v", err)
	}
	if err := svc.IntegrateRelease(ctx, release.Key, false); err != nil {
		t.Fatalf("IntegrateRelease() error = %v", err)
	}

	hookPath := filepath.Join(remoteB, "hooks", "pre-receive")
	if err := os.WriteFile(hookPath, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("write rejecting hook: %v", err)
	}
	if err := svc.PublishRelease(ctx, release.Key, true, "release: 2026-07-01"); err == nil {
		t.Fatal("PublishRelease() with broken second repo succeeded, want error")
	}
	if count := countReleaseOperationLogs(t, cfg.DBPath, release.ID, "release_publish_push"); count == 0 {
		t.Fatal("PublishRelease() failure did not write release operation log")
	}
	releaseRepos, err := db.ListReleaseRepos(ctx, release.ID)
	if err != nil {
		t.Fatalf("ListReleaseRepos() after failed publish error = %v", err)
	}
	if releaseRepos[0].Status != store.ReleaseRepoStatusPublished || !releaseRepos[0].PublishedSHA.Valid {
		t.Fatalf("first repo should be published after partial success: %+v", releaseRepos[0])
	}
	if releaseRepos[1].Status == store.ReleaseRepoStatusPublished {
		t.Fatalf("second repo should not be published after failed push: %+v", releaseRepos[1])
	}
	if releaseRepos[1].Status != store.ReleaseRepoStatusFailed {
		t.Fatalf("second repo should be marked failed after failed push: %+v", releaseRepos[1])
	}
	if _, err := svc.AddRequirementToRelease(ctx, release.Key, extra.Key); err == nil {
		t.Fatal("AddRequirementToRelease() succeeded during publish-in-progress, want error")
	}
	if err := svc.RemoveRequirementFromRelease(ctx, release.Key, ready.Key); err == nil {
		t.Fatal("RemoveRequirementFromRelease() succeeded during publish-in-progress, want error")
	}
	if err := svc.IntegrateRelease(ctx, release.Key, true); err == nil {
		t.Fatal("IntegrateRelease() succeeded during publish-in-progress, want error")
	}
	if err := svc.ReopenRequirement(ctx, ready.Key); err == nil {
		t.Fatal("ReopenRequirement() succeeded during publish-in-progress, want error")
	}

	if err := os.Remove(hookPath); err != nil {
		t.Fatalf("remove rejecting hook: %v", err)
	}
	if err := svc.PublishRelease(ctx, release.Key, true, "release: 2026-07-01"); err != nil {
		t.Fatalf("PublishRelease() retry error = %v", err)
	}
	published, err := db.GetRelease(ctx, release.Key)
	if err != nil {
		t.Fatalf("GetRelease() error = %v", err)
	}
	if published.Status != store.ReleaseStatusPublished {
		t.Fatalf("release status after retry = %s, want published", published.Status)
	}
	completed, err := db.GetRequirement(ctx, req.Key)
	if err != nil {
		t.Fatalf("GetRequirement() completed error = %v", err)
	}
	if completed.Status != store.RequirementStatusCompleted || !completed.ArchivedAt.Valid {
		t.Fatalf("requirement after retry publish = %+v", completed)
	}
}

func TestPublishReleaseInProgressTargetChangeRequiresManualHandling(t *testing.T) {
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
	writeAndPushRequirementRepoFeature(t, ctx, db, svc, req, "backend", "backend.txt", "backend\n")
	writeAndPushRequirementRepoFeature(t, ctx, db, svc, req, "frontend", "frontend.txt", "frontend\n")
	if err := db.MarkRequirementReady(ctx, req.ID); err != nil {
		t.Fatalf("MarkRequirementReady() error = %v", err)
	}
	ready, err := db.GetRequirement(ctx, req.Key)
	if err != nil {
		t.Fatalf("GetRequirement() error = %v", err)
	}
	release, err := svc.CreateRelease(ctx, CreateReleaseParams{
		Title:           "2026-07-01 Release",
		Key:             "2026-07-01",
		RequirementKeys: []string{ready.Key},
	})
	if err != nil {
		t.Fatalf("CreateRelease() error = %v", err)
	}
	if err := svc.IntegrateRelease(ctx, release.Key, false); err != nil {
		t.Fatalf("IntegrateRelease() error = %v", err)
	}

	hookPath := filepath.Join(remoteB, "hooks", "pre-receive")
	if err := os.WriteFile(hookPath, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("write rejecting hook: %v", err)
	}
	if err := svc.PublishRelease(ctx, release.Key, true, "release: 2026-07-01"); err == nil {
		t.Fatal("PublishRelease() with broken second repo succeeded, want error")
	}
	if err := os.Remove(hookPath); err != nil {
		t.Fatalf("remove rejecting hook: %v", err)
	}
	seedRemoteBaseCommit(t, remoteB, "main", "external.txt", "external\n")

	err = svc.PublishRelease(ctx, release.Key, true, "release: 2026-07-01")
	if err == nil {
		t.Fatal("PublishRelease() retry with target branch change succeeded, want manual handling error")
	}
	if !strings.Contains(err.Error(), "publish-in-progress") || !strings.Contains(err.Error(), "manual handling") {
		t.Fatalf("PublishRelease() error = %v, want publish-in-progress manual handling", err)
	}
	if strings.Contains(err.Error(), "reintegrate") {
		t.Fatalf("PublishRelease() error = %v, should not suggest reintegrate during publish-in-progress", err)
	}
	unchanged, err := db.GetRelease(ctx, release.Key)
	if err != nil {
		t.Fatalf("GetRelease() error = %v", err)
	}
	if unchanged.Status == store.ReleaseStatusStale {
		t.Fatalf("release status = %s, want non-stale publish-in-progress state", unchanged.Status)
	}
}

func TestPublishReleaseRetryChecksDirtyAlreadyPublishedRepo(t *testing.T) {
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
	writeAndPushRequirementRepoFeature(t, ctx, db, svc, req, "backend", "backend.txt", "backend\n")
	writeAndPushRequirementRepoFeature(t, ctx, db, svc, req, "frontend", "frontend.txt", "frontend\n")
	if err := db.MarkRequirementReady(ctx, req.ID); err != nil {
		t.Fatalf("MarkRequirementReady() error = %v", err)
	}
	ready, err := db.GetRequirement(ctx, req.Key)
	if err != nil {
		t.Fatalf("GetRequirement() error = %v", err)
	}
	release, err := svc.CreateRelease(ctx, CreateReleaseParams{
		Title:           "2026-07-01 Release",
		Key:             "2026-07-01",
		RequirementKeys: []string{ready.Key},
	})
	if err != nil {
		t.Fatalf("CreateRelease() error = %v", err)
	}
	if err := svc.IntegrateRelease(ctx, release.Key, false); err != nil {
		t.Fatalf("IntegrateRelease() error = %v", err)
	}

	hookPath := filepath.Join(remoteB, "hooks", "pre-receive")
	if err := os.WriteFile(hookPath, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("write rejecting hook: %v", err)
	}
	if err := svc.PublishRelease(ctx, release.Key, true, "release: 2026-07-01"); err == nil {
		t.Fatal("PublishRelease() with broken second repo succeeded, want error")
	}
	releaseRepos, err := db.ListReleaseRepos(ctx, release.ID)
	if err != nil {
		t.Fatalf("ListReleaseRepos() error = %v", err)
	}
	var publishedRepo, failedRepo store.ReleaseRepo
	for _, releaseRepo := range releaseRepos {
		switch releaseRepo.Status {
		case store.ReleaseRepoStatusPublished:
			publishedRepo = releaseRepo
		case store.ReleaseRepoStatusFailed:
			failedRepo = releaseRepo
		}
	}
	if publishedRepo.ID == 0 || failedRepo.ID == 0 {
		t.Fatalf("release repos after partial publish = %+v, want one published and one failed", releaseRepos)
	}
	if err := os.WriteFile(filepath.Join(publishedRepo.WorktreePath, "dirty-after-publish.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatalf("write dirty already-published release worktree: %v", err)
	}
	if err := os.Remove(hookPath); err != nil {
		t.Fatalf("remove rejecting hook: %v", err)
	}

	err = svc.PublishRelease(ctx, release.Key, true, "release: 2026-07-01")
	if err == nil || !strings.Contains(err.Error(), "release worktree has uncommitted changes") {
		t.Fatalf("PublishRelease() retry error = %v, want dirty already-published release worktree error", err)
	}
	releaseRepos, err = db.ListReleaseRepos(ctx, release.ID)
	if err != nil {
		t.Fatalf("ListReleaseRepos() after dirty retry error = %v", err)
	}
	for _, releaseRepo := range releaseRepos {
		if releaseRepo.ID == failedRepo.ID && releaseRepo.Status == store.ReleaseRepoStatusPublished {
			t.Fatalf("dirty already-published repo should prevent publishing remaining repo, got %+v", releaseRepo)
		}
	}
}

func TestPublishReleasePreflightsAllDirtyWorktreesBeforeAnyPush(t *testing.T) {
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
	writeAndPushRequirementRepoFeature(t, ctx, db, svc, req, "backend", "backend.txt", "backend\n")
	writeAndPushRequirementRepoFeature(t, ctx, db, svc, req, "frontend", "frontend.txt", "frontend\n")
	if err := db.MarkRequirementReady(ctx, req.ID); err != nil {
		t.Fatalf("MarkRequirementReady() error = %v", err)
	}
	ready, err := db.GetRequirement(ctx, req.Key)
	if err != nil {
		t.Fatalf("GetRequirement() error = %v", err)
	}
	release, err := svc.CreateRelease(ctx, CreateReleaseParams{
		Title:           "2026-07-01 Release",
		Key:             "2026-07-01",
		RequirementKeys: []string{ready.Key},
	})
	if err != nil {
		t.Fatalf("CreateRelease() error = %v", err)
	}
	if err := svc.IntegrateRelease(ctx, release.Key, false); err != nil {
		t.Fatalf("IntegrateRelease() error = %v", err)
	}
	frontendReleaseWorktree := filepath.Join(cfg.ReleaseDir, release.Slug, "frontend")
	if err := os.WriteFile(filepath.Join(frontendReleaseWorktree, "dirty-before-publish.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatalf("write dirty frontend release worktree: %v", err)
	}

	err = svc.PublishRelease(ctx, release.Key, true, "release: 2026-07-01")
	if err == nil || !strings.Contains(err.Error(), "release worktree has uncommitted changes") {
		t.Fatalf("PublishRelease() error = %v, want dirty release worktree error", err)
	}
	backendMain := filepath.Join(t.TempDir(), "backend-main")
	run(t, "", "git", "clone", remoteA, backendMain)
	runGit(t, backendMain, "checkout", "main")
	if _, err := os.Stat(filepath.Join(backendMain, "backend.txt")); err == nil {
		t.Fatal("backend main contains released feature even though another repo was dirty before publish")
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat backend feature on main: %v", err)
	}
	releaseRepos, err := db.ListReleaseRepos(ctx, release.ID)
	if err != nil {
		t.Fatalf("ListReleaseRepos() error = %v", err)
	}
	for _, releaseRepo := range releaseRepos {
		if releaseRepo.Status == store.ReleaseRepoStatusPublished {
			t.Fatalf("repo %s was published despite dirty preflight failure", releaseRepo.Repo.Name)
		}
	}
}

func TestPublishReleasePreflightsAllTargetBranchesBeforeAnyPush(t *testing.T) {
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
	writeAndPushRequirementRepoFeature(t, ctx, db, svc, req, "backend", "backend.txt", "backend\n")
	writeAndPushRequirementRepoFeature(t, ctx, db, svc, req, "frontend", "frontend.txt", "frontend\n")
	if err := db.MarkRequirementReady(ctx, req.ID); err != nil {
		t.Fatalf("MarkRequirementReady() error = %v", err)
	}
	ready, err := db.GetRequirement(ctx, req.Key)
	if err != nil {
		t.Fatalf("GetRequirement() error = %v", err)
	}
	release, err := svc.CreateRelease(ctx, CreateReleaseParams{
		Title:           "2026-07-01 Release",
		Key:             "2026-07-01",
		RequirementKeys: []string{ready.Key},
	})
	if err != nil {
		t.Fatalf("CreateRelease() error = %v", err)
	}
	if err := svc.IntegrateRelease(ctx, release.Key, false); err != nil {
		t.Fatalf("IntegrateRelease() error = %v", err)
	}
	seedRemoteBaseCommit(t, remoteB, "main", "external.txt", "external\n")

	err = svc.PublishRelease(ctx, release.Key, true, "release: 2026-07-01")
	if err == nil || !strings.Contains(err.Error(), "reintegrate release") {
		t.Fatalf("PublishRelease() error = %v, want reintegrate error", err)
	}
	stale, err := db.GetRelease(ctx, release.Key)
	if err != nil {
		t.Fatalf("GetRelease() error = %v", err)
	}
	if stale.Status != store.ReleaseStatusStale {
		t.Fatalf("release status = %s, want stale", stale.Status)
	}
	backendMain := filepath.Join(t.TempDir(), "backend-main")
	run(t, "", "git", "clone", remoteA, backendMain)
	runGit(t, backendMain, "checkout", "main")
	if _, err := os.Stat(filepath.Join(backendMain, "backend.txt")); err == nil {
		t.Fatal("backend main contains released feature even though another repo target branch was stale before publish")
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat backend feature on main: %v", err)
	}
	releaseRepos, err := db.ListReleaseRepos(ctx, release.ID)
	if err != nil {
		t.Fatalf("ListReleaseRepos() error = %v", err)
	}
	for _, releaseRepo := range releaseRepos {
		if releaseRepo.Status == store.ReleaseRepoStatusPublished {
			t.Fatalf("repo %s was published despite target branch preflight failure", releaseRepo.Repo.Name)
		}
	}
}

func TestPublishReleaseFinalizationFailureDoesNotPartiallyCompleteRequirements(t *testing.T) {
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
	reqA := createReadyFeatureRequirement(t, ctx, db, svc, "pay-flow", "pay.txt", "pay\n")
	reqB := createReadyFeatureRequirement(t, ctx, db, svc, "user-center", "user.txt", "user\n")
	release, err := svc.CreateRelease(ctx, CreateReleaseParams{
		Title:           "2026-07-01 Release",
		Key:             "2026-07-01",
		RequirementKeys: []string{reqA.Key, reqB.Key},
	})
	if err != nil {
		t.Fatalf("CreateRelease() error = %v", err)
	}
	if err := svc.IntegrateRelease(ctx, release.Key, false); err != nil {
		t.Fatalf("IntegrateRelease() error = %v", err)
	}
	raw, err := sql.Open("sqlite", cfg.DBPath)
	if err != nil {
		t.Fatalf("open raw sqlite: %v", err)
	}
	defer raw.Close()
	if _, err := raw.Exec(`CREATE TRIGGER fail_user_center_completion
		BEFORE UPDATE OF status ON requirements
		WHEN NEW.status = 'completed' AND OLD.req_key = 'user-center'
		BEGIN
			SELECT RAISE(ABORT, 'forced user-center completion failure');
		END`); err != nil {
		t.Fatalf("create completion failure trigger: %v", err)
	}

	if err := svc.PublishRelease(ctx, release.Key, true, "release: 2026-07-01"); err == nil {
		t.Fatal("PublishRelease() with failing finalization succeeded, want error")
	}
	for _, reqKey := range []string{reqA.Key, reqB.Key} {
		req, err := db.GetRequirement(ctx, reqKey)
		if err != nil {
			t.Fatalf("GetRequirement(%s) error = %v", reqKey, err)
		}
		if req.Status != store.RequirementStatusActive || !req.ReadyAt.Valid || req.CompletedAt.Valid || req.ArchivedAt.Valid {
			t.Fatalf("requirement %s after failed finalization = %+v, want still ready active", reqKey, req)
		}
	}
	notPublished, err := db.GetRelease(ctx, release.Key)
	if err != nil {
		t.Fatalf("GetRelease() error = %v", err)
	}
	if notPublished.Status == store.ReleaseStatusPublished {
		t.Fatalf("release status after failed finalization = %s, want not published", notPublished.Status)
	}
}

func TestIntegrateReleaseForceDiscardsDirtyReleaseWorktree(t *testing.T) {
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
	req := createReadyFeatureRequirement(t, ctx, db, svc, "pay-flow", "pay.txt", "pay\n")
	release, err := svc.CreateRelease(ctx, CreateReleaseParams{
		Title:           "2026-07-01 Release",
		Key:             "2026-07-01",
		RequirementKeys: []string{req.Key},
	})
	if err != nil {
		t.Fatalf("CreateRelease() error = %v", err)
	}
	if err := svc.IntegrateRelease(ctx, release.Key, false); err != nil {
		t.Fatalf("IntegrateRelease() error = %v", err)
	}
	releaseWorktree := filepath.Join(cfg.ReleaseDir, release.Slug, "backend")
	if err := os.WriteFile(filepath.Join(releaseWorktree, "dirty.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatalf("write dirty release file: %v", err)
	}
	if err := svc.IntegrateRelease(ctx, release.Key, false); err == nil {
		t.Fatal("IntegrateRelease() without force succeeded on dirty worktree, want error")
	}
	if err := svc.IntegrateRelease(ctx, release.Key, true); err != nil {
		t.Fatalf("IntegrateRelease(force) error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(releaseWorktree, "dirty.txt")); !os.IsNotExist(err) {
		t.Fatalf("dirty file should be discarded by force, stat err = %v", err)
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

func seedRemoteBaseCommit(t *testing.T, remote, branch, filename, content string) string {
	t.Helper()
	root := t.TempDir()
	seed := filepath.Join(root, "base-seed")
	run(t, "", "git", "clone", remote, seed)
	runGit(t, seed, "checkout", "-B", branch, "origin/"+branch)
	if err := os.WriteFile(filepath.Join(seed, filename), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", filename, err)
	}
	runGit(t, seed, "add", filename)
	run(t, seed, "git", "-c", "user.name=Workspace Test", "-c", "user.email=workspace@example.com", "commit", "-m", "base update")
	runGit(t, seed, "push", "origin", branch)
	return strings.TrimSpace(runGitOutput(t, seed, "rev-parse", "HEAD"))
}

func pushFeatureUpdate(t *testing.T, remote, branch, filename, content string) string {
	t.Helper()
	root := t.TempDir()
	seed := filepath.Join(root, "feature-update")
	run(t, "", "git", "clone", remote, seed)
	runGit(t, seed, "checkout", branch)
	if err := os.WriteFile(filepath.Join(seed, filename), []byte(content), 0o644); err != nil {
		t.Fatalf("write feature update %s: %v", filename, err)
	}
	runGit(t, seed, "add", filename)
	run(t, seed, "git", "-c", "user.name=Workspace Test", "-c", "user.email=workspace@example.com", "commit", "-m", "feature update")
	runGit(t, seed, "push", "origin", branch)
	return strings.TrimSpace(runGitOutput(t, seed, "rev-parse", "HEAD"))
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

func createReadyFeatureRequirement(t *testing.T, ctx context.Context, db *store.DB, svc *Service, key, filename, content string) store.Requirement {
	t.Helper()
	return createReadyFeatureRequirementForRepo(t, ctx, db, svc, "backend", key, filename, content)
}

func createReadyFeatureRequirementForRepo(t *testing.T, ctx context.Context, db *store.DB, svc *Service, repoName, key, filename, content string) store.Requirement {
	t.Helper()
	req, err := svc.CreateRequirement(ctx, CreateRequirementParams{
		Title:     key,
		Key:       key,
		RepoNames: []string{repoName},
	})
	if err != nil {
		t.Fatalf("CreateRequirement(%s) error = %v", key, err)
	}
	worktree := filepath.Join(req.WorkspacePath, repoName)
	runGit(t, worktree, "config", "user.name", "Workspace Test")
	runGit(t, worktree, "config", "user.email", "workspace@example.com")
	if err := os.WriteFile(filepath.Join(worktree, filename), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", filename, err)
	}
	runGit(t, worktree, "add", filename)
	runGit(t, worktree, "commit", "-m", "feat: "+key)
	runGit(t, worktree, "push", "origin", "HEAD:refs/heads/"+req.FeatureBranch)
	rels, err := db.ListRequirementRepos(ctx, req.ID)
	if err != nil {
		t.Fatalf("ListRequirementRepos(%s) error = %v", key, err)
	}
	if err := svc.git.RemoveWorktree(rels[0].Repo.BarePath, rels[0].WorktreePath); err != nil {
		t.Fatalf("RemoveWorktree(%s) error = %v", key, err)
	}
	if err := db.UpdateRequirementRepoStatus(ctx, rels[0].ID, store.RequirementRepoStatusCompleted); err != nil {
		t.Fatalf("UpdateRequirementRepoStatus(%s) error = %v", key, err)
	}
	if err := db.MarkRequirementReady(ctx, req.ID); err != nil {
		t.Fatalf("MarkRequirementReady(%s) error = %v", key, err)
	}
	ready, err := db.GetRequirement(ctx, key)
	if err != nil {
		t.Fatalf("GetRequirement(%s) error = %v", key, err)
	}
	return ready
}

func writeAndPushRequirementRepoFeature(t *testing.T, ctx context.Context, db *store.DB, svc *Service, req store.Requirement, repoName, filename, content string) {
	t.Helper()
	rels, err := db.ListRequirementRepos(ctx, req.ID)
	if err != nil {
		t.Fatalf("ListRequirementRepos(%s) error = %v", req.Key, err)
	}
	for _, rel := range rels {
		if rel.RepoName != repoName {
			continue
		}
		worktree := rel.WorktreePath
		runGit(t, worktree, "config", "user.name", "Workspace Test")
		runGit(t, worktree, "config", "user.email", "workspace@example.com")
		if err := os.WriteFile(filepath.Join(worktree, filename), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", filename, err)
		}
		runGit(t, worktree, "add", filename)
		runGit(t, worktree, "commit", "-m", "feat: "+repoName)
		runGit(t, worktree, "push", "origin", "HEAD:refs/heads/"+req.FeatureBranch)
		if err := svc.git.RemoveWorktree(rel.Repo.BarePath, rel.WorktreePath); err != nil {
			t.Fatalf("RemoveWorktree(%s) error = %v", repoName, err)
		}
		if err := db.UpdateRequirementRepoStatus(ctx, rel.ID, store.RequirementRepoStatusCompleted); err != nil {
			t.Fatalf("UpdateRequirementRepoStatus(%s) error = %v", repoName, err)
		}
		return
	}
	t.Fatalf("repo %s not found for requirement %s", repoName, req.Key)
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

func countReleaseOperationLogs(t *testing.T, dbPath string, releaseID int64, operation string) int {
	t.Helper()
	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open raw sqlite: %v", err)
	}
	defer raw.Close()
	var count int
	if err := raw.QueryRow(`SELECT COUNT(*) FROM release_operation_logs WHERE release_id = ? AND operation = ? AND status = ?`, releaseID, operation, store.OperationStatusFailed).Scan(&count); err != nil {
		t.Fatalf("count release operation logs: %v", err)
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
