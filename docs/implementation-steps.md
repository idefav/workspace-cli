# workspace-cli 实现步骤记录

## Step 1: 基线盘点与测试校准

- 当前目录不是 Git 仓库，无法创建隔离 git worktree；实现直接在当前 workspace 目录推进。
- 当前代码只有文档、`go.mod` 和测试文件，尚无生产实现。
- 首次执行 `go test ./...` 因默认 Go cache 写入 `~/Library/Caches/go-build` 被沙箱拒绝失败；后续统一使用 `GOCACHE=/private/tmp/workspace-cli-gocache`。
- 使用隔离 cache 执行 `go test ./...` 后进入预期红灯：缺少 `internal/config`、`internal/store`、`internal/git`、`internal/workspace` 的生产代码。
- 现有测试中仍有旧状态模型断言，需校准为当前文档语义：需求完成后 `status=completed`，并写入 `completed_at` 与 `archived_at`。

## Step 2: 配置与 SQLite store 基础实现

- 新增 `internal/config` 的默认配置、初始化和加载能力；`Init` 会创建 `config.yaml`、`workspace.db`、`work/repos`、`work/requirements`。
- 新增 `internal/store` 的 SQLite 迁移和基础 CRUD，包含 `repos`、`requirements`、`requirement_repos`、`operation_logs`。
- `requirement_repos` 在绑定时写入 repo 快照字段；需求完成写入 `status=completed`、`completed_at`、`archived_at`。
- 新增 `modernc.org/sqlite` 依赖并生成 `go.sum`；由于沙箱限制，依赖解析使用 `GOMODCACHE=/private/tmp/workspace-cli-gomodcache` 和 `GOSUMDB=off`。
- 验证命令：`env GOCACHE=/private/tmp/workspace-cli-gocache GOMODCACHE=/private/tmp/workspace-cli-gomodcache GOSUMDB=off go test ./internal/config ./internal/store`，结果通过。

## Step 3: Slug、Git manager 与需求服务纵向路径

- 新增 `Slugify` 与 `FeatureBranch`；ASCII 标题转小写短横线，非 ASCII 标题使用稳定 fallback：`req-<sha1(input)前8位>`。
- 现有 slug 测试中的非 ASCII 期望值没有文档来源，已校准为 SHA-1 fallback 的 `req-5ec9da01`。
- 新增 `internal/git.Manager`，封装 bare clone、fetch、worktree add/remove、状态检查、commit 身份检查、commit、push。
- 新增 `internal/workspace.Service`，实现 `AddRepo`、`CreateRequirement`、`FinishRequirement` 的最小纵向路径。
- 验证命令：`env GOCACHE=/private/tmp/workspace-cli-gocache GOMODCACHE=/private/tmp/workspace-cli-gomodcache GOSUMDB=off go test ./internal/workspace`，结果通过。

## Step 4: 需求可变更 guard 与 CLI 命令面

- 新增服务方法：`UpdateRequirement`、`AddRepoToRequirement`、`ArchiveRequirement`、`ListRequirements`、`GetRequirement`、`ListRepos`。
- 普通 active 需求定义在服务层统一执行：`status=active`、`archived_at` 为空、且没有 `pushed|cleanup_failed` relation。
- completed-but-unarchived 与已归档 completed 需求执行 `update`、`add-repo` 均返回错误；已归档 completed 再次 `archive` 返回成功 no-op。
- 新增 `repo sync/update/remove` 的基础 service 支撑；`repo remove` 只写 `repos.deleted_at`，`repo update` 被 active 或 cleanup-pending 引用时拒绝。
- 新增 Cobra CLI：`workspace init`、`repo add/list/sync/update/remove`、`req create/list/show/update/add-repo/archive/finish`、`dev`。
- 验证命令：`env GOCACHE=/private/tmp/workspace-cli-gocache GOMODCACHE=/private/tmp/workspace-cli-gomodcache GOSUMDB=off go test ./...`，结果通过。

## Step 5: cleanup-pending finish 重试测试

