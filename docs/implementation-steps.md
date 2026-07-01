# workspace-cli 实现步骤记录

## Step 1: 基线盘点与测试校准

- 当前目录不是 Git 仓库，无法创建隔离 git worktree；实现直接在当前 workspace 目录推进。
- 当前代码只有文档、`go.mod` 和测试文件，尚无生产实现。
- 首次执行 `go test ./...` 因默认 Go cache 写入 `~/Library/Caches/go-build` 被沙箱拒绝失败；后续统一使用 `GOCACHE=/private/tmp/workspace-cli-gocache`。
- 使用隔离 cache 执行 `go test ./...` 后进入预期红灯：缺少 `internal/config`、`internal/store`、`internal/git`、`internal/workspace` 的生产代码。
- 历史记录：当时测试曾按“finish 直接进入完成态”的旧模型校准；Step 25/26 已废弃该模型，Step 30 进一步收紧为 finish 写 `ready_at`、release publish 写 completed、released 由 published release active association 推导。

## Step 2: 配置与 SQLite store 基础实现

- 新增 `internal/config` 的默认配置、初始化和加载能力；`Init` 会创建 `config.yaml`、`workspace.db`、`work/repos`、`work/requirements`。
- 新增 `internal/store` 的 SQLite 迁移和基础 CRUD，包含 `repos`、`requirements`、`requirement_repos`、`operation_logs`。
- `requirement_repos` 在绑定时写入 repo 快照字段；历史实现曾在 finish 后直接写完成态字段，Step 25/26 已将目标语义改为 release publish 后写入。
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

- 新增集成测试覆盖 `pushed` relation 的 worktree path 已缺失时，`FinishRequirement` 自愈为 relation `completed`；历史实现随后走旧归档路径，Step 25/26 已将目标语义改为写入 `ready_at`。
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
- 历史实现完成前重新读取 relation，并在旧模型中直接写完成态字段；Step 25/26 已将目标语义改为 finish 只写 `ready_at`。
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

## Step 22: Shell Completion 使用说明

- 确认 Cobra 默认提供 `workspace completion` 命令，支持 `bash`、`fish`、`powershell`、`zsh`。
- 更新 `README.md`，新增 Shell Completion 章节，覆盖 zsh、bash、fish、PowerShell 的补全脚本安装方式，并提示可查看各 shell 的 `workspace completion <shell> --help`。
- 更新 GitHub Pages 首页，新增“补全”导航和自动补全说明区块，展示 zsh 与 fish 的常用配置命令，并在命令列表中补充 `workspace completion zsh`。
- 新增 CLI 测试 `TestCompletionCommandIsAvailable`，通过执行 `workspace completion --help` 断言 completion 命令实际可用且列出四种 shell。

## Step 23: IDE 打开命令

- 新增 `workspace ide <key-or-slug> [--tool vscode|cursor|zed]`，默认使用 `vscode`。
- `workspace ide` 读取需求 workspace path，以该路径作为执行目录，并把 workspace path 追加为 IDE 命令参数，例如 `code <workspacePath>`。
- 扩展默认配置 `tools`，新增 `vscode: "code"`、`cursor: "cursor"`、`zed: "zed"`，保持 `codex`、`claude` 配置兼容。
- 新增配置测试覆盖 IDE 默认命令和自定义覆盖；新增 CLI 测试覆盖命令面、默认 VS Code、指定 Cursor、未知 IDE tool 错误。
- 更新 `README.md`、GitHub Pages 首页、需求规划和技术方案，加入 IDE 打开能力与使用示例。
- 针对验证命令：`env GOCACHE=/private/tmp/workspace-cli-gocache GOMODCACHE=/private/tmp/workspace-cli-gomodcache GOSUMDB=off go test ./internal/config ./internal/cli -run 'TestInitCreatesDefaultConfigAndDirectories|TestLoadCustomIDEToolCommands|TestRootCommandIncludesDocumentedCommandSurface|TestIDECommandDefaultsToVSCodeAndOpensRequirementWorkspace|TestIDECommandUsesSelectedTool|TestIDEUnknownToolReturnsError'`，结果通过。
- 全量验证命令：`env GOCACHE=/private/tmp/workspace-cli-gocache GOMODCACHE=/private/tmp/workspace-cli-gomodcache GOSUMDB=off go test -count=1 ./...`，结果通过。
- 静态检查命令：`env GOCACHE=/private/tmp/workspace-cli-gocache GOMODCACHE=/private/tmp/workspace-cli-gomodcache GOSUMDB=off go vet ./...`，结果通过。
- CLI 构建验证：`env GOCACHE=/private/tmp/workspace-cli-gocache GOMODCACHE=/private/tmp/workspace-cli-gomodcache GOSUMDB=off go build -o /private/tmp/workspace-cli-ide/workspace ./cmd/workspace`，结果通过；`/private/tmp/workspace-cli-ide/workspace ide --help` 展示新命令说明。

