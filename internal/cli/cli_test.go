package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"workspace-cli/internal/store"
)

func TestInitCommandCreatesHomeAndDatabase(t *testing.T) {
	home := t.TempDir()
	cmd := NewRootCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--home", home, "init"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v\n%s", err, out.String())
	}

	for _, path := range []string{
		filepath.Join(home, "config.yaml"),
		filepath.Join(home, "workspace.db"),
		filepath.Join(home, "work", "repos"),
		filepath.Join(home, "work", "requirements"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected %s to exist: %v", path, err)
		}
	}
}

func TestRootCommandIncludesDocumentedCommandSurface(t *testing.T) {
	cmd := NewRootCommand()
	for _, path := range [][]string{
		{"version"},
		{"update"},
		{"ide"},
		{"repo", "add"},
		{"repo", "list"},
		{"repo", "sync"},
		{"repo", "update"},
		{"repo", "remove"},
		{"req", "create"},
		{"req", "list"},
		{"req", "show"},
		{"req", "update"},
		{"req", "add-repo"},
		{"req", "archive"},
		{"req", "finish"},
		{"req", "reopen"},
		{"release", "create"},
		{"release", "list"},
		{"release", "show"},
		{"release", "add-req"},
		{"release", "remove-req"},
		{"release", "status"},
		{"release", "integrate"},
		{"release", "publish"},
		{"dev"},
	} {
		if _, _, err := cmd.Find(path); err != nil {
			t.Fatalf("command %v not found: %v", path, err)
		}
	}
}

func TestCompletionCommandIsAvailable(t *testing.T) {
	cmd := NewRootCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"completion", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("completion --help error = %v\n%s", err, out.String())
	}
	for _, want := range []string{"bash", "fish", "powershell", "zsh"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("completion help missing %q:\n%s", want, out.String())
		}
	}
}

func TestVersionCommandPrintsBuildMetadata(t *testing.T) {
	cmd := NewRootCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"version"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v\n%s", err, out.String())
	}
	for _, want := range []string{
		"version:",
		"commit:",
		"date:",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("version output missing %q:\n%s", want, out.String())
		}
	}
}

func TestCLIEndToEndCreateAndFinishRequirement(t *testing.T) {
	home := t.TempDir()
	remote := seedRemote(t)

	runWorkspace(t, home, "init")
	runWorkspace(t, home, "repo", "add", "backend", remote, "--base", "main")
	runWorkspace(t, home, "req", "create", "Payment Flow", "--key", "pay-flow", "--repo", "backend")

	worktree := filepath.Join(home, "work", "requirements", "pay-flow", "backend")
	runGit(t, worktree, "config", "user.name", "Workspace Test")
	runGit(t, worktree, "config", "user.email", "workspace@example.com")
	if err := os.WriteFile(filepath.Join(worktree, "feature.txt"), []byte("done\n"), 0o644); err != nil {
		t.Fatalf("write feature file: %v", err)
	}

	runWorkspace(t, home, "req", "finish", "pay-flow", "-m", "feat: finish")

	if _, err := os.Stat(worktree); !os.IsNotExist(err) {
		t.Fatalf("worktree should be removed, stat err = %v", err)
	}
	if got := runGitOutput(t, remote, "rev-parse", "refs/heads/feature/pay-flow"); got == "" {
		t.Fatal("expected remote feature branch")
	}
}

func TestReqShowIncludesRepoSnapshots(t *testing.T) {
	home := t.TempDir()
	remote := seedRemote(t)

	runWorkspace(t, home, "init")
	runWorkspace(t, home, "repo", "add", "backend", remote, "--base", "main")
	runWorkspace(t, home, "req", "create", "Payment Flow", "--key", "pay-flow", "--repo", "backend")

	out := runWorkspace(t, home, "req", "show", "pay-flow")
	for _, want := range []string{
		"repo:\tbackend",
		"url:\t" + remote,
		"remote:\torigin",
		"base:\tmain",
		"worktree:\t" + filepath.Join(home, "work", "requirements", "pay-flow", "backend"),
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("req show output missing %q:\n%s", want, out)
		}
	}
}

