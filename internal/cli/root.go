package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"workspace-cli/internal/agent"
	"workspace-cli/internal/config"
	gitx "workspace-cli/internal/git"
	"workspace-cli/internal/store"
	"workspace-cli/internal/update"
	"workspace-cli/internal/version"
	wsvc "workspace-cli/internal/workspace"
)

func NewRootCommand() *cobra.Command {
	var home string
	cmd := &cobra.Command{
		Use:           "workspace",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.PersistentFlags().StringVar(&home, "home", "", "workspace-cli home directory")
	cmd.AddCommand(newVersionCommand())
	cmd.AddCommand(newUpdateCommand())
	cmd.AddCommand(newInitCommand(&home))
	cmd.AddCommand(newRepoCommand(&home))
	cmd.AddCommand(newReqCommand(&home))
	cmd.AddCommand(newReleaseCommand(&home))
	cmd.AddCommand(newDevCommand(&home))
	cmd.AddCommand(newIDECommand(&home))
	return cmd
}

func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "print workspace-cli version",
		RunE: func(cmd *cobra.Command, args []string) error {
			info := version.Current()
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "version:\t%s\ncommit:\t%s\ndate:\t%s\n", info.Version, info.Commit, info.Date)
			return nil
		},
	}
}

func newUpdateCommand() *cobra.Command {
	var checkOnly bool
	var ownerRepo string
	cmd := &cobra.Command{
		Use:   "update",
		Short: "check for and install workspace-cli updates",
		RunE: func(cmd *cobra.Command, args []string) error {
			current := version.Current()
			client := update.Client{
				OwnerRepo:      ownerRepo,
				CurrentVersion: current.Version,
			}
			if checkOnly {
				info, err := client.CheckLatest(cmd.Context())
				if err != nil {
					return err
				}
				if info.UpdateAvailable {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "update available:\t%s -> %s\nrelease:\t%s\n", info.CurrentVersion, info.LatestVersion, info.ReleaseURL)
				} else {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "workspace-cli is up to date:\t%s\n", info.CurrentVersion)
				}
				return nil
			}
			info, err := client.InstallLatest(cmd.Context())
			if err != nil {
				return err
			}
			if !info.UpdateAvailable {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "workspace-cli is up to date:\t%s\n", info.CurrentVersion)
				return nil
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "updated workspace-cli:\t%s -> %s\n", info.CurrentVersion, info.LatestVersion)
			return nil
		},
	}
	cmd.Flags().BoolVar(&checkOnly, "check", false, "only check whether an update is available")
	cmd.Flags().StringVar(&ownerRepo, "repo", update.DefaultOwnerRepo, "GitHub owner/repo to check for releases")
	return cmd
}

func newInitCommand(home *string) *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "initialize workspace-cli home",
		RunE: func(cmd *cobra.Command, args []string) error {
			resolved, err := resolveHome(*home)
			if err != nil {
				return err
			}
			cfg, err := config.Init(resolved)
			if err != nil {
				return err
			}
			db, err := store.Open(cfg.DBPath)
			if err != nil {
				return err
			}
			defer db.Close()
			if err := db.Migrate(context.Background()); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "initialized %s\n", cfg.Home)
			return nil
		},
	}
}

func newRepoCommand(home *string) *cobra.Command {
	cmd := &cobra.Command{Use: "repo", Short: "manage repositories"}
	cmd.AddCommand(newRepoAddCommand(home))
	cmd.AddCommand(newRepoListCommand(home))
	cmd.AddCommand(newRepoSyncCommand(home))
	cmd.AddCommand(newRepoUpdateCommand(home))
	cmd.AddCommand(newRepoRemoveCommand(home))
	return cmd
}

func newRepoAddCommand(home *string) *cobra.Command {
	var remote, base string
	cmd := &cobra.Command{
		Use:  "add <name> <url>",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := loadService(*home)
			if err != nil {
				return err
			}
			defer cleanup()
			repo, err := svc.AddRepo(cmd.Context(), wsvc.AddRepoParams{Name: args[0], URL: args[1], Remote: remote, BaseBranch: base})
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\n", repo.Name, repo.URL)
			return nil
		},
	}
	cmd.Flags().StringVar(&remote, "remote", "origin", "git remote name")
	cmd.Flags().StringVar(&base, "base", "", "base branch")
	return cmd
}