## Step 24: Base 分支同步语义补强

- 明确“更新 master 最新代码”按 repo 注册时配置的 `base_branch` 处理，不固定为 `master`；`--base main` 使用 `origin/main`，`--base master` 使用 `origin/master`。
- 确认现有实现已满足：`CreateRequirement` 和 `AddRepoToRequirement` 在创建 worktree 前执行 fetch，`workspace repo sync [name]` 对托管 bare repo 执行 fetch。
- 新增 `TestCreateRequirementFetchesLatestBaseBranchBeforeWorktree`，覆盖 repo add 后远端 base branch 追加提交，创建需求时 worktree 使用最新 base commit。
- 新增 `TestSyncRepoFetchesLatestBaseBranch` 和 `TestRepoSyncCommandFetchesSingleRepo`，覆盖 service 和 CLI 手动同步会刷新 `refs/remotes/<remote>/<base_branch>`。
- 更新 `README.md`、需求规划和技术方案，说明创建需求会自动同步 base 最新代码，手动可用 `workspace repo sync [name]`。
- 针对验证命令：`env GOCACHE=/private/tmp/workspace-cli-gocache GOMODCACHE=/private/tmp/workspace-cli-gomodcache GOSUMDB=off go test ./internal/workspace -run 'TestCreateRequirementFetchesLatestBaseBranchBeforeWorktree|TestSyncRepoFetchesLatestBaseBranch'`，结果通过。
- CLI 针对验证命令：`env GOCACHE=/private/tmp/workspace-cli-gocache GOMODCACHE=/private/tmp/workspace-cli-gomodcache GOSUMDB=off go test ./internal/cli -run 'TestRepoSyncCommandFetchesSingleRepo'`，结果通过。
- 全量验证命令：`env GOCACHE=/private/tmp/workspace-cli-gocache GOMODCACHE=/private/tmp/workspace-cli-gomodcache GOSUMDB=off go test -count=1 ./...`，结果通过。
- 静态检查命令：`env GOCACHE=/private/tmp/workspace-cli-gocache GOMODCACHE=/private/tmp/workspace-cli-gomodcache GOSUMDB=off go vet ./...`，结果通过。
- CLI 构建验证：`env GOCACHE=/private/tmp/workspace-cli-gocache GOMODCACHE=/private/tmp/workspace-cli-gomodcache GOSUMDB=off go build -o /private/tmp/workspace-cli-sync/workspace ./cmd/workspace`，结果通过；`/private/tmp/workspace-cli-sync/workspace repo sync --help` 展示现有同步命令说明。

## Step 25: 集成与发布流程文档设计

- 本次只更新文档，不实现 Go 代码、不修改 schema、不新增 CLI 实现。
- 更新需求规划，将目标流程调整为：需求 feature 开发完成后进入可集成状态，多个需求集成到 `release/<release-slug>`，测试通过后发布到每个 repo 配置的 `base_branch`。
- 明确 release branch 是可重建产物；需求加入、需求移出、需求 feature SHA 变化或目标分支出现新 commit 都会让 release stale，必须重新 integrate 并重新测试。
- 明确 release 测试发现 bug 时，必须回 feature 分支修复，再重新完成 feature 开发和 release integrate；不把 release worktree 的直接修改作为正式修复来源。
- 更新技术方案，新增 `workspace release create/list/show/add-req/remove-req/status/integrate/publish` 命令面、release workspace 目录、release 状态模型、SQLite 表设计、Git 删除重建策略、commit graph 发布检查和测试方案。
- 明确发布目标分支使用 repo 配置的 `base_branch`，业务上可称为 master，但实现不硬编码 `master`。
- 明确 publish 判断目标分支是否有新代码只看 commit SHA / commit graph，不读取 MR/PR 记录；publish 成功后 active 集成范围内需求写入 `status=completed`、`completed_at` 和 `archived_at`。

