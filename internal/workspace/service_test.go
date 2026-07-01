package workspace

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"workspace-cli/internal/config"
	gitx "workspace-cli/internal/git"
	"workspace-cli/internal/store"
)

func TestCompletedRequirementRejectsUpdateAndAddRepo(t *testing.T) {
	ctx := context.Background()
	cfg, db := testServiceStore(t)
	svc := NewService(cfg, db, gitx.NewManager(gitx.ExecRunner{}))

	req, err := db.CreateRequirement(ctx, store.CreateRequirementParams{
		Key:           "done",
		Title:         "Done",
		Slug:          "done",
		WorkspacePath: "/tmp/done",
		FeatureBranch: "feature/done",
	})
	if err != nil {
		t.Fatalf("CreateRequirement() error = %v", err)
	}
	if err := db.MarkRequirementCompleted(ctx, req.ID); err != nil {
		t.Fatalf("MarkRequirementCompleted() error = %v", err)
	}

	if err := svc.UpdateRequirement(ctx, "done", "New Title"); err == nil {
		t.Fatal("UpdateRequirement() on completed requirement succeeded, want error")
	}
	if _, err := svc.AddRepoToRequirement(ctx, "done", "backend"); err == nil {
		t.Fatal("AddRepoToRequirement() on completed requirement succeeded, want error")
	}
}

func TestArchiveCompletedRequirementIsIdempotent(t *testing.T) {
	ctx := context.Background()
	cfg, db := testServiceStore(t)
	svc := NewService(cfg, db, gitx.NewManager(gitx.ExecRunner{}))

	req, err := db.CreateRequirement(ctx, store.CreateRequirementParams{
		Key:           "done",
		Title:         "Done",
		Slug:          "done",
		WorkspacePath: "/tmp/done",
		FeatureBranch: "feature/done",
	})
	if err != nil {
		t.Fatalf("CreateRequirement() error = %v", err)
	}
	if err := db.MarkRequirementCompleted(ctx, req.ID); err != nil {
		t.Fatalf("MarkRequirementCompleted() error = %v", err)
	}

	if err := svc.ArchiveRequirement(ctx, "done"); err != nil {
		t.Fatalf("ArchiveRequirement() first error = %v", err)
	}
	if err := svc.ArchiveRequirement(ctx, "done"); err != nil {
		t.Fatalf("ArchiveRequirement() second error = %v", err)
	}
}

func TestCompletedButUnarchivedRequirementRejectsUpdateAndAddRepo(t *testing.T) {
	ctx := context.Background()
	cfg, db := testServiceStore(t)
	svc := NewService(cfg, db, gitx.NewManager(gitx.ExecRunner{}))

	req, err := db.CreateRequirement(ctx, store.CreateRequirementParams{
		Key:           "done",
		Title:         "Done",
		Slug:          "done",
		WorkspacePath: "/tmp/done",
		FeatureBranch: "feature/done",
	})
	if err != nil {
		t.Fatalf("CreateRequirement() error = %v", err)
	}
	markRequirementCompletedWithoutArchive(t, cfg, req.ID)

	if err := svc.UpdateRequirement(ctx, "done", "New Title"); err == nil {
		t.Fatal("UpdateRequirement() on completed-but-unarchived requirement succeeded, want error")
	}
	if _, err := svc.AddRepoToRequirement(ctx, "done", "backend"); err == nil {
		t.Fatal("AddRepoToRequirement() on completed-but-unarchived requirement succeeded, want error")
	}
	if err := svc.ArchiveRequirement(ctx, "done"); err != nil {
		t.Fatalf("ArchiveRequirement() on completed-but-unarchived requirement error = %v", err)
	}
	archived, err := db.GetRequirement(ctx, "done")
	if err != nil {
		t.Fatalf("GetRequirement() error = %v", err)
	}
	if !archived.ArchivedAt.Valid {
		t.Fatal("ArchiveRequirement() did not write archived_at for completed-but-unarchived requirement")
	}
}