func newRepoListCommand(home *string) *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use: "list",
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := loadService(*home)
			if err != nil {
				return err
			}
			defer cleanup()
			repos, err := svc.ListRepos(cmd.Context(), all)
			if err != nil {
				return err
			}
			for _, repo := range repos {
				deleted := ""
				if repo.DeletedAt.Valid {
					deleted = "\tdeleted"
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\t%s%s\n", repo.Name, repo.URL, repo.Remote, repo.BaseBranch, deleted)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "include deleted repos")
	return cmd
}

func newRepoSyncCommand(home *string) *cobra.Command {
	return &cobra.Command{
		Use:  "sync [name]",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := loadService(*home)
			if err != nil {
				return err
			}
			defer cleanup()
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			return svc.SyncRepo(cmd.Context(), name)
		},
	}
}

func newRepoUpdateCommand(home *string) *cobra.Command {
	var url, remote, base string
	cmd := &cobra.Command{
		Use:  "update <name>",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := loadService(*home)
			if err != nil {
				return err
			}
			defer cleanup()
			repo, err := svc.UpdateRepo(cmd.Context(), wsvc.UpdateRepoParams{Name: args[0], URL: url, Remote: remote, BaseBranch: base})
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\t%s\n", repo.Name, repo.URL, repo.Remote, repo.BaseBranch)
			return nil
		},
	}
	cmd.Flags().StringVar(&url, "url", "", "new git URL")
	cmd.Flags().StringVar(&remote, "remote", "", "new remote name")
	cmd.Flags().StringVar(&base, "base", "", "new base branch")
	return cmd
}

func newRepoRemoveCommand(home *string) *cobra.Command {
	return &cobra.Command{
		Use:  "remove <name>",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := loadService(*home)
			if err != nil {
				return err
			}
			defer cleanup()
			return svc.RemoveRepo(cmd.Context(), args[0])
		},
	}
}

func newReqCommand(home *string) *cobra.Command {
	cmd := &cobra.Command{Use: "req", Short: "manage requirements"}
	cmd.AddCommand(newReqCreateCommand(home))
	cmd.AddCommand(newReqListCommand(home))
	cmd.AddCommand(newReqShowCommand(home))
	cmd.AddCommand(newReqUpdateCommand(home))
	cmd.AddCommand(newReqAddRepoCommand(home))
	cmd.AddCommand(newReqArchiveCommand(home))
	cmd.AddCommand(newReqFinishCommand(home))
	cmd.AddCommand(newReqReopenCommand(home))
	return cmd
}

func newReqCreateCommand(home *string) *cobra.Command {
	var key string
	var repos []string
	cmd := &cobra.Command{
		Use:  "create <title>",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(repos) == 0 {
				return fmt.Errorf("at least one --repo is required")
			}
			svc, cleanup, err := loadService(*home)
			if err != nil {
				return err
			}
			defer cleanup()
			req, err := svc.CreateRequirement(cmd.Context(), wsvc.CreateRequirementParams{Title: args[0], Key: key, RepoNames: repos})
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\n", req.Key, req.WorkspacePath)
			return nil
		},
	}
	cmd.Flags().StringVar(&key, "key", "", "requirement key/slug")
	cmd.Flags().StringArrayVar(&repos, "repo", nil, "repo to bind")
	return cmd
}

func newReqListCommand(home *string) *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use: "list",
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := loadService(*home)
			if err != nil {
				return err
			}
			defer cleanup()
			reqs, err := svc.ListRequirements(cmd.Context(), all)
			if err != nil {
				return err
			}
			for _, req := range reqs {
				archived := "false"
				if req.ArchivedAt.Valid {
					archived = "true"
				}
				stage, err := svc.RequirementStage(cmd.Context(), req)
				if err != nil {
					return err
				}
				completion, err := svc.RequirementCompletion(cmd.Context(), req)
				if err != nil {
					return err
				}
				if completion != "" {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\t%s\t%s\t%s\n", req.Key, req.Status, stage, archived, req.Title, completion)
					continue
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\t%s\t%s\n", req.Key, req.Status, stage, archived, req.Title)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "include archived requirements")
	return cmd
}

func newReqShowCommand(home *string) *cobra.Command {
	return &cobra.Command{
		Use:  "show <key-or-slug>",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := loadService(*home)
			if err != nil {
				return err
			}
			defer cleanup()
			req, err := svc.GetRequirement(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			stage, err := svc.RequirementStage(cmd.Context(), req)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "key:\t%s\nstatus:\t%s\nstage:\t%s\nworkspace:\t%s\nbranch:\t%s\n", req.Key, req.Status, stage, req.WorkspacePath, req.FeatureBranch)
			completion, err := svc.RequirementCompletion(cmd.Context(), req)
			if err != nil {
				return err
			}
			if completion != "" {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "completion:\t%s\n", completion)
			}
			rels, err := svc.ListRequirementRepos(cmd.Context(), req.ID)
			if err != nil {
				return err
			}
			for _, rel := range rels {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "repo:\t%s\nurl:\t%s\nremote:\t%s\nbase:\t%s\nworktree:\t%s\n", rel.RepoName, rel.RepoURL, rel.RepoRemote, rel.RepoBaseBranch, rel.WorktreePath)
			}
			return nil
		},
	}
}

