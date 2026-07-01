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

type CreateReleaseParams struct {
	Title           string
	Key             string
	RequirementKeys []string
}

type createdWorktree struct {
	repo store.Repo
	path string
}

type releaseRequirementRepoPlan struct {
	membership store.ReleaseRequirement
	rel        store.RequirementRepo
}

type releaseRepoPlan struct {
	repo store.Repo
	rels []releaseRequirementRepoPlan
}

type ReleaseDiagnostics struct {
	PublishInProgress bool
	StaleReasons      []string
	ManualReasons     []string
}

type releaseStateChangedError struct {
	reason string
}

func (err releaseStateChangedError) Error() string {
	return err.reason
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
	releaseBlocked, err := s.db.RepoHasUnpublishedReleaseRefs(ctx, repo.ID)
	if err != nil {
		return store.Repo{}, err
	}
	if releaseBlocked {
		return store.Repo{}, fmt.Errorf("repo %s is referenced by an unpublished release", repo.Name)
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
	releaseBlocked, err := s.db.RepoHasUnpublishedReleaseRefs(ctx, repo.ID)
	if err != nil {
		return err
	}
	if releaseBlocked {
		return fmt.Errorf("repo %s is referenced by an unpublished release", repo.Name)
	}
	return s.db.SoftDeleteRepo(ctx, repo.ID)
}

func (s *Service) CreateRequirement(ctx context.Context, params CreateRequirementParams) (store.Requirement, error) {
	if params.Key == "" {
		params.Key = Slugify(params.Title)
	}
	if len(params.RepoNames) == 0 {
		return store.Requirement{}, fmt.Errorf("at least one repo is required")
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

func (s *Service) CreateRelease(ctx context.Context, params CreateReleaseParams) (store.Release, error) {
	if params.Key == "" {
		params.Key = Slugify(params.Title)
	}
	if len(params.RequirementKeys) == 0 {
		return store.Release{}, fmt.Errorf("at least one requirement is required")
	}
	slug := Slugify(params.Key)
	var requirements []store.Requirement
	seenRequirements := map[int64]bool{}
	for _, key := range params.RequirementKeys {
		req, err := s.db.GetRequirement(ctx, key)
		if err != nil {
			return store.Release{}, err
		}
		if req.Status != store.RequirementStatusActive || req.ArchivedAt.Valid || !req.ReadyAt.Valid {
			return store.Release{}, fmt.Errorf("requirement %s is not ready for release", req.Key)
		}
		if seenRequirements[req.ID] {
			return store.Release{}, fmt.Errorf("requirement %s is already in release create request", req.Key)
		}
		seenRequirements[req.ID] = true
		requirements = append(requirements, req)
	}
	releasePath := filepath.Join(s.cfg.ReleaseDir, slug)
	if err := os.MkdirAll(releasePath, 0o755); err != nil {
		return store.Release{}, fmt.Errorf("create release workspace: %w", err)
	}
	release, err := s.db.CreateRelease(ctx, store.CreateReleaseParams{
		Key:           params.Key,
		Title:         params.Title,
		Slug:          slug,
		WorkspacePath: releasePath,
		BranchName:    "release/" + slug,
		TargetBranch:  "per-repo",
	})
	if err != nil {
		cleanupErr := os.Remove(releasePath)
		if os.IsNotExist(cleanupErr) {
			cleanupErr = nil
		}
		return store.Release{}, withCleanupError(err, cleanupErr)
	}
	for i, req := range requirements {
		if _, err := s.db.AddReleaseRequirement(ctx, release.ID, req.ID, i+1); err != nil {
			cleanupErr := s.cleanupFailedRelease(ctx, release)
			return store.Release{}, withCleanupError(err, cleanupErr)
		}
	}
	return release, nil
}

func (s *Service) AddRequirementToRelease(ctx context.Context, releaseKeyOrSlug, requirementKeyOrSlug string) (store.ReleaseRequirement, error) {
	release, err := s.db.GetRelease(ctx, releaseKeyOrSlug)
	if err != nil {
		return store.ReleaseRequirement{}, err
	}
	if release.Status == store.ReleaseStatusPublished {
		return store.ReleaseRequirement{}, fmt.Errorf("release %s is already published", release.Key)
	}
	if inProgress, err := s.releasePublishInProgress(ctx, release); err != nil {
		return store.ReleaseRequirement{}, err
	} else if inProgress {
		return store.ReleaseRequirement{}, fmt.Errorf("release %s is publish-in-progress and cannot be changed", release.Key)
	}
	req, err := s.db.GetRequirement(ctx, requirementKeyOrSlug)
	if err != nil {
		return store.ReleaseRequirement{}, err
	}
	if req.Status != store.RequirementStatusActive || req.ArchivedAt.Valid || !req.ReadyAt.Valid {
		return store.ReleaseRequirement{}, fmt.Errorf("requirement %s is not ready for release", req.Key)
	}
	memberships, err := s.db.ListReleaseRequirements(ctx, release.ID, true)
	if err != nil {
		return store.ReleaseRequirement{}, err
	}
	position := 1
	for _, membership := range memberships {
		if membership.Position >= position {
			position = membership.Position + 1
		}
	}
	if release.Status != store.ReleaseStatusDraft {
		return s.db.AddReleaseRequirementAndMarkStale(ctx, release.ID, req.ID, position)
	}
	added, err := s.db.AddReleaseRequirement(ctx, release.ID, req.ID, position)
	if err != nil {
		return store.ReleaseRequirement{}, err
	}
	return added, nil
}

func (s *Service) RemoveRequirementFromRelease(ctx context.Context, releaseKeyOrSlug, requirementKeyOrSlug string) error {
	release, err := s.db.GetRelease(ctx, releaseKeyOrSlug)
	if err != nil {
		return err
	}
	if release.Status == store.ReleaseStatusPublished {
		return fmt.Errorf("release %s is already published", release.Key)
	}
	if inProgress, err := s.releasePublishInProgress(ctx, release); err != nil {
		return err
	} else if inProgress {
		return fmt.Errorf("release %s is publish-in-progress and cannot be changed", release.Key)
	}
	req, err := s.db.GetRequirement(ctx, requirementKeyOrSlug)
	if err != nil {
		return err
	}
	if release.Status == store.ReleaseStatusDraft {
		return s.db.RemoveReleaseRequirement(ctx, release.ID, req.ID)
	}
	return s.db.RemoveReleaseRequirementAndMarkStale(ctx, release.ID, req.ID)
}

func (s *Service) IntegrateRelease(ctx context.Context, keyOrSlug string, force bool) error {
	release, err := s.db.GetRelease(ctx, keyOrSlug)
	if err != nil {
		return err
	}
	if release.Status == store.ReleaseStatusPublished {
		return fmt.Errorf("release %s is already published", release.Key)
	}
	if inProgress, err := s.releasePublishInProgress(ctx, release); err != nil {
		return err
	} else if inProgress {
		return fmt.Errorf("release %s is publish-in-progress and cannot be integrated", release.Key)
	}
	memberships, err := s.db.ListReleaseRequirements(ctx, release.ID, false)
	if err != nil {
		return err
	}
	if len(memberships) == 0 {
		return fmt.Errorf("release %s has no active requirements", release.Key)
	}
	plans, err := s.releaseRepoPlans(ctx, memberships)
	if err != nil {
		return err
	}
	activeRepoIDs := map[int64]bool{}
	for _, plan := range plans {
		activeRepoIDs[plan.repo.ID] = true
	}
	existingReleaseRepos, err := s.db.ListReleaseRepos(ctx, release.ID)
	if err != nil {
		return err
	}
	if err := s.cleanupObsoleteReleaseRepos(ctx, release, existingReleaseRepos, activeRepoIDs, force); err != nil {
		return err
	}
	var releaseRepos []store.CreateReleaseRepoParams
	var releaseRequirementRepos []store.CreateReleaseRequirementRepoParams
	for _, plan := range plans {
		repo := plan.repo
		if err := s.git.Fetch(repo.BarePath, repo.Remote); err != nil {
			return s.failReleaseOperation(ctx, release.ID, repo.ID, "release_integrate_fetch", err)
		}
		expectedReleaseSHA := ""
		if s.git.RemoteBranchExists(repo.BarePath, repo.Remote, release.BranchName) {
			var err error
			expectedReleaseSHA, err = s.git.RevParseBare(repo.BarePath, repo.Remote+"/"+release.BranchName)
			if err != nil {
				return s.failReleaseOperation(ctx, release.ID, repo.ID, "release_integrate_expected_sha", err)
			}
		}
		worktreePath := filepath.Join(release.WorkspacePath, repo.Name)
		if _, err := os.Stat(worktreePath); err == nil {
			hasChanges, err := s.git.HasChanges(worktreePath)
			if err != nil {
				return s.failReleaseOperation(ctx, release.ID, repo.ID, "release_integrate_status", err)
			}
			if hasChanges && !force {
				return fmt.Errorf("release worktree has uncommitted changes: %s", worktreePath)
			}
			removeWorktree := s.git.RemoveWorktree
			if force {
				removeWorktree = s.git.RemoveWorktreeForce
			}
			if err := removeWorktree(repo.BarePath, worktreePath); err != nil {
				return s.failReleaseOperation(ctx, release.ID, repo.ID, "release_integrate_remove_worktree", err)
			}
		} else if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("check release worktree %s: %w", worktreePath, err)
		}
		if err := s.git.DeleteLocalBranch(repo.BarePath, release.BranchName); err != nil {
			return s.failReleaseOperation(ctx, release.ID, repo.ID, "release_integrate_delete_branch", err)
		}
		baseRef := repo.Remote + "/" + repo.BaseBranch
		baseSHA, err := s.git.RevParseBare(repo.BarePath, baseRef)
		if err != nil {
			return s.failReleaseOperation(ctx, release.ID, repo.ID, "release_integrate_base_sha", err)
		}
		if err := s.git.CreateWorktree(repo.BarePath, worktreePath, release.BranchName, baseRef); err != nil {
			return s.failReleaseOperation(ctx, release.ID, repo.ID, "release_integrate_worktree", err)
		}
		for _, reqPlan := range plan.rels {
			featureRef := repo.Remote + "/" + reqPlan.membership.Requirement.FeatureBranch
			if err := s.git.Merge(worktreePath, featureRef); err != nil {
				return s.failReleaseOperation(ctx, release.ID, repo.ID, "release_integrate_merge", err)
			}
			featureSHA, err := s.git.RevParseBare(repo.BarePath, featureRef)
			if err != nil {
				return s.failReleaseOperation(ctx, release.ID, repo.ID, "release_integrate_feature_sha", err)
			}
			releaseRequirementRepos = append(releaseRequirementRepos, store.CreateReleaseRequirementRepoParams{
				ReleaseRequirementID: reqPlan.membership.ID,
				ReleaseID:            release.ID,
				RequirementID:        reqPlan.membership.RequirementID,
				RepoID:               repo.ID,
				FeatureBranch:        reqPlan.membership.Requirement.FeatureBranch,
				FeatureSHA:           featureSHA,
			})
		}
		releaseSHA, err := s.git.RevParse(worktreePath, "HEAD")
		if err != nil {
			return s.failReleaseOperation(ctx, release.ID, repo.ID, "release_integrate_release_sha", err)
		}
		if err := s.git.ForcePushBranch(worktreePath, repo.Remote, release.BranchName, expectedReleaseSHA); err != nil {
			return s.failReleaseOperation(ctx, release.ID, repo.ID, "release_integrate_force_push", err)
		}
		releaseRepos = append(releaseRepos, store.CreateReleaseRepoParams{
			ReleaseID:           release.ID,
			RepoID:              repo.ID,
			ReleaseBranch:       release.BranchName,
			WorktreePath:        worktreePath,
			PublishWorktreePath: filepath.Join(release.WorkspacePath, ".publish", repo.Name),
			TargetBranch:        repo.BaseBranch,
			IntegratedBaseSHA:   baseSHA,
			ReleaseSHA:          releaseSHA,
			Status:              store.ReleaseRepoStatusIntegrated,
		})
	}
	if err := s.db.ReplaceReleaseSnapshots(ctx, release.ID, releaseRepos, releaseRequirementRepos); err != nil {
		return s.failReleaseOperation(ctx, release.ID, 0, "release_integrate_snapshot", err)
	}
	return s.db.UpdateReleaseStatus(ctx, release.ID, store.ReleaseStatusIntegrated)
}

func (s *Service) PublishRelease(ctx context.Context, keyOrSlug string, tested bool, message string) error {
	if !tested {
		return fmt.Errorf("release publish requires --tested")
	}
	release, err := s.db.GetRelease(ctx, keyOrSlug)
	if err != nil {
		return err
	}
	inProgress, err := s.releasePublishInProgress(ctx, release)
	if err != nil {
		return err
	}
	if release.Status != store.ReleaseStatusIntegrated && !inProgress {
		return fmt.Errorf("release %s is not integrated", release.Key)
	}
	if message == "" {
		message = "release: " + release.Key
	}
	releaseRepos, err := s.db.ListReleaseRepos(ctx, release.ID)
	if err != nil {
		return err
	}
	if len(releaseRepos) == 0 {
		return fmt.Errorf("release %s has no integrated repos", release.Key)
	}
	if err := s.ensureReleasePublishCurrent(ctx, release, releaseRepos, inProgress); err != nil {
		return err
	}
	publishWorktreeExistsByID := map[int64]bool{}
	for _, releaseRepo := range releaseRepos {
		repo := releaseRepo.Repo
		if err := s.git.Fetch(repo.BarePath, repo.Remote); err != nil {
			return s.failReleasePublishOperation(ctx, release.ID, releaseRepo, "release_publish_fetch", err)
		}
		if hasChanges, err := s.git.HasChanges(releaseRepo.WorktreePath); err != nil {
			return s.failReleasePublishOperation(ctx, release.ID, releaseRepo, "release_publish_release_status", err)
		} else if hasChanges {
			return fmt.Errorf("release worktree has uncommitted changes: %s", releaseRepo.WorktreePath)
		}
		publishWorktreeExists := false
		if _, err := os.Stat(releaseRepo.PublishWorktreePath); err == nil {
			if !s.git.IsWorktree(releaseRepo.PublishWorktreePath) {
				return fmt.Errorf("publish path exists but is not a git worktree; move or remove it before retrying: %s", releaseRepo.PublishWorktreePath)
			}
			hasChanges, err := s.git.HasChanges(releaseRepo.PublishWorktreePath)
			if err != nil {
				return s.failReleasePublishOperation(ctx, release.ID, releaseRepo, "release_publish_worktree_status", err)
			}
			if hasChanges {
				return fmt.Errorf("publish worktree has uncommitted changes: %s", releaseRepo.PublishWorktreePath)
			}
			publishWorktreeExists = true
		} else if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("check publish worktree %s: %w", releaseRepo.PublishWorktreePath, err)
		}
		publishWorktreeExistsByID[releaseRepo.ID] = publishWorktreeExists
	}
	for i := range releaseRepos {
		releaseRepo := &releaseRepos[i]
		repo := releaseRepo.Repo
		remoteBase := repo.Remote + "/" + releaseRepo.TargetBranch
		if releaseRepo.Status == store.ReleaseRepoStatusPublished {
			currentBaseSHA, err := s.git.RevParseBare(repo.BarePath, remoteBase)
			if err != nil {
				return s.failReleasePublishOperation(ctx, release.ID, *releaseRepo, "release_publish_base_sha", err)
			}
			if !releaseRepo.PublishedSHA.Valid || currentBaseSHA != releaseRepo.PublishedSHA.String {
				return fmt.Errorf("published repo %s target branch changed after publish; manual handling required", repo.Name)
			}
			continue
		}
		hasNewCommits, err := s.git.HasNewCommitsSince(repo.BarePath, repo.Remote, releaseRepo.TargetBranch, releaseRepo.IntegratedBaseSHA)
		if err != nil {
			return s.failReleasePublishOperation(ctx, release.ID, *releaseRepo, "release_publish_base_new_commits", err)
		}
		if hasNewCommits {
			currentBaseSHA, err := s.git.RevParseBare(repo.BarePath, remoteBase)
			if err != nil {
				return s.failReleasePublishOperation(ctx, release.ID, *releaseRepo, "release_publish_base_sha", err)
			}
			alreadyMerged, err := s.git.CommitHasParentBare(repo.BarePath, currentBaseSHA, releaseRepo.ReleaseSHA)
			if err != nil {
				return s.failReleasePublishOperation(ctx, release.ID, *releaseRepo, "release_publish_self_heal_parent", err)
			}
			if alreadyMerged {
				if err := s.db.MarkReleaseRepoPublished(ctx, releaseRepo.ID, currentBaseSHA); err != nil {
					return err
				}
				releaseRepo.Status = store.ReleaseRepoStatusPublished
				releaseRepo.PublishedSHA.Valid = true
				releaseRepo.PublishedSHA.String = currentBaseSHA
				inProgress = true
				continue
			}
			if inProgress {
				return fmt.Errorf("target branch %s has new commits while release %s is publish-in-progress; manual handling required", remoteBase, release.Key)
			}
			_ = s.db.UpdateReleaseStatus(ctx, release.ID, store.ReleaseStatusStale)
			return fmt.Errorf("target branch %s has new commits; reintegrate release %s", remoteBase, release.Key)
		}
		if reason := s.releaseBranchChangeReason(*releaseRepo); reason != "" {
			return s.releaseStaleOrManual(ctx, release, inProgress, reason)
		}
	}
	for _, releaseRepo := range releaseRepos {
		repo := releaseRepo.Repo
		publishWorktreeExists := publishWorktreeExistsByID[releaseRepo.ID]
		if releaseRepo.Status == store.ReleaseRepoStatusPublished {
			continue
		}
		remoteBase := repo.Remote + "/" + releaseRepo.TargetBranch
		if publishWorktreeExists {
			if err := s.git.ResetHard(releaseRepo.PublishWorktreePath, remoteBase); err != nil {
				return s.failReleasePublishOperation(ctx, release.ID, releaseRepo, "release_publish_reset_worktree", err)
			}
		} else {
			if err := s.git.CreateDetachedWorktree(repo.BarePath, releaseRepo.PublishWorktreePath, remoteBase); err != nil {
				return s.failReleasePublishOperation(ctx, release.ID, releaseRepo, "release_publish_worktree", err)
			}
		}
		releaseRef := repo.Remote + "/" + release.BranchName
		if err := s.git.MergeNoFF(releaseRepo.PublishWorktreePath, releaseRef, message); err != nil {
			return s.failReleasePublishOperation(ctx, release.ID, releaseRepo, "release_publish_merge", err)
		}
		if err := s.git.PushBranch(releaseRepo.PublishWorktreePath, repo.Remote, releaseRepo.TargetBranch); err != nil {
			return s.failReleasePublishOperation(ctx, release.ID, releaseRepo, "release_publish_push", err)
		}
		if err := s.git.Fetch(repo.BarePath, repo.Remote); err != nil {
			return s.failReleasePublishOperation(ctx, release.ID, releaseRepo, "release_publish_fetch_after_push", err)
		}
		publishedSHA, err := s.git.RevParseBare(repo.BarePath, remoteBase)
		if err != nil {
			return s.failReleasePublishOperation(ctx, release.ID, releaseRepo, "release_publish_published_sha", err)
		}
		if err := s.db.MarkReleaseRepoPublished(ctx, releaseRepo.ID, publishedSHA); err != nil {
			return err
		}
	}
	memberships, err := s.db.ListReleaseRequirements(ctx, release.ID, false)
	if err != nil {
		return err
	}
	var requirementIDs []int64
	for _, membership := range memberships {
		requirementIDs = append(requirementIDs, membership.RequirementID)
	}
	return s.db.FinalizeReleasePublished(ctx, release.ID, requirementIDs)
}

func (s *Service) RefreshReleaseStatus(ctx context.Context, keyOrSlug string) (store.Release, error) {
	release, err := s.db.GetRelease(ctx, keyOrSlug)
	if err != nil {
		return store.Release{}, err
	}
	if release.Status == store.ReleaseStatusPublished || release.Status == store.ReleaseStatusDraft {
		return release, nil
	}
	inProgress, err := s.releasePublishInProgress(ctx, release)
	if err != nil {
		return store.Release{}, err
	}
	releaseRepos, err := s.db.ListReleaseRepos(ctx, release.ID)
	if err != nil {
		return store.Release{}, err
	}
	if len(releaseRepos) == 0 {
		return release, nil
	}
	if err := s.ensureReleasePublishCurrent(ctx, release, releaseRepos, inProgress); err != nil {
		if inProgress {
			return release, nil
		}
		refreshed, getErr := s.db.GetRelease(ctx, keyOrSlug)
		if getErr != nil {
			return store.Release{}, getErr
		}
		return refreshed, nil
	}
	for _, releaseRepo := range releaseRepos {
		if releaseRepo.Status == store.ReleaseRepoStatusPublished {
			continue
		}
		if err := s.git.Fetch(releaseRepo.Repo.BarePath, releaseRepo.Repo.Remote); err != nil {
			return store.Release{}, err
		}
		hasNewCommits, err := s.git.HasNewCommitsSince(releaseRepo.Repo.BarePath, releaseRepo.Repo.Remote, releaseRepo.TargetBranch, releaseRepo.IntegratedBaseSHA)
		if err != nil {
			return store.Release{}, err
		}
		if hasNewCommits {
			if inProgress {
				return release, nil
			}
			if err := s.db.UpdateReleaseStatus(ctx, release.ID, store.ReleaseStatusStale); err != nil {
				return store.Release{}, err
			}
			return s.db.GetRelease(ctx, keyOrSlug)
		}
		if reason := s.releaseBranchChangeReason(releaseRepo); reason != "" {
			if inProgress {
				return release, nil
			}
			if err := s.db.UpdateReleaseStatus(ctx, release.ID, store.ReleaseStatusStale); err != nil {
				return store.Release{}, err
			}
			return s.db.GetRelease(ctx, keyOrSlug)
		}
	}
	return s.db.GetRelease(ctx, keyOrSlug)
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
	return s.db.MarkRequirementReady(ctx, req.ID)
}

func (s *Service) ReopenRequirement(ctx context.Context, keyOrSlug string) error {
	req, err := s.db.GetRequirement(ctx, keyOrSlug)
	if err != nil {
		return err
	}
	if req.Status != store.RequirementStatusActive || req.ArchivedAt.Valid || !req.ReadyAt.Valid {
		return fmt.Errorf("requirement %s is not a ready requirement", req.Key)
	}
	if inProgress, err := s.requirementInPublishInProgressRelease(ctx, req.ID); err != nil {
		return err
	} else if inProgress {
		return fmt.Errorf("requirement %s is in a publish-in-progress release and cannot be reopened", req.Key)
	}
	rels, err := s.db.ListRequirementRepos(ctx, req.ID)
	if err != nil {
		return err
	}
	if hasCleanupPending(rels) {
		return fmt.Errorf("requirement %s is cleanup-pending and cannot be reopened", req.Key)
	}
	for _, rel := range rels {
		if rel.Status != store.RequirementRepoStatusCompleted {
			return fmt.Errorf("requirement %s repo %s is not ready for reopen", req.Key, rel.RepoName)
		}
		if rel.Repo.DeletedAt.Valid {
			return fmt.Errorf("repo %s has been removed and cannot be reopened", rel.RepoName)
		}
		if _, err := os.Stat(rel.Repo.BarePath); err != nil {
			return fmt.Errorf("repo %s bare path is unavailable: %w", rel.RepoName, err)
		}
		if _, err := os.Stat(rel.WorktreePath); err == nil {
			return fmt.Errorf("worktree path already exists: %s", rel.WorktreePath)
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("check worktree path %s: %w", rel.WorktreePath, err)
		}
		if s.git.BranchInUse(rel.Repo.BarePath, req.FeatureBranch) {
			return fmt.Errorf("branch %s is already used by another worktree", req.FeatureBranch)
		}
		if !s.git.LocalBranchExists(rel.Repo.BarePath, req.FeatureBranch) && !s.git.RemoteBranchExists(rel.Repo.BarePath, rel.Repo.Remote, req.FeatureBranch) {
			return fmt.Errorf("feature branch %s not found for repo %s", req.FeatureBranch, rel.RepoName)
		}
	}

	var created []createdWorktree
	for _, rel := range rels {
		baseRef := req.FeatureBranch
		if !s.git.LocalBranchExists(rel.Repo.BarePath, req.FeatureBranch) {
			baseRef = rel.Repo.Remote + "/" + req.FeatureBranch
		}
		if err := s.git.CreateWorktree(rel.Repo.BarePath, rel.WorktreePath, req.FeatureBranch, baseRef); err != nil {
			cleanupErr := s.cleanupCreatedWorktrees(ctx, req.ID, created)
			return withCleanupError(err, cleanupErr)
		}
		created = append(created, createdWorktree{repo: rel.Repo, path: rel.WorktreePath})
	}
	var relationIDs []int64
	for _, rel := range rels {
		relationIDs = append(relationIDs, rel.ID)
	}
	if err := s.db.ReopenRequirement(ctx, req.ID, relationIDs); err != nil {
		cleanupErr := s.cleanupCreatedWorktrees(ctx, req.ID, created)
		return withCleanupError(err, cleanupErr)
	}
	return nil
}

func hasCleanupPending(rels []store.RequirementRepo) bool {
	for _, rel := range rels {
		if rel.Status == store.RequirementRepoStatusPushed || rel.Status == store.RequirementRepoStatusCleanupFailed {
			return true
		}
	}
	return false
}

func dedupeStrings(values []string) []string {
	seen := map[string]bool{}
	var deduped []string
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		deduped = append(deduped, value)
	}
	return deduped
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

func (s *Service) RequirementStage(ctx context.Context, req store.Requirement) (string, error) {
	if req.Status == store.RequirementStatusCompleted {
		return "completed", nil
	}
	rels, err := s.db.ListRequirementRepos(ctx, req.ID)
	if err != nil {
		return "", err
	}
	if hasCleanupPending(rels) {
		return "cleanup-pending", nil
	}
	if req.ReadyAt.Valid {
		return "ready", nil
	}
	return "active", nil
}

func (s *Service) RequirementCompletion(ctx context.Context, req store.Requirement) (string, error) {
	if req.Status != store.RequirementStatusCompleted {
		return "", nil
	}
	released, err := s.db.RequirementHasPublishedReleaseAssociation(ctx, req.ID)
	if err != nil {
		return "", err
	}
	if released {
		return "released", nil
	}
	return "legacy-completed", nil
}

func (s *Service) GetRelease(ctx context.Context, keyOrSlug string) (store.Release, error) {
	return s.db.GetRelease(ctx, keyOrSlug)
}

func (s *Service) ListReleases(ctx context.Context, includePublished bool) ([]store.Release, error) {
	releases, err := s.db.ListReleases(ctx, includePublished)
	if err != nil {
		return nil, err
	}
	for i, release := range releases {
		refreshed, err := s.RefreshReleaseStatus(ctx, release.Key)
		if err != nil {
			return nil, err
		}
		releases[i] = refreshed
	}
	return releases, nil
}

func (s *Service) ListReleaseRequirements(ctx context.Context, releaseID int64, includeRemoved bool) ([]store.ReleaseRequirement, error) {
	return s.db.ListReleaseRequirements(ctx, releaseID, includeRemoved)
}

func (s *Service) ListReleaseRepos(ctx context.Context, releaseID int64) ([]store.ReleaseRepo, error) {
	return s.db.ListReleaseRepos(ctx, releaseID)
}

func (s *Service) ListReleaseRequirementRepos(ctx context.Context, releaseID int64) ([]store.ReleaseRequirementRepo, error) {
	return s.db.ListReleaseRequirementRepos(ctx, releaseID)
}

func (s *Service) ReleaseDiagnostics(ctx context.Context, release store.Release) (ReleaseDiagnostics, error) {
	inProgress, err := s.releasePublishInProgress(ctx, release)
	if err != nil {
		return ReleaseDiagnostics{}, err
	}
	diagnostics := ReleaseDiagnostics{PublishInProgress: inProgress}
	releaseRepos, err := s.db.ListReleaseRepos(ctx, release.ID)
	if err != nil {
		return ReleaseDiagnostics{}, err
	}
	for _, releaseRepo := range releaseRepos {
		repo := releaseRepo.Repo
		if err := s.git.Fetch(repo.BarePath, repo.Remote); err != nil {
			reason := fmt.Sprintf("repo %s fetch failed during release status check: %v", repo.Name, err)
			if inProgress {
				diagnostics.ManualReasons = append(diagnostics.ManualReasons, reason)
			} else if release.Status == store.ReleaseStatusStale {
				diagnostics.StaleReasons = append(diagnostics.StaleReasons, reason)
			}
			continue
		}
		remoteBase := repo.Remote + "/" + releaseRepo.TargetBranch
		if releaseRepo.Status == store.ReleaseRepoStatusPublished {
			currentBaseSHA, err := s.git.RevParseBare(repo.BarePath, remoteBase)
			if err != nil {
				reason := fmt.Sprintf("repo %s target branch cannot be read during release status check: %v", repo.Name, err)
				diagnostics.ManualReasons = append(diagnostics.ManualReasons, reason)
				continue
			}
			if !releaseRepo.PublishedSHA.Valid || currentBaseSHA != releaseRepo.PublishedSHA.String {
				diagnostics.ManualReasons = append(diagnostics.ManualReasons, fmt.Sprintf("published repo %s target branch changed after publish", repo.Name))
			}
			continue
		}
		hasNewCommits, err := s.git.HasNewCommitsSince(repo.BarePath, repo.Remote, releaseRepo.TargetBranch, releaseRepo.IntegratedBaseSHA)
		if err != nil {
			reason := fmt.Sprintf("repo %s target branch cannot be checked during release status check: %v", repo.Name, err)
			if inProgress {
				diagnostics.ManualReasons = append(diagnostics.ManualReasons, reason)
			} else if release.Status == store.ReleaseStatusStale {
				diagnostics.StaleReasons = append(diagnostics.StaleReasons, reason)
			}
			continue
		}
		if hasNewCommits {
			reason := fmt.Sprintf("repo %s target branch changed after integration", repo.Name)
			if inProgress {
				diagnostics.ManualReasons = append(diagnostics.ManualReasons, reason)
			} else if release.Status == store.ReleaseStatusStale {
				diagnostics.StaleReasons = append(diagnostics.StaleReasons, reason)
			}
		}
		if reason := s.releaseBranchChangeReason(releaseRepo); reason != "" {
			if inProgress {
				diagnostics.ManualReasons = append(diagnostics.ManualReasons, reason)
			} else if release.Status == store.ReleaseStatusStale {
				diagnostics.StaleReasons = append(diagnostics.StaleReasons, reason)
			}
		}
	}
	currentReasons, err := s.releaseCurrentChangeReasons(ctx, release, releaseRepos)
	if err != nil {
		return ReleaseDiagnostics{}, err
	}
	if inProgress {
		diagnostics.ManualReasons = append(diagnostics.ManualReasons, currentReasons...)
	} else if release.Status == store.ReleaseStatusStale {
		diagnostics.StaleReasons = append(diagnostics.StaleReasons, currentReasons...)
	}
	diagnostics.StaleReasons = dedupeStrings(diagnostics.StaleReasons)
	diagnostics.ManualReasons = dedupeStrings(diagnostics.ManualReasons)
	return diagnostics, nil
}

func (s *Service) ListRepos(ctx context.Context, includeDeleted bool) ([]store.Repo, error) {
	return s.db.ListRepos(ctx, includeDeleted)
}

func (s *Service) ensureOrdinaryActive(ctx context.Context, req store.Requirement) error {
	if req.Status != store.RequirementStatusActive || req.ArchivedAt.Valid {
		return fmt.Errorf("requirement %s is not a mutable active requirement", req.Key)
	}
	if req.ReadyAt.Valid {
		return fmt.Errorf("requirement %s is ready and cannot be modified", req.Key)
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

func (s *Service) releaseRepoPlans(ctx context.Context, memberships []store.ReleaseRequirement) ([]releaseRepoPlan, error) {
	plansByRepo := map[int64]*releaseRepoPlan{}
	var repoOrder []int64
	for _, membership := range memberships {
		req := membership.Requirement
		if req.Status != store.RequirementStatusActive || !req.ReadyAt.Valid || req.ArchivedAt.Valid {
			return nil, releaseStateChangedError{reason: fmt.Sprintf("requirement %s is not ready for release integration", req.Key)}
		}
		rels, err := s.db.ListRequirementRepos(ctx, req.ID)
		if err != nil {
			return nil, err
		}
		for _, rel := range rels {
			if rel.Status != store.RequirementRepoStatusCompleted {
				return nil, releaseStateChangedError{reason: fmt.Sprintf("requirement %s repo %s is not ready for release integration", req.Key, rel.RepoName)}
			}
			plan := plansByRepo[rel.RepoID]
			if plan == nil {
				repoOrder = append(repoOrder, rel.RepoID)
				plan = &releaseRepoPlan{repo: rel.Repo}
				plansByRepo[rel.RepoID] = plan
			}
			plan.rels = append(plan.rels, releaseRequirementRepoPlan{membership: membership, rel: rel})
		}
	}
	var plans []releaseRepoPlan
	for _, repoID := range repoOrder {
		plans = append(plans, *plansByRepo[repoID])
	}
	return plans, nil
}

func (s *Service) ensureReleasePublishCurrent(ctx context.Context, release store.Release, releaseRepos []store.ReleaseRepo, publishInProgress bool) error {
	memberships, err := s.db.ListReleaseRequirements(ctx, release.ID, false)
	if err != nil {
		return err
	}
	plans, err := s.releaseRepoPlans(ctx, memberships)
	if err != nil {
		var staleErr releaseStateChangedError
		if errors.As(err, &staleErr) {
			return s.releaseStaleOrManual(ctx, release, publishInProgress, staleErr.Error())
		}
		return err
	}
	activeRepoIDs := map[int64]bool{}
	for _, plan := range plans {
		activeRepoIDs[plan.repo.ID] = true
	}
	releaseRepoIDs := map[int64]store.ReleaseRepo{}
	for _, releaseRepo := range releaseRepos {
		releaseRepoIDs[releaseRepo.RepoID] = releaseRepo
	}
	if len(activeRepoIDs) != len(releaseRepoIDs) {
		return s.releaseStaleOrManual(ctx, release, publishInProgress, "release repo scope no longer matches active requirements")
	}
	for repoID := range activeRepoIDs {
		if _, ok := releaseRepoIDs[repoID]; !ok {
			return s.releaseStaleOrManual(ctx, release, publishInProgress, "release repo scope no longer matches active requirements")
		}
	}
	snapshots, err := s.db.ListReleaseRequirementRepos(ctx, release.ID)
	if err != nil {
		return err
	}
	snapshotByMembershipRepo := map[string]store.ReleaseRequirementRepo{}
	for _, snapshot := range snapshots {
		key := fmt.Sprintf("%d:%d", snapshot.ReleaseRequirementID, snapshot.RepoID)
		snapshotByMembershipRepo[key] = snapshot
	}
	for _, plan := range plans {
		if err := s.git.Fetch(plan.repo.BarePath, plan.repo.Remote); err != nil {
			return err
		}
		for _, reqPlan := range plan.rels {
			key := fmt.Sprintf("%d:%d", reqPlan.membership.ID, plan.repo.ID)
			snapshot, ok := snapshotByMembershipRepo[key]
			if !ok {
				return s.releaseStaleOrManual(ctx, release, publishInProgress, "release feature snapshot is missing")
			}
			currentSHA, err := s.git.RevParseBare(plan.repo.BarePath, plan.repo.Remote+"/"+snapshot.FeatureBranch)
			if err != nil {
				return err
			}
			if currentSHA != snapshot.FeatureSHA {
				return s.releaseStaleOrManual(ctx, release, publishInProgress, "feature branch changed after integration")
			}
		}
	}
	return nil
}

func (s *Service) releaseCurrentChangeReasons(ctx context.Context, release store.Release, releaseRepos []store.ReleaseRepo) ([]string, error) {
	memberships, err := s.db.ListReleaseRequirements(ctx, release.ID, false)
	if err != nil {
		return nil, err
	}
	plans, err := s.releaseRepoPlans(ctx, memberships)
	if err != nil {
		return []string{err.Error()}, nil
	}
	var reasons []string
	activeRepoIDs := map[int64]bool{}
	for _, plan := range plans {
		activeRepoIDs[plan.repo.ID] = true
	}
	releaseRepoIDs := map[int64]store.ReleaseRepo{}
	for _, releaseRepo := range releaseRepos {
		releaseRepoIDs[releaseRepo.RepoID] = releaseRepo
	}
	if len(activeRepoIDs) != len(releaseRepoIDs) {
		reasons = append(reasons, "release repo scope no longer matches active requirements")
	}
	for repoID := range activeRepoIDs {
		if _, ok := releaseRepoIDs[repoID]; !ok {
			reasons = append(reasons, "release repo scope no longer matches active requirements")
			break
		}
	}
	snapshots, err := s.db.ListReleaseRequirementRepos(ctx, release.ID)
	if err != nil {
		return nil, err
	}
	snapshotByMembershipRepo := map[string]store.ReleaseRequirementRepo{}
	for _, snapshot := range snapshots {
		key := fmt.Sprintf("%d:%d", snapshot.ReleaseRequirementID, snapshot.RepoID)
		snapshotByMembershipRepo[key] = snapshot
	}
	for _, plan := range plans {
		if err := s.git.Fetch(plan.repo.BarePath, plan.repo.Remote); err != nil {
			reasons = append(reasons, fmt.Sprintf("repo %s fetch failed during feature snapshot check: %v", plan.repo.Name, err))
			continue
		}
		for _, reqPlan := range plan.rels {
			key := fmt.Sprintf("%d:%d", reqPlan.membership.ID, plan.repo.ID)
			snapshot, ok := snapshotByMembershipRepo[key]
			if !ok {
				reasons = append(reasons, "release feature snapshot is missing")
				continue
			}
			currentSHA, err := s.git.RevParseBare(plan.repo.BarePath, plan.repo.Remote+"/"+snapshot.FeatureBranch)
			if err != nil {
				reasons = append(reasons, fmt.Sprintf("feature branch %s cannot be read for repo %s: %v", snapshot.FeatureBranch, plan.repo.Name, err))
				continue
			}
			if currentSHA != snapshot.FeatureSHA {
				reasons = append(reasons, fmt.Sprintf("feature branch changed after integration for requirement %s repo %s", reqPlan.membership.Requirement.Key, plan.repo.Name))
			}
		}
	}
	return dedupeStrings(reasons), nil
}

func (s *Service) releaseStaleOrManual(ctx context.Context, release store.Release, publishInProgress bool, reason string) error {
	if publishInProgress {
		return fmt.Errorf("%s; release %s is publish-in-progress and requires manual handling", reason, release.Key)
	}
	_ = s.db.UpdateReleaseStatus(ctx, release.ID, store.ReleaseStatusStale)
	return fmt.Errorf("%s; reintegrate release %s", reason, release.Key)
}

func (s *Service) releaseBranchChangeReason(releaseRepo store.ReleaseRepo) string {
	remoteRelease := releaseRepo.Repo.Remote + "/" + releaseRepo.ReleaseBranch
	currentReleaseSHA, err := s.git.RevParseBare(releaseRepo.Repo.BarePath, remoteRelease)
	if err != nil {
		return fmt.Sprintf("release branch %s cannot be read after integration: %v", remoteRelease, err)
	}
	if currentReleaseSHA != releaseRepo.ReleaseSHA {
		return fmt.Sprintf("release branch %s changed after integration", remoteRelease)
	}
	return ""
}

func (s *Service) failReleaseOperation(ctx context.Context, releaseID, repoID int64, operation string, err error) error {
	if err != nil {
		_ = s.db.LogReleaseOperation(ctx, releaseID, 0, repoID, operation, store.OperationStatusFailed, err.Error())
	}
	_ = s.db.UpdateReleaseStatus(ctx, releaseID, store.ReleaseStatusFailed)
	return err
}

func (s *Service) failReleasePublishOperation(ctx context.Context, releaseID int64, releaseRepo store.ReleaseRepo, operation string, err error) error {
	if err != nil {
		_ = s.db.LogReleaseOperation(ctx, releaseID, 0, releaseRepo.RepoID, operation, store.OperationStatusFailed, err.Error())
	}
	if releaseRepo.Status != store.ReleaseRepoStatusPublished {
		_ = s.db.MarkReleaseRepoFailed(ctx, releaseRepo.ID)
	}
	return err
}

func (s *Service) cleanupObsoleteReleaseRepos(ctx context.Context, release store.Release, releaseRepos []store.ReleaseRepo, activeRepoIDs map[int64]bool, force bool) error {
	for _, releaseRepo := range releaseRepos {
		if activeRepoIDs[releaseRepo.RepoID] {
			continue
		}
		if _, err := os.Stat(releaseRepo.WorktreePath); err == nil {
			hasChanges, err := s.git.HasChanges(releaseRepo.WorktreePath)
			if err != nil {
				return s.failReleaseOperation(ctx, release.ID, releaseRepo.RepoID, "release_integrate_obsolete_status", err)
			}
			if hasChanges && !force {
				return fmt.Errorf("obsolete release worktree has uncommitted changes: %s", releaseRepo.WorktreePath)
			}
			removeWorktree := s.git.RemoveWorktree
			if force {
				removeWorktree = s.git.RemoveWorktreeForce
			}
			if err := removeWorktree(releaseRepo.Repo.BarePath, releaseRepo.WorktreePath); err != nil {
				return s.failReleaseOperation(ctx, release.ID, releaseRepo.RepoID, "release_integrate_remove_obsolete_worktree", err)
			}
		} else if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("check obsolete release worktree %s: %w", releaseRepo.WorktreePath, err)
		}
		if err := s.git.DeleteLocalBranch(releaseRepo.Repo.BarePath, release.BranchName); err != nil {
			return s.failReleaseOperation(ctx, release.ID, releaseRepo.RepoID, "release_integrate_delete_obsolete_branch", err)
		}
	}
	return nil
}

func (s *Service) releasePublishInProgress(ctx context.Context, release store.Release) (bool, error) {
	if release.Status == store.ReleaseStatusPublished {
		return false, nil
	}
	repos, err := s.db.ListReleaseRepos(ctx, release.ID)
	if err != nil {
		return false, err
	}
	for _, repo := range repos {
		if repo.Status == store.ReleaseRepoStatusPublished {
			return true, nil
		}
	}
	return false, nil
}

func (s *Service) requirementInPublishInProgressRelease(ctx context.Context, requirementID int64) (bool, error) {
	releases, err := s.db.ListReleases(ctx, true)
	if err != nil {
		return false, err
	}
	for _, release := range releases {
		inProgress, err := s.releasePublishInProgress(ctx, release)
		if err != nil {
			return false, err
		}
		if !inProgress {
			continue
		}
		memberships, err := s.db.ListReleaseRequirements(ctx, release.ID, false)
		if err != nil {
			return false, err
		}
		for _, membership := range memberships {
			if membership.RequirementID == requirementID {
				return true, nil
			}
		}
	}
	return false, nil
}

func (s *Service) worktreeBaseRef(repo store.Repo, featureBranch string) string {
	if s.git.RemoteBranchExists(repo.BarePath, repo.Remote, featureBranch) {
		return repo.Remote + "/" + featureBranch
	}
	return repo.Remote + "/" + repo.BaseBranch
}

func (s *Service) cleanupFailedRequirement(ctx context.Context, req store.Requirement, rels []store.RequirementRepo, worktrees []createdWorktree) error {
	var cleanupErr error
	cleanupErr = errors.Join(cleanupErr, s.cleanupCreatedWorktrees(ctx, req.ID, worktrees))
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

func (s *Service) cleanupFailedRelease(ctx context.Context, release store.Release) error {
	var cleanupErr error
	if err := s.db.DeleteRelease(ctx, release.ID); err != nil {
		cleanupErr = errors.Join(cleanupErr, err)
	}
	if err := os.Remove(release.WorkspacePath); err != nil && !os.IsNotExist(err) {
		cleanupErr = errors.Join(cleanupErr, err)
	}
	return cleanupErr
}

func (s *Service) cleanupCreatedWorktrees(ctx context.Context, requirementID int64, worktrees []createdWorktree) error {
	var cleanupErr error
	for i := len(worktrees) - 1; i >= 0; i-- {
		wt := worktrees[i]
		if err := s.git.RemoveWorktree(wt.repo.BarePath, wt.path); err != nil {
			cleanupErr = errors.Join(cleanupErr, err)
			_ = s.db.LogOperation(ctx, requirementID, wt.repo.ID, "cleanup", store.OperationStatusFailed, err.Error())
		}
	}
	return cleanupErr
}

func withCleanupError(err, cleanupErr error) error {
	if cleanupErr == nil {
		return err
	}
	return fmt.Errorf("%w; cleanup failed: %v", err, cleanupErr)
}
