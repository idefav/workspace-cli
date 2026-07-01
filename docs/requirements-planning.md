# workspace-cli 需求规划

## 1. 产品定位

workspace-cli 是一个本地命令行工具，用于管理“需求开发空间”。这里的 workspace 不是单个代码仓库，而是围绕一个需求临时组织起来的开发空间。一个需求可以绑定多个 Git repo，workspace-cli 会把这些 repo 的 feature 分支 worktree 集中到同一个需求目录中，方便使用 Codex、Claude Code 或 IDE 进行跨仓库开发。

当前已实现范围管理本地需求开发与 release 发布前集成流程：创建需求、选择 repo、准备 feature worktree、启动开发工具、完成 feature 开发、提交并推送代码、清理 worktree、将多个 ready 需求集成到 release 分支、发布到各 repo 的 base branch 并归档已发布需求。它不接管 PR/MR 创建、CI 编排、代码评审、远端 issue 或权限体系。

Release 流程是在本地需求开发流程之上增加发布前集成：完成 feature 分支开发后，多个 ready 需求集成到可重建的 release 分支，在 release 上测试，最终发布到每个 repo 的发布目标分支。

说明：当前实现采用两阶段完成语义：`FinishRequirement` 只写 `ready_at`，表示 feature 分支可集成；`release publish` 成功后才写入 completed/archived。历史版本中已经通过 `req finish` 直接完成的需求继续保留为 legacy completed。

## 2. 核心概念

- **workspace-cli home**：工具自己的状态目录，默认 `~/.workspace-cli`，保存配置、SQLite 数据库和托管 repo。
- **work 目录**：默认 `~/.workspace-cli/work`，用于存放 bare repo 和需求 workspace。
- **托管 repo**：workspace-cli 注册并管理的 Git 仓库。每个 repo 记录名称、remote URL、remote 名称、base branch 和本地 bare repo 路径。
- **需求**：一次待开发的需求项，包含标题、slug、生命周期状态、归档时间、workspace path、feature branch 和绑定的多个 repo。
- **cleanup-pending**：需求生命周期仍为 `active`，但任一绑定 repo 关系状态为 `pushed` 或 `cleanup_failed` 时推导出的临时锁定态。该状态表示所有 repo 已完成 push，需求只剩 worktree 清理或清理重试。
- **普通活跃需求**：`status=active`、`ready_at` 为空、`archived_at` 为空、且不是 cleanup-pending 的需求。只有普通活跃需求允许修改标题或追加 repo。
- **需求阶段 stage**：从 `status`、`ready_at`、`archived_at` 和 repo 关系状态推导出的展示字段，取值为 `active`、`cleanup-pending`、`ready`、`completed`。它用于 `req list/show`，避免 ready 需求被误认为普通 active；completed 需求额外展示 completion：`released` 或 `legacy-completed`。
- **需求 workspace**：一个需求对应的集中开发目录，默认 `~/.workspace-cli/work/requirements/<req-slug>`。
- **feature 分支**：需求开发分支，默认统一为 `feature/<req-slug>`。同一个需求绑定的所有 repo 使用同一个 feature 分支名。
- **git worktree**：从托管 bare repo 创建出的实际工作目录，放在需求 workspace 下，例如 `.../requirements/pay-flow/backend`。
- **需求开发完成**：对需求绑定的所有 repo 执行检查、提交、推送；全部成功并完成 worktree 清理后，需求进入可集成状态，但不代表已发布完成。
- **release**：一次发布候选集合，包含多个已完成 feature 开发的需求，用于把这些需求集中集成到 release 分支并进行测试。
- **release workspace**：一个 release 对应的集中测试目录，默认 `~/.workspace-cli/work/releases/<release-slug>`，每个相关 repo 在该目录下有一个 release worktree。
- **release branch**：发布候选分支，默认统一为 `release/<release-slug>`。同一个 release 涉及的所有 repo 使用同一个 release 分支名。
- **active release requirement / active 集成范围**：release 当前仍参与集成和发布的需求集合和顺序，数据库上定义为 `release_requirements.removed_at IS NULL`。每次加入 release 都会形成独立 membership；同一需求被移出后再次加入会生成新的 membership。被移出的需求只保留历史记录，不再参与后续 integrate、publish 或 released 推导。
- **集成范围**：默认指 active 集成范围。需求加入、移出或需求 feature 分支代码变化都会让既有 release 集成结果失效；尚未集成过的 draft release 只更新范围，仍保持 draft。
- **stale release**：非 publish-in-progress release 已集成过，但由于 active 集成范围变化、active 需求不再 ready、需求 feature SHA 变化或发布目标分支出现外部新 commit，当前 release 分支不再可信，必须删除并重新集成或调整集成范围。
- **publish-in-progress**：不新增 release status，而是由任一 `release_repos.status=published` 且 release 尚未整体 `published` 推导出的发布中锁定态。该状态只允许查看和继续 publish retry，不允许变更集成范围或重新集成。
- **发布目标分支**：每个 repo 配置的 `base_branch`，业务上可称为 master，但技术实现不硬编码 `master`。
- **released 需求**：需求只有在存在 published release active association 时才进入 released 语义；active association 指 published release 中 `release_requirements.removed_at IS NULL` 的当前 membership。release publish 成功后默认只同步 active 集成范围内需求的 `completed_at` 和 `archived_at`，让已发布需求从默认列表隐藏。
- **legacy completed**：历史版本中通过 `workspace req finish` 直接完成并归档的需求。它保持不可修改和默认隐藏，但不代表已经通过 release 发布到 base branch，不自动变 ready，不自动纳入 release，也不自动展示为 released。