func newReqUpdateCommand(home *string) *cobra.Command {
	var title string
	cmd := &cobra.Command{
		Use:  "update <key-or-slug>",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if title == "" {
				return fmt.Errorf("--title is required")
			}
			svc, cleanup, err := loadService(*home)
			if err != nil {
				return err
			}
			defer cleanup()
			return svc.UpdateRequirement(cmd.Context(), args[0], title)
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "new title")
	return cmd
}

func newReqAddRepoCommand(home *string) *cobra.Command {
	return &cobra.Command{
		Use:  "add-repo <key-or-slug> <repo>",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := loadService(*home)
			if err != nil {
				return err
			}
			defer cleanup()
			_, err = svc.AddRepoToRequirement(cmd.Context(), args[0], args[1])
			return err
		},
	}
}

func newReqArchiveCommand(home *string) *cobra.Command {
	return &cobra.Command{
		Use:  "archive <key-or-slug>",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := loadService(*home)
			if err != nil {
				return err
			}
			defer cleanup()
			return svc.ArchiveRequirement(cmd.Context(), args[0])
		},
	}
}

func newReqFinishCommand(home *string) *cobra.Command {
	var message string
	cmd := &cobra.Command{
		Use:  "finish <key-or-slug>",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := loadService(*home)
			if err != nil {
				return err
			}
			defer cleanup()
			if message == "" {
				message = "finish " + args[0]
			}
			return svc.FinishRequirement(cmd.Context(), args[0], message)
		},
	}
	cmd.Flags().StringVarP(&message, "message", "m", "", "commit message")
	return cmd
}

func newReqReopenCommand(home *string) *cobra.Command {
	return &cobra.Command{
		Use:  "reopen <key-or-slug>",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := loadService(*home)
			if err != nil {
				return err
			}
			defer cleanup()
			return svc.ReopenRequirement(cmd.Context(), args[0])
		},
	}
}

func newReleaseCommand(home *string) *cobra.Command {
	cmd := &cobra.Command{Use: "release", Short: "manage releases"}
	cmd.AddCommand(newReleaseCreateCommand(home))
	cmd.AddCommand(newReleaseListCommand(home))
	cmd.AddCommand(newReleaseShowCommand(home))
	cmd.AddCommand(newReleaseAddReqCommand(home))
	cmd.AddCommand(newReleaseRemoveReqCommand(home))
	cmd.AddCommand(newReleaseStatusCommand(home))
	cmd.AddCommand(newReleaseIntegrateCommand(home))
	cmd.AddCommand(newReleasePublishCommand(home))
	return cmd
}

func newReleaseCreateCommand(home *string) *cobra.Command {
	var key string
	var requirements []string
	cmd := &cobra.Command{
		Use:  "create <title>",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(requirements) == 0 {
				return fmt.Errorf("at least one --req is required")
			}
			svc, cleanup, err := loadService(*home)
			if err != nil {
				return err
			}
			defer cleanup()
			release, err := svc.CreateRelease(cmd.Context(), wsvc.CreateReleaseParams{Title: args[0], Key: key, RequirementKeys: requirements})
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\n", release.Key, release.WorkspacePath)
			return nil
		},
	}
	cmd.Flags().StringVar(&key, "key", "", "release key/slug")
	cmd.Flags().StringArrayVar(&requirements, "req", nil, "ready requirement to include")
	return cmd
}