- 新增集成测试覆盖 `pushed` relation 的 worktree path 已缺失时，`FinishRequirement` 自愈为 relation `completed` 并完成需求归档。
- 新增集成测试覆盖 cleanup-pending worktree 存在未提交变更时，`FinishRequirement` 返回错误且保留 worktree。
- 验证命令：`env GOCACHE=/private/tmp/workspace-cli-gocache GOMODCACHE=/private/tmp/workspace-cli-gomodcache GOSUMDB=off go test ./internal/workspace`，结果通过。
- 全量验证命令：`env GOCACHE=/private/tmp/workspace-cli-gocache GOMODCACHE=/private/tmp/workspace-cli-gomodcache GOSUMDB=off go test ./...`，结果通过。

## Step 6: push 失败状态机修复

- 自审发现 `FinishRequirement` 在每个 repo push 成功后立即持久写入 `pushed`，不符合“全部 push 成功后再批量写入 `pushed`”的文档语义。
- 新增两 repo 集成测试：第一个 repo push 成功、第二个 repo push 失败时，所有 relation 状态仍保持 `active`，worktree 不进入 cleanup。
- 修复 `FinishRequirement`：先收集本轮成功 push 的 relation ID，只有所有 active relation 都 push 成功后，再批量更新为 `pushed`。
- 验证命令：`env GOCACHE=/private/tmp/workspace-cli-gocache GOMODCACHE=/private/tmp/workspace-cli-gomodcache GOSUMDB=off go test ./internal/workspace -run TestFinishPushFailureDoesNotPersistPushedStatus`，结果通过。

## Step 7: 远端 feature 分支与 operation log

- 新增集成测试覆盖远端已存在 `feature/<req-slug>` 时，创建需求 worktree 能看到远端 feature 分支内容。
- 当前 Git 实现通过 bare clone/fetch 保留远端 feature head，测试验证该路径已可用。
- 新增 operation log 类型和 `LogOperation` / `ListOperationLogs`；push 失败时写入 `operation=push`、`status=failed`。
- 扩展 push 失败状态机测试，确认失败后不仅 relation 保持 `active`，也能查询到失败 operation log。
- 扩展 finish 失败日志：工作区状态检查、commit 身份预检、commit、push、cleanup 失败都会尽力写入 `operation_logs`。
- 验证命令：`env GOCACHE=/private/tmp/workspace-cli-gocache GOMODCACHE=/private/tmp/workspace-cli-gomodcache GOSUMDB=off go test ./...`，结果通过。
- 静态检查：`env GOCACHE=/private/tmp/workspace-cli-gocache GOMODCACHE=/private/tmp/workspace-cli-gomodcache GOSUMDB=off go vet ./...`，结果通过。

## Step 8: repo 默认分支探测

- 自审发现技术方案列出 `DefaultBranch(barePath, remote)`，但 `AddRepo` 未传 `--base` 时仍直接 fallback 到 `main`。
- 新增 `internal/git.Manager.DefaultBranch`，使用 `git ls-remote --symref <remote> HEAD` 探测远端默认分支。
- `Service.AddRepo` 在 clone/fetch 后执行默认分支探测；探测失败时保守 fallback 到 `main`。
- 验证命令：`env GOCACHE=/private/tmp/workspace-cli-gocache GOMODCACHE=/private/tmp/workspace-cli-gomodcache GOSUMDB=off go test ./...`，结果通过。
- 静态检查：`env GOCACHE=/private/tmp/workspace-cli-gocache GOMODCACHE=/private/tmp/workspace-cli-gomodcache GOSUMDB=off go vet ./...`，结果通过。

## Step 9: repo 注册后出现的远端 feature 分支

- 新增集成测试覆盖 repo 已注册后，远端才出现 `feature/<req-slug>` 的场景。
- 修复 worktree base ref 选择：先检查 `refs/remotes/<remote>/<feature-branch>`，存在则从远端 feature 分支创建本地 worktree 分支；否则从 `<remote>/<base_branch>` 创建。
- 新增 `internal/git.Manager.RemoteBranchExists`。
- 验证命令：`env GOCACHE=/private/tmp/workspace-cli-gocache GOMODCACHE=/private/tmp/workspace-cli-gomodcache GOSUMDB=off go test ./internal/workspace -run TestCreateRequirementUsesRemoteFeatureBranchCreatedAfterRepoAdd`，结果通过。