## Step 26: Release 方案 Review 后补漏

- 本次只修复文档和公共说明，不实现 Go 代码、不修改当前 schema 或 CLI 实现。
- 当时曾按两阶段目标调整 README；Step 27 已重新区分当前已实现命令和规划中的 release/reopen 命令，避免 README 误导用户执行未实现能力。
- 补充 `workspace req reopen <key-or-slug>` 方案：ready 需求可恢复 feature worktree，清空 `ready_at`，回到普通 active 修复；active 集成范围包含该需求的未发布 release 标记为 stale。
- 补充 release dirty guard：publish 前 release worktree 必须干净；integrate 删除旧 release worktree 前默认拒绝 dirty，`--force` 才表示丢弃临时测试修改。
- 补充 publish 部分成功重试：已成功 repo 记录 `release_repos.status=published` 和 `published_sha`，retry 跳过已成功 repo，目标分支等于 `published_sha` 不算外部新 commit。
- 补充 `req list/show` 的推导 `stage=active|cleanup-pending|ready|completed` 展示，避免 ready 需求被误认为普通 active。
- 补充 Git 原语使用语义：`force-with-lease` 使用 fetch 后的 expected SHA，merge 冲突保留 release worktree 诊断现场，下一次 integrate 负责删除并重建。

## Step 27: Release 方案 Review 集成修复

- 本次只修复文档和公共说明，不实现 Go 代码、不修改当前 schema 或 CLI 实现。
- README 快速开始在当时恢复为已实现路径：`init -> repo add -> req create -> dev/ide -> req finish`；`workspace release ...` 和 `workspace req reopen` 当时只保留在“规划中的 Release 流程”小节，并明确标注当时版本尚未实现。
- 需求规划补齐 publish 临时 worktree、`published_sha` retry、repo update/remove 锁定、`req reopen` Git 来源和 failed release 可重试语义。
- 技术方案补齐 versioned migration/backfill：旧 `v0.1.0` DB 增加 `requirements.ready_at`，旧 completed/active 数据保持原语义；`MarkRequirementCompleted` 只能由 `PublishRelease` 成功路径调用。
- 技术方案新增 publish 临时 worktree 路径 `~/.workspace-cli/work/releases/<release-slug>/.publish/<repo>`，dirty guard 同时覆盖 release worktree 和 `.publish/<repo>`。
- publish 部分成功 retry 改为使用 `published_sha`：已发布 repo 先校验目标分支 HEAD 等于 `published_sha` 才跳过；该记录中的不相等处理已在 Step 28 收紧为阻塞人工处理。
- repo update/remove 在被普通 active、cleanup-pending、ready 需求或未 published release 引用时都禁止执行；released 与 legacy completed 历史展示继续使用 repo 绑定快照。
- `req reopen` 展示使用 repo 快照，实际 Git 操作通过 `repo_id` 读取当前托管 repo；repo 已软删、bare repo 缺失、feature 分支占用或 worktree path 已存在时返回可恢复错误。
- failed release 允许重新 integrate，published release 禁止重新 integrate；merge 冲突保留诊断 worktree，下一次 integrate 按 dirty/force 规则删除并重建。
- 文档一致性搜索：`rg -n "workspace release|workspace req reopen|规划中|尚未实现|ready_at|published_sha|\\.publish|MarkRequirementCompleted|failed" README.md docs/requirements-planning.md docs/technical-implementation-plan.md docs/implementation-steps.md`，确认 README 的 release/reopen 命令只出现在规划语境。
- 静态检查命令：`git diff --check`，结果通过。
- 当前代码健康检查命令：`env GOCACHE=/private/tmp/workspace-cli-gocache GOMODCACHE=/private/tmp/workspace-cli-gomodcache GOSUMDB=off go test -count=1 ./...`，结果通过。

## Step 28: Release 文档 Review 迁移边界修复