## 3. 功能范围

3.1 到 3.5 覆盖本地需求开发能力，3.6 到 3.8 覆盖 release 集成与发布能力。当前实现已经进入 `finish -> ready -> release publish -> completed/archived` 的两阶段流程。

### 3.1 初始化与配置

- 初始化 workspace-cli home。
- 创建默认配置文件 `config.yaml`。
- 创建 SQLite 数据库并执行迁移。
- 创建 work 目录、repo 目录和 requirements 目录。

### 3.2 Repo 管理

- 注册 repo：指定名称、Git URL、remote 名称和 base branch。
- 首次注册时 clone 为本地 bare repo。
- 同步 repo：对 bare repo 执行 fetch，更新本地 remote tracking refs，例如 `refs/remotes/origin/main` 或 `refs/remotes/origin/master`。
- 更新 repo 元数据：URL、remote、base branch；repo 被普通 active、cleanup-pending、ready 需求引用，或被任何未 published release 引用时，禁止修改这些字段，避免影响已创建 worktree、reopen、集成和发布流程。
- 删除 repo：仅允许删除未被普通 active、cleanup-pending、ready 需求和未 published release 使用的 repo，并通过 `deleted_at` 软删除，保留历史审计信息。
- 列出所有未删除 repo，可通过 `repo list --all` 查看已删除 repo。

### 3.3 需求管理

- 创建需求：指定标题、可选 key/slug，并绑定一个或多个 repo；初始绑定 repo 时写入 `repo_name`、`repo_url`、`repo_remote`、`repo_base_branch` 快照。
- 修改需求：普通活跃需求允许更新标题；cleanup-pending 或 completed 需求不可修改。
- 添加 repo：普通活跃需求允许追加新的 repo，并立即创建该 repo 的 worktree；cleanup-pending 或 completed 需求不可追加 repo。
- 查看需求详情：展示状态、workspace path、feature branch、绑定 repo、repo 绑定快照和 worktree path。历史展示使用绑定快照，不被后续 repo 元数据更新污染。
- 列出需求：默认展示未归档的活跃或 ready 需求。`--all` 展示所有需求，并同时展示生命周期 `status`、推导 `stage` 和是否 `archived`。
- 完成 feature 开发：需求开发完成后只表示 feature 分支已提交和推送，可以进入 release 集成范围；最终 released 由 release 发布成功后推导。
- 重新打开 ready 需求：`workspace req reopen <key-or-slug>` 对已 ready、未 completed 的需求恢复原绑定 repo 的 feature worktree，清空 `ready_at` 并让需求回到普通 active 开发阶段；active 集成范围包含该需求且已有集成结果的非 publish-in-progress、未发布 release 标记为 stale，draft release 保持 draft，用于 release 测试发现 bug 后回 feature 分支修复。reopen 必须 all-or-nothing，任一 repo 恢复失败或 DB 状态更新失败时清理本次已创建 worktree，并保持需求 ready 状态、relation 状态和 release stale 状态不变。若需求已包含在 publish-in-progress release 的 active 集成范围中，reopen 必须拒绝。
- 归档需求：只允许归档已发布完成或 legacy completed 的需求，写入 `archived_at` 后默认不可修改。已归档 completed 需求再次 archive 返回成功 no-op。活跃、待集成或 cleanup-pending 需求执行 archive 返回可恢复错误；不提供 unarchive 或 cancel 命令。

### 3.4 需求 workspace 创建

