package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

const (
	RequirementStatusActive    = "active"
	RequirementStatusCompleted = "completed"

	RequirementRepoStatusActive        = "active"
	RequirementRepoStatusPushed        = "pushed"
	RequirementRepoStatusCompleted     = "completed"
	RequirementRepoStatusCleanupFailed = "cleanup_failed"

	OperationStatusSuccess = "success"
	OperationStatusFailed  = "failed"

	ReleaseStatusDraft      = "draft"
	ReleaseStatusIntegrated = "integrated"
	ReleaseStatusStale      = "stale"
	ReleaseStatusPublished  = "published"
	ReleaseStatusFailed     = "failed"

	ReleaseRepoStatusPending    = "pending"
	ReleaseRepoStatusIntegrated = "integrated"
	ReleaseRepoStatusStale      = "stale"
	ReleaseRepoStatusPublished  = "published"
	ReleaseRepoStatusFailed     = "failed"

	MigrationBaselineV010     = "0001_baseline_v0_1_0"
	MigrationReadyReleaseFlow = "0002_ready_release_flow"
)

type DB struct {
	sql *sql.DB
}

type Repo struct {
	ID         int64
	Name       string
	URL        string
	Remote     string
	BaseBranch string
	BarePath   string
	DeletedAt  sql.NullTime
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type Requirement struct {
	ID            int64
	Key           string
	Title         string
	Slug          string
	Status        string
	WorkspacePath string
	FeatureBranch string
	CreatedAt     time.Time
	UpdatedAt     time.Time
	CompletedAt   sql.NullTime
	ArchivedAt    sql.NullTime
	ReadyAt       sql.NullTime
}

type RequirementRepo struct {
	ID             int64
	RequirementID  int64
	RepoID         int64
	RepoName       string
	RepoURL        string
	RepoRemote     string
	RepoBaseBranch string
	WorktreePath   string
	Status         string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	Repo           Repo
}

type Release struct {
	ID            int64
	Key           string
	Title         string
	Slug          string
	Status        string
	WorkspacePath string
	BranchName    string
	TargetBranch  string
	CreatedAt     time.Time
	UpdatedAt     time.Time
	IntegratedAt  sql.NullTime
	PublishedAt   sql.NullTime
}

type ReleaseRequirement struct {
	ID            int64
	ReleaseID     int64
	RequirementID int64
	Position      int
	RemovedAt     sql.NullTime
	CreatedAt     time.Time
	UpdatedAt     time.Time
	Requirement   Requirement
}

type ReleaseRepo struct {
	ID                  int64
	ReleaseID           int64
	RepoID              int64
	ReleaseBranch       string
	WorktreePath        string
	PublishWorktreePath string
	TargetBranch        string
	IntegratedBaseSHA   string
	ReleaseSHA          string
	PublishedSHA        sql.NullString
	Status              string
	CreatedAt           time.Time
	UpdatedAt           time.Time
	Repo                Repo
}

type ReleaseRequirementRepo struct {
	ID                   int64
	ReleaseRequirementID int64
	ReleaseID            int64
	RequirementID        int64
	RepoID               int64
	FeatureBranch        string
	FeatureSHA           string
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

type OperationLog struct {
	ID            int64
	RequirementID sql.NullInt64
	RepoID        sql.NullInt64
	Operation     string
	Status        string
	Message       sql.NullString
	CreatedAt     time.Time
}

type CreateRepoParams struct {
	Name       string
	URL        string
	Remote     string
	BaseBranch string
	BarePath   string
}

type CreateRequirementParams struct {
	Key           string
	Title         string
	Slug          string
	WorkspacePath string
	FeatureBranch string
}

type CreateReleaseParams struct {
	Key           string
	Title         string
	Slug          string
	WorkspacePath string
	BranchName    string
	TargetBranch  string
}

type CreateReleaseRepoParams struct {
	ReleaseID           int64
	RepoID              int64
	ReleaseBranch       string
	WorktreePath        string
	PublishWorktreePath string
	TargetBranch        string
	IntegratedBaseSHA   string
	ReleaseSHA          string
	Status              string
}

type CreateReleaseRequirementRepoParams struct {
	ReleaseRequirementID int64
	ReleaseID            int64
	RequirementID        int64
	RepoID               int64
	FeatureBranch        string
	FeatureSHA           string
}

func Open(path string) (*DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	return &DB{sql: db}, nil
}

func (db *DB) Close() error {
	return db.sql.Close()
}

func (db *DB) Migrate(ctx context.Context) error {
	if _, err := db.sql.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		return fmt.Errorf("create schema migrations table: %w", err)
	}
	migrations := []struct {
		version string
		name    string
		run     func(context.Context, *sql.Tx) error
	}{
		{version: MigrationBaselineV010, name: "baseline v0.1.0", run: migrateBaselineV010},
		{version: MigrationReadyReleaseFlow, name: "ready release flow", run: migrateReadyReleaseFlow},
	}
	for _, migration := range migrations {
		applied, err := db.migrationApplied(ctx, migration.version)
		if err != nil {
			return err
		}
		if applied {
			continue
		}
		tx, err := db.sql.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration %s: %w", migration.version, err)
		}
		if err := migration.run(ctx, tx); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migration %s: %w", migration.version, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, name) VALUES (?, ?)`, migration.version, migration.name); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %s: %w", migration.version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %s: %w", migration.version, err)
		}
	}
	return nil
}

func (db *DB) migrationApplied(ctx context.Context, version string) (bool, error) {
	var count int
	err := db.sql.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, version).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("query migration %s: %w", version, err)
	}
	return count > 0, nil
}

