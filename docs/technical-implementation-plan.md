# workspace-cli 技术实现方案

## 1. 技术栈

- 语言：Go。
- CLI 框架：Cobra。
- 状态存储：SQLite，使用纯 Go 驱动 `modernc.org/sqlite`。
- 配置文件：YAML，默认 `~/.workspace-cli/config.yaml`。
- Git 操作：调用本机 `git` 命令，统一通过 runner 封装。
- 测试：Go testing；核心 Git 流程使用临时 bare remote 做集成测试。

## 2. 目录布局

项目源码布局：

```text
cmd/workspace/main.go
internal/agent
internal/cli
internal/config
internal/git
internal/store
internal/workspace
docs
```

运行时默认目录：

```text
~/.workspace-cli/
  config.yaml
  workspace.db
  work/
    repos/
      <repo>.git
    requirements/
      <req-slug>/
        <repo>/
```

测试和自动化可通过 `WORKSPACE_CLI_HOME` 或 CLI 的全局 `--home` 指定隔离目录。home 解析优先级为：`--home` > `WORKSPACE_CLI_HOME` > `~/.workspace-cli`。

## 3. CLI 命令面

```text
workspace [--home <path>] <command>

workspace init

workspace repo add <name> <url> [--remote origin] [--base <branch>]
workspace repo list [--all]
workspace repo sync [name]
workspace repo update <name> [--url <url>] [--remote <remote>] [--base <branch>]
workspace repo remove <name>

workspace req create <title> --repo <name> [--repo <name>] [--key <slug>]
workspace req list [--all]
workspace req show <key-or-slug>
workspace req update <key-or-slug> [--title <title>]
workspace req add-repo <key-or-slug> <repo>
workspace req archive <key-or-slug>
workspace req finish <key-or-slug> [-m <commit-message>]

workspace dev <key-or-slug> --tool codex|claude
workspace ide <key-or-slug> [--tool vscode|cursor|zed]
```

Cobra 层只负责参数解析、加载配置、构造 service 和输出结果。业务规则放到 `internal/workspace`。

## 4. 数据模型

SQLite 表：

- `repos`
  - `id`
  - `name`
  - `url`
  - `remote`
  - `base_branch`
  - `bare_path`
  - `deleted_at`
  - `created_at`
  - `updated_at`
- `requirements`
  - `id`
  - `req_key`
  - `title`
  - `slug`
  - `status`
  - `workspace_path`
  - `feature_branch`
  - `created_at`
  - `updated_at`
  - `completed_at`
  - `archived_at`
- `requirement_repos`
  - `id`
  - `requirement_id`
  - `repo_id`
  - `repo_name`
  - `repo_url`
  - `repo_remote`
  - `repo_base_branch`
  - `worktree_path`
  - `status`
  - `created_at`
  - `updated_at`
- `operation_logs`
  - `id`
  - `requirement_id`
  - `repo_id`
  - `operation`
  - `status`
  - `message`
  - `created_at`

状态约定：

- requirement status：`active`、`completed`。归档不是 status，通过 `archived_at` 是否为空判断。
- requirement repo status：`active`、`pushed`、`completed`、`cleanup_failed`。
- cleanup-pending 不是新的 requirement status，而是由任一 `requirement_repos.status in ('pushed', 'cleanup_failed')` 推导出的临时锁定态。
- operation status：`success`、`failed`。
- v1 必须记录关键失败操作；`success` 状态为后续完整审计预留，不要求每个成功操作都落库。
- repo 删除是软删除，通过 `repos.deleted_at` 判断。默认 repo list 隐藏已删除 repo，`--all` 可展示。
- v1 不提供 `req remove-repo`，因此 `requirement_repos.status` 不包含 `removed`；`repo remove` 只写 `repos.deleted_at`。
- `requirement_repos.repo_*` 是绑定时快照，用于需求详情和历史展示；实际 Git 操作仍通过 `repo_id` 读取当前托管 repo。
- `--home` 是 root command 全局 flag，所有子命令都使用同一套 home 解析逻辑。

## 5. Git 编排

Git 操作统一封装在 `internal/git`：

- `CloneBare(url, barePath)`
- `Fetch(barePath, remote)`
- `DefaultBranch(barePath, remote)`
- `SetRemoteURL(barePath, remote, url)`
- `RenameRemote(barePath, oldRemote, newRemote)`
- `LocalBranchExists(barePath, branch)`
- `RemoteBranchExists(barePath, remote, branch)`
- `BranchInUse(barePath, branch)`
- `CreateWorktree(barePath, worktreePath, branch, baseBranch)`
- `HasChanges(worktreePath)`
- `CommitIdentity(worktreePath)`
- `CommitAll(worktreePath, message)`
- `PushBranch(worktreePath, remote, branch)`
- `RemoveWorktree(barePath, worktreePath)`

策略：