- 创建需求前先同步相关 repo，确保配置的 base branch remote tracking ref 是最新状态。
- 每个 repo 按 feature 分支选择规则准备 `feature/<req-slug>`。
- 使用 git worktree 把多个 repo 集中到同一个需求 workspace。
- 如果本地 feature 分支已存在且未被其他 worktree 占用，则复用该分支创建 worktree。
- 如果远端 feature 分支已存在但本地不存在，则从 `<remote>/feature/<req-slug>` 创建本地分支并用于 worktree。
- 如果本地和远端 feature 分支都不存在，则从最新的 `<remote>/<base_branch>` 创建本地 feature 分支；`base_branch` 来自 repo 注册配置，不固定为 `master`。
- 如果 worktree path 已存在，或目标分支已被其他 worktree 占用，返回可恢复错误，不覆盖用户文件。

### 3.5 开发工具启动

- 支持 `workspace dev <req> --tool codex|claude`。
- 支持 `workspace ide <req> --tool vscode|cursor|zed`，默认使用 `vscode`。
- 命令进入需求 workspace 后启动配置中的工具命令。
- IDE 命令进入需求 workspace 后，把需求 workspace path 作为参数传给 IDE，只打开需求 workspace 根目录。
- 当前版本和 Release 里程碑都不记录、不恢复、不编排 Codex 或 Claude Code 会话。

### 3.6 需求 feature 开发完成

- 对需求绑定的所有 repo 检查工作区状态。
- 对所有存在变更的 repo 预检 `user.name` 和 `user.email`；任一 repo 缺失身份信息时，在任何 commit、push、cleanup 之前失败，并提示对应 `git config` 修复命令。
- 有变更时执行 `git add -A` 和 commit。
- 无变更时跳过 commit，但仍推送当前 HEAD 到 feature 分支。
- 每个 repo 推送到对应 remote 的 `refs/heads/feature/<req-slug>`；commit/push 阶段不持久推进 repo 关系状态。
- 所有 repo 都推送成功后，批量将 repo 关系状态记为 `pushed`，需求进入 cleanup-pending 锁定态，才开始删除 worktree。
- worktree 全部删除成功后，将 repo 关系状态记为 `completed`，并写入需求 `ready_at`，表示需求可集成，但不写入最终 released 语义。
- 任一 repo commit 或 push 失败时停止后续流程，保留全部 worktree，需求保持 `status=active`，repo 关系状态不进入 `pushed`，并写入操作日志；若某些远端分支已被本轮 push 更新，工具不回滚远端，下一次 finish 重新检查并重试。
- 若所有 repo push 已成功但 worktree 删除失败，将失败 repo 关系状态记为 `cleanup_failed`，保留需求可重试；再次执行 finish 时，不重复提交或重复 push，只处理 `pushed` 或 `cleanup_failed` 的 repo。对仍存在的 worktree，删除前必须确认工作区干净；若存在未提交变更，拒绝清理并保留原状态，提示用户先处理变更。若 worktree path 已不存在，视为该 repo 已完成清理并将关系状态自愈为 `completed`。所有 repo 关系都变为 `completed` 后，需求可进入 release 集成范围。cleanup-pending 期间只允许 `show`、`list`、`finish`，禁止 `update`、`add-repo`、`archive`。

### 3.7 Release 集成

- 创建 release：指定标题、可选 key/slug，并选择一个或多个已完成 feature 开发的需求。
- release 关联 repo 集合由 active 集成范围内需求绑定 repo 的并集推导，不要求每个需求都覆盖所有 repo；移出需求独占的 repo 会在下一次 integrate 后退出 release repo scope。
- release integrate 前同步所有相关 repo，检查需求 feature 分支和目标分支的最新 commit。
- integrate 使用删除并重建语义：旧 release worktree、release 分支和 repo/feature SHA 快照视为可丢弃产物，重新从最新发布目标分支创建 `release/<release-slug>`，再按 active 集成范围顺序 merge 每个需求的 feature 分支。删除旧 release worktree 前先检查 dirty；默认 dirty 时拒绝删除，只有用户显式传 `--force` 才表示丢弃 release worktree 的临时修改。
- 集成成功后，release 进入 `integrated` 状态，用户在 release workspace 上进行测试。
- release 分支不接受长期直接修复；测试发现 bug 时，用户必须用 `workspace req reopen <req>` 回到对应需求的 feature 分支修复、重新完成 feature 开发，然后重新执行 release integrate。若 release 已进入 publish-in-progress，相关需求不能 reopen，必须先走人工处理。
- 需求加入 release、从 release 移出、需求 feature 分支 commit 变化，都会让已有集成结果的非 publish-in-progress release 变为 stale，需要删除并重新集成；draft release 只更新 active 集成范围，仍保持 draft。
- 某个需求临时取消发布时，只从 active 集成范围移出并写入 `release_requirements.removed_at`，不删除需求、不删除 feature 分支；移出后该需求不再参与后续 integrate、publish 或 released 推导。若之后重新加入同一 release，必须创建新的 membership 和新的集成顺序。已有集成结果的非 publish-in-progress release 必须重新集成，重建后的 release repo scope 不再包含被移出需求独占的 repo；draft release 保持 draft。publish-in-progress release 禁止移出需求，必须人工处理。
- merge 冲突时，release integrate 停止并保留可诊断错误；用户需要回 feature 分支修复冲突后重新 integrate。

