# workspace-cli 需求规划

## 1. 产品定位

workspace-cli 是一个本地命令行工具，用于管理“需求开发空间”。这里的 workspace 不是单个代码仓库，而是围绕一个需求临时组织起来的开发空间。一个需求可以绑定多个 Git repo，workspace-cli 会把这些 repo 的 feature 分支 worktree 集中到同一个需求目录中，方便使用 Codex、Claude Code 或 IDE 进行跨仓库开发。

v1 只管理本地需求开发流程：创建需求、选择 repo、准备 worktree、启动开发工具、完成需求、提交并推送代码、清理 worktree、归档需求。它不接管 PR、CI、代码评审、远端 issue 或权限体系。

## 2. 核心概念

- **workspace-cli home**：工具自己的状态目录，默认 `~/.workspace-cli`，保存配置、SQLite 数据库和托管 repo。
- **work 目录**：默认 `~/.workspace-cli/work`，用于存放 bare repo 和需求 workspace。
- **托管 repo**：workspace-cli 注册并管理的 Git 仓库。每个 repo 记录名称、remote URL、remote 名称、base branch 和本地 bare repo 路径。
- **需求**：一次待开发的需求项，包含标题、slug、生命周期状态、归档时间、workspace path、feature branch 和绑定的多个 repo。
- **cleanup-pending**：需求生命周期仍为 `active`，但任一绑定 repo 关系状态为 `pushed` 或 `cleanup_failed` 时推导出的临时锁定态。该状态表示所有 repo 已完成 push，需求只剩 worktree 清理或清理重试。
- **普通活跃需求**：`status=active`、`archived_at` 为空、且不是 cleanup-pending 的需求。v1 只有普通活跃需求允许修改标题或追加 repo。
- **需求 workspace**：一个需求对应的集中开发目录，默认 `~/.workspace-cli/work/requirements/<req-slug>`。
- **feature 分支**：需求开发分支，默认统一为 `feature/<req-slug>`。同一个需求绑定的所有 repo 使用同一个 feature 分支名。
- **git worktree**：从托管 bare repo 创建出的实际工作目录，放在需求 workspace 下，例如 `.../requirements/pay-flow/backend`。
- **完成**：对需求绑定的所有 repo 执行检查、提交、推送；全部成功并完成 worktree 清理后，标记 `status=completed`。
- **归档**：归档是可见性标记，通过 `archived_at` 表示，不是生命周期状态。归档需求不再参与默认列表和修改流程。完成流程会自动归档；也支持手动归档。

## 3. v1 功能范围

### 3.1 初始化与配置

- 初始化 workspace-cli home。
- 创建默认配置文件 `config.yaml`。
- 创建 SQLite 数据库并执行迁移。
- 创建 work 目录、repo 目录和 requirements 目录。

### 3.2 Repo 管理

- 注册 repo：指定名称、Git URL、remote 名称和 base branch。
- 首次注册时 clone 为本地 bare repo。
- 同步 repo：对 bare repo 执行 fetch。
- 更新 repo 元数据：URL、remote、base branch；repo 被活跃或 cleanup-pending 需求引用时禁止修改这些字段，避免影响已创建 worktree 和清理流程。
- 删除 repo：仅允许删除未被普通 active 或 cleanup-pending 需求使用的 repo，并通过 `deleted_at` 软删除，保留历史审计信息。
- 列出所有未删除 repo，可通过 `repo list --all` 查看已删除 repo。

### 3.3 需求管理

- 创建需求：指定标题、可选 key/slug，并绑定一个或多个 repo；初始绑定 repo 时写入 `repo_name`、`repo_url`、`repo_remote`、`repo_base_branch` 快照。
- 修改需求：普通活跃需求允许更新标题；cleanup-pending 或 completed 需求不可修改。
- 添加 repo：普通活跃需求允许追加新的 repo，并立即创建该 repo 的 worktree；cleanup-pending 或 completed 需求不可追加 repo。
- 查看需求详情：展示状态、workspace path、feature branch、绑定 repo、repo 绑定快照和 worktree path。历史展示使用绑定快照，不被后续 repo 元数据更新污染。
- 列出需求：默认展示未归档的活跃需求；`--all` 展示所有需求，并同时展示生命周期 `status` 和是否 `archived`。
- 归档需求：v1 只允许归档已完成需求，写入 `archived_at` 后默认不可修改。已归档 completed 需求再次 archive 返回成功 no-op。活跃或 cleanup-pending 需求执行 archive 返回可恢复错误，提示先 finish；v1 不提供 unarchive 或 cancel 命令。