func TestRepoSyncCommandFetchesSingleRepo(t *testing.T) {
	home := t.TempDir()
	remote := seedRemote(t)

	runWorkspace(t, home, "init")
	runWorkspace(t, home, "repo", "add", "backend", remote, "--base", "main")
	latest := seedRemoteBaseCommit(t, remote, "main", "latest-sync.txt", "latest sync\n")
	barePath := filepath.Join(home, "work", "repos", "backend.git")
	if got := strings.TrimSpace(runGitOutput(t, barePath, "rev-parse", "refs/remotes/origin/main")); got == latest {
		t.Fatalf("test setup expected bare repo to be stale before sync, got %s", got)
	}

	runWorkspace(t, home, "repo", "sync", "backend")

	if got := strings.TrimSpace(runGitOutput(t, barePath, "rev-parse", "refs/remotes/origin/main")); got != latest {
		t.Fatalf("refs/remotes/origin/main = %s, want %s", got, latest)
	}
}

func TestRepoListAllShowsSoftDeletedRepo(t *testing.T) {
	home := t.TempDir()
	remote := seedRemote(t)

	runWorkspace(t, home, "init")
	runWorkspace(t, home, "repo", "add", "backend", remote, "--base", "main")
	runWorkspace(t, home, "repo", "remove", "backend")

	if out := runWorkspace(t, home, "repo", "list"); strings.Contains(out, "backend") {
		t.Fatalf("repo list should hide soft-deleted repo, got:\n%s", out)
	}
	out := runWorkspace(t, home, "repo", "list", "--all")
	for _, want := range []string{"backend", "deleted"} {
		if !strings.Contains(out, want) {
			t.Fatalf("repo list --all output missing %q:\n%s", want, out)
		}
	}
}

func TestReqListShowsReadyRequirementAfterFinish(t *testing.T) {
	home := t.TempDir()
	remote := seedRemote(t)

	runWorkspace(t, home, "init")
	runWorkspace(t, home, "repo", "add", "backend", remote, "--base", "main")
	runWorkspace(t, home, "req", "create", "Payment Flow", "--key", "pay-flow", "--repo", "backend")

	activeOut := runWorkspace(t, home, "req", "list")
	if !strings.Contains(activeOut, "pay-flow\tactive\tactive\tfalse\tPayment Flow") {
		t.Fatalf("req list missing active requirement:\n%s", activeOut)
	}

	runWorkspace(t, home, "req", "finish", "pay-flow", "-m", "feat: finish")
	readyOut := runWorkspace(t, home, "req", "list")
	if !strings.Contains(readyOut, "pay-flow\tactive\tready\tfalse\tPayment Flow") {
		t.Fatalf("req list missing ready requirement:\n%s", readyOut)
	}
	allOut := runWorkspace(t, home, "req", "list", "--all")
	if !strings.Contains(allOut, "pay-flow\tactive\tready\tfalse\tPayment Flow") {
		t.Fatalf("req list --all missing ready requirement:\n%s", allOut)
	}
}

func TestReqArchiveRejectsActiveAndIsIdempotentForCompleted(t *testing.T) {
	home := t.TempDir()
	remote := seedRemote(t)

	runWorkspace(t, home, "init")
	runWorkspace(t, home, "repo", "add", "backend", remote, "--base", "main")
	runWorkspace(t, home, "req", "create", "Payment Flow", "--key", "pay-flow", "--repo", "backend")

	if err := runWorkspaceError(home, "req", "archive", "pay-flow"); err == nil {
		t.Fatal("req archive succeeded for active requirement, want error")
	}
	runWorkspace(t, home, "req", "finish", "pay-flow", "-m", "feat: finish")
	if err := runWorkspaceError(home, "req", "archive", "pay-flow"); err == nil {
		t.Fatal("req archive succeeded for ready requirement, want error")
	}
	runWorkspace(t, home, "release", "create", "2026-07-01 Release", "--key", "2026-07-01", "--req", "pay-flow")
	runWorkspace(t, home, "release", "integrate", "2026-07-01")
	runWorkspace(t, home, "release", "publish", "2026-07-01", "--tested")
	runWorkspace(t, home, "req", "archive", "pay-flow")
	runWorkspace(t, home, "req", "archive", "pay-flow")
}

