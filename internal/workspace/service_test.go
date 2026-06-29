package workspace

import (
	"context"
	"database/sql"
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
