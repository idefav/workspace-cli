package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestRepoAndRequirementCRUD(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "workspace.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	repo, err := db.CreateRepo(ctx, CreateRepoParams{
		Name:       "backend",
		URL:        "git@example.com:org/backend.git",
		Remote:     "origin",
		BaseBranch: "main",
		BarePath:   "/tmp/backend.git",
	})
	if err != nil {
		t.Fatalf("CreateRepo() error = %v", err)
	}

	req, err := db.CreateRequirement(ctx, CreateRequirementParams{
		Key:           "pay-flow",
		Title:         "Payment Flow",
		Slug:          "pay-flow",
		WorkspacePath: "/tmp/pay-flow",
		FeatureBranch: "feature/pay-flow",
	})
	if err != nil {
		t.Fatalf("CreateRequirement() error = %v", err)
	}

	if _, err := db.AddRepoToRequirement(ctx, req.ID, repo.ID, "/tmp/pay-flow/backend"); err != nil {
		t.Fatalf("AddRepoToRequirement() error = %v", err)
	}

	withRepos, err := db.GetRequirement(ctx, "pay-flow")
	if err != nil {
		t.Fatalf("GetRequirement() error = %v", err)
	}
	if withRepos.Status != RequirementStatusActive {
		t.Fatalf("Status = %q", withRepos.Status)
	}

	rels, err := db.ListRequirementRepos(ctx, req.ID)
	if err != nil {
		t.Fatalf("ListRequirementRepos() error = %v", err)
	}
	if len(rels) != 1 || rels[0].Repo.Name != "backend" {
		t.Fatalf("unexpected requirement repos: %+v", rels)
	}

	if err := db.MarkRequirementCompleted(ctx, req.ID); err != nil {
		t.Fatalf("MarkRequirementCompleted() error = %v", err)
	}
	completed, err := db.GetRequirement(ctx, "pay-flow")
	if err != nil {
		t.Fatalf("GetRequirement() after complete error = %v", err)
	}
	if completed.Status != RequirementStatusCompleted || !completed.CompletedAt.Valid || !completed.ArchivedAt.Valid {
		t.Fatalf("completion state = %+v", completed)
	}
}