- 本次只修复文档和公共说明，不实现 Go 代码、不修改当前 schema 或 CLI 实现。
- README 在当时规划中的 Release 流程补充迁移边界：两阶段语义会与 `workspace release ...` 和 `workspace req reopen` 同批发布，不会单独改变当时的 `workspace req finish` 行为。
- 需求规划当时明确 release 功能里程碑必须一次性交付 `ready_at`、`req reopen`、`release create/integrate/publish`；在这些命令实现前，当时 finish 仍保持 completed/archive 体验。
- 补充 publish-in-progress 推导态：任一 repo 已 `published` 且 release 尚未整体 published 时，只允许 `show/status/publish retry`，拒绝 `add-req`、`remove-req` 和 `integrate`。
- 收紧部分发布 retry：已发布 repo 的目标分支 HEAD 等于 `published_sha` 才跳过；不相等时阻塞并要求人工处理，不允许重新 integrate 覆盖已发布 repo。
- 补充 `req reopen` all-or-nothing：先完成所有 repo 可行性预检，任一 worktree 创建失败时清理本次已创建 worktree，并保持 `ready_at`、relation status 和 release stale 状态不变。
- 补充 publish 临时 worktree 恢复规则：`.publish/<repo>` 不存在则创建，干净 worktree 则 reset，dirty worktree 或非 worktree 路径则拒绝 publish。
- 技术方案明确 future Go 落点：`store.Requirement` 增加 `ReadyAt sql.NullTime`，所有 requirement SELECT、scanner、CRUD 和 CLI `req list/show` 输出都纳入 `ready_at` 和推导 `stage`。
- 文档一致性搜索：`rg -n 'release 功能里程碑|publish-in-progress|ready_at|ReadyAt|all-or-nothing|\\.publish|MarkRequirementCompleted|workspace release|workspace req reopen' README.md docs/requirements-planning.md docs/technical-implementation-plan.md docs/implementation-steps.md`，确认关键语义已覆盖。
- 旧语义残留搜索：检查旧的“第一阶段迁移”、单独提前发布 ready-only、以及已发布 repo 偏离 `published_sha` 后继续走 commit graph 的表述，未发现旧语义残留。
- 静态检查命令：`git diff --check`，结果通过。
- 当前代码健康检查命令：`env GOCACHE=/private/tmp/workspace-cli-gocache GOMODCACHE=/private/tmp/workspace-cli-gomodcache GOSUMDB=off go test -count=1 ./...`，结果通过。

## Step 29: Release 文档状态机修复

- 本次只修复文档和公共说明，不实现 Go 代码、不修改当前 schema 或 CLI 实现。
- README 补充历史兼容说明：当前版本已经 `req finish` 的需求会保留为 legacy completed，不会在 Release 里程碑中自动视为 released，也不会自动进入 release 集成范围。
- 需求规划拆分当前已实现本地需求开发流程与后续 Release 里程碑目标，避免把 `ready_at`、`release`、`req reopen` 写成当前可用能力。
- 技术方案把旧库 `status=completed` 定义为 legacy completed：不可修改、默认隐藏、不自动变 ready、不自动纳入 release、不自动标记 released。
- 明确 released 是推导语义：只有 completed 需求存在 published release active association 时才展示为 released，不新增 `completed_source` 字段。
- 明确 publish-in-progress 优先级高于 stale：只允许 `show/status/publish retry`，拒绝 `add-req`、`remove-req`、`integrate` 和相关需求 `reopen`。
- publish-in-progress 中若 feature SHA、目标分支 HEAD、active 集成范围变化，或已发布 repo HEAD 偏离 `published_sha`，阻塞并展示人工处理提示，不自动走 stale/reintegrate。
- 更新测试方案，覆盖 legacy completed 迁移、publish-in-progress 人工阻塞、相关需求 reopen 拒绝和非 publish-in-progress stale/reintegrate 正常路径。

## Step 30: Release 文档 active association 补丁