## Step 10: CLI 端到端主流程

- 新增 CLI 端到端测试，直接通过 Cobra root command 执行 `init -> repo add -> req create -> req finish`。
- 测试使用临时 bare remote，完成后验证 worktree 已删除、远端 `feature/pay-flow` 已存在。
- 验证命令：`env GOCACHE=/private/tmp/workspace-cli-gocache GOMODCACHE=/private/tmp/workspace-cli-gomodcache GOSUMDB=off go test ./internal/cli -run TestCLIEndToEndCreateAndFinishRequirement`，结果通过。
- 全量验证命令：`env GOCACHE=/private/tmp/workspace-cli-gocache GOMODCACHE=/private/tmp/workspace-cli-gomodcache GOSUMDB=off go test ./...`，结果通过。
- 静态检查：`env GOCACHE=/private/tmp/workspace-cli-gocache GOMODCACHE=/private/tmp/workspace-cli-gomodcache GOSUMDB=off go vet ./...`，结果通过。

## Step 11: repo update/remove 行为补强

- 新增集成测试覆盖 `UpdateRepo` 的 URL only、remote only + base branch 更新规则，并验证 bare repo remote 配置同步。
- 新增集成测试覆盖 repo 被 active 需求引用时，`UpdateRepo` 和 `RemoveRepo` 都返回错误。
- 新增集成测试覆盖未引用 repo 执行 `RemoveRepo` 后只写 `deleted_at`，默认列表隐藏，`--all` 仍可查看。
- 验证命令：`env GOCACHE=/private/tmp/workspace-cli-gocache GOMODCACHE=/private/tmp/workspace-cli-gomodcache GOSUMDB=off go test ./internal/workspace -run 'TestUpdateRepoURLRemoteAndBaseBranch|TestRepoUpdateAndRemoveRejectActiveRequirementReference|TestRemoveRepoSoftDeletesUnreferencedRepo'`，结果通过。
- 全量验证命令：`env GOCACHE=/private/tmp/workspace-cli-gocache GOMODCACHE=/private/tmp/workspace-cli-gomodcache GOSUMDB=off go test ./...`，结果通过。
- 静态检查：`env GOCACHE=/private/tmp/workspace-cli-gocache GOMODCACHE=/private/tmp/workspace-cli-gomodcache GOSUMDB=off go vet ./...`，结果通过。

## Step 12: req show 展示 repo 绑定快照

- 自审发现 `req show` 只输出需求基本字段，未展示文档要求的绑定 repo、repo 快照和 worktree path。
- 新增 CLI 测试覆盖 `req show` 输出 `repo`、`url`、`remote`、`base`、`worktree`。
- 新增 service 方法 `ListRequirementRepos`，CLI 使用 `requirement_repos.repo_*` 快照字段输出历史信息，不读取当前 repo 元数据污染展示。
- 验证命令：`env GOCACHE=/private/tmp/workspace-cli-gocache GOMODCACHE=/private/tmp/workspace-cli-gomodcache GOSUMDB=off go test ./internal/cli -run TestReqShowIncludesRepoSnapshots`，结果通过。
- 全量验证命令：`env GOCACHE=/private/tmp/workspace-cli-gocache GOMODCACHE=/private/tmp/workspace-cli-gomodcache GOSUMDB=off go test ./...`，结果通过。
- 静态检查：`env GOCACHE=/private/tmp/workspace-cli-gocache GOMODCACHE=/private/tmp/workspace-cli-gomodcache GOSUMDB=off go vet ./...`，结果通过。

## Step 13: Review 5 cleanup-pending 状态机修复