### 3.8 Release 发布

- 发布前用户必须确认 release 已测试；workspace-cli 不编排 CI，也不判断测试结果。
- 发布前 CLI 必须检查每个 release worktree 干净；dirty 时拒绝发布，提示回 feature 分支修复，不把 release worktree 上的直接修改作为正式代码来源。
- 发布前 CLI fetch 每个相关 repo 的发布目标分支，并用 commit SHA/commit graph 判断目标分支是否出现外部新代码。
- 发布前 CLI 必须确认当前 release repo scope 与 active 集成范围推导出的 repo 并集一致；不一致说明 release 已过期，需要重新 integrate 或进入 publish-in-progress 人工处理。
- 发布前 CLI 必须确认 active 集成范围中的需求仍是 ready、每个 repo relation 仍已完成 cleanup；如果某个需求已被其它 release 发布成 completed，当前 release 必须变 stale 或进入 publish-in-progress 人工处理，不能继续发布。
- 发布使用目标分支临时 worktree，路径为 `~/.workspace-cli/work/releases/<release-slug>/.publish/<repo>`；publish 前创建或重置到最新 `<remote>/<base_branch>`，成功后可清理，失败时保留诊断信息，下次 publish 可重建。
- `.publish/<repo>` 不存在时创建并 checkout/reset 到最新 `<remote>/<base_branch>`；存在且是干净 worktree 时允许 `reset --hard`；存在但 dirty 时拒绝 publish，提示用户手动确认和清理；存在但不是 git worktree 时拒绝 publish，提示移走该路径。
- dirty guard 同时覆盖 release worktree 和 `.publish/<repo>` 临时 worktree。
- “是否有新代码”只看 commit，不看 MR/PR 记录；空 MR/PR 或平台记录不影响判断。
- 如果发布目标分支比本次 integrate 记录的 base SHA 多出外部新 commit，且 release 尚未进入 publish-in-progress，publish 必须阻塞，release 标记为 stale，提示重新 integrate 和重新测试。
- 如果没有外部新 commit，publish 将 release 分支合并到每个 repo 的发布目标分支并 push。
- 任一 repo publish 失败时停止后续 repo，将失败 repo 记为 `release_repos.status=failed` 并写入 release operation log；如果尚无 repo 成功发布，release 保持 `integrated` 以便修复外部原因后直接 retry；已成功 repo 标记为 `published`、记录 `published_sha` 为成功 push 后 `<remote>/<base_branch>` 的 HEAD，且不回滚。
- 如果某 repo 已经成功 push 到目标分支，但本地写入 `published` 状态或 `published_sha` 失败，下一次 publish 可以自愈：fetch 后若目标分支当前 HEAD 是包含本 release SHA 作为父提交的发布 merge commit，则补写该 repo 的 `published` 和 `published_sha`，并继续发布流程；若目标分支后续又出现其他新 commit，则按外部新代码处理。
- 再次 publish 时，已 `published` repo 先 fetch 并检查当前 `<remote>/<base_branch>` 是否等于 `published_sha`；相等则跳过，不相等时直接阻塞并要求人工处理，不允许重新 integrate 覆盖已发布 repo。
- 进入 publish-in-progress 后，该推导态优先级高于 stale：只允许 `show`、`status` 和 `publish` retry；`add-req`、`remove-req`、`integrate`、相关需求 `reopen` 等会改变集成范围、feature SHA 或重建 release 的操作必须拒绝，避免覆盖已发布 repo。
- publish-in-progress 中如果 feature SHA、目标分支 HEAD、active 集成范围等发生变化，`release status/show` 必须展示为需要人工处理，而不是提示重新 integrate。
- `release show/status` 展示集成范围时必须区分 active requirements 和 removed history；默认 integrate、publish 和 released 推导只看 active requirements。
- 所有 active 集成范围涉及的 repo 发布成功后，release 标记为 `published`，只将 active 集成范围内的需求标记为 completed，并通过 published release active association 推导为 released，默认归档隐藏；已写入 `removed_at` 的需求不会随该 release publish 变成 completed 或 released。

## 4. 非目标

- 不创建或更新 Pull Request / Merge Request。
- 不读取 MR/PR 记录判断是否有新代码。
- 不编排 CI/CD；测试由用户或外部系统完成。
- 不做代码评审或测试结果分析。
- 不同步 Jira、GitHub Issues、Linear 等需求平台。
- 不删除远端 feature 分支。
- 不做多人权限、锁、审批和共享状态服务。
- 不管理运行时环境、容器、依赖安装或 IDE 设置。
- 不支持在 release 分支上做长期直接修复；bugfix 必须回 feature 分支。