- 本次只修复文档，不实现 Go 代码、不修改 schema、不新增 CLI。
- 明确不新增 `completed_source` 或 membership 字段，继续使用 `release_requirements.removed_at IS NULL` 作为唯一 active release association 定义。
- 需求规划补充 active 集成范围概念：已写入 `removed_at` 的需求只保留历史记录，不参与后续 integrate、publish 或 released 推导。
- 技术方案同步约束 `IntegrateRelease`、`ReleaseStatus` 和 `PublishRelease`：三者都只处理 `removed_at IS NULL` 的 active requirements；`ShowRelease/ReleaseStatus` 需要同时展示 active requirements 和 removed history。
- `PublishRelease` 成功后只将 active requirements 写入 completed/archived；被 `remove-req` 软移出的需求不会随该 release publish 变成 completed 或 released。
- 测试方案补充 removed requirement 不参与 integrate/publish/released 推导，并把 add/remove 后 stale 的断言限定为非 publish-in-progress release。
- publish-in-progress release 继续锁定 add-req、remove-req、integrate 和相关 req reopen，只允许 show/status/publish retry。

## Step 31: Release 文档集成补漏修复

- 本次只修复文档，不实现 Go 代码、不修改当前 schema 或 CLI。
- 技术方案为 `release_requirement_repos` 增加 `release_requirement_id`，指向具体 `release_requirements.id`，解决同一需求 remove 后 re-add 的 feature SHA 快照归属歧义。
- 明确 `release_requirements` 允许保留 removed history，但同一 release 同一 requirement 同一时间只能有一个 active association；后续实现使用 `release_id, requirement_id WHERE removed_at IS NULL` 的 partial unique index 约束。
- 明确 `release_repos` 和 `release_requirement_repos` 是 latest integrate 快照表，不是 append-only audit；每次非 publish-in-progress integrate 都按 active repo union 删除并重建快照。
- `PublishRelease` 前必须校验 `release_repos` 与 active repo union 一致；不一致时非 publish-in-progress release 标记 stale，publish-in-progress release 阻塞人工处理。
- 迁移方案补齐 `schema_migrations(version, name, applied_at)`、`0001_baseline_v0_1_0` 和 `0002_ready_release_flow`，并要求事务内幂等执行。
- 需求规划补充：移出需求独占的 repo 会在下一次 integrate 后退出 release repo scope；移出后再次加入同一需求会生成新的 membership。

## Step 32: Release 基础流程实现

- 新增 versioned migration：`schema_migrations`、`0001_baseline_v0_1_0`、`0002_ready_release_flow`；`requirements.ready_at`、release 相关表、`release_requirement_id` 和 active membership partial unique index 已落库。
- `workspace req finish` 调整为提交、推送、清理 worktree 后写入 `ready_at`；需求最终 `completed_at`、`archived_at` 由 `workspace release publish --tested` 成功后写入。
- 新增 `workspace req reopen`：ready 需求可恢复 feature worktree，成功后清空 `ready_at` 并把 relation 状态恢复为 `active`。
- 新增 release 命令基础链路：`create/list/show/status/add-req/remove-req/integrate/publish`；`integrate` 会重建 release worktree、按 active membership 顺序 merge feature 分支、写最新快照并推送 `release/<slug>`；`publish` 会使用 `.publish/<repo>` 临时 worktree 合并到 base branch，并完成 active requirements。
- 已覆盖测试：schema migration、legacy completed 兼容、membership remove/re-add、CLI release happy path、release integrate/publish Git 集成、finish 后 ready stage 展示、reopen worktree 恢复。
- 验证命令：`go test -count=1 ./...`、`go vet ./...`、`git diff --check`，结果通过。

## Step 33: Release Review 集成问题修复

- 修复 ready 需求可变更漏洞：`UpdateRequirement` 和 `AddRepoToRequirement` 统一通过 `ready_at` guard 拒绝 ready 需求，避免 release 集成范围被绕过修改。
- 修复 `workspace release status` 只读旧状态的问题：CLI status 与 show 一样调用 `RefreshReleaseStatus`，并输出 active/removed requirements、repo publish 状态、base/release SHA、`published_sha` 和 feature SHA 快照。
- 修复 `workspace release publish -m` 不生效的问题：publish 阶段使用 `MergeNoFF`，将 release branch 以用户提供的 message 合并到 base branch。
- 补充 release 失败日志入口：新增 `LogReleaseOperation`，publish push 失败会写入 `release_operation_logs`，用于部分发布失败后的诊断。
- 收紧 release branch 强推：`ForcePushBranch` 接受 integrate 前 fetch 到的 expected SHA，并使用显式 `--force-with-lease=refs/heads/<branch>:<expected-sha>`。