- 根据 Delta review 和本地红测，拆分 `FinishRequirement` 为普通 active 路径与 cleanup-pending cleanup-only 路径。
- 普通 active 路径先完成所有 repo 的状态检查、commit 身份预检、commit 和 push；只有全部 push 成功后，使用事务批量将 relation 状态写为 `pushed`。
- cleanup-pending 路径不执行 commit/push；遇到仍为 `active` 的 relation 时返回可恢复错误并保留 cleanup-pending 状态。
- cleanup 重试时先显式检查 worktree path：不存在则自愈为 `completed`；存在但 `HasChanges` 失败则写 `cleanup_failed` 和 operation log，并停止删除；dirty worktree 拒绝删除且保留原状态。
- 完成前重新读取 relation，只有全部 relation 都为 `completed` 才写入需求 `status=completed`、`completed_at`、`archived_at`。
- 补充测试覆盖 cleanup-pending 不 commit/push active relation、status 检查失败不删除 worktree、completed-but-unarchived 不可修改、cleanup-pending 修改锁和 repo update/remove 拒绝、remote+URL 同时更新。
- 针对验证命令：`env GOCACHE=/private/tmp/workspace-cli-gocache GOMODCACHE=/private/tmp/workspace-cli-gomodcache GOSUMDB=off go test ./internal/workspace -run 'TestFinishCleanupPendingDoesNotCommitOrPushActiveRelations|TestFinishCleanupPendingStopsWhenStatusCheckFails|TestFinishCleanupPendingSelfHealsMissingWorktree|TestFinishCleanupPendingRejectsDirtyWorktree'`，结果通过。
- 全量验证命令：`env GOCACHE=/private/tmp/workspace-cli-gocache GOMODCACHE=/private/tmp/workspace-cli-gomodcache GOSUMDB=off go test ./...`，结果通过。
- 静态检查：`env GOCACHE=/private/tmp/workspace-cli-gocache GOMODCACHE=/private/tmp/workspace-cli-gomodcache GOSUMDB=off go vet ./...`，结果通过。

## Step 14: 完整目标审计补漏

- 按需求文档和技术方案逐项审计当前实现，发现两个剩余缺口：`internal/git` 缺少技术方案列出的 `BranchInUse` 封装；创建/追加需求时 fetch 或 worktree 失败未写入 `operation_logs`。
- 先补红测：`TestBranchInUseDetectsCheckedOutWorktreeBranch`、`TestCreateRequirementLogsFetchFailure`、`TestCreateRequirementLogsWorktreeFailure`。初次运行结果为预期失败：`BranchInUse` 未定义，fetch/worktree 失败日志为空。
- 新增 `git.Manager.BranchInUse`，通过 `git worktree list --porcelain` 判断本地分支是否已被其他 worktree 使用；`CreateWorktree` 在复用本地 feature 分支前显式返回可恢复错误。
- `CreateRequirement` 和 `AddRepoToRequirement` 在 fetch 或 worktree 创建失败时写入 `operation_logs`，operation 分别为 `fetch` 和 `worktree`。
- 补充 `TestAddRepoToRequirementLogsWorktreeFailure`，覆盖追加 repo 的 worktree 失败日志路径。
- 针对验证命令：`env GOCACHE=/private/tmp/workspace-cli-gocache GOMODCACHE=/private/tmp/workspace-cli-gomodcache GOSUMDB=off go test ./internal/git ./internal/workspace -run 'TestBranchInUseDetectsCheckedOutWorktreeBranch|TestCreateRequirementLogsFetchFailure|TestCreateRequirementLogsWorktreeFailure|TestAddRepoToRequirementLogsWorktreeFailure'`，结果通过。
- 全量验证命令：`env GOCACHE=/private/tmp/workspace-cli-gocache GOMODCACHE=/private/tmp/workspace-cli-gomodcache GOSUMDB=off go test ./...`，结果通过。
- 静态检查：`env GOCACHE=/private/tmp/workspace-cli-gocache GOMODCACHE=/private/tmp/workspace-cli-gomodcache GOSUMDB=off go vet ./...`，结果通过。

## Step 15: Review 7 非阻塞测试增强