## 5. 关键流程

### 5.1 初始化

1. 用户运行 `workspace init`；所有命令也可通过全局 `--home` 或 `WORKSPACE_CLI_HOME` 指定隔离的 workspace-cli home。
2. CLI 创建 home、work、repos、requirements 目录。
3. CLI 写入默认配置。
4. CLI 创建 SQLite 数据库并执行迁移。

### 5.2 注册 repo

1. 用户运行 `workspace repo add backend git@github.com:org/backend.git --base main`。
2. CLI clone bare repo 到 `~/.workspace-cli/work/repos/backend.git`。
3. CLI 探测或保存 base branch。
4. CLI 写入 repo 元数据。

### 5.3 创建需求

1. 用户运行 `workspace req create "支付链路优化" --key pay-flow --repo backend --repo frontend`。
2. CLI 创建需求记录，slug 为 `pay-flow`，feature branch 为 `feature/pay-flow`。
3. CLI 为每个初始 repo 写入绑定快照：`repo_name`、`repo_url`、`repo_remote`、`repo_base_branch`。
4. CLI 为每个 repo fetch 最新代码，更新对应 remote tracking refs。
5. CLI 按本地 feature 分支、远端 feature 分支、base branch 的优先级创建或复用 feature 分支。
6. CLI 在 `~/.workspace-cli/work/requirements/pay-flow/` 下创建多个 worktree。

### 5.4 开发

1. 用户运行 `workspace dev pay-flow --tool codex`。
2. CLI 进入需求 workspace。
3. CLI 启动 `codex` 命令。
4. 用户也可以运行 `workspace ide pay-flow`，默认使用 VS Code 打开需求 workspace，或通过 `--tool cursor|zed` 选择其他 IDE。
5. 用户在集中 workspace 中完成跨仓库开发。

### 5.5 完成需求 feature 开发

1. 用户运行 `workspace req finish pay-flow -m "feat: complete pay-flow"`。
2. CLI 对每个 repo 检查变更。
3. CLI 对存在变更的 repo 预检 commit 身份。
4. 有变更则提交，无变更则跳过提交。
5. CLI 推送所有 repo 的 feature 分支；如果任一 repo commit 或 push 失败，保留所有 worktree，状态不进入 cleanup。
6. 全部推送成功后批量标记 repo 关系为 `pushed`，进入 cleanup-pending，开始删除全部 worktree。
7. 删除仍存在的 worktree 前先确认工作区干净；dirty worktree 拒绝删除并保持可重试，已不存在的 worktree 视为已清理并标记对应 repo 关系为 `completed`。
8. 所有 repo 关系都变为 `completed` 后，CLI 写入 `ready_at`，需求 stage 变为 `ready`；cleanup 失败时记录 `cleanup_failed` 并允许重试。

### 5.6 创建并集成 release

1. 用户运行 `workspace release create "2026-07-01 发布" --key 2026-07-01 --req pay-flow --req user-center`。
2. CLI 创建 release 记录，release branch 为 `release/2026-07-01`，active 集成范围包含多个需求。
3. 用户运行 `workspace release integrate 2026-07-01`。
4. CLI 对所有相关 repo fetch 最新代码，读取每个需求 feature 分支 SHA 和发布目标分支 SHA。
5. CLI 删除旧 release worktree、本地 release branch 和上次 integrate 的 repo/feature SHA 快照，从最新 `<remote>/<base_branch>` 重建 release branch。
6. CLI 按 active 集成范围中的需求顺序 merge 每个需求的 feature 分支，记录每个 active repo 的 base SHA、release SHA、feature SHA，并把 feature SHA 关联到当前 membership。
7. CLI push 远端 `release/<release-slug>`，进入 `integrated` 状态。
8. 用户在 release workspace 上测试。

### 5.7 测试修复与重新集成