## Step 34: Release 状态展示与引用锁补强

- `workspace release status/show` 增加 diagnostics 输出：始终展示 `publish-in-progress` 推导值；非 publish-in-progress 的 stale release 展示 `stale` 原因；publish-in-progress 中 feature/base/scope 变化展示 `manual` 原因，避免误导用户重新 integrate 覆盖已发布 repo。
- `ReleaseDiagnostics` 复用 active membership、repo union、feature SHA、target branch HEAD 和 `published_sha` 规则，只读计算展示原因，不改变 release status。
- `repo update/remove` 增加未发布 release 引用检查：repo 被 `release_requirements.removed_at IS NULL` 的 active membership 或当前 `release_repos` 快照引用，且 release 尚未 `published` 时拒绝修改或软删除。
- 补充测试覆盖：publish-in-progress 下已发布 repo target branch 偏离 `published_sha` 时 `release status` 输出 manual reason；非 publish-in-progress feature 变化时输出 stale reason；异常数据中 repo 被未发布 release 引用时 update/remove 被拒绝。

## Step 35: Publish 临时路径恢复规则补强

- `release publish` 在复用 `.publish/<repo>` 路径前先确认该路径是 Git worktree；若普通目录或其它非 worktree 路径占用，返回可恢复错误并提示用户移走或删除该路径。
- 非 worktree `.publish/<repo>` 不会被 reset、删除或覆盖，也不会把 release 状态标记为 failed，保持用户可自行修复后重试。
- 补充集成测试覆盖：`.publish/<repo>` 被普通目录占用时 publish 拒绝，release 仍保持 integrated。

## Step 36: Release Review 恢复路径与展示补强

- 修复 publish 首个 repo 失败后的重试卡死：publish 阶段失败写入 `release_operation_logs`，但尚无 repo 成功发布时 release 保持 `integrated`，修复外部原因后可直接 retry；部分成功仍通过 `release_repos.status=published` 推导 publish-in-progress。
- 修复 `req reopen` DB 更新失败后的半恢复风险：relation 回 active、清空 `ready_at`、标记相关 release stale 在 store 层事务内完成；事务失败时服务层清理本次已创建 worktree，保持 ready 状态不变。
- 增加 completed requirement 的 completion 推导：存在 published release active association 时展示 `released`，否则展示 `legacy-completed`；`req list --all` 和 `req show` 均输出该语义。
- `workspace release list` 现在复用 `RefreshReleaseStatus`，非 published release 在列表输出前刷新 stale 状态，避免 feature/base 变化后仍显示旧 `integrated`。
- 补充测试覆盖：首 repo publish 失败可重试、reopen DB 失败 all-or-nothing、released vs legacy completed 推导、release list 刷新 stale。

## Step 37: Publish 目标分支 commit graph 判断

- 新增 `internal/git.Manager.HasNewCommitsSince(barePath, remote, branch, oldSHA)`，通过 `git rev-list --count <oldSHA>..<remote>/<branch>` 判断目标分支是否出现外部新 commit。
- `PublishRelease`、`RefreshReleaseStatus` 和 `ReleaseDiagnostics` 对未发布 repo 使用 commit graph 判断，不再用目标分支 HEAD 与 `integrated_base_sha` 裸相等判断，避免目标分支回退但没有新 commit 时误判 stale。
- 已发布 repo 仍使用目标分支 HEAD 与 `published_sha` 精确相等校验，保证 publish-in-progress 的人工处理语义不变。
- 补充测试覆盖：`HasNewCommitsSince` 对 unchanged/new commit/rewind 的判断，以及目标分支回退但无新 commit 时 release publish 仍可继续。
- 补齐文档列出的 Git 原语 `Checkout` 和 `ResetHard`，并用集成测试覆盖 branch checkout 与 tracked file reset 行为。

## Step 38: Publish 失败 repo 状态落库

