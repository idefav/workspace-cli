package store

import (
	"context"
	"database/sql"
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

func TestMigrateCreatesReadyAtReleaseTablesAndMigrationRecords(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "workspace.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	assertMigrationApplied(t, db, "0001_baseline_v0_1_0")
	assertMigrationApplied(t, db, "0002_ready_release_flow")
	assertColumnExists(t, db, "requirements", "ready_at")
	for _, table := range []string{
		"releases",
		"release_requirements",
		"release_repos",
		"release_requirement_repos",
		"release_operation_logs",
	} {
		assertTableExists(t, db, table)
	}
	assertColumnExists(t, db, "release_requirement_repos", "release_requirement_id")
}

func TestMigrateUpgradesLegacyV01DatabaseWithoutChangingCompletedRequirements(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "workspace.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open raw sqlite: %v", err)
	}
	createLegacyV01Schema(t, raw)
	if _, err := raw.Exec(`
		INSERT INTO requirements (req_key, title, slug, status, workspace_path, feature_branch, completed_at, archived_at)
		VALUES ('legacy-done', 'Legacy Done', 'legacy-done', 'completed', '/tmp/legacy-done', 'feature/legacy-done', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`); err != nil {
		t.Fatalf("insert legacy requirement: %v", err)
	}
	_ = raw.Close()

	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	assertMigrationApplied(t, db, "0001_baseline_v0_1_0")
	assertMigrationApplied(t, db, "0002_ready_release_flow")
	assertColumnExists(t, db, "requirements", "ready_at")

	req, err := db.GetRequirement(ctx, "legacy-done")
	if err != nil {
		t.Fatalf("GetRequirement() error = %v", err)
	}
	if req.Status != RequirementStatusCompleted || !req.CompletedAt.Valid || !req.ArchivedAt.Valid {
		t.Fatalf("legacy completed state changed: %+v", req)
	}
	if req.ReadyAt.Valid {
		t.Fatalf("legacy completed requirement should not be backfilled as ready: %+v", req)
	}
}

func TestReleaseRequirementAllowsReAddButOnlyOneActiveMembership(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "workspace.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() error = %v", err)
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
	releaseID := insertRelease(t, db, "2026-07-01")
	if _, err := db.sql.ExecContext(ctx, `INSERT INTO release_requirements (release_id, requirement_id, position) VALUES (?, ?, ?)`, releaseID, req.ID, 1); err != nil {
		t.Fatalf("insert active release requirement: %v", err)
	}
	if _, err := db.sql.ExecContext(ctx, `INSERT INTO release_requirements (release_id, requirement_id, position) VALUES (?, ?, ?)`, releaseID, req.ID, 2); err == nil {
		t.Fatal("second active release requirement insert succeeded, want partial unique failure")
	}
	if _, err := db.sql.ExecContext(ctx, `UPDATE release_requirements SET removed_at = CURRENT_TIMESTAMP WHERE release_id = ? AND requirement_id = ?`, releaseID, req.ID); err != nil {
		t.Fatalf("soft-remove release requirement: %v", err)
	}
	if _, err := db.sql.ExecContext(ctx, `INSERT INTO release_requirements (release_id, requirement_id, position) VALUES (?, ?, ?)`, releaseID, req.ID, 2); err != nil {
		t.Fatalf("re-add after removed_at should create new membership: %v", err)
	}
}

func assertMigrationApplied(t *testing.T, db *DB, version string) {
	t.Helper()
	var count int
	if err := db.sql.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, version).Scan(&count); err != nil {
		t.Fatalf("query schema_migrations %s: %v", version, err)
	}
	if count != 1 {
		t.Fatalf("migration %s count = %d, want 1", version, count)
	}
}

func assertTableExists(t *testing.T, db *DB, table string) {
	t.Helper()
	var count int
	if err := db.sql.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&count); err != nil {
		t.Fatalf("query table %s: %v", table, err)
	}
	if count != 1 {
		t.Fatalf("table %s count = %d, want 1", table, count)
	}
}

func assertColumnExists(t *testing.T, db *DB, table, column string) {
	t.Helper()
	rows, err := db.sql.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		t.Fatalf("PRAGMA table_info(%s): %v", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var defaultValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			t.Fatalf("scan table_info(%s): %v", table, err)
		}
		if name == column {
			return
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate table_info(%s): %v", table, err)
	}
	t.Fatalf("column %s.%s not found", table, column)
}

func insertRelease(t *testing.T, db *DB, slug string) int64 {
	t.Helper()
	res, err := db.sql.Exec(`INSERT INTO releases (release_key, title, slug, status, workspace_path, branch_name, target_branch) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		slug, "Release "+slug, slug, "draft", "/tmp/releases/"+slug, "release/"+slug, "main")
	if err != nil {
		t.Fatalf("insert release: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("release id: %v", err)
	}
	return id
}

func createLegacyV01Schema(t *testing.T, db *sql.DB) {
	t.Helper()
	stmts := []string{
		`CREATE TABLE repos (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL UNIQUE,
			url TEXT NOT NULL,
			remote TEXT NOT NULL,
			base_branch TEXT NOT NULL,
			bare_path TEXT NOT NULL,
			deleted_at TIMESTAMP,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE requirements (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			req_key TEXT NOT NULL UNIQUE,
			title TEXT NOT NULL,
			slug TEXT NOT NULL UNIQUE,
			status TEXT NOT NULL DEFAULT 'active',
			workspace_path TEXT NOT NULL,
			feature_branch TEXT NOT NULL,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			completed_at TIMESTAMP,
			archived_at TIMESTAMP
		)`,
		`CREATE TABLE requirement_repos (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			requirement_id INTEGER NOT NULL,
			repo_id INTEGER NOT NULL,
			repo_name TEXT NOT NULL,
			repo_url TEXT NOT NULL,
			repo_remote TEXT NOT NULL,
			repo_base_branch TEXT NOT NULL,
			worktree_path TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'active',
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(requirement_id, repo_id),
			FOREIGN KEY(requirement_id) REFERENCES requirements(id),
			FOREIGN KEY(repo_id) REFERENCES repos(id)
		)`,
		`CREATE TABLE operation_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			requirement_id INTEGER,
			repo_id INTEGER,
			operation TEXT NOT NULL,
			status TEXT NOT NULL,
			message TEXT,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("legacy schema statement failed: %v", err)
		}
	}
}