1. 如果 release 测试发现 bug，用户运行 `workspace req reopen <req>`，CLI 先对所有绑定 repo 预检 repo 未软删、bare repo 存在、feature 分支可用、目标 worktree path 不存在。
2. 预检全部通过后，CLI 按需求绑定 repo 恢复 `feature/<req-slug>` worktree 到需求 workspace；任一 worktree 创建失败时，删除本次已创建 worktree，保持 `ready_at`、relation 状态和 release stale 状态不变。
3. 所有 worktree 创建成功后，CLI 在同一个 DB 事务中清空 `ready_at`、让 relation 回到 `active`，并将 active 集成范围包含该需求且已有集成结果的非 publish-in-progress、未发布 release 标记为 stale；draft release 保持 draft。事务失败时删除本次已创建 worktree，并保持原 DB 状态。
4. 用户在 feature 分支修复后再次运行 `workspace req finish <req>`，提交并推送 feature 分支，让需求重新进入 ready。
5. CLI 检测到需求 feature SHA 与上次 integrate 记录不同，非 publish-in-progress release 变为 stale。
6. 用户重新运行 `workspace release integrate <release>`，CLI 删除并重建 release 分支，重新测试。若旧 release worktree 存在未提交变更，默认拒绝删除；用户确认这些修改只是临时测试修改时，可用 `--force` 丢弃。
7. 如果某需求临时取消发布，用户运行 `workspace release remove-req <release> <req>`；已有集成结果的非 publish-in-progress release 变为 stale，并必须重新 integrate，draft release 保持 draft。若之后重新加入同一需求，会形成新的 membership；重新 integrate 后被移出需求独占的 repo 不再参与 release。
8. 如果 release 已进入 publish-in-progress，`req reopen`、`add-req`、`remove-req` 和 `integrate` 都必须拒绝；任何 feature SHA、目标分支 HEAD 或集成范围变化都进入人工处理提示。

### 5.8 发布 release

1. 用户确认 release 测试完成后运行 `workspace release publish 2026-07-01 --tested -m "release: 2026-07-01"`。
2. CLI 检查所有待发布 release worktree 和 publish 临时 worktree 必须干净；dirty 时拒绝 publish，提示回 feature 分支修复后重新集成。
3. CLI fetch 所有相关 repo 的发布目标分支，并为每个 active repo 创建或重置 `~/.workspace-cli/work/releases/<release-slug>/.publish/<repo>` 到最新 `<remote>/<base_branch>`。
4. CLI 先校验当前 release repo scope 与 active 集成范围推导出的 repo 并集完全一致，再用 commit SHA/commit graph 检查目标分支是否有上次 integrate 之后的外部新 commit；已成功发布 repo 先检查目标分支 HEAD 是否等于 `published_sha`，相等则跳过该 repo。
5. 如果目标分支有外部新 commit，且 release 尚未进入 publish-in-progress，publish 失败并把 release 标记为 stale，要求重新 integrate 和重新测试。
6. 如果目标分支没有外部新 commit，CLI 在 publish 临时 worktree 中将 release 分支 merge 到每个 repo 的 `base_branch` 并 push。
7. 如果某 repo publish 失败，已成功 repo 标记为 `published`，记录 `published_sha` 为成功 push 后 `<remote>/<base_branch>` 的 HEAD；下次 publish 先校验该 repo 目标分支 HEAD 仍等于 `published_sha`，相等则跳过，不相等则阻塞并要求人工处理。
8. 若 release 已有任一 repo 标记为 `published` 但整体尚未发布完成，release 进入 publish-in-progress 推导态；此时只能继续 publish retry，不能 add/remove requirement、reopen 相关需求或重新 integrate。
9. publish-in-progress 中如果已发布 repo 的目标分支 HEAD 偏离 `published_sha`，或 feature SHA、目标分支 HEAD、active 集成范围发生变化，CLI 阻塞并要求人工处理，不自动改为 stale 重集成。
10. 所有 repo 发布成功后，release 标记为 `published`，active 集成范围内需求标记为 completed，并通过 published release active association 推导为 released，默认归档隐藏；已移出的需求保留历史但不随该 release 完成。

## 6. 状态与错误原则