- 修复 release publish 失败 repo 状态不落库的问题：非已发布 repo 在 fetch、merge、push、post-push fetch 或 published SHA 读取失败时，写入 `release_repos.status=failed` 并保留 release operation log。
- 首个 repo publish 失败时 release 仍保持 `integrated`，但 repo 行展示为 `failed`，修复外部原因后再次 publish 会继续处理该 failed repo。
- 部分成功场景中，已成功 repo 保持 `published` 和 `published_sha`；失败 repo 记为 `failed`，retry 跳过已发布 repo 并继续发布 failed repo。
- 补充 focused 集成测试覆盖首 repo 失败与第二 repo 失败两条 retry 路径，并同步需求规划和技术方案中的失败状态描述。

## Step 39: Publish push 成功但 DB 标记失败自愈

- 补充 publish 恢复路径：如果目标分支已经被上一次 publish push 到发布 merge commit，但本地 `release_repos.status=published` 或 `published_sha` 写入失败，下一次 publish 不再误判为普通外部新 commit。
- 新增 `internal/git.Manager.CommitHasParentBare`，读取目标分支 HEAD 的父提交；只有当前目标分支 HEAD 的父提交包含本 release SHA 时，才补写该 repo 的 `published` 和 `published_sha`。
- 该自愈条件刻意不使用“release SHA 是祖先”作为判断，避免目标分支在发布 merge 后又有外部新 commit 时被误吞。
- 补充集成测试用 SQLite trigger 强制 published 状态写入失败，验证远端已 push、DB 未标记时 retry 可以补写状态、完成 release 和 requirement。

## Step 40: CreateRelease 失败清理

- 修复 `CreateRelease` 初始 membership 写入中途失败后留下 draft release 的问题。
- 新增 `store.DeleteRelease`，按 release id 删除 release operation log、repo 快照、feature SHA 快照、membership 和 release row，用于创建失败清理。
- `CreateRelease` 在任一初始 `release_requirements` 写入失败时调用 cleanup，并删除本次创建的 release workspace 空目录。
- 补充单元测试用 SQLite trigger 强制第二个 membership 插入失败，验证失败后不留下 release row 或 partial membership。

## Step 41: Release CLI 成功输出

- `workspace release integrate <release>` 成功后输出 release key、status、workspace path 和 release branch，方便用户直接看到测试目录与分支。
- `workspace release publish <release> --tested` 成功后输出 release key 和 published status。
- 补充 CLI 测试覆盖 integrate/publish 成功输出，避免成功路径静默偏离技术方案。

## Step 42: Publish 临时 worktree 覆盖补强

- 补齐 `.publish/<repo>` 临时 worktree 的 clean-existing 与 dirty 行为测试。
- dirty publish worktree 会拒绝 publish，不 push 目标分支，release 保持 integrated。
- 已存在且干净的 publish worktree 可被 publish 恢复/重建并继续发布。
- 结合既有非 worktree 路径测试，覆盖技术方案中 `.publish/<repo>` 的不存在、干净 worktree、dirty worktree、非 worktree 路径四类状态。

## Step 43: 移出需求后的 obsolete repo worktree 清理

- 修复 release 移出需求后重新 integrate 只更新 DB repo scope、但旧独占 repo release worktree 仍留在 workspace 的问题。
- `IntegrateRelease` 会读取上一轮 `release_repos` 快照，计算不再属于 active repo union 的 obsolete repo，执行 dirty guard 后删除旧 release worktree 和本地 release branch。
- 补充集成测试覆盖：release 包含 backend/frontend 两个需求，移出 frontend-only 需求后重新 integrate，`release_repos` 只剩 backend，frontend release worktree 被删除。

## Step 44: Release publish Review 补漏

- 修复 `CreateRequirement` service 层缺少 repo 数量校验的问题；即使绕过 CLI 直接调用 service，也必须至少绑定一个 repo，且失败时不创建 requirement row。
- 修复 publish-in-progress retry 跳过已 published repo dirty guard 的问题；retry 时已发布 repo 的 release worktree 和 `.publish/<repo>` 仍必须保持干净，否则阻止继续发布剩余 repo。
- 修复 `.publish/<repo>` 干净 worktree 的恢复语义：存在且干净时原地 `reset --hard` 到最新目标分支，不再删除重建。
- 修复 release 最终完成落库的原子性：所有 repo 发布成功后，通过 store 事务同时写 release published 和 active requirements completed/archived；若最终落库失败，不会部分完成需求。
- 将 release 级 `target_branch` summary 值改为 `per-repo`，并在技术方案中明确实际发布目标以 `release_repos.target_branch` 为准。
- 补充 focused 测试覆盖：空 repo 创建需求、已发布 repo dirty 后 retry、publish finalization DB 失败不部分完成、干净 publish worktree 原地 reset。