func TestRequirementCompletionDistinguishesReleasedAndLegacyCompleted(t *testing.T) {
	ctx := context.Background()
	cfg, db := testServiceStore(t)
	svc := NewService(cfg, db, gitx.NewManager(gitx.ExecRunner{}))

	legacy, err := db.CreateRequirement(ctx, store.CreateRequirementParams{
		Key:           "legacy",
		Title:         "Legacy",
		Slug:          "legacy",
		WorkspacePath: "/tmp/legacy",
		FeatureBranch: "feature/legacy",
	})
	if err != nil {
		t.Fatalf("CreateRequirement(legacy) error = %v", err)
	}
	if err := db.MarkRequirementCompleted(ctx, legacy.ID); err != nil {
		t.Fatalf("MarkRequirementCompleted(legacy) error = %v", err)
	}
	legacy, err = db.GetRequirement(ctx, legacy.Key)
	if err != nil {
		t.Fatalf("GetRequirement(legacy) error = %v", err)
	}
	completion, err := svc.RequirementCompletion(ctx, legacy)
	if err != nil {
		t.Fatalf("RequirementCompletion(legacy) error = %v", err)
	}
	if completion != "legacy-completed" {
		t.Fatalf("legacy completion = %q, want legacy-completed", completion)
	}

	released, err := db.CreateRequirement(ctx, store.CreateRequirementParams{
		Key:           "released",
		Title:         "Released",
		Slug:          "released",
		WorkspacePath: "/tmp/released",
		FeatureBranch: "feature/released",
	})
	if err != nil {
		t.Fatalf("CreateRequirement(released) error = %v", err)
	}
	if err := db.MarkRequirementCompleted(ctx, released.ID); err != nil {
		t.Fatalf("MarkRequirementCompleted(released) error = %v", err)
	}
	released, err = db.GetRequirement(ctx, released.Key)
	if err != nil {
		t.Fatalf("GetRequirement(released) error = %v", err)
	}
	release, err := db.CreateRelease(ctx, store.CreateReleaseParams{
		Key:           "2026-07-01",
		Title:         "2026-07-01 Release",
		Slug:          "2026-07-01",
		WorkspacePath: "/tmp/releases/2026-07-01",
		BranchName:    "release/2026-07-01",
		TargetBranch:  "base_branch",
	})
	if err != nil {
		t.Fatalf("CreateRelease() error = %v", err)
	}
	if _, err := db.AddReleaseRequirement(ctx, release.ID, released.ID, 1); err != nil {
		t.Fatalf("AddReleaseRequirement(released) error = %v", err)
	}
	if err := db.UpdateReleaseStatus(ctx, release.ID, store.ReleaseStatusPublished); err != nil {
		t.Fatalf("UpdateReleaseStatus(published) error = %v", err)
	}
	completion, err = svc.RequirementCompletion(ctx, released)
	if err != nil {
		t.Fatalf("RequirementCompletion(released) error = %v", err)
	}
	if completion != "released" {
		t.Fatalf("released completion = %q, want released", completion)
	}

	removed, err := db.CreateRequirement(ctx, store.CreateRequirementParams{
		Key:           "removed",
		Title:         "Removed",
		Slug:          "removed",
		WorkspacePath: "/tmp/removed",
		FeatureBranch: "feature/removed",
	})
	if err != nil {
		t.Fatalf("CreateRequirement(removed) error = %v", err)
	}
	if err := db.MarkRequirementCompleted(ctx, removed.ID); err != nil {
		t.Fatalf("MarkRequirementCompleted(removed) error = %v", err)
	}
	removed, err = db.GetRequirement(ctx, removed.Key)
	if err != nil {
		t.Fatalf("GetRequirement(removed) error = %v", err)
	}
	removedRelease, err := db.CreateRelease(ctx, store.CreateReleaseParams{
		Key:           "2026-07-02",
		Title:         "2026-07-02 Release",
		Slug:          "2026-07-02",
		WorkspacePath: "/tmp/releases/2026-07-02",
		BranchName:    "release/2026-07-02",
		TargetBranch:  "base_branch",
	})
	if err != nil {
		t.Fatalf("CreateRelease(removed) error = %v", err)
	}
	if _, err := db.AddReleaseRequirement(ctx, removedRelease.ID, removed.ID, 1); err != nil {
		t.Fatalf("AddReleaseRequirement(removed) error = %v", err)
	}
	if err := db.RemoveReleaseRequirement(ctx, removedRelease.ID, removed.ID); err != nil {
		t.Fatalf("RemoveReleaseRequirement() error = %v", err)
	}
	if err := db.UpdateReleaseStatus(ctx, removedRelease.ID, store.ReleaseStatusPublished); err != nil {
		t.Fatalf("UpdateReleaseStatus(removed published) error = %v", err)
	}
	completion, err = svc.RequirementCompletion(ctx, removed)
	if err != nil {
		t.Fatalf("RequirementCompletion(removed) error = %v", err)
	}
	if completion != "legacy-completed" {
		t.Fatalf("removed completion = %q, want legacy-completed", completion)
	}

	active, err := db.CreateRequirement(ctx, store.CreateRequirementParams{
		Key:           "active",
		Title:         "Active",
		Slug:          "active",
		WorkspacePath: "/tmp/active",
		FeatureBranch: "feature/active",
	})
	if err != nil {
		t.Fatalf("CreateRequirement(active) error = %v", err)
	}
	completion, err = svc.RequirementCompletion(ctx, active)
	if err != nil {
		t.Fatalf("RequirementCompletion(active) error = %v", err)
	}
	if completion != "" {
		t.Fatalf("active completion = %q, want empty", completion)
	}
}