- Review 7 approved 后，根据非阻塞建议补充测试可追踪性，不改生产代码语义。
- 新增 `TestAddRepoToRequirementLogsFetchFailure`，直接覆盖追加 repo 时 fetch 失败会写 `operation=fetch`、`status=failed` 的 operation log。
- 对 create/add-repo 的 fetch/worktree 失败日志测试补充字段断言：`requirement_id`、`repo_id` 和 `message` 必须存在且匹配。
- 新增测试 helper `assertFailedOperationLogFields`，集中断言 operation log 的审计字段。
- 针对验证命令：`env GOCACHE=/private/tmp/workspace-cli-gocache GOMODCACHE=/private/tmp/workspace-cli-gomodcache GOSUMDB=off go test ./internal/workspace -run 'TestCreateRequirementLogsFetchFailure|TestCreateRequirementLogsWorktreeFailure|TestAddRepoToRequirementLogsFetchFailure|TestAddRepoToRequirementLogsWorktreeFailure'`，结果通过。
- 全量验证命令：`env GOCACHE=/private/tmp/workspace-cli-gocache GOMODCACHE=/private/tmp/workspace-cli-gomodcache GOSUMDB=off go test ./...`，结果通过。
- 静态检查：`env GOCACHE=/private/tmp/workspace-cli-gocache GOMODCACHE=/private/tmp/workspace-cli-gomodcache GOSUMDB=off go vet ./...`，结果通过。

## Step 16: 最终完成审计与构建验证

- 完成审计范围：对照 `docs/requirements-planning.md` 与 `docs/technical-implementation-plan.md`，检查 CLI 命令面、配置目录、SQLite schema、repo/requirement service、Git worktree 策略、finish 状态机、operation log、agent 启动、测试覆盖和 Review 记录。
- 审计结论：未发现新的 blocking 缺口；Review 8 已确认 Review 7 的非阻塞测试建议闭环。
- 最终全量验证命令：`env GOCACHE=/private/tmp/workspace-cli-gocache GOMODCACHE=/private/tmp/workspace-cli-gomodcache GOSUMDB=off go test ./...`，结果通过。
- 最终静态检查命令：`env GOCACHE=/private/tmp/workspace-cli-gocache GOMODCACHE=/private/tmp/workspace-cli-gomodcache GOSUMDB=off go vet ./...`，结果通过。
- CLI 构建验证命令：`env GOCACHE=/private/tmp/workspace-cli-gocache GOMODCACHE=/private/tmp/workspace-cli-gomodcache GOSUMDB=off go build -o /private/tmp/workspace-cli-build/workspace ./cmd/workspace`，结果通过。
- CLI 启动验证命令：`/private/tmp/workspace-cli-build/workspace --help`，结果通过并展示 `init`、`repo`、`req`、`dev` 命令。

## Step 17: CreateRequirement all-or-nothing 修复

- Review 9 发现 `CreateRequirement` 失败后会留下半成品需求；先补红测覆盖 fetch 失败不留 requirement、同 key 可重试、多 repo 中途 worktree 失败清理已创建 worktree、add-repo relation 失败清理刚创建的 worktree。
- 新增 store 补偿 API：`DeleteRequirementRepo`、`DeleteRequirement`，仅供创建失败补偿使用，不暴露 CLI 删除需求能力。
- `CreateRequirement` 现在跟踪已创建 worktree 和 relation；任一 repo fetch/worktree/relation 失败时记录失败 operation log，反向移除已创建 worktree、删除 relation、删除 requirement，并尝试删除空 workspace 目录。
- `AddRepoToRequirement` 在 relation 写入失败时记录 `operation=relation`，并移除刚创建的 worktree；已有需求保持 active。
- 文档澄清 operation log v1 语义：关键失败操作必须记录，成功日志保留为后续增强。
- 补强 CLI 测试：`repo list --all`、`req list --all`、`req archive`、`dev --tool unknown`、`WORKSPACE_CLI_HOME`。
- 针对验证命令：`env GOCACHE=/private/tmp/workspace-cli-gocache GOMODCACHE=/private/tmp/workspace-cli-gomodcache GOSUMDB=off go test ./internal/workspace -run 'TestCreateRequirementLogsFetchFailure|TestCreateRequirementFetchFailureDoesNotLeaveRequirement|TestCreateRequirementLogsWorktreeFailure|TestCreateRequirementWorktreeFailureCleansPartialRepoState|TestAddRepoToRequirementRelationFailureRemovesCreatedWorktree'`，结果通过。
- CLI 针对验证命令：`env GOCACHE=/private/tmp/workspace-cli-gocache GOMODCACHE=/private/tmp/workspace-cli-gomodcache GOSUMDB=off go test ./internal/cli -run 'TestRepoListAllShowsSoftDeletedRepo|TestReqListAllShowsCompletedAndArchived|TestReqArchiveRejectsActiveAndIsIdempotentForCompleted|TestDevUnknownToolReturnsError|TestWorkspaceCLIHomeEnvIsUsedWhenHomeFlagMissing'`，结果通过。
- 全量验证命令：`env GOCACHE=/private/tmp/workspace-cli-gocache GOMODCACHE=/private/tmp/workspace-cli-gomodcache GOSUMDB=off go test -count=1 ./...`，结果通过。
- 静态检查：`env GOCACHE=/private/tmp/workspace-cli-gocache GOMODCACHE=/private/tmp/workspace-cli-gomodcache GOSUMDB=off go vet ./...`，结果通过。
- CLI 构建验证：`env GOCACHE=/private/tmp/workspace-cli-gocache GOMODCACHE=/private/tmp/workspace-cli-gomodcache GOSUMDB=off go build -o /private/tmp/workspace-cli-review/workspace ./cmd/workspace`，结果通过。