func TestCLIReleaseFlowPublishesReadyRequirement(t *testing.T) {
	home := t.TempDir()
	remote := seedRemote(t)

	runWorkspace(t, home, "init")
	runWorkspace(t, home, "repo", "add", "backend", remote, "--base", "main")
	runWorkspace(t, home, "req", "create", "Payment Flow", "--key", "pay-flow", "--repo", "backend")
	worktree := filepath.Join(home, "work", "requirements", "pay-flow", "backend")
	runGit(t, worktree, "config", "user.name", "Workspace Test")
	runGit(t, worktree, "config", "user.email", "workspace@example.com")
	if err := os.WriteFile(filepath.Join(worktree, "pay.txt"), []byte("pay\n"), 0o644); err != nil {
		t.Fatalf("write pay feature: %v", err)
	}
	runWorkspace(t, home, "req", "finish", "pay-flow", "-m", "feat: pay flow")
	runWorkspace(t, home, "release", "create", "2026-07-01 Release", "--key", "2026-07-01", "--req", "pay-flow")
	integrateOut := runWorkspace(t, home, "release", "integrate", "2026-07-01")
	if !strings.Contains(integrateOut, "2026-07-01\tintegrated\t") || !strings.Contains(integrateOut, "release/2026-07-01") {
		t.Fatalf("release integrate output missing integrated release details:\n%s", integrateOut)
	}
	publishOut := runWorkspace(t, home, "release", "publish", "2026-07-01", "--tested")
	if !strings.Contains(publishOut, "2026-07-01\tpublished") {
		t.Fatalf("release publish output missing published release:\n%s", publishOut)
	}

	checkout := filepath.Join(t.TempDir(), "checkout")
	run(t, "", "git", "clone", remote, checkout)
	runGit(t, checkout, "checkout", "main")
	if _, err := os.Stat(filepath.Join(checkout, "pay.txt")); err != nil {
		t.Fatalf("published main missing pay.txt: %v", err)
	}
	allOut := runWorkspace(t, home, "req", "list", "--all")
	if !strings.Contains(allOut, "pay-flow\tcompleted\tcompleted\ttrue\tPayment Flow") {
		t.Fatalf("req list --all missing published completed requirement:\n%s", allOut)
	}
	if !strings.Contains(allOut, "released") {
		t.Fatalf("req list --all missing released completion:\n%s", allOut)
	}
	showOut := runWorkspace(t, home, "req", "show", "pay-flow")
	if !strings.Contains(showOut, "completion:\treleased") {
		t.Fatalf("req show missing released completion:\n%s", showOut)
	}
}