func TestCleanupPendingRequirementRejectsMutationAndRepoChanges(t *testing.T) {
	ctx := context.Background()
	cfg, db := testServiceStore(t)
	svc := NewService(cfg, db, gitx.NewManager(gitx.ExecRunner{}))

	repo, err := db.CreateRepo(ctx, store.CreateRepoParams{
		Name:       "backend",
		URL:        "/tmp/backend.git",
		Remote:     "origin",
		BaseBranch: "main",
		BarePath:   "/tmp/backend.git",
	})
	if err != nil {
		t.Fatalf("CreateRepo() error = %v", err)
	}
	req, err := db.CreateRequirement(ctx, store.CreateRequirementParams{
		Key:           "pay-flow",
		Title:         "Payment Flow",
		Slug:          "pay-flow",
		WorkspacePath: "/tmp/pay-flow",
		FeatureBranch: "feature/pay-flow",
	})
	if err != nil {
		t.Fatalf("CreateRequirement() error = %v", err)
	}
	rel, err := db.AddRepoToRequirement(ctx, req.ID, repo.ID, "/tmp/pay-flow/backend")
	if err != nil {
		t.Fatalf("AddRepoToRequirement(db) error = %v", err)
	}
	if err := db.UpdateRequirementRepoStatus(ctx, rel.ID, store.RequirementRepoStatusPushed); err != nil {
		t.Fatalf("UpdateRequirementRepoStatus() error = %v", err)
	}

	if err := svc.UpdateRequirement(ctx, req.Key, "New Title"); err == nil {
		t.Fatal("UpdateRequirement() on cleanup-pending requirement succeeded, want error")
	}
	if _, err := svc.AddRepoToRequirement(ctx, req.Key, "other"); err == nil {
		t.Fatal("AddRepoToRequirement() on cleanup-pending requirement succeeded, want error")
	}
	if err := svc.ArchiveRequirement(ctx, req.Key); err == nil {
		t.Fatal("ArchiveRequirement() on cleanup-pending requirement succeeded, want error")
	}
	if _, err := svc.UpdateRepo(ctx, UpdateRepoParams{Name: repo.Name, BaseBranch: "develop"}); err == nil {
		t.Fatal("UpdateRepo() for cleanup-pending repo succeeded, want error")
	}
	if err := svc.RemoveRepo(ctx, repo.Name); err == nil {
		t.Fatal("RemoveRepo() for cleanup-pending repo succeeded, want error")
	}
}

func TestRepoUpdateRemoveRejectsUnpublishedReleaseReference(t *testing.T) {
	ctx := context.Background()
	cfg, db := testServiceStore(t)
	svc := NewService(cfg, db, gitx.NewManager(gitx.ExecRunner{}))

	repo, err := db.CreateRepo(ctx, store.CreateRepoParams{
		Name:       "backend",
		URL:        "/tmp/backend.git",
		Remote:     "origin",
		BaseBranch: "main",
		BarePath:   "/tmp/backend.git",
	})
	if err != nil {
		t.Fatalf("CreateRepo() error = %v", err)
	}
	req, err := db.CreateRequirement(ctx, store.CreateRequirementParams{
		Key:           "done",
		Title:         "Done",
		Slug:          "done",
		WorkspacePath: "/tmp/done",
		FeatureBranch: "feature/done",
	})
	if err != nil {
		t.Fatalf("CreateRequirement() error = %v", err)
	}
	if _, err := db.AddRepoToRequirement(ctx, req.ID, repo.ID, "/tmp/done/backend"); err != nil {
		t.Fatalf("AddRepoToRequirement() error = %v", err)
	}
	if err := db.MarkRequirementCompleted(ctx, req.ID); err != nil {
		t.Fatalf("MarkRequirementCompleted() error = %v", err)
	}
	release, err := db.CreateRelease(ctx, store.CreateReleaseParams{
		Key:           "2026-07-01",
		Title:         "2026-07-01 Release",
		Slug:          "2026-07-01",
		WorkspacePath: "/tmp/releases/2026-07-01",
		BranchName:    "release/2026-07-01",
		TargetBranch:  "base_branch",
	})
	if err != nil {
		t.Fatalf("CreateRelease() error = %v", err)
	}
	if _, err := db.AddReleaseRequirement(ctx, release.ID, req.ID, 1); err != nil {
		t.Fatalf("AddReleaseRequirement() error = %v", err)
	}

	if _, err := svc.UpdateRepo(ctx, UpdateRepoParams{Name: repo.Name, BaseBranch: "develop"}); err == nil {
		t.Fatal("UpdateRepo() for repo referenced by unpublished release succeeded, want error")
	}
	if err := svc.RemoveRepo(ctx, repo.Name); err == nil {
		t.Fatal("RemoveRepo() for repo referenced by unpublished release succeeded, want error")
	}
}