## Step 18: Review 10 非阻塞测试增强

- Review 10 approved 后，根据非阻塞建议补充 `CreateRequirement` 初始绑定 relation 写入失败的 focused 回归测试。
- 新增 `TestCreateRequirementRelationFailureCleansPartialRepoState`：通过 SQLite trigger 让第二个初始 repo 的 relation insert 失败，断言 requirement 行被删除、已创建的 backend/frontend worktree 都被清理、`requirement_repos` 不残留、relation 失败 operation log 保留。
- 新增测试 helper `countRequirementRepos`，用于直接断言创建失败补偿没有留下 relation 行。
- 针对验证命令：`env GOCACHE=/private/tmp/workspace-cli-gocache GOMODCACHE=/private/tmp/workspace-cli-gomodcache GOSUMDB=off go test ./internal/workspace -run 'TestCreateRequirementRelationFailureCleansPartialRepoState|TestCreateRequirementFetchFailureDoesNotLeaveRequirement|TestCreateRequirementWorktreeFailureCleansPartialRepoState|TestAddRepoToRequirementRelationFailureRemovesCreatedWorktree'`，结果通过。
- 全量验证命令：`env GOCACHE=/private/tmp/workspace-cli-gocache GOMODCACHE=/private/tmp/workspace-cli-gomodcache GOSUMDB=off go test -count=1 ./...`，结果通过。
- 静态检查：`env GOCACHE=/private/tmp/workspace-cli-gocache GOMODCACHE=/private/tmp/workspace-cli-gomodcache GOSUMDB=off go vet ./...`，结果通过。
- CLI 构建验证：`env GOCACHE=/private/tmp/workspace-cli-gocache GOMODCACHE=/private/tmp/workspace-cli-gomodcache GOSUMDB=off go build -o /private/tmp/workspace-cli-review/workspace ./cmd/workspace`，结果通过。

## Step 19: GitHub 仓库与 Pages 发布

- 新增项目入口文档 `README.md`，说明 workspace-cli 的定位、核心能力、构建方式、快速开始命令和设计文档入口。
- 新增 GitHub Pages 介绍页 `docs/index.html`、`docs/assets/styles.css` 和 `docs/favicon.svg`，用于展示项目定位、关键流程、命令面和设计原则。
- 初始化本地 Git 仓库，首次提交 `3d0feeea6849bd3ec44f09c6b35a853c1b5d9f09`，创建公开仓库 `idefav/workspace-cli` 并推送 `main`。
- 启用 GitHub Pages，发布源为 `main` 分支 `/docs` 目录，站点地址为 `https://idefav.github.io/workspace-cli/`。
- 本地页面验证：通过本地静态服务器和浏览器检查桌面 `1280x900`、移动 `390x844` 视口，CSS 生效、favicon 可访问、无横向溢出、控制台无错误。
- 发布验证：Pages build 状态为 `built`，首页、`requirements-planning.html`、`technical-implementation-plan.html`、CSS 和 favicon 均返回 HTTP 200。
- 项目验证：`go test -count=1 ./...`、`go vet ./...`、`go build -o /private/tmp/workspace-cli-site/workspace ./cmd/workspace` 均通过。