- repo 注册时 clone 为 bare repo。
- 创建或扩展需求前先 fetch。
- feature 分支名统一为 `feature/<req-slug>`。
- 如果本地 feature 分支已存在且未被其他 worktree 占用，则直接基于该分支创建 worktree。
- 如果远端 feature 分支已存在但本地不存在，则从 `<remote>/<feature-branch>` 创建本地分支并创建 worktree。
- 如果本地和远端 feature 分支都不存在，则从 `<remote>/<base_branch>` 创建本地 feature 分支。
- 如果 feature 分支已被其他 worktree 占用，或目标 worktree path 已存在，则返回可恢复错误，不覆盖用户文件。
- push 使用 `git push <remote> HEAD:refs/heads/<feature-branch>`。
- finish 前先对所有存在变更的 repo 检查 `user.name` 和 `user.email`；缺失时在任何 commit、push、cleanup 前失败，并输出对应 repo 的修复命令。
- repo update 只传 `--url` 时，仅执行 `SetRemoteURL(barePath, currentRemote, newURL)`。
- repo update 只传 `--remote` 时，仅执行 `RenameRemote(barePath, oldRemote, newRemote)` 并保留原 URL。
- repo update 同时传 `--remote` 和 `--url` 时，先执行 `RenameRemote(barePath, oldRemote, newRemote)`，再执行 `SetRemoteURL(barePath, newRemote, newURL)`。

## 6. 核心服务

`internal/workspace.Service` 负责：

- `AddRepo`：clone bare repo，探测 base branch，写入 repo 表。
- `SyncRepo` / `SyncAllRepos`：fetch 托管 repo。
- `UpdateRepo`：更新 URL、remote、base branch，并同步 bare repo remote 配置；只更新 URL 时调用 `SetRemoteURL`，只更新 remote 时调用 `RenameRemote` 并保留原 URL，同时更新 remote 和 URL 时先 `RenameRemote` 再 `SetRemoteURL`；repo 被 active 或 cleanup-pending 需求引用时禁止修改 URL、remote、base branch。completed 历史需求展示使用绑定快照，不随 repo update 改变。
- `RemoveRepo`：检查 repo 未被普通 active 或 cleanup-pending 需求引用后写入 `deleted_at`，保留历史记录和 bare repo 清理的后续扩展空间。
- `CreateRequirement`：创建需求记录、为每个初始 repo 写入 `repo_name`、`repo_url`、`repo_remote`、`repo_base_branch` 绑定快照、准备 workspace、创建 worktree。
- `AddRepoToRequirement`：向普通 active 需求追加 repo、写入 repo 绑定快照并创建 worktree；普通 active 定义为 `status=active`、`archived_at` 为空且不是 cleanup-pending；非普通 active 需求不可追加 repo。
- `FinishRequirement`：普通 active 路径执行 commit 身份预检、提交、推送和 cleanup；cleanup-pending 重试路径不执行 commit/push，只执行 dirty guard、missing worktree 自愈和 cleanup 重试；全部完成后写入 `status=completed`、`completed_at`、`archived_at`。
- `ArchiveRequirement`：仅允许 `status=completed` 的需求写入 `archived_at`；completed 且已归档时返回成功 no-op；active 或 cleanup-pending 需求返回可恢复错误，提示先 finish 或等待未来 cancel/discard 流程。
- `UpdateRequirement`：仅普通 active 需求允许更新标题；普通 active 定义为 `status=active`、`archived_at` 为空且不是 cleanup-pending；非普通 active 需求不可修改。

错误原则：

- 任一 repo 的 fetch、worktree、relation、status、commit identity、commit、push、cleanup 失败，都写 operation log。
- finish 中任一 commit/push 失败，不删除任何 worktree，需求保持 `status=active`，repo 关系状态保持 `active` 或原状态，不持久写入本轮 `pushed`。
- 只有所有 repo push 全部成功后，才批量将 `requirement_repos.status` 记为 `pushed` 并进入 cleanup；删除 worktree 成功后记为 `completed`。
- 删除 worktree 只在所有 push 成功后执行。
- cleanup 失败时将失败 repo 关系记为 `cleanup_failed`，需求保持可重试且不写入 `archived_at`；再次 finish 时跳过已 `completed` 的 repo cleanup，继续处理 `pushed` 或 `cleanup_failed` 的 repo。
- cleanup-pending 重试时，对仍存在的 `pushed` 或 `cleanup_failed` worktree 先执行 `HasChanges(worktreePath)`；若 dirty，返回可恢复错误，不删除 worktree，不修改该 relation 状态。
- cleanup-pending 重试时，如果 `pushed` 或 `cleanup_failed` relation 的 worktree path 已不存在，视为清理已完成，将该 relation 标记为 `completed`。
- 所有 relation 都变为 `completed` 后，才能写入 requirement 的 `status=completed`、`completed_at`、`archived_at`。
- cleanup-pending 需求只允许 `show`、`list`、`finish`；`UpdateRequirement`、`AddRepoToRequirement`、`ArchiveRequirement` 必须返回可恢复错误。
- completed 需求返回明确错误，不执行修改；archive 对已归档 completed 需求重复执行除外，作为幂等 no-op 成功返回。