func migrateBaselineV010(ctx context.Context, tx *sql.Tx) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS repos (
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
		`CREATE TABLE IF NOT EXISTS requirements (
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
		`CREATE TABLE IF NOT EXISTS requirement_repos (
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
		`CREATE TABLE IF NOT EXISTS operation_logs (
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
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	return nil
}

func migrateReadyReleaseFlow(ctx context.Context, tx *sql.Tx) error {
	readyAtExists, err := txColumnExists(ctx, tx, "requirements", "ready_at")
	if err != nil {
		return err
	}
	if !readyAtExists {
		if _, err := tx.ExecContext(ctx, `ALTER TABLE requirements ADD COLUMN ready_at TIMESTAMP`); err != nil {
			return fmt.Errorf("add requirements.ready_at: %w", err)
		}
	}
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS releases (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			release_key TEXT NOT NULL UNIQUE,
			title TEXT NOT NULL,
			slug TEXT NOT NULL UNIQUE,
			status TEXT NOT NULL DEFAULT 'draft',
			workspace_path TEXT NOT NULL,
			branch_name TEXT NOT NULL,
			target_branch TEXT NOT NULL,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			integrated_at TIMESTAMP,
			published_at TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS release_requirements (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			release_id INTEGER NOT NULL,
			requirement_id INTEGER NOT NULL,
			position INTEGER NOT NULL,
			removed_at TIMESTAMP,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY(release_id) REFERENCES releases(id),
			FOREIGN KEY(requirement_id) REFERENCES requirements(id)
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS release_requirements_one_active
			ON release_requirements(release_id, requirement_id)
			WHERE removed_at IS NULL`,
		`CREATE TABLE IF NOT EXISTS release_repos (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			release_id INTEGER NOT NULL,
			repo_id INTEGER NOT NULL,
			release_branch TEXT NOT NULL,
			worktree_path TEXT NOT NULL,
			publish_worktree_path TEXT NOT NULL,
			target_branch TEXT NOT NULL,
			integrated_base_sha TEXT,
			release_sha TEXT,
			published_sha TEXT,
			status TEXT NOT NULL DEFAULT 'pending',
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(release_id, repo_id),
			FOREIGN KEY(release_id) REFERENCES releases(id),
			FOREIGN KEY(repo_id) REFERENCES repos(id)
		)`,
		`CREATE TABLE IF NOT EXISTS release_requirement_repos (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			release_requirement_id INTEGER NOT NULL,
			release_id INTEGER NOT NULL,
			requirement_id INTEGER NOT NULL,
			repo_id INTEGER NOT NULL,
			feature_branch TEXT NOT NULL,
			feature_sha TEXT NOT NULL,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY(release_requirement_id) REFERENCES release_requirements(id),
			FOREIGN KEY(release_id) REFERENCES releases(id),
			FOREIGN KEY(requirement_id) REFERENCES requirements(id),
			FOREIGN KEY(repo_id) REFERENCES repos(id)
		)`,
		`CREATE TABLE IF NOT EXISTS release_operation_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			release_id INTEGER,
			requirement_id INTEGER,
			repo_id INTEGER,
			operation TEXT NOT NULL,
			status TEXT NOT NULL,
			message TEXT,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY(release_id) REFERENCES releases(id),
			FOREIGN KEY(requirement_id) REFERENCES requirements(id),
			FOREIGN KEY(repo_id) REFERENCES repos(id)
		)`,
	}
	for _, stmt := range stmts {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("migrate release flow: %w", err)
		}
	}
	return nil
}

func txColumnExists(ctx context.Context, tx *sql.Tx, table, column string) (bool, error) {
	rows, err := tx.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return false, fmt.Errorf("table info %s: %w", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var defaultValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return false, fmt.Errorf("scan table info %s: %w", table, err)
		}
		if name == column {
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("iterate table info %s: %w", table, err)
	}
	return false, nil
}

func (db *DB) CreateRepo(ctx context.Context, params CreateRepoParams) (Repo, error) {
	res, err := db.sql.ExecContext(ctx, `INSERT INTO repos (name, url, remote, base_branch, bare_path) VALUES (?, ?, ?, ?, ?)`,
		params.Name, params.URL, params.Remote, params.BaseBranch, params.BarePath)
	if err != nil {
		return Repo{}, fmt.Errorf("create repo %s: %w", params.Name, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Repo{}, fmt.Errorf("repo id: %w", err)
	}
	return db.GetRepoByID(ctx, id)
}

func (db *DB) GetRepo(ctx context.Context, name string) (Repo, error) {
	return db.scanRepo(db.sql.QueryRowContext(ctx, `SELECT id, name, url, remote, base_branch, bare_path, deleted_at, created_at, updated_at FROM repos WHERE name = ? AND deleted_at IS NULL`, name))
}

func (db *DB) GetRepoByID(ctx context.Context, id int64) (Repo, error) {
	return db.scanRepo(db.sql.QueryRowContext(ctx, `SELECT id, name, url, remote, base_branch, bare_path, deleted_at, created_at, updated_at FROM repos WHERE id = ?`, id))
}

func (db *DB) ListRepos(ctx context.Context, includeDeleted bool) ([]Repo, error) {
	query := `SELECT id, name, url, remote, base_branch, bare_path, deleted_at, created_at, updated_at FROM repos`
	if !includeDeleted {
		query += ` WHERE deleted_at IS NULL`
	}
	query += ` ORDER BY name`
	rows, err := db.sql.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list repos: %w", err)
	}
	defer rows.Close()
	var repos []Repo
	for rows.Next() {
		var repo Repo
		if err := rows.Scan(&repo.ID, &repo.Name, &repo.URL, &repo.Remote, &repo.BaseBranch, &repo.BarePath, &repo.DeletedAt, &repo.CreatedAt, &repo.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan repo: %w", err)
		}
		repos = append(repos, repo)
	}
	return repos, rows.Err()
}