## Step 20: Release、安装脚本与 CLI 自更新

- 新增 GitHub Actions release workflow：推送 `v*.*.*` tag 时运行测试，构建 `darwin-amd64`、`darwin-arm64`、`linux-amd64`、`linux-arm64`、`windows-amd64` 五个平台包，生成 `checksums.txt` 并发布 GitHub Release。
- 新增 `internal/version`，支持通过 `-ldflags` 注入 `Version`、`Commit`、`Date`；新增 `workspace version` 输出构建元数据。
- 新增 `internal/update`，实现 GitHub latest release 查询、平台 asset 匹配、semver 比较、`checksums.txt` 解析、sha256 校验、tar.gz/zip 解包和当前二进制替换。
- 新增 `workspace update --check` 和 `workspace update`；默认检查 `idefav/workspace-cli`，安装更新前必须校验 release checksum。
- 新增 `docs/install.sh`，用户可复制 `curl -fsSL https://idefav.github.io/workspace-cli/install.sh | sh` 一键安装；默认安装到 `/usr/local/bin/workspace`，支持 `INSTALL_DIR` 覆盖。
- 更新 GitHub Pages 首页和 `README.md`，展示一键安装命令、更新命令和 tag release 说明。
- TDD 记录：先补 `version/update` 命令面测试和 `internal/update` 的 asset 命名、版本比较、release 查询、checksum 解析、下载安装替换测试；红测失败后实现并跑绿。
- 本地验证：`sh -n docs/install.sh`、Ruby YAML 解析 release workflow、`go test -count=1 ./...`、`go vet ./...`、本机 ldflags 构建运行 `workspace version` 均通过。
- 跨平台构建验证：使用与 workflow 等价的 `GOOS/GOARCH` 和 ldflags 本地构建五个平台目标，均成功产出二进制。
- Pages 本地验证：通过静态服务器和浏览器检查桌面 `1280x900`、移动 `390x844` 视口，安装区块可见、安装命令正确、CSS 生效且无横向溢出。

## Step 21: v0.1.0 Release 验证

- 推送 `main` 提交 `dcb931fc8aa2cd28f81706d8354ff9181e04e863` 后，创建并推送 tag `v0.1.0`。
- GitHub Actions run `28408532038` 成功完成，Release workflow 的 test、build archives、publish release 步骤全部通过。
- GitHub Release `v0.1.0` 已发布，包含 `checksums.txt`、`workspace-cli_v0.1.0_darwin_amd64.tar.gz`、`workspace-cli_v0.1.0_darwin_arm64.tar.gz`、`workspace-cli_v0.1.0_linux_amd64.tar.gz`、`workspace-cli_v0.1.0_linux_arm64.tar.gz`、`workspace-cli_v0.1.0_windows_amd64.zip`。
- Pages build 已更新到提交 `dcb931fc8aa2cd28f81706d8354ff9181e04e863`，`https://idefav.github.io/workspace-cli/install.sh` 返回 HTTP 200。
- 一键安装验证：`curl -fsSL https://idefav.github.io/workspace-cli/install.sh | INSTALL_DIR=/private/tmp/workspace-cli-install-test sh` 成功安装 `v0.1.0`，输出 commit `dcb931fc8aa2cd28f81706d8354ff9181e04e863`。
- 更新检查验证：安装后的 `/private/tmp/workspace-cli-install-test/workspace update --check` 输出 `workspace-cli is up to date: v0.1.0`。
- 自更新验证：本地构建 `v0.0.1` 旧版二进制后执行 `workspace update`，成功替换为 release 中的 `v0.1.0`。