## 7. Agent 与 IDE 启动

`internal/agent` 根据配置启动工具：

- `codex` 默认命令：`codex`。
- `claude` 默认命令：`claude`。
- `vscode` 默认命令：`code`。
- `cursor` 默认命令：`cursor`。
- `zed` 默认命令：`zed`。
- 启动目录为需求 workspace path。
- `workspace dev` 直接执行 agent 命令。
- `workspace ide` 默认选择 `vscode`，把需求 workspace path 追加为 IDE 命令参数，例如 `code <workspacePath>`。
- stdin/stdout/stderr 继承当前终端。
- v1 不记录会话 ID，不管理会话恢复。

## 8. 测试方案

单元测试：

- config 默认路径和 init 输出。
- config 默认包含 `codex`、`claude`、`vscode`、`cursor`、`zed` 工具命令，且可覆盖 IDE 命令。
- slug 与 feature branch 生成。
- SQLite migration 和基本 CRUD。
- requirement status 与 `archived_at` 双轴状态。
- cleanup-pending 由 `requirement_repos.status` 推导，不新增 requirement status。
- `requirement_repos.status` 枚举不包含 `removed`。
- repo soft delete 的 `deleted_at` 行为。
- `CreateRequirement` 的初始 repo 绑定和 `AddRepoToRequirement` 的追加绑定都写入 `repo_name`、`repo_url`、`repo_remote`、`repo_base_branch` 快照。
- repo update 被活跃需求引用时禁止修改 URL、remote、base branch。
- repo update 被 cleanup-pending 需求引用时禁止修改 URL、remote、base branch。
- repo update 只传 URL、只传 remote、同时传 URL 和 remote 时分别触发正确的 bare repo remote 更新规则。
- completed 历史需求详情使用 repo 快照，不随 repo update 改变。
- completed-but-unarchived 和已归档 completed 需求执行 update、add-repo 均被拒绝；已归档 completed 需求再次 archive 成功 no-op。
- active 和 cleanup-pending 需求执行 archive 报错，completed 需求可写入 `archived_at`，已归档 completed 需求再次 archive 成功 no-op。
- home 解析优先级为 `--home` > `WORKSPACE_CLI_HOME` > 默认 home。

Git 集成测试：

- 使用临时目录创建 bare remote。
- seed 一个 main 分支。
- `AddRepo` clone bare repo。
- `CreateRequirement` 创建 feature worktree。
- 本地 feature 分支、远端 feature 分支、base branch 三种创建路径。
- 修改 worktree 文件。
- `FinishRequirement` 在普通 active 路径 commit、push feature 分支、删除 worktree、标记完成归档。

失败场景：

- push 失败时不删除 worktree。
- push 部分成功后不持久写入本轮 `pushed`，再次 finish 会重新检查并重试。
- commit 失败时不归档需求。
- commit 身份缺失时在 commit/push/cleanup 前失败。
- 已存在 worktree 报错。
- feature 分支被其他 worktree 占用时报错。
- 无变更时 finish 仍可 push 并清理。
- cleanup 失败时记录 `cleanup_failed`，再次 finish 可继续清理且不重复提交。
- cleanup-pending 需求执行 update、add-repo、archive 均报错，只允许 show、list、finish。
- finish 重试 cleanup-pending 时处理 `pushed|cleanup_failed` repo，不执行 commit/push。
- finish 重试 cleanup-pending 时，dirty worktree 拒绝删除并保留 relation 状态。
- finish 重试 cleanup-pending 时，worktree path 已不存在的 `pushed|cleanup_failed` relation 自愈为 `completed`。
- 所有 relation 都变为 `completed` 后，需求写入 completed 和 archived 时间。
- repo remove 只写 `repos.deleted_at`，不写 `requirement_repos.status=removed`。
- repo update 的 URL only 场景调用 `SetRemoteURL`，remote only 场景调用 `RenameRemote` 并保留 URL，remote + URL 场景先 `RenameRemote` 再 `SetRemoteURL`。

CLI 测试：

- 命令缺参和重复资源返回错误。
- `repo list`、`req list`、`req show` 输出可读。
- `repo list` 默认隐藏 soft deleted repo，`repo list --all` 展示。
- `req list --all` 同时展示 lifecycle status 和 archived 状态。
- `workspace --home <path> repo list` 等任意子命令都使用指定 home。
- `dev` 对未知工具返回错误。
- `ide` 默认使用 `vscode`，把 requirement workspace path 作为最后一个参数传给 IDE 命令。
- `ide --tool cursor|zed` 使用对应配置命令。
- `ide` 对未知 IDE tool 返回 `unknown ide tool "<tool>"`。