- 需求最终 completed 只能由 release publish 成功写入；released 是由 completed 需求存在 published release active association 推导出的展示语义，active association 定义为当前 membership 的 `release_requirements.removed_at IS NULL`。需求 feature 开发完成只是可集成状态。
- 归档通过 `archived_at` 表示，归档不是生命周期状态；release publish 默认同步归档已发布需求。
- 所有破坏性清理必须发生在所有 repo 推送成功之后。
- 失败时保留用户工作目录，避免丢失未推送代码。
- cleanup 失败时流程必须可重试，不能要求用户手动修改数据库。
- cleanup-pending 期间需求不可修改，不可追加 repo，不可归档；只能继续执行 finish 清理。
- cleanup-pending 重试删除仍存在的 worktree 前必须检查工作区干净；若存在未提交变更，返回可恢复错误，不自动 commit、stash 或删除。
- cleanup-pending 重试发现 `pushed` 或 `cleanup_failed` repo 的 worktree path 已不存在时，视为清理已完成，将该 repo 关系自愈为 `completed`。
- finish 重试完成条件是所有 repo 关系都变为 `completed`，此时需求才能进入可集成状态；`completed_at` 和最终归档时间由 release publish 成功后写入。
- commit/push 阶段失败时不能持久写入 `pushed`，避免下次 finish 跳过仍需检查的 repo。
- 关键失败操作写入 operation log，便于追查失败点；成功操作日志可作为后续增强。
- CLI 错误消息必须包含 repo 名、需求 key 和可恢复建议。
- `req reopen` 只允许 ready 且未 completed 的需求；展示仍使用 `requirement_repos.repo_*` 快照，实际 Git 操作通过 `repo_id` 读取当前托管 repo 的 `bare_path`、`remote`、`base_branch`。repo 已软删、bare repo 不存在、feature 分支被其他 worktree 占用或目标 worktree path 已存在时返回可恢复错误；执行成功后清空 `ready_at`、恢复 relation 为 active，并将 active 集成范围包含该需求且已有集成结果的非 publish-in-progress、未发布 release 标记为 stale，draft release 保持 draft。普通 active、cleanup-pending、completed 需求，以及已包含在 publish-in-progress release active 集成范围中的 ready 需求执行时返回可恢复错误。
- release 分支是可重建产物；非 publish-in-progress release 一旦 stale，必须删除并重新集成。
- release repo/feature SHA 记录是最新 integrate 快照；非 publish-in-progress release 重新 integrate 会按 active 集成范围删除并重建快照，不保留 obsolete repo 作为待发布范围。
- release integrate 允许从 `draft`、`integrated`、`stale`、`failed` 状态执行；禁止对 `published` release 执行。merge 冲突后的 `failed` release 保留诊断 worktree，下一次 integrate 按 dirty/force 规则删除并重建。
- release integrate 删除旧 release worktree 前必须检查 dirty；dirty 时默认拒绝，`--force` 明确表示丢弃 release worktree 的临时修改。
- release publish 前必须检查待发布 release worktree 和 `.publish/<repo>` 临时 worktree 干净；dirty 时拒绝发布，防止 release 上的直接修改进入目标分支。
- release publish 前必须校验 release repo scope 与 active 集成范围的 repo 并集一致；不一致时拒绝发布。
- release publish 必须基于 commit SHA/commit graph 判断目标分支是否有外部新代码，不依赖 MR/PR 记录。
- release publish 失败但尚未发布任何 repo 时保持 `integrated` 并允许直接 retry；部分成功后不回滚，已发布 repo 记录为 `published` 并保存 `published_sha`。重试时先 fetch 并检查目标分支 HEAD 是否仍等于 `published_sha`，相等则跳过；不相等时直接阻塞并要求人工处理。
- publish-in-progress 优先级高于 stale，只允许 `show/status/publish retry`；`add-req`、`remove-req`、`integrate` 和相关需求 `reopen` 必须拒绝。若已 published repo 的目标分支 HEAD 不等于 `published_sha`，或发布中期间出现 feature SHA、目标分支 HEAD、active 集成范围变化，retry 必须阻塞并要求人工处理，不允许重新 integrate 覆盖已发布 repo。
- bugfix 必须回 feature 分支，不能把 release worktree 上的直接修改作为正式修复来源。
- completed、released 和 legacy completed 需求默认不可修改，即使 `archived_at` 为空也按已完成生命周期处理；活跃、可集成和 cleanup-pending 需求不能手动归档。

## 7. 验收标准

当前已实现基础流程：

- 可以初始化一个全新的 workspace-cli home。
- 可以注册并同步多个 Git repo。
- 可以通过 `workspace repo sync [name]` 手动同步托管 repo 的 base branch remote tracking ref。
- 可以创建一个绑定多个 repo 的需求。
- 创建需求的初始 repo 和后续追加 repo 都写入一致的 repo 绑定快照。
- 需求 workspace 下包含多个 repo 的 worktree。
- 同一个需求的所有 repo 使用统一 feature 分支名。
- 可以向普通活跃需求追加 repo 并创建对应 worktree。
- 可以启动 Codex 或 Claude Code 到需求 workspace。
- 可以默认用 VS Code 打开需求 workspace，也可以选择 Cursor 或 Zed。
- 当前版本 `workspace req finish` 成功后会提交、推送、清理 worktree，并写入 `ready_at`，需求进入 ready/可集成阶段。
- 任一 push 或 commit 失败时，需求保持 active，worktree 不被删除。
- worktree 删除失败时，finish 可幂等重试清理，不能重复提交或重复推送已成功 repo。
- cleanup-pending 重试发现 dirty worktree 时拒绝删除并保留状态。
- cleanup-pending 重试发现 worktree path 已不存在时可自愈为 `completed`，不锁死需求。
- cleanup-pending 需求不能 update、add-repo 或 archive，只能 show/list/finish。
- completed 历史需求展示使用 repo 绑定快照，不随 repo update 改变。
- 需求列表能同时展示 lifecycle status 和 archived 状态。
- completed 需求默认不可修改；即使历史数据中 `archived_at` 为空，也按已完成生命周期处理。

