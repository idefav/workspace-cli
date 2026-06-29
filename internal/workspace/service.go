package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"workspace-cli/internal/config"
	gitx "workspace-cli/internal/git"
	"workspace-cli/internal/store"
)

type Service struct {
	cfg config.Config
	db  *store.DB
	git *gitx.Manager
}

type AddRepoParams struct {
	Name       string
	URL        string
	Remote     string
	BaseBranch string
}

type UpdateRepoParams struct {
	Name       string
	URL        string
	Remote     string
	BaseBranch string
}

type CreateRequirementParams struct {
	Title     string
	Key       string
	RepoNames []string
}

type createdWorktree struct {
	repo store.Repo
	path string
}

func NewService(cfg config.Config, db *store.DB, gitManager *gitx.Manager) *Service {
	return &Service{cfg: cfg, db: db, git: gitManager}
}

func (s *Service) AddRepo(ctx context.Context, params AddRepoParams) (store.Repo, error) {
	if params.Remote == "" {
		params.Remote = "origin"
	}
	barePath := filepath.Join(s.cfg.ReposDir, params.Name+".git")
	if err := s.git.CloneBare(params.URL, barePath); err != nil {
		return store.Repo{}, err
	}
	if err := s.git.Fetch(barePath, params.Remote); err != nil {
		return store.Repo{}, err
	}
	if params.BaseBranch == "" {
		detected, err := s.git.DefaultBranch(barePath, params.Remote)
		if err != nil {
			detected = "main"
		}
		params.BaseBranch = detected
	}
	return s.db.CreateRepo(ctx, store.CreateRepoParams{
		Name:       params.Name,
		URL:        params.URL,
		Remote:     params.Remote,
		BaseBranch: params.BaseBranch,
		BarePath:   barePath,
	})
}