func (db *DB) UpdateRepo(ctx context.Context, id int64, url, remote, baseBranch string) error {
	_, err := db.sql.ExecContext(ctx, `UPDATE repos SET url = ?, remote = ?, base_branch = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, url, remote, baseBranch, id)
	if err != nil {
		return fmt.Errorf("update repo: %w", err)
	}
	return nil
}

func (db *DB) SoftDeleteRepo(ctx context.Context, id int64) error {
	_, err := db.sql.ExecContext(ctx, `UPDATE repos SET deleted_at = COALESCE(deleted_at, CURRENT_TIMESTAMP), updated_at = CURRENT_TIMESTAMP WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("remove repo: %w", err)
	}
	return nil
}

func (db *DB) RepoHasActiveOrCleanupRefs(ctx context.Context, repoID int64) (bool, error) {
	var count int
	err := db.sql.QueryRowContext(ctx, `SELECT COUNT(*)
		FROM requirement_repos rr
		JOIN requirements req ON req.id = rr.requirement_id
		WHERE rr.repo_id = ?
		  AND (
			(req.status = 'active' AND req.archived_at IS NULL)
			OR rr.status IN ('pushed', 'cleanup_failed')
		  )`, repoID).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("count repo refs: %w", err)
	}
	return count > 0, nil
}

func (db *DB) RepoHasUnpublishedReleaseRefs(ctx context.Context, repoID int64) (bool, error) {
	var count int
	err := db.sql.QueryRowContext(ctx, `SELECT COUNT(*) FROM (
		SELECT 1
		FROM release_requirements rel_req
		JOIN releases rel ON rel.id = rel_req.release_id
		JOIN requirement_repos req_repo ON req_repo.requirement_id = rel_req.requirement_id
		WHERE req_repo.repo_id = ?
		  AND rel_req.removed_at IS NULL
		  AND rel.status != 'published'
		UNION
		SELECT 1
		FROM release_repos rel_repo
		JOIN releases rel ON rel.id = rel_repo.release_id
		WHERE rel_repo.repo_id = ?
		  AND rel.status != 'published'
	)`, repoID, repoID).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("count unpublished release refs: %w", err)
	}
	return count > 0, nil
}

func (db *DB) RequirementHasPublishedReleaseAssociation(ctx context.Context, requirementID int64) (bool, error) {
	var count int
	err := db.sql.QueryRowContext(ctx, `SELECT COUNT(*)
		FROM release_requirements rel_req
		JOIN releases rel ON rel.id = rel_req.release_id
		WHERE rel_req.requirement_id = ?
		  AND rel_req.removed_at IS NULL
		  AND rel.status = ?`, requirementID, ReleaseStatusPublished).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("count requirement published release associations: %w", err)
	}
	return count > 0, nil
}