func TestReleaseStatusRefreshesStaleStateAndShowsSnapshots(t *testing.T) {
	home := t.TempDir()
	remote := seedRemote(t)

	runWorkspace(t, home, "init")
	runWorkspace(t, home, "repo", "add", "backend", remote, "--base", "main")
	runWorkspace(t, home, "req", "create", "Payment Flow", "--key", "pay-flow", "--repo", "backend")
	worktree := filepath.Join(home, "work", "requirements", "pay-flow", "backend")
	runGit(t, worktree, "config", "user.name", "Workspace Test")
	runGit(t, worktree, "config", "user.email", "workspace@example.com")
	if err := os.WriteFile(filepath.Join(worktree, "pay.txt"), []byte("pay\n"), 0o644); err != nil {
		t.Fatalf("write pay feature: %v", err)
	}
	runWorkspace(t, home, "req", "finish", "pay-flow", "-m", "feat: pay flow")
	runWorkspace(t, home, "release", "create", "2026-07-01 Release", "--key", "2026-07-01", "--req", "pay-flow")
	runWorkspace(t, home, "release", "integrate", "2026-07-01")

	pushRemoteCommit(t, remote, "feature/pay-flow", "late.txt", "late\n")

	listOut := runWorkspace(t, home, "release", "list")
	if !strings.Contains(listOut, "2026-07-01\tstale") {
		t.Fatalf("release list did not refresh stale state:\n%s", listOut)
	}

	out := runWorkspace(t, home, "release", "status", "2026-07-01")
	for _, want := range []string{
		"2026-07-01\tstale",
		"stale\tfeature branch changed after integration for requirement pay-flow repo backend",
		"req\tpay-flow\tactive",
		"repo\tbackend\tintegrated\tmain\t",
		"feature\tpay-flow\tbackend\tfeature/pay-flow\t",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("release status output missing %q:\n%s", want, out)
		}
	}
}

func TestReleaseStatusShowsManualReasonDuringPublishInProgress(t *testing.T) {
	home := t.TempDir()
	remoteA := seedRemote(t)
	remoteB := seedRemote(t)

	runWorkspace(t, home, "init")
	runWorkspace(t, home, "repo", "add", "backend", remoteA, "--base", "main")
	runWorkspace(t, home, "repo", "add", "frontend", remoteB, "--base", "main")
	runWorkspace(t, home, "req", "create", "Payment Flow", "--key", "pay-flow", "--repo", "backend", "--repo", "frontend")
	for repoName, filename := range map[string]string{"backend": "backend.txt", "frontend": "frontend.txt"} {
		worktree := filepath.Join(home, "work", "requirements", "pay-flow", repoName)
		runGit(t, worktree, "config", "user.name", "Workspace Test")
		runGit(t, worktree, "config", "user.email", "workspace@example.com")
		if err := os.WriteFile(filepath.Join(worktree, filename), []byte(repoName+"\n"), 0o644); err != nil {
			t.Fatalf("write %s feature: %v", repoName, err)
		}
	}
	runWorkspace(t, home, "req", "finish", "pay-flow", "-m", "feat: pay flow")
	runWorkspace(t, home, "release", "create", "2026-07-01 Release", "--key", "2026-07-01", "--req", "pay-flow")
	runWorkspace(t, home, "release", "integrate", "2026-07-01")

	hookPath := filepath.Join(remoteB, "hooks", "pre-receive")
	if err := os.WriteFile(hookPath, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("write rejecting hook: %v", err)
	}
	if err := runWorkspaceError(home, "release", "publish", "2026-07-01", "--tested"); err == nil {
		t.Fatal("release publish succeeded with rejecting second repo, want error")
	}
	seedRemoteBaseCommit(t, remoteA, "main", "external.txt", "external\n")

	listOut := runWorkspace(t, home, "release", "list")
	if !strings.Contains(listOut, "2026-07-01\tintegrated\tpublish-in-progress") {
		t.Fatalf("release list output missing publish-in-progress state:\n%s", listOut)
	}

	out := runWorkspace(t, home, "release", "status", "2026-07-01")
	for _, want := range []string{
		"publish-in-progress\ttrue",
		"manual\tpublished repo backend target branch changed after publish",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("release status output missing %q:\n%s", want, out)
		}
	}
}