## Step 45: Publish 前 release branch 完整性校验

- 修复 release branch 在 integrate 后被外部改写仍可能被 publish 合并的问题：`PublishRelease` 在真正 merge/push 目标分支前，会校验尚未发布 repo 的远端 `release/<release-slug>` HEAD 等于 `release_repos.release_sha`。
- 若远端 release branch HEAD 与已测试的 `release_sha` 不一致，非 publish-in-progress release 标记 stale 并要求重新 integrate；publish-in-progress release 阻塞并提示人工处理，避免覆盖已发布 repo。
- 保留 DB 写 published 状态失败后的自愈路径：如果目标分支已经是包含本次 `release_sha` 的发布 merge commit，则先补写 `published_sha`，不因 release branch 后续漂移误挡同一次发布恢复。
- 补充 TDD 回归测试：先证明外部 force-push release branch 后旧实现会误发布，再加入 release SHA 预检，验证 publish 拒绝且不 push 未测试内容到 base branch。
- 追加 status/show 覆盖：`RefreshReleaseStatus` 和 `ReleaseDiagnostics` 复用同一 release branch SHA 检查，远端 release branch 漂移时非 publish-in-progress release 会变 stale，并在 diagnostics 中展示 release branch 原因。
- 追加 active membership 健康检查：status/publish 发现 active 集成范围内需求已经不再 ready 或 relation 不再 completed 时，非 publish-in-progress release 标记 stale，publish-in-progress release 阻塞人工处理；补充测试覆盖同一 ready 需求被两个 release 引用、其中一个发布后另一个 release 自动变 stale。

## Step 46: Draft release 范围变更状态修复

- 修复 draft release 执行 `add-req`、`remove-req` 后被错误标记为 stale 的问题；draft 尚未产生集成结果，范围变更后仍保持 draft。
- 修复 `req reopen` 会把包含该需求的 draft release 标记为 stale 的问题；只有已有集成结果的未发布 release 才在 reopen 后进入 stale。
- 已 integrated/stale/failed 且非 publish-in-progress 的 release 范围变化仍会标记 stale，publish-in-progress 继续拒绝 add/remove/reopen。
- 补充 TDD 测试覆盖：draft add/remove/re-add 保持 draft、integrated add/remove 变 stale、draft release 中的 ready requirement reopen 后 release 仍保持 draft。

## Step 47: Release list 发布中状态展示

- 修复 `workspace release list` 只输出 DB status、无法从列表识别 publish-in-progress 的问题。
- `release list` 现在输出 key、DB status、推导 phase 和 title；当任一 repo 已 published 且 release 尚未整体 published 时，phase 显示为 `publish-in-progress`，否则 phase 等于 status。
- 保留 `release show/status` 的详细 diagnostics 输出，用于展示 stale/manual 原因、repo SHA、feature SHA 和 per-repo publish 状态。
- 补充 CLI 回归测试：部分发布失败后，`workspace release list` 必须包含 `integrated	publish-in-progress`。

## Step 48: Release membership 原子性与 cleanup-pending 工具入口修复

- 修复非 draft release 执行 `add-req` / `remove-req` 时 membership 变更和 release 标记 stale 分两步写入的问题。
- 新增 store 层事务方法，确保非 draft release 的 membership 变更和 stale 状态更新要么同时成功，要么同时回滚。
- 补充回归测试：强制 release status 更新失败时，`add-req` 不留下新 active membership，`remove-req` 不提前写入 `removed_at`。
- 修复 cleanup-pending 需求仍可通过 `workspace dev` / `workspace ide` 启动外部工具的问题；CLI 现在在启动前检查 requirement stage，并提示继续执行 `workspace req finish <req>` 完成清理。
- 补充 CLI 回归测试：cleanup-pending 需求执行 `dev` 或 `ide` 会拒绝启动，fake Codex/IDE 命令不会被调用。
- 补充 CreateRelease all-or-nothing 细节：release row 插入失败时删除本次创建的 release workspace 空目录，并用 SQLite trigger 回归测试覆盖该失败窗口。