func (db *DB) CreateRequirement(ctx context.Context, params CreateRequirementParams) (Requirement, error) {
	res, err := db.sql.ExecContext(ctx, `INSERT INTO requirements (req_key, title, slug, status, workspace_path, feature_branch) VALUES (?, ?, ?, ?, ?, ?)`,
		params.Key, params.Title, params.Slug, RequirementStatusActive, params.WorkspacePath, params.FeatureBranch)
	if err != nil {
		return Requirement{}, fmt.Errorf("create requirement %s: %w", params.Key, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Requirement{}, fmt.Errorf("requirement id: %w", err)
	}
	return db.getRequirementByID(ctx, id)
}

func (db *DB) MarkRequirementReady(ctx context.Context, requirementID int64) error {
	_, err := db.sql.ExecContext(ctx, `UPDATE requirements SET ready_at = COALESCE(ready_at, CURRENT_TIMESTAMP), updated_at = CURRENT_TIMESTAMP WHERE id = ?`, requirementID)
	if err != nil {
		return fmt.Errorf("mark requirement ready: %w", err)
	}
	return nil
}

func (db *DB) ClearRequirementReady(ctx context.Context, requirementID int64) error {
	_, err := db.sql.ExecContext(ctx, `UPDATE requirements SET ready_at = NULL, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, requirementID)
	if err != nil {
		return fmt.Errorf("clear requirement ready: %w", err)
	}
	return nil
}

func (db *DB) ReopenRequirement(ctx context.Context, requirementID int64, relationIDs []int64) error {
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin reopen requirement: %w", err)
	}
	for _, relationID := range relationIDs {
		if _, err := tx.ExecContext(ctx, `UPDATE requirement_repos SET status = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, RequirementRepoStatusActive, relationID); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("reopen requirement relation: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE requirements SET ready_at = NULL, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, requirementID); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("clear requirement ready: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE releases
		SET status = ?, updated_at = CURRENT_TIMESTAMP
		WHERE status NOT IN (?, ?)
		  AND id IN (
			SELECT release_id FROM release_requirements
			WHERE requirement_id = ? AND removed_at IS NULL
		  )`, ReleaseStatusStale, ReleaseStatusPublished, ReleaseStatusDraft, requirementID); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("mark releases stale for requirement: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit reopen requirement: %w", err)
	}
	return nil
}

func (db *DB) GetRequirement(ctx context.Context, keyOrSlug string) (Requirement, error) {
	return db.scanRequirement(db.sql.QueryRowContext(ctx, `SELECT id, req_key, title, slug, status, workspace_path, feature_branch, created_at, updated_at, completed_at, archived_at, ready_at FROM requirements WHERE req_key = ? OR slug = ?`, keyOrSlug, keyOrSlug))
}

func (db *DB) ListRequirements(ctx context.Context, includeArchived bool) ([]Requirement, error) {
	query := `SELECT id, req_key, title, slug, status, workspace_path, feature_branch, created_at, updated_at, completed_at, archived_at, ready_at FROM requirements`
	if !includeArchived {
		query += ` WHERE status = 'active' AND archived_at IS NULL`
	}
	query += ` ORDER BY created_at, id`
	rows, err := db.sql.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list requirements: %w", err)
	}
	defer rows.Close()
	var reqs []Requirement
	for rows.Next() {
		var req Requirement
		if err := rows.Scan(&req.ID, &req.Key, &req.Title, &req.Slug, &req.Status, &req.WorkspacePath, &req.FeatureBranch, &req.CreatedAt, &req.UpdatedAt, &req.CompletedAt, &req.ArchivedAt, &req.ReadyAt); err != nil {
			return nil, fmt.Errorf("scan requirement: %w", err)
		}
		reqs = append(reqs, req)
	}
	return reqs, rows.Err()
}

func (db *DB) AddRepoToRequirement(ctx context.Context, requirementID, repoID int64, worktreePath string) (RequirementRepo, error) {
	repo, err := db.GetRepoByID(ctx, repoID)
	if err != nil {
		return RequirementRepo{}, err
	}
	res, err := db.sql.ExecContext(ctx, `INSERT INTO requirement_repos (requirement_id, repo_id, repo_name, repo_url, repo_remote, repo_base_branch, worktree_path, status) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		requirementID, repoID, repo.Name, repo.URL, repo.Remote, repo.BaseBranch, worktreePath, RequirementRepoStatusActive)
	if err != nil {
		return RequirementRepo{}, fmt.Errorf("add repo %d to requirement %d: %w", repoID, requirementID, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return RequirementRepo{}, fmt.Errorf("requirement repo id: %w", err)
	}
	return db.getRequirementRepoByID(ctx, id)
}

func (db *DB) CreateRelease(ctx context.Context, params CreateReleaseParams) (Release, error) {
	res, err := db.sql.ExecContext(ctx, `INSERT INTO releases (release_key, title, slug, status, workspace_path, branch_name, target_branch) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		params.Key, params.Title, params.Slug, ReleaseStatusDraft, params.WorkspacePath, params.BranchName, params.TargetBranch)
	if err != nil {
		return Release{}, fmt.Errorf("create release %s: %w", params.Key, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Release{}, fmt.Errorf("release id: %w", err)
	}
	return db.getReleaseByID(ctx, id)
}

func (db *DB) GetRelease(ctx context.Context, keyOrSlug string) (Release, error) {
	return db.scanRelease(db.sql.QueryRowContext(ctx, `SELECT id, release_key, title, slug, status, workspace_path, branch_name, target_branch, created_at, updated_at, integrated_at, published_at FROM releases WHERE release_key = ? OR slug = ?`, keyOrSlug, keyOrSlug))
}

func (db *DB) ListReleases(ctx context.Context, includePublished bool) ([]Release, error) {
	query := `SELECT id, release_key, title, slug, status, workspace_path, branch_name, target_branch, created_at, updated_at, integrated_at, published_at FROM releases`
	if !includePublished {
		query += ` WHERE status != 'published'`
	}
	query += ` ORDER BY created_at, id`
	rows, err := db.sql.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list releases: %w", err)
	}
	defer rows.Close()
	var releases []Release
	for rows.Next() {
		release, err := scanReleaseRows(rows)
		if err != nil {
			return nil, err
		}
		releases = append(releases, release)
	}
	return releases, rows.Err()
}

func (db *DB) AddReleaseRequirement(ctx context.Context, releaseID, requirementID int64, position int) (ReleaseRequirement, error) {
	res, err := db.sql.ExecContext(ctx, `INSERT INTO release_requirements (release_id, requirement_id, position) VALUES (?, ?, ?)`, releaseID, requirementID, position)
	if err != nil {
		return ReleaseRequirement{}, fmt.Errorf("add requirement %d to release %d: %w", requirementID, releaseID, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return ReleaseRequirement{}, fmt.Errorf("release requirement id: %w", err)
	}
	return db.getReleaseRequirementByID(ctx, id)
}

func (db *DB) AddReleaseRequirementAndMarkStale(ctx context.Context, releaseID, requirementID int64, position int) (ReleaseRequirement, error) {
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return ReleaseRequirement{}, fmt.Errorf("begin add release requirement: %w", err)
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO release_requirements (release_id, requirement_id, position) VALUES (?, ?, ?)`, releaseID, requirementID, position)
	if err != nil {
		_ = tx.Rollback()
		return ReleaseRequirement{}, fmt.Errorf("add requirement %d to release %d: %w", requirementID, releaseID, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		_ = tx.Rollback()
		return ReleaseRequirement{}, fmt.Errorf("release requirement id: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE releases SET status = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, ReleaseStatusStale, releaseID); err != nil {
		_ = tx.Rollback()
		return ReleaseRequirement{}, fmt.Errorf("mark release stale after add requirement: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return ReleaseRequirement{}, fmt.Errorf("commit add release requirement: %w", err)
	}
	return db.getReleaseRequirementByID(ctx, id)
}

func (db *DB) ListReleaseRequirements(ctx context.Context, releaseID int64, includeRemoved bool) ([]ReleaseRequirement, error) {
	query := `SELECT rr.id, rr.release_id, rr.requirement_id, rr.position, rr.removed_at, rr.created_at, rr.updated_at,
		req.id, req.req_key, req.title, req.slug, req.status, req.workspace_path, req.feature_branch, req.created_at, req.updated_at, req.completed_at, req.archived_at, req.ready_at
		FROM release_requirements rr JOIN requirements req ON req.id = rr.requirement_id
		WHERE rr.release_id = ?`
	if !includeRemoved {
		query += ` AND rr.removed_at IS NULL`
	}
	query += ` ORDER BY rr.position, rr.id`
	rows, err := db.sql.QueryContext(ctx, query, releaseID)
	if err != nil {
		return nil, fmt.Errorf("list release requirements: %w", err)
	}
	defer rows.Close()
	var requirements []ReleaseRequirement
	for rows.Next() {
		rel, err := scanReleaseRequirementRows(rows)
		if err != nil {
			return nil, err
		}
		requirements = append(requirements, rel)
	}
	return requirements, rows.Err()
}

func (db *DB) RemoveReleaseRequirement(ctx context.Context, releaseID, requirementID int64) error {
	res, err := db.sql.ExecContext(ctx, `UPDATE release_requirements
		SET removed_at = COALESCE(removed_at, CURRENT_TIMESTAMP), updated_at = CURRENT_TIMESTAMP
		WHERE release_id = ? AND requirement_id = ? AND removed_at IS NULL`, releaseID, requirementID)
	if err != nil {
		return fmt.Errorf("remove requirement %d from release %d: %w", requirementID, releaseID, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("remove release requirement rows affected: %w", err)
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (db *DB) RemoveReleaseRequirementAndMarkStale(ctx context.Context, releaseID, requirementID int64) error {
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin remove release requirement: %w", err)
	}
	res, err := tx.ExecContext(ctx, `UPDATE release_requirements
		SET removed_at = COALESCE(removed_at, CURRENT_TIMESTAMP), updated_at = CURRENT_TIMESTAMP
		WHERE release_id = ? AND requirement_id = ? AND removed_at IS NULL`, releaseID, requirementID)
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("remove requirement %d from release %d: %w", requirementID, releaseID, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("remove release requirement rows affected: %w", err)
	}
	if affected == 0 {
		_ = tx.Rollback()
		return sql.ErrNoRows
	}
	if _, err := tx.ExecContext(ctx, `UPDATE releases SET status = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, ReleaseStatusStale, releaseID); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("mark release stale after remove requirement: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit remove release requirement: %w", err)
	}
	return nil
}

func (db *DB) DeleteRelease(ctx context.Context, releaseID int64) error {
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin delete release: %w", err)
	}
	stmts := []string{
		`DELETE FROM release_requirement_repos WHERE release_id = ?`,
		`DELETE FROM release_repos WHERE release_id = ?`,
		`DELETE FROM release_operation_logs WHERE release_id = ?`,
		`DELETE FROM release_requirements WHERE release_id = ?`,
		`DELETE FROM releases WHERE id = ?`,
	}
	for _, stmt := range stmts {
		if _, err := tx.ExecContext(ctx, stmt, releaseID); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("delete release: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit delete release: %w", err)
	}
	return nil
}

func (db *DB) UpdateReleaseStatus(ctx context.Context, releaseID int64, status string) error {
	query := `UPDATE releases SET status = ?, updated_at = CURRENT_TIMESTAMP`
	if status == ReleaseStatusIntegrated {
		query += `, integrated_at = CURRENT_TIMESTAMP`
	}
	if status == ReleaseStatusPublished {
		query += `, published_at = CURRENT_TIMESTAMP`
	}
	query += ` WHERE id = ?`
	_, err := db.sql.ExecContext(ctx, query, status, releaseID)
	if err != nil {
		return fmt.Errorf("update release status: %w", err)
	}
	return nil
}

func (db *DB) MarkReleasesStaleForRequirement(ctx context.Context, requirementID int64) error {
	_, err := db.sql.ExecContext(ctx, `UPDATE releases
		SET status = ?, updated_at = CURRENT_TIMESTAMP
		WHERE status NOT IN (?, ?)
		  AND id IN (
			SELECT release_id FROM release_requirements
			WHERE requirement_id = ? AND removed_at IS NULL
		  )`, ReleaseStatusStale, ReleaseStatusPublished, ReleaseStatusDraft, requirementID)
	if err != nil {
		return fmt.Errorf("mark releases stale for requirement: %w", err)
	}
	return nil
}

func (db *DB) ReplaceReleaseSnapshots(ctx context.Context, releaseID int64, repos []CreateReleaseRepoParams, requirementRepos []CreateReleaseRequirementRepoParams) error {
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin replace release snapshots: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM release_requirement_repos WHERE release_id = ?`, releaseID); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("delete release requirement repo snapshots: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM release_repos WHERE release_id = ?`, releaseID); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("delete release repo snapshots: %w", err)
	}
	for _, repo := range repos {
		if repo.Status == "" {
			repo.Status = ReleaseRepoStatusIntegrated
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO release_repos (release_id, repo_id, release_branch, worktree_path, publish_worktree_path, target_branch, integrated_base_sha, release_sha, status)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			repo.ReleaseID, repo.RepoID, repo.ReleaseBranch, repo.WorktreePath, repo.PublishWorktreePath, repo.TargetBranch, repo.IntegratedBaseSHA, repo.ReleaseSHA, repo.Status); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("insert release repo snapshot: %w", err)
		}
	}
	for _, reqRepo := range requirementRepos {
		if _, err := tx.ExecContext(ctx, `INSERT INTO release_requirement_repos (release_requirement_id, release_id, requirement_id, repo_id, feature_branch, feature_sha)
			VALUES (?, ?, ?, ?, ?, ?)`,
			reqRepo.ReleaseRequirementID, reqRepo.ReleaseID, reqRepo.RequirementID, reqRepo.RepoID, reqRepo.FeatureBranch, reqRepo.FeatureSHA); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("insert release requirement repo snapshot: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit replace release snapshots: %w", err)
	}
	return nil
}

func (db *DB) ListReleaseRepos(ctx context.Context, releaseID int64) ([]ReleaseRepo, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT rr.id, rr.release_id, rr.repo_id, rr.release_branch, rr.worktree_path, rr.publish_worktree_path, rr.target_branch, rr.integrated_base_sha, rr.release_sha, rr.published_sha, rr.status, rr.created_at, rr.updated_at,
		r.id, r.name, r.url, r.remote, r.base_branch, r.bare_path, r.deleted_at, r.created_at, r.updated_at
		FROM release_repos rr JOIN repos r ON r.id = rr.repo_id
		WHERE rr.release_id = ? ORDER BY rr.id`, releaseID)
	if err != nil {
		return nil, fmt.Errorf("list release repos: %w", err)
	}
	defer rows.Close()
	var repos []ReleaseRepo
	for rows.Next() {
		var repo ReleaseRepo
		if err := rows.Scan(&repo.ID, &repo.ReleaseID, &repo.RepoID, &repo.ReleaseBranch, &repo.WorktreePath, &repo.PublishWorktreePath, &repo.TargetBranch, &repo.IntegratedBaseSHA, &repo.ReleaseSHA, &repo.PublishedSHA, &repo.Status, &repo.CreatedAt, &repo.UpdatedAt,
			&repo.Repo.ID, &repo.Repo.Name, &repo.Repo.URL, &repo.Repo.Remote, &repo.Repo.BaseBranch, &repo.Repo.BarePath, &repo.Repo.DeletedAt, &repo.Repo.CreatedAt, &repo.Repo.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan release repo: %w", err)
		}
		repos = append(repos, repo)
	}
	return repos, rows.Err()
}

func (db *DB) ListReleaseRequirementRepos(ctx context.Context, releaseID int64) ([]ReleaseRequirementRepo, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT id, release_requirement_id, release_id, requirement_id, repo_id, feature_branch, feature_sha, created_at, updated_at
		FROM release_requirement_repos WHERE release_id = ? ORDER BY id`, releaseID)
	if err != nil {
		return nil, fmt.Errorf("list release requirement repos: %w", err)
	}
	defer rows.Close()
	var snapshots []ReleaseRequirementRepo
	for rows.Next() {
		var snapshot ReleaseRequirementRepo
		if err := rows.Scan(&snapshot.ID, &snapshot.ReleaseRequirementID, &snapshot.ReleaseID, &snapshot.RequirementID, &snapshot.RepoID, &snapshot.FeatureBranch, &snapshot.FeatureSHA, &snapshot.CreatedAt, &snapshot.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan release requirement repo: %w", err)
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots, rows.Err()
}

func (db *DB) MarkReleaseRepoPublished(ctx context.Context, releaseRepoID int64, publishedSHA string) error {
	_, err := db.sql.ExecContext(ctx, `UPDATE release_repos
		SET status = ?, published_sha = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`, ReleaseRepoStatusPublished, publishedSHA, releaseRepoID)
	if err != nil {
		return fmt.Errorf("mark release repo published: %w", err)
	}
	return nil
}

func (db *DB) MarkReleaseRepoFailed(ctx context.Context, releaseRepoID int64) error {
	_, err := db.sql.ExecContext(ctx, `UPDATE release_repos
		SET status = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`, ReleaseRepoStatusFailed, releaseRepoID)
	if err != nil {
		return fmt.Errorf("mark release repo failed: %w", err)
	}
	return nil
}

func (db *DB) DeleteRequirementRepo(ctx context.Context, id int64) error {
	_, err := db.sql.ExecContext(ctx, `DELETE FROM requirement_repos WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete requirement repo: %w", err)
	}
	return nil
}

func (db *DB) DeleteRequirement(ctx context.Context, id int64) error {
	_, err := db.sql.ExecContext(ctx, `DELETE FROM requirements WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete requirement: %w", err)
	}
	return nil
}

func (db *DB) ListRequirementRepos(ctx context.Context, requirementID int64) ([]RequirementRepo, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT rr.id, rr.requirement_id, rr.repo_id, rr.repo_name, rr.repo_url, rr.repo_remote, rr.repo_base_branch, rr.worktree_path, rr.status, rr.created_at, rr.updated_at,
		r.id, r.name, r.url, r.remote, r.base_branch, r.bare_path, r.deleted_at, r.created_at, r.updated_at
		FROM requirement_repos rr JOIN repos r ON r.id = rr.repo_id WHERE rr.requirement_id = ? ORDER BY rr.id`, requirementID)
	if err != nil {
		return nil, fmt.Errorf("list requirement repos: %w", err)
	}
	defer rows.Close()

	var rels []RequirementRepo
	for rows.Next() {
		var rel RequirementRepo
		if err := rows.Scan(&rel.ID, &rel.RequirementID, &rel.RepoID, &rel.RepoName, &rel.RepoURL, &rel.RepoRemote, &rel.RepoBaseBranch, &rel.WorktreePath, &rel.Status, &rel.CreatedAt, &rel.UpdatedAt,
			&rel.Repo.ID, &rel.Repo.Name, &rel.Repo.URL, &rel.Repo.Remote, &rel.Repo.BaseBranch, &rel.Repo.BarePath, &rel.Repo.DeletedAt, &rel.Repo.CreatedAt, &rel.Repo.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan requirement repo: %w", err)
		}
		rels = append(rels, rel)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate requirement repos: %w", err)
	}
	return rels, nil
}

func (db *DB) MarkRequirementCompleted(ctx context.Context, requirementID int64) error {
	_, err := db.sql.ExecContext(ctx, `UPDATE requirements SET status = ?, completed_at = COALESCE(completed_at, CURRENT_TIMESTAMP), archived_at = COALESCE(archived_at, CURRENT_TIMESTAMP), updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		RequirementStatusCompleted, requirementID)
	if err != nil {
		return fmt.Errorf("mark requirement completed: %w", err)
	}
	return nil
}

func (db *DB) FinalizeReleasePublished(ctx context.Context, releaseID int64, requirementIDs []int64) error {
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin finalize release publish: %w", err)
	}
	for _, requirementID := range requirementIDs {
		if _, err := tx.ExecContext(ctx, `UPDATE requirements
			SET status = ?, completed_at = COALESCE(completed_at, CURRENT_TIMESTAMP), archived_at = COALESCE(archived_at, CURRENT_TIMESTAMP), updated_at = CURRENT_TIMESTAMP
			WHERE id = ?`, RequirementStatusCompleted, requirementID); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("finalize requirement completed: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE releases
		SET status = ?, updated_at = CURRENT_TIMESTAMP, published_at = CURRENT_TIMESTAMP
		WHERE id = ?`, ReleaseStatusPublished, releaseID); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("finalize release published: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit finalize release publish: %w", err)
	}
	return nil
}

func (db *DB) UpdateRequirementTitle(ctx context.Context, requirementID int64, title string) error {
	_, err := db.sql.ExecContext(ctx, `UPDATE requirements SET title = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, title, requirementID)
	if err != nil {
		return fmt.Errorf("update requirement title: %w", err)
	}
	return nil
}

func (db *DB) ArchiveRequirement(ctx context.Context, requirementID int64) error {
	_, err := db.sql.ExecContext(ctx, `UPDATE requirements SET archived_at = COALESCE(archived_at, CURRENT_TIMESTAMP), updated_at = CURRENT_TIMESTAMP WHERE id = ?`, requirementID)
	if err != nil {
		return fmt.Errorf("archive requirement: %w", err)
	}
	return nil
}

func (db *DB) UpdateRequirementRepoStatus(ctx context.Context, id int64, status string) error {
	_, err := db.sql.ExecContext(ctx, `UPDATE requirement_repos SET status = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, status, id)
	if err != nil {
		return fmt.Errorf("update requirement repo status: %w", err)
	}
	return nil
}

func (db *DB) UpdateRequirementRepoStatuses(ctx context.Context, ids []int64, status string) error {
	if len(ids) == 0 {
		return nil
	}
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin requirement repo status update: %w", err)
	}
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `UPDATE requirement_repos SET status = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, status, id); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("update requirement repo statuses: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit requirement repo status update: %w", err)
	}
	return nil
}

func (db *DB) LogOperation(ctx context.Context, requirementID, repoID int64, operation, status, message string) error {
	_, err := db.sql.ExecContext(ctx, `INSERT INTO operation_logs (requirement_id, repo_id, operation, status, message) VALUES (?, ?, ?, ?, ?)`,
		requirementID, repoID, operation, status, message)
	if err != nil {
		return fmt.Errorf("log operation: %w", err)
	}
	return nil
}

func (db *DB) LogReleaseOperation(ctx context.Context, releaseID, requirementID, repoID int64, operation, status, message string) error {
	_, err := db.sql.ExecContext(ctx, `INSERT INTO release_operation_logs (release_id, requirement_id, repo_id, operation, status, message) VALUES (?, ?, ?, ?, ?, ?)`,
		releaseID, requirementID, repoID, operation, status, message)
	if err != nil {
		return fmt.Errorf("log release operation: %w", err)
	}
	return nil
}

func (db *DB) ListOperationLogs(ctx context.Context, requirementID int64) ([]OperationLog, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT id, requirement_id, repo_id, operation, status, message, created_at FROM operation_logs WHERE requirement_id = ? ORDER BY id`, requirementID)
	if err != nil {
		return nil, fmt.Errorf("list operation logs: %w", err)
	}
	defer rows.Close()
	var logs []OperationLog
	for rows.Next() {
		var log OperationLog
		if err := rows.Scan(&log.ID, &log.RequirementID, &log.RepoID, &log.Operation, &log.Status, &log.Message, &log.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan operation log: %w", err)
		}
		logs = append(logs, log)
	}
	return logs, rows.Err()
}

func (db *DB) scanRepo(row *sql.Row) (Repo, error) {
	var repo Repo
	if err := row.Scan(&repo.ID, &repo.Name, &repo.URL, &repo.Remote, &repo.BaseBranch, &repo.BarePath, &repo.DeletedAt, &repo.CreatedAt, &repo.UpdatedAt); err != nil {
		return Repo{}, fmt.Errorf("repo: %w", err)
	}
	return repo, nil
}

func (db *DB) scanRelease(row *sql.Row) (Release, error) {
	var release Release
	if err := row.Scan(&release.ID, &release.Key, &release.Title, &release.Slug, &release.Status, &release.WorkspacePath, &release.BranchName, &release.TargetBranch, &release.CreatedAt, &release.UpdatedAt, &release.IntegratedAt, &release.PublishedAt); err != nil {
		return Release{}, fmt.Errorf("release: %w", err)
	}
	return release, nil
}

func scanReleaseRows(rows *sql.Rows) (Release, error) {
	var release Release
	if err := rows.Scan(&release.ID, &release.Key, &release.Title, &release.Slug, &release.Status, &release.WorkspacePath, &release.BranchName, &release.TargetBranch, &release.CreatedAt, &release.UpdatedAt, &release.IntegratedAt, &release.PublishedAt); err != nil {
		return Release{}, fmt.Errorf("scan release: %w", err)
	}
	return release, nil
}

func (db *DB) scanRequirement(row *sql.Row) (Requirement, error) {
	var req Requirement
	if err := row.Scan(&req.ID, &req.Key, &req.Title, &req.Slug, &req.Status, &req.WorkspacePath, &req.FeatureBranch, &req.CreatedAt, &req.UpdatedAt, &req.CompletedAt, &req.ArchivedAt, &req.ReadyAt); err != nil {
		return Requirement{}, fmt.Errorf("requirement: %w", err)
	}
	return req, nil
}

func (db *DB) getRequirementByID(ctx context.Context, id int64) (Requirement, error) {
	return db.scanRequirement(db.sql.QueryRowContext(ctx, `SELECT id, req_key, title, slug, status, workspace_path, feature_branch, created_at, updated_at, completed_at, archived_at, ready_at FROM requirements WHERE id = ?`, id))
}

func (db *DB) getReleaseByID(ctx context.Context, id int64) (Release, error) {
	return db.scanRelease(db.sql.QueryRowContext(ctx, `SELECT id, release_key, title, slug, status, workspace_path, branch_name, target_branch, created_at, updated_at, integrated_at, published_at FROM releases WHERE id = ?`, id))
}

func (db *DB) getReleaseRequirementByID(ctx context.Context, id int64) (ReleaseRequirement, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT rr.id, rr.release_id, rr.requirement_id, rr.position, rr.removed_at, rr.created_at, rr.updated_at,
		req.id, req.req_key, req.title, req.slug, req.status, req.workspace_path, req.feature_branch, req.created_at, req.updated_at, req.completed_at, req.archived_at, req.ready_at
		FROM release_requirements rr JOIN requirements req ON req.id = rr.requirement_id WHERE rr.id = ?`, id)
	if err != nil {
		return ReleaseRequirement{}, fmt.Errorf("get release requirement: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return ReleaseRequirement{}, sql.ErrNoRows
	}
	rel, err := scanReleaseRequirementRows(rows)
	if err != nil {
		return ReleaseRequirement{}, err
	}
	return rel, rows.Err()
}

func scanReleaseRequirementRows(rows *sql.Rows) (ReleaseRequirement, error) {
	var rel ReleaseRequirement
	if err := rows.Scan(&rel.ID, &rel.ReleaseID, &rel.RequirementID, &rel.Position, &rel.RemovedAt, &rel.CreatedAt, &rel.UpdatedAt,
		&rel.Requirement.ID, &rel.Requirement.Key, &rel.Requirement.Title, &rel.Requirement.Slug, &rel.Requirement.Status, &rel.Requirement.WorkspacePath, &rel.Requirement.FeatureBranch, &rel.Requirement.CreatedAt, &rel.Requirement.UpdatedAt, &rel.Requirement.CompletedAt, &rel.Requirement.ArchivedAt, &rel.Requirement.ReadyAt); err != nil {
		return ReleaseRequirement{}, fmt.Errorf("scan release requirement: %w", err)
	}
	return rel, nil
}

func (db *DB) getRequirementRepoByID(ctx context.Context, id int64) (RequirementRepo, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT rr.id, rr.requirement_id, rr.repo_id, rr.repo_name, rr.repo_url, rr.repo_remote, rr.repo_base_branch, rr.worktree_path, rr.status, rr.created_at, rr.updated_at,
		r.id, r.name, r.url, r.remote, r.base_branch, r.bare_path, r.deleted_at, r.created_at, r.updated_at
		FROM requirement_repos rr JOIN repos r ON r.id = rr.repo_id WHERE rr.id = ?`, id)
	if err != nil {
		return RequirementRepo{}, fmt.Errorf("get requirement repo: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return RequirementRepo{}, sql.ErrNoRows
	}
	var rel RequirementRepo
	if err := rows.Scan(&rel.ID, &rel.RequirementID, &rel.RepoID, &rel.RepoName, &rel.RepoURL, &rel.RepoRemote, &rel.RepoBaseBranch, &rel.WorktreePath, &rel.Status, &rel.CreatedAt, &rel.UpdatedAt,
		&rel.Repo.ID, &rel.Repo.Name, &rel.Repo.URL, &rel.Repo.Remote, &rel.Repo.BaseBranch, &rel.Repo.BarePath, &rel.Repo.DeletedAt, &rel.Repo.CreatedAt, &rel.Repo.UpdatedAt); err != nil {
		return RequirementRepo{}, fmt.Errorf("scan requirement repo: %w", err)
	}
	return rel, rows.Err()
}
