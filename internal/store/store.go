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
		if _, err := db.sql.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	return nil
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

func (db *DB) GetRequirement(ctx context.Context, keyOrSlug string) (Requirement, error) {
	return db.scanRequirement(db.sql.QueryRowContext(ctx, `SELECT id, req_key, title, slug, status, workspace_path, feature_branch, created_at, updated_at, completed_at, archived_at FROM requirements WHERE req_key = ? OR slug = ?`, keyOrSlug, keyOrSlug))
}

func (db *DB) ListRequirements(ctx context.Context, includeArchived bool) ([]Requirement, error) {
	query := `SELECT id, req_key, title, slug, status, workspace_path, feature_branch, created_at, updated_at, completed_at, archived_at FROM requirements`
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
		if err := rows.Scan(&req.ID, &req.Key, &req.Title, &req.Slug, &req.Status, &req.WorkspacePath, &req.FeatureBranch, &req.CreatedAt, &req.UpdatedAt, &req.CompletedAt, &req.ArchivedAt); err != nil {
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

func (db *DB) scanRequirement(row *sql.Row) (Requirement, error) {
	var req Requirement
	if err := row.Scan(&req.ID, &req.Key, &req.Title, &req.Slug, &req.Status, &req.WorkspacePath, &req.FeatureBranch, &req.CreatedAt, &req.UpdatedAt, &req.CompletedAt, &req.ArchivedAt); err != nil {
		return Requirement{}, fmt.Errorf("requirement: %w", err)
	}
	return req, nil
}

func (db *DB) getRequirementByID(ctx context.Context, id int64) (Requirement, error) {
	return db.scanRequirement(db.sql.QueryRowContext(ctx, `SELECT id, req_key, title, slug, status, workspace_path, feature_branch, created_at, updated_at, completed_at, archived_at FROM requirements WHERE id = ?`, id))
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