func (s *Service) SyncRepo(ctx context.Context, name string) error {
	if name != "" {
		repo, err := s.db.GetRepo(ctx, name)
		if err != nil {
			return err
		}
		return s.git.Fetch(repo.BarePath, repo.Remote)
	}
	repos, err := s.db.ListRepos(ctx, false)
	if err != nil {
		return err
	}
	for _, repo := range repos {
		if err := s.git.Fetch(repo.BarePath, repo.Remote); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) UpdateRepo(ctx context.Context, params UpdateRepoParams) (store.Repo, error) {
	repo, err := s.db.GetRepo(ctx, params.Name)
	if err != nil {
		return store.Repo{}, err
	}
	blocked, err := s.db.RepoHasActiveOrCleanupRefs(ctx, repo.ID)
	if err != nil {
		return store.Repo{}, err
	}
	if blocked {
		return store.Repo{}, fmt.Errorf("repo %s is referenced by an active or cleanup-pending requirement", repo.Name)
	}
	newURL := repo.URL
	newRemote := repo.Remote
	newBase := repo.BaseBranch
	if params.URL != "" {
		newURL = params.URL
	}
	if params.Remote != "" {
		newRemote = params.Remote
	}
	if params.BaseBranch != "" {
		newBase = params.BaseBranch
	}
	if params.Remote != "" && params.Remote != repo.Remote {
		if err := s.git.RenameRemote(repo.BarePath, repo.Remote, params.Remote); err != nil {
			return store.Repo{}, err
		}
	}
	if params.URL != "" {
		if err := s.git.SetRemoteURL(repo.BarePath, newRemote, params.URL); err != nil {
			return store.Repo{}, err
		}
	}
	if err := s.db.UpdateRepo(ctx, repo.ID, newURL, newRemote, newBase); err != nil {
		return store.Repo{}, err
	}
	return s.db.GetRepoByID(ctx, repo.ID)
}

func (s *Service) RemoveRepo(ctx context.Context, name string) error {
	repo, err := s.db.GetRepo(ctx, name)
	if err != nil {
		return err
	}
	blocked, err := s.db.RepoHasActiveOrCleanupRefs(ctx, repo.ID)
	if err != nil {
		return err
	}
	if blocked {
		return fmt.Errorf("repo %s is referenced by an active or cleanup-pending requirement", repo.Name)
	}
	return s.db.SoftDeleteRepo(ctx, repo.ID)
}

func (s *Service) CreateRequirement(ctx context.Context, params CreateRequirementParams) (store.Requirement, error) {
	if params.Key == "" {
		params.Key = Slugify(params.Title)
	}
	slug := Slugify(params.Key)
	req := store.CreateRequirementParams{
		Key:           params.Key,
		Title:         params.Title,
		Slug:          slug,
		WorkspacePath: filepath.Join(s.cfg.ReqDir, slug),
		FeatureBranch: FeatureBranch(slug),
	}
	if err := os.MkdirAll(req.WorkspacePath, 0o755); err != nil {
		return store.Requirement{}, fmt.Errorf("create requirement workspace: %w", err)
	}
	created, err := s.db.CreateRequirement(ctx, req)
	if err != nil {
		return store.Requirement{}, err
	}
	var createdRels []store.RequirementRepo
	var createdWorktrees []createdWorktree
	for _, repoName := range params.RepoNames {
		repo, err := s.db.GetRepo(ctx, repoName)
		if err != nil {
			cleanupErr := s.cleanupFailedRequirement(ctx, created, createdRels, createdWorktrees)
			return store.Requirement{}, withCleanupError(fmt.Errorf("get repo %s: %w", repoName, err), cleanupErr)
		}
		if err := s.git.Fetch(repo.BarePath, repo.Remote); err != nil {
			_ = s.db.LogOperation(ctx, created.ID, repo.ID, "fetch", store.OperationStatusFailed, err.Error())
			cleanupErr := s.cleanupFailedRequirement(ctx, created, createdRels, createdWorktrees)
			return store.Requirement{}, withCleanupError(err, cleanupErr)
		}
		worktreePath := filepath.Join(created.WorkspacePath, repo.Name)
		baseRef := s.worktreeBaseRef(repo, created.FeatureBranch)
		if err := s.git.CreateWorktree(repo.BarePath, worktreePath, created.FeatureBranch, baseRef); err != nil {
			_ = s.db.LogOperation(ctx, created.ID, repo.ID, "worktree", store.OperationStatusFailed, err.Error())
			cleanupErr := s.cleanupFailedRequirement(ctx, created, createdRels, createdWorktrees)
			return store.Requirement{}, withCleanupError(err, cleanupErr)
		}
		createdWorktrees = append(createdWorktrees, createdWorktree{repo: repo, path: worktreePath})
		rel, err := s.db.AddRepoToRequirement(ctx, created.ID, repo.ID, worktreePath)
		if err != nil {
			_ = s.db.LogOperation(ctx, created.ID, repo.ID, "relation", store.OperationStatusFailed, err.Error())
			cleanupErr := s.cleanupFailedRequirement(ctx, created, createdRels, createdWorktrees)
			return store.Requirement{}, withCleanupError(err, cleanupErr)
		}
		createdRels = append(createdRels, rel)
	}
	return created, nil
}

func (s *Service) FinishRequirement(ctx context.Context, keyOrSlug, message string) error {
	req, err := s.db.GetRequirement(ctx, keyOrSlug)
	if err != nil {
		return err
	}
	if req.Status == store.RequirementStatusCompleted {
		return nil
	}
	rels, err := s.db.ListRequirementRepos(ctx, req.ID)
	if err != nil {
		return err
	}
	if hasCleanupPending(rels) {
		return s.finishCleanup(ctx, req, rels)
	}

	changed := map[int64]bool{}
	for _, rel := range rels {
		if rel.Status != store.RequirementRepoStatusActive {
			continue
		}
		hasChanges, err := s.git.HasChanges(rel.WorktreePath)
		if err != nil {
			_ = s.db.LogOperation(ctx, req.ID, rel.RepoID, "status", store.OperationStatusFailed, err.Error())
			return err
		}
		changed[rel.ID] = hasChanges
		if hasChanges {
			if err := s.git.CommitIdentity(rel.WorktreePath); err != nil {
				message := fmt.Sprintf("requirement %s repo %s missing git commit identity; run: git -C %q config user.name <name>; git -C %q config user.email <email>: %v",
					req.Key, rel.RepoName, rel.WorktreePath, rel.WorktreePath, err)
				_ = s.db.LogOperation(ctx, req.ID, rel.RepoID, "commit_identity", store.OperationStatusFailed, message)
				return fmt.Errorf("%s", message)
			}
		}
	}
	var pushedIDs []int64
	for _, rel := range rels {
		if rel.Status != store.RequirementRepoStatusActive {
			continue
		}
		if changed[rel.ID] {
			if err := s.git.CommitAll(rel.WorktreePath, message); err != nil {
				_ = s.db.LogOperation(ctx, req.ID, rel.RepoID, "commit", store.OperationStatusFailed, err.Error())
				return err
			}
		}
		if err := s.git.PushBranch(rel.WorktreePath, rel.Repo.Remote, req.FeatureBranch); err != nil {
			_ = s.db.LogOperation(ctx, req.ID, rel.RepoID, "push", store.OperationStatusFailed, err.Error())
			return err
		}
		pushedIDs = append(pushedIDs, rel.ID)
	}
	if err := s.db.UpdateRequirementRepoStatuses(ctx, pushedIDs, store.RequirementRepoStatusPushed); err != nil {
		return err
	}
	rels, err = s.db.ListRequirementRepos(ctx, req.ID)
	if err != nil {
		return err
	}
	return s.finishCleanup(ctx, req, rels)
}

func (s *Service) finishCleanup(ctx context.Context, req store.Requirement, rels []store.RequirementRepo) error {
	for _, rel := range rels {
		if rel.Status == store.RequirementRepoStatusActive {
			return fmt.Errorf("requirement %s is cleanup-pending but repo %s is still active", req.Key, rel.RepoName)
		}
	}
	for _, rel := range rels {
		switch rel.Status {
		case store.RequirementRepoStatusCompleted:
			continue
		case store.RequirementRepoStatusPushed, store.RequirementRepoStatusCleanupFailed:
			if _, err := os.Stat(rel.WorktreePath); os.IsNotExist(err) {
				if err := s.db.UpdateRequirementRepoStatus(ctx, rel.ID, store.RequirementRepoStatusCompleted); err != nil {
					return err
				}
				continue
			} else if err != nil {
				_ = s.db.UpdateRequirementRepoStatus(ctx, rel.ID, store.RequirementRepoStatusCleanupFailed)
				_ = s.db.LogOperation(ctx, req.ID, rel.RepoID, "cleanup_status", store.OperationStatusFailed, err.Error())
				return fmt.Errorf("stat cleanup worktree %s: %w", rel.WorktreePath, err)
			}
			hasChanges, err := s.git.HasChanges(rel.WorktreePath)
			if os.IsNotExist(err) {
				if err := s.db.UpdateRequirementRepoStatus(ctx, rel.ID, store.RequirementRepoStatusCompleted); err != nil {
					return err
				}
				continue
			}
			if err != nil {
				_ = s.db.UpdateRequirementRepoStatus(ctx, rel.ID, store.RequirementRepoStatusCleanupFailed)
				_ = s.db.LogOperation(ctx, req.ID, rel.RepoID, "cleanup_status", store.OperationStatusFailed, err.Error())
				return fmt.Errorf("check cleanup worktree %s: %w", rel.WorktreePath, err)
			}
			if hasChanges {
				_ = s.db.LogOperation(ctx, req.ID, rel.RepoID, "cleanup", store.OperationStatusFailed, "worktree has uncommitted changes")
				return fmt.Errorf("worktree has uncommitted changes during cleanup: %s", rel.WorktreePath)
			}
			if err := s.git.RemoveWorktree(rel.Repo.BarePath, rel.WorktreePath); err != nil {
				_ = s.db.UpdateRequirementRepoStatus(ctx, rel.ID, store.RequirementRepoStatusCleanupFailed)
				_ = s.db.LogOperation(ctx, req.ID, rel.RepoID, "cleanup", store.OperationStatusFailed, err.Error())
				return err
			}
			if err := s.db.UpdateRequirementRepoStatus(ctx, rel.ID, store.RequirementRepoStatusCompleted); err != nil {
				return err
			}
		default:
			return fmt.Errorf("requirement %s repo %s has unsupported relation status %s", req.Key, rel.RepoName, rel.Status)
		}
	}
	rels, err := s.db.ListRequirementRepos(ctx, req.ID)
	if err != nil {
		return err
	}
	for _, rel := range rels {
		if rel.Status != store.RequirementRepoStatusCompleted {
			return fmt.Errorf("requirement %s cannot complete until repo %s cleanup is completed", req.Key, rel.RepoName)
		}
	}
	return s.db.MarkRequirementCompleted(ctx, req.ID)
}

func hasCleanupPending(rels []store.RequirementRepo) bool {
	for _, rel := range rels {
		if rel.Status == store.RequirementRepoStatusPushed || rel.Status == store.RequirementRepoStatusCleanupFailed {
			return true
		}
	}
	return false
}

func (s *Service) UpdateRequirement(ctx context.Context, keyOrSlug, title string) error {
	req, err := s.db.GetRequirement(ctx, keyOrSlug)
	if err != nil {
		return err
	}
	if err := s.ensureOrdinaryActive(ctx, req); err != nil {
		return err
	}
	return s.db.UpdateRequirementTitle(ctx, req.ID, title)
}

func (s *Service) AddRepoToRequirement(ctx context.Context, keyOrSlug, repoName string) (store.RequirementRepo, error) {
	req, err := s.db.GetRequirement(ctx, keyOrSlug)
	if err != nil {
		return store.RequirementRepo{}, err
	}
	if err := s.ensureOrdinaryActive(ctx, req); err != nil {
		return store.RequirementRepo{}, err
	}
	repo, err := s.db.GetRepo(ctx, repoName)
	if err != nil {
		return store.RequirementRepo{}, err
	}
	if err := s.git.Fetch(repo.BarePath, repo.Remote); err != nil {
		_ = s.db.LogOperation(ctx, req.ID, repo.ID, "fetch", store.OperationStatusFailed, err.Error())
		return store.RequirementRepo{}, err
	}
	worktreePath := filepath.Join(req.WorkspacePath, repo.Name)
	if err := s.git.CreateWorktree(repo.BarePath, worktreePath, req.FeatureBranch, s.worktreeBaseRef(repo, req.FeatureBranch)); err != nil {
		_ = s.db.LogOperation(ctx, req.ID, repo.ID, "worktree", store.OperationStatusFailed, err.Error())
		return store.RequirementRepo{}, err
	}
	rel, err := s.db.AddRepoToRequirement(ctx, req.ID, repo.ID, worktreePath)
	if err != nil {
		_ = s.db.LogOperation(ctx, req.ID, repo.ID, "relation", store.OperationStatusFailed, err.Error())
		if cleanupErr := s.git.RemoveWorktree(repo.BarePath, worktreePath); cleanupErr != nil {
			_ = s.db.LogOperation(ctx, req.ID, repo.ID, "cleanup", store.OperationStatusFailed, cleanupErr.Error())
			return store.RequirementRepo{}, withCleanupError(err, cleanupErr)
		}
		return store.RequirementRepo{}, err
	}
	return rel, nil
}

func (s *Service) ArchiveRequirement(ctx context.Context, keyOrSlug string) error {
	req, err := s.db.GetRequirement(ctx, keyOrSlug)
	if err != nil {
		return err
	}
	if req.Status != store.RequirementStatusCompleted {
		return fmt.Errorf("only completed requirements can be archived")
	}
	if req.ArchivedAt.Valid {
		return nil
	}
	return s.db.ArchiveRequirement(ctx, req.ID)
}

func (s *Service) ListRequirements(ctx context.Context, includeArchived bool) ([]store.Requirement, error) {
	return s.db.ListRequirements(ctx, includeArchived)
}

func (s *Service) GetRequirement(ctx context.Context, keyOrSlug string) (store.Requirement, error) {
	return s.db.GetRequirement(ctx, keyOrSlug)
}

func (s *Service) ListRequirementRepos(ctx context.Context, requirementID int64) ([]store.RequirementRepo, error) {
	return s.db.ListRequirementRepos(ctx, requirementID)
}

func (s *Service) ListRepos(ctx context.Context, includeDeleted bool) ([]store.Repo, error) {
	return s.db.ListRepos(ctx, includeDeleted)
}

func (s *Service) ensureOrdinaryActive(ctx context.Context, req store.Requirement) error {
	if req.Status != store.RequirementStatusActive || req.ArchivedAt.Valid {
		return fmt.Errorf("requirement %s is not a mutable active requirement", req.Key)
	}
	rels, err := s.db.ListRequirementRepos(ctx, req.ID)
	if err != nil {
		return err
	}
	for _, rel := range rels {
		if rel.Status == store.RequirementRepoStatusPushed || rel.Status == store.RequirementRepoStatusCleanupFailed {
			return fmt.Errorf("requirement %s is cleanup-pending and cannot be modified", req.Key)
		}
	}
	return nil
}

func (s *Service) worktreeBaseRef(repo store.Repo, featureBranch string) string {
	if s.git.RemoteBranchExists(repo.BarePath, repo.Remote, featureBranch) {
		return repo.Remote + "/" + featureBranch
	}
	return repo.Remote + "/" + repo.BaseBranch
}

func (s *Service) cleanupFailedRequirement(ctx context.Context, req store.Requirement, rels []store.RequirementRepo, worktrees []createdWorktree) error {
	var cleanupErr error
	for i := len(worktrees) - 1; i >= 0; i-- {
		wt := worktrees[i]
		if err := s.git.RemoveWorktree(wt.repo.BarePath, wt.path); err != nil {
			cleanupErr = errors.Join(cleanupErr, err)
			_ = s.db.LogOperation(ctx, req.ID, wt.repo.ID, "cleanup", store.OperationStatusFailed, err.Error())
		}
	}
	for i := len(rels) - 1; i >= 0; i-- {
		if err := s.db.DeleteRequirementRepo(ctx, rels[i].ID); err != nil {
			cleanupErr = errors.Join(cleanupErr, err)
		}
	}
	if err := s.db.DeleteRequirement(ctx, req.ID); err != nil {
		cleanupErr = errors.Join(cleanupErr, err)
	}
	_ = os.Remove(req.WorkspacePath)
	return cleanupErr
}

func withCleanupError(err, cleanupErr error) error {
	if cleanupErr == nil {
		return err
	}
	return fmt.Errorf("%w; cleanup failed: %v", err, cleanupErr)
}