Release 流程验收：

- `finish -> ready_at` 已与 `req reopen`、`release create/integrate/publish` 同批落地；当前完成体验是先 ready，发布成功后 completed/archived。
- finish 成功后需求写入 `ready_at`，`req list/show` 能展示 `stage=ready`，但不写 completed 或 archived 时间。
- ready 需求可通过 `workspace req reopen <req>` 恢复 feature worktree、回到普通 active，并让 active 集成范围包含它且已有集成结果的非 publish-in-progress release 变 stale；尚未集成过的 draft release 保持 draft。
- `req reopen` 是 all-or-nothing；worktree 创建失败或 DB 事务失败都不会留下半恢复状态，也不会提前清空 `ready_at` 或标记 release stale。
- publish-in-progress release 中的需求不能 reopen；普通 active、cleanup-pending、completed 和 legacy completed 需求也不能 reopen。
- `req list/show` 能展示 `stage=active|cleanup-pending|ready|completed`，并在 completed 需求上区分 `released` 与 `legacy-completed`，避免 ready 需求被当成普通 active，也避免旧完成态被误认为已发布。
- 可以创建 release 并选择多个已完成 feature 开发的 ready 需求；legacy completed 不自动进入 release 集成范围。
- release 创建必须 all-or-nothing；初始需求 membership 写入失败时不能留下 draft release 或部分 membership。
- release integrate 会删除并重建 release 分支，把多个需求 feature 分支合入 `release/<release-slug>`。
- release integrate 会按 active repo union 重建 release repo/feature SHA 快照；移出需求独占的 repo 不再参与后续 publish，且旧 release worktree 会在重新 integrate 时移除。
- 非 publish-in-progress release 中，已集成过的 release 在需求加入、需求移出、需求 feature SHA 变化或发布目标分支出现外部新 commit 时会变为 stale 并要求重新 integrate；draft release 调整需求集合后仍保持 draft。
- 非 publish-in-progress release 中，已有集成结果的 release 在 active 集成范围内需求不再 ready 或 relation 不再 completed 时会变为 stale，并要求用户调整范围或重新进入正确流程；draft release 保持 draft，但在需求重新 ready 前不能成功 integrate。
- 同一需求从 release 移出后再次加入，会生成新的 membership；旧 membership 保留 history 但不参与 integrate/publish/released 推导。
- release 测试发现 bug 时，用户必须通过 `req reopen` 回 feature 分支修复后重新 finish 和 integrate。
- release integrate 删除 dirty 的旧 release worktree 时默认拒绝；传 `--force` 才丢弃 release worktree 临时修改。
- release publish 前发现 dirty release worktree 或 dirty `.publish/<repo>` 临时 worktree 时拒绝发布。
- publish 前用 commit 判断目标分支是否有外部新代码；非 publish-in-progress release 有外部新代码时必须重新 integrate 和重新测试。
- publish 前发现 release repo scope 与 active 集成范围推导出的 repo 并集不一致时必须拒绝发布。
- status/publish 前发现尚未发布 repo 的远端 release branch HEAD 与上次 integrate 记录的 release SHA 不一致时，必须展示 stale 或人工处理原因并拒绝发布，避免把未经测试的 release commit 合并到目标分支；非 publish-in-progress release 要求重新 integrate 和重新测试，publish-in-progress release 阻塞并提示人工处理。
- publish 首个 repo 失败时可在保持 `integrated` 状态下重试；部分成功后可重试，已成功 repo 只有在目标分支 HEAD 仍等于 `published_sha` 时才跳过，未成功 repo 可继续发布。
- publish-in-progress 优先级高于 stale；不能 add/remove requirement、重新 integrate 或 reopen 相关需求。
- publish-in-progress 中出现 feature SHA、目标分支 HEAD、active 集成范围变化，或已发布 repo 的目标分支 HEAD 偏离 `published_sha` 时，阻塞并提示人工处理。
- publish 成功后，release 分支合并到每个 repo 的 `base_branch`，active 集成范围内需求进入 completed，并通过 published release active association 推导为 released，默认归档隐藏。
- 已写入 `removed_at` 的需求不参与 integrate/publish，也不会随该 release publish 进入 completed 或 released。
- 旧库 completed 需求迁移后保持 legacy completed：不可修改、默认隐藏、不自动变 ready、不自动标为 released。
- 活跃、可集成和 cleanup-pending 需求不能手动归档，已发布完成需求默认归档隐藏。
- released 和 legacy completed 需求默认不可修改；即使历史数据中 `archived_at` 为空，也按已完成生命周期处理。