### 3.4 需求 workspace 创建

- 创建需求前先同步相关 repo。
- 每个 repo 按 feature 分支选择规则准备 `feature/<req-slug>`。
- 使用 git worktree 把多个 repo 集中到同一个需求 workspace。
- 如果本地 feature 分支已存在且未被其他 worktree 占用，则复用该分支创建 worktree。
- 如果远端 feature 分支已存在但本地不存在，则从 `<remote>/feature/<req-slug>` 创建本地分支并用于 worktree。
- 如果本地和远端 feature 分支都不存在，则从 `<remote>/<base_branch>` 创建本地 feature 分支。
- 如果 worktree path 已存在，或目标分支已被其他 worktree 占用，返回可恢复错误，不覆盖用户文件。

### 3.5 开发工具启动

- 支持 `workspace dev <req> --tool codex|claude`。
- 支持 `workspace ide <req> --tool vscode|cursor|zed`，默认使用 `vscode`。
- 命令进入需求 workspace 后启动配置中的工具命令。
- IDE 命令进入需求 workspace 后，把需求 workspace path 作为参数传给 IDE，只打开需求 workspace 根目录。
- v1 不记录、不恢复、不编排 Codex 或 Claude Code 会话。

### 3.6 需求完成

- 对需求绑定的所有 repo 检查工作区状态。
- 对所有存在变更的 repo 预检 `user.name` 和 `user.email`；任一 repo 缺失身份信息时，在任何 commit、push、cleanup 之前失败，并提示对应 `git config` 修复命令。
- 有变更时执行 `git add -A` 和 commit。
- 无变更时跳过 commit，但仍推送当前 HEAD 到 feature 分支。
- 每个 repo 推送到对应 remote 的 `refs/heads/feature/<req-slug>`；commit/push 阶段不持久推进 repo 关系状态。
- 所有 repo 都推送成功后，批量将 repo 关系状态记为 `pushed`，需求进入 cleanup-pending 锁定态，才开始删除 worktree。
- worktree 全部删除成功后，将 repo 关系状态记为 `completed`，并将需求设置为 `status=completed`、写入 `completed_at` 和 `archived_at`。
- 任一 repo commit 或 push 失败时停止后续流程，保留全部 worktree，需求保持 `status=active`，repo 关系状态不进入 `pushed`，并写入操作日志；若某些远端分支已被本轮 push 更新，v1 不回滚远端，下一次 finish 重新检查并重试。
- 若所有 repo push 已成功但 worktree 删除失败，将失败 repo 关系状态记为 `cleanup_failed`，保留需求可重试；再次执行 finish 时，不重复提交或重复 push，只处理 `pushed` 或 `cleanup_failed` 的 repo。对仍存在的 worktree，删除前必须确认工作区干净；若存在未提交变更，拒绝清理并保留原状态，提示用户先处理变更。若 worktree path 已不存在，视为该 repo 已完成清理并将关系状态自愈为 `completed`。所有 repo 关系都变为 `completed` 后，再将需求设置为 `status=completed`、写入 `completed_at` 和 `archived_at`。cleanup-pending 期间只允许 `show`、`list`、`finish`，禁止 `update`、`add-repo`、`archive`。

## 4. 非目标

- 不创建或更新 Pull Request。
- 不编排 CI/CD。
- 不做代码评审或测试结果分析。
- 不同步 Jira、GitHub Issues、Linear 等需求平台。
- 不删除远端 feature 分支。
- 不做多人权限、锁、审批和共享状态服务。
- 不管理运行时环境、容器、依赖安装或 IDE 设置。

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
4. CLI 为每个 repo fetch 最新代码。
5. CLI 按本地 feature 分支、远端 feature 分支、base branch 的优先级创建或复用 feature 分支。
6. CLI 在 `~/.workspace-cli/work/requirements/pay-flow/` 下创建多个 worktree。

### 5.4 开发