func TestReleaseStatusShowsRemovedRequirementFeatureSnapshotName(t *testing.T) {
	home := t.TempDir()
	remote := seedRemote(t)

	runWorkspace(t, home, "init")
	runWorkspace(t, home, "repo", "add", "backend", remote, "--base", "main")
	for _, req := range []struct {
		key      string
		title    string
		filename string
		content  string
	}{
		{key: "pay-flow", title: "Payment Flow", filename: "pay.txt", content: "pay\n"},
		{key: "user-center", title: "User Center", filename: "user.txt", content: "user\n"},
	} {
		runWorkspace(t, home, "req", "create", req.title, "--key", req.key, "--repo", "backend")
		worktree := filepath.Join(home, "work", "requirements", req.key, "backend")
		runGit(t, worktree, "config", "user.name", "Workspace Test")
		runGit(t, worktree, "config", "user.email", "workspace@example.com")
		if err := os.WriteFile(filepath.Join(worktree, req.filename), []byte(req.content), 0o644); err != nil {
			t.Fatalf("write %s feature: %v", req.key, err)
		}
		runWorkspace(t, home, "req", "finish", req.key, "-m", "feat: "+req.key)
	}
	runWorkspace(t, home, "release", "create", "2026-07-01 Release", "--key", "2026-07-01", "--req", "pay-flow", "--req", "user-center")
	runWorkspace(t, home, "release", "integrate", "2026-07-01")
	runWorkspace(t, home, "release", "remove-req", "2026-07-01", "user-center")

	out := runWorkspace(t, home, "release", "status", "2026-07-01")
	for _, want := range []string{
		"req\tuser-center\tremoved",
		"feature\tuser-center\tbackend\tfeature/user-center\t",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("release status output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "feature\t\tbackend") {
		t.Fatalf("release status output has unnamed removed feature snapshot:\n%s", out)
	}
}

func TestDevUnknownToolReturnsError(t *testing.T) {
	home := t.TempDir()
	remote := seedRemote(t)

	runWorkspace(t, home, "init")
	runWorkspace(t, home, "repo", "add", "backend", remote, "--base", "main")
	runWorkspace(t, home, "req", "create", "Payment Flow", "--key", "pay-flow", "--repo", "backend")

	err := runWorkspaceError(home, "dev", "pay-flow", "--tool", "unknown")
	if err == nil || !strings.Contains(err.Error(), "unknown tool") {
		t.Fatalf("dev --tool unknown error = %v, want unknown tool", err)
	}
}

func TestDevRejectsCleanupPendingRequirement(t *testing.T) {
	home := t.TempDir()
	remote := seedRemote(t)
	logPath := filepath.Join(t.TempDir(), "codex.log")
	fakeCodex := writeFakeCommand(t, logPath)

	runWorkspace(t, home, "init")
	replaceConfigLine(t, filepath.Join(home, "config.yaml"), `  codex: "codex"`, `  codex: "`+fakeCodex+`"`)
	runWorkspace(t, home, "repo", "add", "backend", remote, "--base", "main")
	runWorkspace(t, home, "req", "create", "Payment Flow", "--key", "pay-flow", "--repo", "backend")
	markRequirementCleanupPending(t, home, "pay-flow")

	err := runWorkspaceError(home, "dev", "pay-flow", "--tool", "codex")
	if err == nil || !strings.Contains(err.Error(), "cleanup-pending") {
		t.Fatalf("dev cleanup-pending error = %v, want cleanup-pending", err)
	}
	if _, statErr := os.Stat(logPath); !os.IsNotExist(statErr) {
		t.Fatalf("dev should not start tool for cleanup-pending requirement, stat err = %v", statErr)
	}
}

func TestIDECommandDefaultsToVSCodeAndOpensRequirementWorkspace(t *testing.T) {
	home := t.TempDir()
	remote := seedRemote(t)
	logPath := filepath.Join(t.TempDir(), "ide.log")
	fakeIDE := writeFakeCommand(t, logPath)

	runWorkspace(t, home, "init")
	replaceConfigLine(t, filepath.Join(home, "config.yaml"), `  vscode: "code"`, `  vscode: "`+fakeIDE+`"`)
	runWorkspace(t, home, "repo", "add", "backend", remote, "--base", "main")
	runWorkspace(t, home, "req", "create", "Payment Flow", "--key", "pay-flow", "--repo", "backend")

	runWorkspace(t, home, "ide", "pay-flow")

	wantWorkspace := filepath.Join(home, "work", "requirements", "pay-flow")
	assertFakeCommandInvocation(t, logPath, wantWorkspace)
}

func TestIDECommandUsesSelectedTool(t *testing.T) {
	home := t.TempDir()
	remote := seedRemote(t)
	logPath := filepath.Join(t.TempDir(), "cursor.log")
	fakeIDE := writeFakeCommand(t, logPath)

	runWorkspace(t, home, "init")
	replaceConfigLine(t, filepath.Join(home, "config.yaml"), `  cursor: "cursor"`, `  cursor: "`+fakeIDE+`"`)
	runWorkspace(t, home, "repo", "add", "backend", remote, "--base", "main")
	runWorkspace(t, home, "req", "create", "Payment Flow", "--key", "pay-flow", "--repo", "backend")

	runWorkspace(t, home, "ide", "pay-flow", "--tool", "cursor")

	wantWorkspace := filepath.Join(home, "work", "requirements", "pay-flow")
	assertFakeCommandInvocation(t, logPath, wantWorkspace)
}

func TestIDERejectsCleanupPendingRequirement(t *testing.T) {
	home := t.TempDir()
	remote := seedRemote(t)
	logPath := filepath.Join(t.TempDir(), "ide.log")
	fakeIDE := writeFakeCommand(t, logPath)

	runWorkspace(t, home, "init")
	replaceConfigLine(t, filepath.Join(home, "config.yaml"), `  vscode: "code"`, `  vscode: "`+fakeIDE+`"`)
	runWorkspace(t, home, "repo", "add", "backend", remote, "--base", "main")
	runWorkspace(t, home, "req", "create", "Payment Flow", "--key", "pay-flow", "--repo", "backend")
	markRequirementCleanupPending(t, home, "pay-flow")

	err := runWorkspaceError(home, "ide", "pay-flow")
	if err == nil || !strings.Contains(err.Error(), "cleanup-pending") {
		t.Fatalf("ide cleanup-pending error = %v, want cleanup-pending", err)
	}
	if _, statErr := os.Stat(logPath); !os.IsNotExist(statErr) {
		t.Fatalf("ide should not start tool for cleanup-pending requirement, stat err = %v", statErr)
	}
}

func TestIDEUnknownToolReturnsError(t *testing.T) {
	home := t.TempDir()
	remote := seedRemote(t)

	runWorkspace(t, home, "init")
	runWorkspace(t, home, "repo", "add", "backend", remote, "--base", "main")
	runWorkspace(t, home, "req", "create", "Payment Flow", "--key", "pay-flow", "--repo", "backend")

	err := runWorkspaceError(home, "ide", "pay-flow", "--tool", "unknown")
	if err == nil || !strings.Contains(err.Error(), `unknown ide tool "unknown"`) {
		t.Fatalf("ide --tool unknown error = %v, want unknown ide tool", err)
	}
}

func TestWorkspaceCLIHomeEnvIsUsedWhenHomeFlagMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WORKSPACE_CLI_HOME", home)

	cmd := NewRootCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"init"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("workspace init with WORKSPACE_CLI_HOME error = %v\n%s", err, out.String())
	}
	if _, err := os.Stat(filepath.Join(home, "config.yaml")); err != nil {
		t.Fatalf("config.yaml should be created under WORKSPACE_CLI_HOME: %v", err)
	}
}