func TestReadyRequirementRejectsUpdateAndAddRepo(t *testing.T) {
	ctx := context.Background()
	cfg, db := testServiceStore(t)
	svc := NewService(cfg, db, gitx.NewManager(gitx.ExecRunner{}))

	req, err := db.CreateRequirement(ctx, store.CreateRequirementParams{
		Key:           "pay-flow",
		Title:         "Payment Flow",
		Slug:          "pay-flow",
		WorkspacePath: "/tmp/pay-flow",
		FeatureBranch: "feature/pay-flow",
	})
	if err != nil {
		t.Fatalf("CreateRequirement() error = %v", err)
	}
	if err := db.MarkRequirementReady(ctx, req.ID); err != nil {
		t.Fatalf("MarkRequirementReady() error = %v", err)
	}

	if err := svc.UpdateRequirement(ctx, req.Key, "New Title"); err == nil {
		t.Fatal("UpdateRequirement() on ready requirement succeeded, want error")
	}
	if _, err := svc.AddRepoToRequirement(ctx, req.Key, "backend"); err == nil {
		t.Fatal("AddRepoToRequirement() on ready requirement succeeded, want error")
	}
}

func TestCreateReleaseRequiresReadyRequirementAndCreatesActiveMembership(t *testing.T) {
	ctx := context.Background()
	cfg, db := testServiceStore(t)
	svc := NewService(cfg, db, gitx.NewManager(gitx.ExecRunner{}))

	req, err := db.CreateRequirement(ctx, store.CreateRequirementParams{
		Key:           "pay-flow",
		Title:         "Payment Flow",
		Slug:          "pay-flow",
		WorkspacePath: "/tmp/pay-flow",
		FeatureBranch: "feature/pay-flow",
	})
	if err != nil {
		t.Fatalf("CreateRequirement() error = %v", err)
	}
	if _, err := svc.CreateRelease(ctx, CreateReleaseParams{
		Title:           "2026-07-01 Release",
		Key:             "2026-07-01",
		RequirementKeys: []string{req.Key},
	}); err == nil {
		t.Fatal("CreateRelease() with non-ready requirement succeeded, want error")
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
	if release.Status != store.ReleaseStatusDraft || release.BranchName != "release/2026-07-01" || release.TargetBranch != "per-repo" {
		t.Fatalf("release = %+v", release)
	}
	memberships, err := db.ListReleaseRequirements(ctx, release.ID, false)
	if err != nil {
		t.Fatalf("ListReleaseRequirements() error = %v", err)
	}
	if len(memberships) != 1 || memberships[0].RequirementID != req.ID || memberships[0].Position != 1 || memberships[0].RemovedAt.Valid {
		t.Fatalf("memberships = %+v", memberships)
	}
}

func TestCreateRequirementRequiresAtLeastOneRepo(t *testing.T) {
	ctx := context.Background()
	cfg, db := testServiceStore(t)
	svc := NewService(cfg, db, gitx.NewManager(gitx.ExecRunner{}))

	if _, err := svc.CreateRequirement(ctx, CreateRequirementParams{
		Title: "Payment Flow",
		Key:   "pay-flow",
	}); err == nil {
		t.Fatal("CreateRequirement() without repos succeeded, want error")
	}
	reqs, err := db.ListRequirements(ctx, true)
	if err != nil {
		t.Fatalf("ListRequirements() error = %v", err)
	}
	if len(reqs) != 0 {
		t.Fatalf("CreateRequirement() without repos left requirements: %+v", reqs)
	}
}

func TestCreateReleaseDuplicateRequirementDoesNotLeavePartialRelease(t *testing.T) {
	ctx := context.Background()
	cfg, db := testServiceStore(t)
	svc := NewService(cfg, db, gitx.NewManager(gitx.ExecRunner{}))
	req := createReadyRequirementForRelease(t, ctx, db, "pay-flow")

	if _, err := svc.CreateRelease(ctx, CreateReleaseParams{
		Title:           "2026-07-01 Release",
		Key:             "2026-07-01",
		RequirementKeys: []string{req.Key, req.Key},
	}); err == nil {
		t.Fatal("CreateRelease() with duplicate requirement succeeded, want error")
	}
	releases, err := db.ListReleases(ctx, true)
	if err != nil {
		t.Fatalf("ListReleases() error = %v", err)
	}
	if len(releases) != 0 {
		t.Fatalf("duplicate create left partial releases: %+v", releases)
	}
}

func TestCreateReleaseInsertFailureRemovesWorkspaceDirectory(t *testing.T) {
	ctx := context.Background()
	cfg, db := testServiceStore(t)
	svc := NewService(cfg, db, gitx.NewManager(gitx.ExecRunner{}))
	req := createReadyRequirementForRelease(t, ctx, db, "pay-flow")

	raw, err := sql.Open("sqlite", cfg.DBPath)
	if err != nil {
		t.Fatalf("open raw sqlite: %v", err)
	}
	defer raw.Close()
	if _, err := raw.Exec(`CREATE TRIGGER fail_release_insert
		BEFORE INSERT ON releases
		BEGIN
			SELECT RAISE(ABORT, 'forced release insert failure');
		END`); err != nil {
		t.Fatalf("create release insert failure trigger: %v", err)
	}

	if _, err := svc.CreateRelease(ctx, CreateReleaseParams{
		Title:           "2026-07-01 Release",
		Key:             "2026-07-01",
		RequirementKeys: []string{req.Key},
	}); err == nil {
		t.Fatal("CreateRelease() with failing release insert succeeded, want error")
	}
	releases, err := db.ListReleases(ctx, true)
	if err != nil {
		t.Fatalf("ListReleases() error = %v", err)
	}
	if len(releases) != 0 {
		t.Fatalf("failed CreateRelease() left partial releases: %+v", releases)
	}
	releasePath := filepath.Join(cfg.ReleaseDir, "2026-07-01")
	if _, err := os.Stat(releasePath); !os.IsNotExist(err) {
		t.Fatalf("failed CreateRelease() should remove release workspace %s, stat err = %v", releasePath, err)
	}
}

func TestCreateReleaseMembershipFailureDoesNotLeavePartialRelease(t *testing.T) {
	ctx := context.Background()
	cfg, db := testServiceStore(t)
	svc := NewService(cfg, db, gitx.NewManager(gitx.ExecRunner{}))
	reqA := createReadyRequirementForRelease(t, ctx, db, "pay-flow")
	reqB := createReadyRequirementForRelease(t, ctx, db, "user-center")

	raw, err := sql.Open("sqlite", cfg.DBPath)
	if err != nil {
		t.Fatalf("open raw sqlite: %v", err)
	}
	defer raw.Close()
	if _, err := raw.Exec(`CREATE TRIGGER fail_second_release_requirement_insert
		BEFORE INSERT ON release_requirements
		WHEN NEW.position = 2
		BEGIN
			SELECT RAISE(ABORT, 'forced second release requirement failure');
		END`); err != nil {
		t.Fatalf("create release requirement failure trigger: %v", err)
	}

	if _, err := svc.CreateRelease(ctx, CreateReleaseParams{
		Title:           "2026-07-01 Release",
		Key:             "2026-07-01",
		RequirementKeys: []string{reqA.Key, reqB.Key},
	}); err == nil {
		t.Fatal("CreateRelease() with failing membership insert succeeded, want error")
	}
	releases, err := db.ListReleases(ctx, true)
	if err != nil {
		t.Fatalf("ListReleases() error = %v", err)
	}
	if len(releases) != 0 {
		t.Fatalf("failed CreateRelease() left partial releases: %+v", releases)
	}
	var memberships int
	if err := raw.QueryRow(`SELECT COUNT(*) FROM release_requirements`).Scan(&memberships); err != nil {
		t.Fatalf("count release_requirements: %v", err)
	}
	if memberships != 0 {
		t.Fatalf("failed CreateRelease() left %d release memberships, want 0", memberships)
	}
}

func TestDraftReleaseAddRemoveAndReAddRequirementKeepsDraftStatus(t *testing.T) {
	ctx := context.Background()
	cfg, db := testServiceStore(t)
	svc := NewService(cfg, db, gitx.NewManager(gitx.ExecRunner{}))

	reqA := createReadyRequirementForRelease(t, ctx, db, "pay-flow")
	reqB := createReadyRequirementForRelease(t, ctx, db, "user-center")
	release, err := svc.CreateRelease(ctx, CreateReleaseParams{
		Title:           "2026-07-01 Release",
		Key:             "2026-07-01",
		RequirementKeys: []string{reqA.Key},
	})
	if err != nil {
		t.Fatalf("CreateRelease() error = %v", err)
	}

	added, err := svc.AddRequirementToRelease(ctx, release.Key, reqB.Key)
	if err != nil {
		t.Fatalf("AddRequirementToRelease() error = %v", err)
	}
	if added.RequirementID != reqB.ID || added.Position != 2 {
		t.Fatalf("added membership = %+v", added)
	}
	updatedRelease, err := db.GetRelease(ctx, release.Key)
	if err != nil {
		t.Fatalf("GetRelease() error = %v", err)
	}
	if updatedRelease.Status != store.ReleaseStatusDraft {
		t.Fatalf("release status after add = %s, want draft", updatedRelease.Status)
	}

	if err := svc.RemoveRequirementFromRelease(ctx, release.Key, reqB.Key); err != nil {
		t.Fatalf("RemoveRequirementFromRelease() error = %v", err)
	}
	updatedRelease, err = db.GetRelease(ctx, release.Key)
	if err != nil {
		t.Fatalf("GetRelease(after remove) error = %v", err)
	}
	if updatedRelease.Status != store.ReleaseStatusDraft {
		t.Fatalf("release status after remove = %s, want draft", updatedRelease.Status)
	}
	active, err := db.ListReleaseRequirements(ctx, release.ID, false)
	if err != nil {
		t.Fatalf("ListReleaseRequirements(active) error = %v", err)
	}
	if len(active) != 1 || active[0].RequirementID != reqA.ID {
		t.Fatalf("active memberships after remove = %+v", active)
	}
	all, err := db.ListReleaseRequirements(ctx, release.ID, true)
	if err != nil {
		t.Fatalf("ListReleaseRequirements(all) error = %v", err)
	}
	if len(all) != 2 || !all[1].RemovedAt.Valid {
		t.Fatalf("all memberships after remove = %+v", all)
	}

	readded, err := svc.AddRequirementToRelease(ctx, release.Key, reqB.Key)
	if err != nil {
		t.Fatalf("AddRequirementToRelease(re-add) error = %v", err)
	}
	if readded.ID == added.ID || readded.Position != 3 {
		t.Fatalf("readded membership = %+v, original = %+v", readded, added)
	}
	updatedRelease, err = db.GetRelease(ctx, release.Key)
	if err != nil {
		t.Fatalf("GetRelease(after re-add) error = %v", err)
	}
	if updatedRelease.Status != store.ReleaseStatusDraft {
		t.Fatalf("release status after re-add = %s, want draft", updatedRelease.Status)
	}
}

func TestIntegratedReleaseAddRemoveRequirementMarksStale(t *testing.T) {
	ctx := context.Background()
	cfg, db := testServiceStore(t)
	svc := NewService(cfg, db, gitx.NewManager(gitx.ExecRunner{}))

	reqA := createReadyRequirementForRelease(t, ctx, db, "pay-flow")
	reqB := createReadyRequirementForRelease(t, ctx, db, "user-center")
	reqC := createReadyRequirementForRelease(t, ctx, db, "coupon")
	release, err := svc.CreateRelease(ctx, CreateReleaseParams{
		Title:           "2026-07-01 Release",
		Key:             "2026-07-01",
		RequirementKeys: []string{reqA.Key},
	})
	if err != nil {
		t.Fatalf("CreateRelease() error = %v", err)
	}
	if err := db.UpdateReleaseStatus(ctx, release.ID, store.ReleaseStatusIntegrated); err != nil {
		t.Fatalf("UpdateReleaseStatus(integrated) error = %v", err)
	}

	if _, err := svc.AddRequirementToRelease(ctx, release.Key, reqB.Key); err != nil {
		t.Fatalf("AddRequirementToRelease() error = %v", err)
	}
	updatedRelease, err := db.GetRelease(ctx, release.Key)
	if err != nil {
		t.Fatalf("GetRelease(after add) error = %v", err)
	}
	if updatedRelease.Status != store.ReleaseStatusStale {
		t.Fatalf("release status after add = %s, want stale", updatedRelease.Status)
	}

	if err := db.UpdateReleaseStatus(ctx, release.ID, store.ReleaseStatusIntegrated); err != nil {
		t.Fatalf("UpdateReleaseStatus(integrated again) error = %v", err)
	}
	if _, err := svc.AddRequirementToRelease(ctx, release.Key, reqC.Key); err != nil {
		t.Fatalf("AddRequirementToRelease(second) error = %v", err)
	}
	if err := svc.RemoveRequirementFromRelease(ctx, release.Key, reqC.Key); err != nil {
		t.Fatalf("RemoveRequirementFromRelease() error = %v", err)
	}
	updatedRelease, err = db.GetRelease(ctx, release.Key)
	if err != nil {
		t.Fatalf("GetRelease(after remove) error = %v", err)
	}
	if updatedRelease.Status != store.ReleaseStatusStale {
		t.Fatalf("release status after remove = %s, want stale", updatedRelease.Status)
	}
}

func TestAddRequirementToIntegratedReleaseStatusFailureDoesNotLeaveMembership(t *testing.T) {
	ctx := context.Background()
	cfg, db := testServiceStore(t)
	svc := NewService(cfg, db, gitx.NewManager(gitx.ExecRunner{}))

	reqA := createReadyRequirementForRelease(t, ctx, db, "pay-flow")
	reqB := createReadyRequirementForRelease(t, ctx, db, "user-center")
	release, err := svc.CreateRelease(ctx, CreateReleaseParams{
		Title:           "2026-07-01 Release",
		Key:             "2026-07-01",
		RequirementKeys: []string{reqA.Key},
	})
	if err != nil {
		t.Fatalf("CreateRelease() error = %v", err)
	}
	if err := db.UpdateReleaseStatus(ctx, release.ID, store.ReleaseStatusIntegrated); err != nil {
		t.Fatalf("UpdateReleaseStatus(integrated) error = %v", err)
	}
	installFailReleaseStaleTrigger(t, cfg)

	if _, err := svc.AddRequirementToRelease(ctx, release.Key, reqB.Key); err == nil {
		t.Fatal("AddRequirementToRelease() succeeded with stale status failure, want error")
	}
	active, err := db.ListReleaseRequirements(ctx, release.ID, false)
	if err != nil {
		t.Fatalf("ListReleaseRequirements(active) error = %v", err)
	}
	if len(active) != 1 || active[0].RequirementID != reqA.ID {
		t.Fatalf("active memberships after failed add = %+v, want only original requirement", active)
	}
	unchanged, err := db.GetRelease(ctx, release.Key)
	if err != nil {
		t.Fatalf("GetRelease() error = %v", err)
	}
	if unchanged.Status != store.ReleaseStatusIntegrated {
		t.Fatalf("release status after failed add = %s, want integrated", unchanged.Status)
	}
}

func TestRemoveRequirementFromIntegratedReleaseStatusFailureDoesNotRemoveMembership(t *testing.T) {
	ctx := context.Background()
	cfg, db := testServiceStore(t)
	svc := NewService(cfg, db, gitx.NewManager(gitx.ExecRunner{}))

	reqA := createReadyRequirementForRelease(t, ctx, db, "pay-flow")
	reqB := createReadyRequirementForRelease(t, ctx, db, "user-center")
	release, err := svc.CreateRelease(ctx, CreateReleaseParams{
		Title:           "2026-07-01 Release",
		Key:             "2026-07-01",
		RequirementKeys: []string{reqA.Key, reqB.Key},
	})
	if err != nil {
		t.Fatalf("CreateRelease() error = %v", err)
	}
	if err := db.UpdateReleaseStatus(ctx, release.ID, store.ReleaseStatusIntegrated); err != nil {
		t.Fatalf("UpdateReleaseStatus(integrated) error = %v", err)
	}
	installFailReleaseStaleTrigger(t, cfg)

	if err := svc.RemoveRequirementFromRelease(ctx, release.Key, reqB.Key); err == nil {
		t.Fatal("RemoveRequirementFromRelease() succeeded with stale status failure, want error")
	}
	active, err := db.ListReleaseRequirements(ctx, release.ID, false)
	if err != nil {
		t.Fatalf("ListReleaseRequirements(active) error = %v", err)
	}
	if len(active) != 2 {
		t.Fatalf("active memberships after failed remove = %+v, want both requirements", active)
	}
	unchanged, err := db.GetRelease(ctx, release.Key)
	if err != nil {
		t.Fatalf("GetRelease() error = %v", err)
	}
	if unchanged.Status != store.ReleaseStatusIntegrated {
		t.Fatalf("release status after failed remove = %s, want integrated", unchanged.Status)
	}
}

func TestPublishReleaseRejectsRepoScopeMismatch(t *testing.T) {
	ctx := context.Background()
	cfg, db := testServiceStore(t)
	svc := NewService(cfg, db, gitx.NewManager(gitx.ExecRunner{}))
	release := createIntegratedReleaseWithMissingRepoSnapshot(t, ctx, db, store.ReleaseRepoStatusIntegrated)

	if err := svc.PublishRelease(ctx, release.Key, true, "release: 2026-07-01"); err == nil {
		t.Fatal("PublishRelease() succeeded with repo scope mismatch, want error")
	}
	if got, err := db.GetRelease(ctx, release.Key); err != nil {
		t.Fatalf("GetRelease() error = %v", err)
	} else if got.Status != store.ReleaseStatusStale {
		t.Fatalf("release status = %s, want stale", got.Status)
	}
}

func TestPublishInProgressScopeMismatchRequiresManualHandling(t *testing.T) {
	ctx := context.Background()
	cfg, db := testServiceStore(t)
	svc := NewService(cfg, db, gitx.NewManager(gitx.ExecRunner{}))
	release := createIntegratedReleaseWithMissingRepoSnapshot(t, ctx, db, store.ReleaseRepoStatusPublished)

	err := svc.PublishRelease(ctx, release.Key, true, "release: 2026-07-01")
	if err == nil {
		t.Fatal("PublishRelease() succeeded with publish-in-progress repo scope mismatch, want error")
	}
	if !strings.Contains(err.Error(), "publish-in-progress") || !strings.Contains(err.Error(), "manual handling") {
		t.Fatalf("PublishRelease() error = %v, want publish-in-progress manual handling", err)
	}
	if got, err := db.GetRelease(ctx, release.Key); err != nil {
		t.Fatalf("GetRelease() error = %v", err)
	} else if got.Status == store.ReleaseStatusStale {
		t.Fatalf("release status = %s, want non-stale manual state", got.Status)
	}
}

func testServiceStore(t *testing.T) (config.Config, *store.DB) {
	t.Helper()
	cfg, err := config.Init(t.TempDir())
	if err != nil {
		t.Fatalf("config.Init() error = %v", err)
	}
	db, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	return cfg, db
}

func markRequirementCompletedWithoutArchive(t *testing.T, cfg config.Config, reqID int64) {
	t.Helper()
	raw, err := sql.Open("sqlite", cfg.DBPath)
	if err != nil {
		t.Fatalf("open raw sqlite: %v", err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	if _, err := raw.Exec(`UPDATE requirements SET status = ?, completed_at = CURRENT_TIMESTAMP, archived_at = NULL WHERE id = ?`, store.RequirementStatusCompleted, reqID); err != nil {
		t.Fatalf("mark completed without archive: %v", err)
	}
}

func installFailReleaseStaleTrigger(t *testing.T, cfg config.Config) {
	t.Helper()
	raw, err := sql.Open("sqlite", cfg.DBPath)
	if err != nil {
		t.Fatalf("open raw sqlite: %v", err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	if _, err := raw.Exec(`CREATE TRIGGER fail_release_status_to_stale
		BEFORE UPDATE OF status ON releases
		WHEN NEW.status = 'stale'
		BEGIN
			SELECT RAISE(ABORT, 'forced release status update failure');
		END;`); err != nil {
		t.Fatalf("create release stale failure trigger: %v", err)
	}
}

func createReadyRequirementForRelease(t *testing.T, ctx context.Context, db *store.DB, key string) store.Requirement {
	t.Helper()
	req, err := db.CreateRequirement(ctx, store.CreateRequirementParams{
		Key:           key,
		Title:         key,
		Slug:          key,
		WorkspacePath: "/tmp/" + key,
		FeatureBranch: "feature/" + key,
	})
	if err != nil {
		t.Fatalf("CreateRequirement(%s) error = %v", key, err)
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

func createIntegratedReleaseWithMissingRepoSnapshot(t *testing.T, ctx context.Context, db *store.DB, releaseRepoStatus string) store.Release {
	t.Helper()
	repoA, err := db.CreateRepo(ctx, store.CreateRepoParams{
		Name:       "backend",
		URL:        "/tmp/backend.git",
		Remote:     "origin",
		BaseBranch: "main",
		BarePath:   "/tmp/backend.git",
	})
	if err != nil {
		t.Fatalf("CreateRepo(backend) error = %v", err)
	}
	repoB, err := db.CreateRepo(ctx, store.CreateRepoParams{
		Name:       "frontend",
		URL:        "/tmp/frontend.git",
		Remote:     "origin",
		BaseBranch: "main",
		BarePath:   "/tmp/frontend.git",
	})
	if err != nil {
		t.Fatalf("CreateRepo(frontend) error = %v", err)
	}
	req, err := db.CreateRequirement(ctx, store.CreateRequirementParams{
		Key:           "pay-flow",
		Title:         "Payment Flow",
		Slug:          "pay-flow",
		WorkspacePath: "/tmp/pay-flow",
		FeatureBranch: "feature/pay-flow",
	})
	if err != nil {
		t.Fatalf("CreateRequirement() error = %v", err)
	}
	for _, repo := range []store.Repo{repoA, repoB} {
		rel, err := db.AddRepoToRequirement(ctx, req.ID, repo.ID, "/tmp/pay-flow/"+repo.Name)
		if err != nil {
			t.Fatalf("AddRepoToRequirement(%s) error = %v", repo.Name, err)
		}
		if err := db.UpdateRequirementRepoStatus(ctx, rel.ID, store.RequirementRepoStatusCompleted); err != nil {
			t.Fatalf("UpdateRequirementRepoStatus(%s) error = %v", repo.Name, err)
		}
	}
	if err := db.MarkRequirementReady(ctx, req.ID); err != nil {
		t.Fatalf("MarkRequirementReady() error = %v", err)
	}
	release, err := db.CreateRelease(ctx, store.CreateReleaseParams{
		Key:           "2026-07-01",
		Title:         "2026-07-01 Release",
		Slug:          "2026-07-01",
		WorkspacePath: "/tmp/releases/2026-07-01",
		BranchName:    "release/2026-07-01",
		TargetBranch:  "per-repo",
	})
	if err != nil {
		t.Fatalf("CreateRelease() error = %v", err)
	}
	if _, err := db.AddReleaseRequirement(ctx, release.ID, req.ID, 1); err != nil {
		t.Fatalf("AddReleaseRequirement() error = %v", err)
	}
	if err := db.ReplaceReleaseSnapshots(ctx, release.ID, []store.CreateReleaseRepoParams{
		{
			ReleaseID:           release.ID,
			RepoID:              repoA.ID,
			ReleaseBranch:       release.BranchName,
			WorktreePath:        "/tmp/releases/2026-07-01/backend",
			PublishWorktreePath: "/tmp/releases/2026-07-01/.publish/backend",
			TargetBranch:        repoA.BaseBranch,
			IntegratedBaseSHA:   "base-a",
			ReleaseSHA:          "release-a",
			Status:              releaseRepoStatus,
		},
	}, nil); err != nil {
		t.Fatalf("ReplaceReleaseSnapshots() error = %v", err)
	}
	if err := db.UpdateReleaseStatus(ctx, release.ID, store.ReleaseStatusIntegrated); err != nil {
		t.Fatalf("UpdateReleaseStatus(integrated) error = %v", err)
	}
	return release
}