1. 用户运行 `workspace dev pay-flow --tool codex`。
2. CLI 进入需求 workspace。
3. CLI 启动 `codex` 命令。
4. 用户也可以运行 `workspace ide pay-flow`，默认使用 VS Code 打开需求 workspace，或通过 `--tool cursor|zed` 选择其他 IDE。
5. 用户在集中 workspace 中完成跨仓库开发。

### 5.5 完成需求

1. 用户运行 `workspace req finish pay-flow -m "feat: complete pay-flow"`。
2. CLI 对每个 repo 检查变更。
3. CLI 对存在变更的 repo 预检 commit 身份。
4. 有变更则提交，无变更则跳过提交。
5. CLI 推送所有 repo 的 feature 分支；如果任一 repo commit 或 push 失败，保留所有 worktree，状态不进入 cleanup。
6. 全部推送成功后批量标记 repo 关系为 `pushed`，进入 cleanup-pending，开始删除全部 worktree。
7. 删除仍存在的 worktree 前先确认工作区干净；dirty worktree 拒绝删除并保持可重试，已不存在的 worktree 视为已清理并标记对应 repo 关系为 `completed`。
8. 所有 repo 关系都变为 `completed` 后，CLI 标记需求 `status=completed`，写入 `completed_at` 和 `archived_at`；cleanup 失败时记录 `cleanup_failed` 并允许重试。

## 6. 状态与错误原则

- 需求生命周期状态只有 `active` 和 `completed`；归档通过 `archived_at` 表示。
- 所有破坏性清理必须发生在所有 repo 推送成功之后。
- 失败时保留用户工作目录，避免丢失未推送代码。
- cleanup 失败时流程必须可重试，不能要求用户手动修改数据库。
- cleanup-pending 期间需求不可修改，不可追加 repo，不可归档；只能继续执行 finish 清理。
- cleanup-pending 重试删除仍存在的 worktree 前必须检查工作区干净；若存在未提交变更，返回可恢复错误，不自动 commit、stash 或删除。
- cleanup-pending 重试发现 `pushed` 或 `cleanup_failed` repo 的 worktree path 已不存在时，视为清理已完成，将该 repo 关系自愈为 `completed`。
- finish 重试完成条件是所有 repo 关系都变为 `completed`，此时才能写入需求 `status=completed`、`completed_at` 和 `archived_at`。
- commit/push 阶段失败时不能持久写入 `pushed`，避免下次 finish 跳过仍需检查的 repo。
- 关键失败操作写入 operation log，便于追查失败点；成功操作日志可作为后续增强。
- CLI 错误消息必须包含 repo 名、需求 key 和可恢复建议。
- completed 需求默认不可修改，即使 `archived_at` 为空也按已完成生命周期处理；活跃和 cleanup-pending 需求不能手动归档。

## 7. 验收标准

- 可以初始化一个全新的 workspace-cli home。
- 可以注册并同步多个 Git repo。
- 可以创建一个绑定多个 repo 的需求。
- 创建需求的初始 repo 和后续追加 repo 都写入一致的 repo 绑定快照。
- 需求 workspace 下包含多个 repo 的 worktree。
- 同一个需求的所有 repo 使用统一 feature 分支名。
- 可以向普通活跃需求追加 repo 并创建对应 worktree。
- 可以启动 Codex 或 Claude Code 到需求 workspace。
- 可以默认用 VS Code 打开需求 workspace，也可以选择 Cursor 或 Zed。
- 完成需求时，全部 repo 推送成功后才删除 worktree。
- 任一 push 或 commit 失败时，需求保持 active，worktree 不被删除。
- worktree 删除失败时，finish 可幂等重试清理，不能重复提交或重复推送已成功 repo。
- cleanup-pending 重试发现 dirty worktree 时拒绝删除并保留状态。
- cleanup-pending 重试发现 worktree path 已不存在时可自愈为 `completed`，不锁死需求。
- cleanup-pending 需求不能 update、add-repo 或 archive，只能 show/list/finish。
- completed 历史需求展示使用 repo 绑定快照，不随 repo update 改变。
- 活跃需求不能手动归档，已完成需求可归档隐藏。
- 需求列表能同时展示 lifecycle status 和 archived 状态。
- completed 需求默认不可修改，即使尚未写入 `archived_at`。