func newReleaseListCommand(home *string) *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use: "list",
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := loadService(*home)
			if err != nil {
				return err
			}
			defer cleanup()
			releases, err := svc.ListReleases(cmd.Context(), all)
			if err != nil {
				return err
			}
			for _, release := range releases {
				diagnostics, err := svc.ReleaseDiagnostics(cmd.Context(), release)
				if err != nil {
					return err
				}
				phase := release.Status
				if diagnostics.PublishInProgress {
					phase = "publish-in-progress"
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\t%s\n", release.Key, release.Status, phase, release.Title)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "include published releases")
	return cmd
}

func newReleaseShowCommand(home *string) *cobra.Command {
	return &cobra.Command{
		Use:  "show <key-or-slug>",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := loadService(*home)
			if err != nil {
				return err
			}
			defer cleanup()
			release, err := svc.RefreshReleaseStatus(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "key:\t%s\nstatus:\t%s\nworkspace:\t%s\nbranch:\t%s\n", release.Key, release.Status, release.WorkspacePath, release.BranchName)
			return printReleaseDetails(cmd, svc, release)
		},
	}
}

func newReleaseAddReqCommand(home *string) *cobra.Command {
	return &cobra.Command{
		Use:  "add-req <release> <req>",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := loadService(*home)
			if err != nil {
				return err
			}
			defer cleanup()
			_, err = svc.AddRequirementToRelease(cmd.Context(), args[0], args[1])
			return err
		},
	}
}

func newReleaseRemoveReqCommand(home *string) *cobra.Command {
	return &cobra.Command{
		Use:  "remove-req <release> <req>",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := loadService(*home)
			if err != nil {
				return err
			}
			defer cleanup()
			return svc.RemoveRequirementFromRelease(cmd.Context(), args[0], args[1])
		},
	}
}

func newReleaseStatusCommand(home *string) *cobra.Command {
	return &cobra.Command{
		Use:  "status <key-or-slug>",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := loadService(*home)
			if err != nil {
				return err
			}
			defer cleanup()
			release, err := svc.RefreshReleaseStatus(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\n", release.Key, release.Status)
			return printReleaseDetails(cmd, svc, release)
		},
	}
}

func printReleaseDetails(cmd *cobra.Command, svc *wsvc.Service, release store.Release) error {
	diagnostics, err := svc.ReleaseDiagnostics(cmd.Context(), release)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "publish-in-progress\t%t\n", diagnostics.PublishInProgress)
	for _, reason := range diagnostics.StaleReasons {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "stale\t%s\n", reason)
	}
	for _, reason := range diagnostics.ManualReasons {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "manual\t%s\n", reason)
	}
	requirements, err := svc.ListReleaseRequirements(cmd.Context(), release.ID, true)
	if err != nil {
		return err
	}
	reqNames := map[int64]string{}
	for _, requirement := range requirements {
		state := "active"
		if requirement.RemovedAt.Valid {
			state = "removed"
		}
		reqNames[requirement.RequirementID] = requirement.Requirement.Key
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "req\t%s\t%s\n", requirement.Requirement.Key, state)
	}
	repos, err := svc.ListReleaseRepos(cmd.Context(), release.ID)
	if err != nil {
		return err
	}
	repoNames := map[int64]string{}
	for _, repo := range repos {
		repoNames[repo.RepoID] = repo.Repo.Name
		publishedSHA := ""
		if repo.PublishedSHA.Valid {
			publishedSHA = repo.PublishedSHA.String
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "repo\t%s\t%s\t%s\t%s\t%s\t%s\n", repo.Repo.Name, repo.Status, repo.TargetBranch, repo.IntegratedBaseSHA, repo.ReleaseSHA, publishedSHA)
	}
	snapshots, err := svc.ListReleaseRequirementRepos(cmd.Context(), release.ID)
	if err != nil {
		return err
	}
	for _, snapshot := range snapshots {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "feature\t%s\t%s\t%s\t%s\n", reqNames[snapshot.RequirementID], repoNames[snapshot.RepoID], snapshot.FeatureBranch, snapshot.FeatureSHA)
	}
	return nil
}

func newReleaseIntegrateCommand(home *string) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:  "integrate <key-or-slug>",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := loadService(*home)
			if err != nil {
				return err
			}
			defer cleanup()
			if err := svc.IntegrateRelease(cmd.Context(), args[0], force); err != nil {
				return err
			}
			release, err := svc.GetRelease(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\t%s\n", release.Key, release.Status, release.WorkspacePath, release.BranchName)
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "discard dirty release worktree changes")
	return cmd
}