func runWorkspace(t *testing.T, home string, args ...string) string {
	t.Helper()
	cmd := NewRootCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(append([]string{"--home", home}, args...))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("workspace %v error = %v\n%s", args, err, out.String())
	}
	return out.String()
}

func runWorkspaceError(home string, args ...string) error {
	cmd := NewRootCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(append([]string{"--home", home}, args...))
	return cmd.Execute()
}

func writeFakeCommand(t *testing.T, logPath string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-ide")
	script := "#!/bin/sh\npwd > " + shellQuote(logPath) + "\nprintf '%s\\n' \"$@\" >> " + shellQuote(logPath) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake command: %v", err)
	}
	return path
}

func markRequirementCleanupPending(t *testing.T, home, key string) {
	t.Helper()
	db, err := store.Open(filepath.Join(home, "workspace.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(t.Context()); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	req, err := db.GetRequirement(t.Context(), key)
	if err != nil {
		t.Fatalf("GetRequirement(%s) error = %v", key, err)
	}
	rels, err := db.ListRequirementRepos(t.Context(), req.ID)
	if err != nil {
		t.Fatalf("ListRequirementRepos() error = %v", err)
	}
	if len(rels) == 0 {
		t.Fatalf("requirement %s has no repos", key)
	}
	if err := db.UpdateRequirementRepoStatus(t.Context(), rels[0].ID, store.RequirementRepoStatusPushed); err != nil {
		t.Fatalf("UpdateRequirementRepoStatus(pushed) error = %v", err)
	}
}

func assertFakeCommandInvocation(t *testing.T, logPath, workspacePath string) {
	t.Helper()
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read fake command log: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("fake command log lines = %v, want cwd and workspace arg", lines)
	}
	if lines[0] != workspacePath {
		t.Fatalf("fake command cwd = %q, want %q", lines[0], workspacePath)
	}
	if lines[1] != workspacePath {
		t.Fatalf("fake command arg = %q, want %q", lines[1], workspacePath)
	}
}

func replaceConfigLine(t *testing.T, path, oldLine, newLine string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	updated := strings.Replace(string(data), oldLine, newLine, 1)
	if updated == string(data) {
		t.Fatalf("config line %q not found in:\n%s", oldLine, data)
	}
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func seedRemote(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	run(t, "", "git", "init", "--bare", remote)
	seed := filepath.Join(root, "seed")
	run(t, "", "git", "clone", remote, seed)
	runGit(t, seed, "checkout", "-b", "main")
	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("# seed\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	runGit(t, seed, "add", "README.md")
	run(t, seed, "git", "-c", "user.name=Workspace Test", "-c", "user.email=workspace@example.com", "commit", "-m", "init")
	runGit(t, seed, "push", "origin", "main")
	return remote
}

func seedRemoteBaseCommit(t *testing.T, remote, branch, filename, content string) string {
	t.Helper()
	root := t.TempDir()
	seed := filepath.Join(root, "base-seed")
	run(t, "", "git", "clone", remote, seed)
	runGit(t, seed, "checkout", "-B", branch, "origin/"+branch)
	if err := os.WriteFile(filepath.Join(seed, filename), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", filename, err)
	}
	runGit(t, seed, "add", filename)
	run(t, seed, "git", "-c", "user.name=Workspace Test", "-c", "user.email=workspace@example.com", "commit", "-m", "base update")
	runGit(t, seed, "push", "origin", branch)
	return strings.TrimSpace(runGitOutput(t, seed, "rev-parse", "HEAD"))
}

func pushRemoteCommit(t *testing.T, remote, branch, filename, content string) string {
	t.Helper()
	root := t.TempDir()
	seed := filepath.Join(root, "branch-seed")
	run(t, "", "git", "clone", remote, seed)
	runGit(t, seed, "checkout", "-B", branch, "origin/"+branch)
	if err := os.WriteFile(filepath.Join(seed, filename), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", filename, err)
	}
	runGit(t, seed, "add", filename)
	run(t, seed, "git", "-c", "user.name=Workspace Test", "-c", "user.email=workspace@example.com", "commit", "-m", "branch update")
	runGit(t, seed, "push", "origin", branch)
	return strings.TrimSpace(runGitOutput(t, seed, "rev-parse", "HEAD"))
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	run(t, dir, "git", args...)
}

func runGitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v in %s failed: %v", args, dir, err)
	}
	return string(out)
}

func run(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v in %s failed: %v\n%s", name, args, dir, err, out)
	}
}
