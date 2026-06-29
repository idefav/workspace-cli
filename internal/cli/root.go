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
	cmd.AddCommand(newInitCommand(&home))
	cmd.AddCommand(newRepoCommand(&home))
	cmd.AddCommand(newReqCommand(&home))
	cmd.AddCommand(newDevCommand(&home))
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
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\t%s\n", req.Key, req.Status, archived, req.Title)
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
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "key:\t%s\nstatus:\t%s\nworkspace:\t%s\nbranch:\t%s\n", req.Key, req.Status, req.WorkspacePath, req.FeatureBranch)
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