func newReleasePublishCommand(home *string) *cobra.Command {
	var tested bool
	var message string
	cmd := &cobra.Command{
		Use:  "publish <key-or-slug>",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := loadService(*home)
			if err != nil {
				return err
			}
			defer cleanup()
			if err := svc.PublishRelease(cmd.Context(), args[0], tested, message); err != nil {
				return err
			}
			release, err := svc.GetRelease(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\n", release.Key, release.Status)
			return nil
		},
	}
	cmd.Flags().BoolVar(&tested, "tested", false, "confirm release has been tested")
	cmd.Flags().StringVarP(&message, "message", "m", "", "merge commit message")
	return cmd
}

func newDevCommand(home *string) *cobra.Command {
	var tool string
	cmd := &cobra.Command{
		Use:  "dev <key-or-slug>",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, svc, cleanup, err := loadConfigAndService(*home)
			if err != nil {
				return err
			}
			defer cleanup()
			req, err := svc.GetRequirement(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if err := ensureRequirementCanLaunchTool(cmd.Context(), svc, req); err != nil {
				return err
			}
			switch tool {
			case "codex":
				return agent.Run(req.WorkspacePath, cfg.Tools.Codex)
			case "claude":
				return agent.Run(req.WorkspacePath, cfg.Tools.Claude)
			default:
				return fmt.Errorf("unknown tool %q", tool)
			}
		},
	}
	cmd.Flags().StringVar(&tool, "tool", "codex", "tool to run: codex or claude")
	return cmd
}

func newIDECommand(home *string) *cobra.Command {
	var tool string
	cmd := &cobra.Command{
		Use:  "ide <key-or-slug>",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, svc, cleanup, err := loadConfigAndService(*home)
			if err != nil {
				return err
			}
			defer cleanup()
			req, err := svc.GetRequirement(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if err := ensureRequirementCanLaunchTool(cmd.Context(), svc, req); err != nil {
				return err
			}
			var command []string
			switch tool {
			case "vscode":
				command = cfg.Tools.VSCode
			case "cursor":
				command = cfg.Tools.Cursor
			case "zed":
				command = cfg.Tools.Zed
			default:
				return fmt.Errorf("unknown ide tool %q", tool)
			}
			if len(command) == 0 {
				return fmt.Errorf("empty ide tool command for %q", tool)
			}
			command = append(append([]string{}, command...), req.WorkspacePath)
			return agent.Run(req.WorkspacePath, command)
		},
	}
	cmd.Flags().StringVar(&tool, "tool", "vscode", "IDE to run: vscode, cursor, or zed")
	return cmd
}

func ensureRequirementCanLaunchTool(ctx context.Context, svc *wsvc.Service, req store.Requirement) error {
	stage, err := svc.RequirementStage(ctx, req)
	if err != nil {
		return err
	}
	if stage == "cleanup-pending" {
		return fmt.Errorf("requirement %s is cleanup-pending; run workspace req finish %s to finish cleanup", req.Key, req.Key)
	}
	return nil
}

func resolveHome(flagHome string) (string, error) {
	if flagHome != "" {
		return flagHome, nil
	}
	if envHome := os.Getenv("WORKSPACE_CLI_HOME"); envHome != "" {
		return envHome, nil
	}
	userHome, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(userHome, ".workspace-cli"), nil
}

func loadService(flagHome string) (*wsvc.Service, func(), error) {
	_, svc, cleanup, err := loadConfigAndService(flagHome)
	return svc, cleanup, err
}

func loadConfigAndService(flagHome string) (config.Config, *wsvc.Service, func(), error) {
	home, err := resolveHome(flagHome)
	if err != nil {
		return config.Config{}, nil, nil, err
	}
	cfg, err := config.Load(home)
	if err != nil {
		return config.Config{}, nil, nil, err
	}
	db, err := store.Open(cfg.DBPath)
	if err != nil {
		return config.Config{}, nil, nil, err
	}
	cleanup := func() { _ = db.Close() }
	if err := db.Migrate(context.Background()); err != nil {
		cleanup()
		return config.Config{}, nil, nil, err
	}
	return cfg, wsvc.NewService(cfg, db, gitx.NewManager(gitx.ExecRunner{})), cleanup, nil
}
