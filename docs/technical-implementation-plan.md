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
    releases/
      <release-slug>/
        <repo>/
        .publish/
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
workspace req reopen <key-or-slug>

workspace release create <name> --req <key> [--req <key>] [--key <slug>]
workspace release list [--all]
workspace release show <key-or-slug>
workspace release add-req <release> <req>
workspace release remove-req <release> <req>
workspace release status <release>
workspace release integrate <release> [--force]
workspace release publish <release> --tested [-m <message>]

workspace dev <key-or-slug> --tool codex|claude
workspace ide <key-or-slug> [--tool vscode|cursor|zed]
```

Cobra 层只负责参数解析、加载配置、构造 service 和输出结果。业务规则放到 `internal/workspace`。`workspace release ...` 和 `workspace req reopen` 是 release 流程的当前命令面。

## 4. 数据模型

SQLite 表：

- `schema_migrations`
  - `version`
  - `name`
  - `applied_at`
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
  - `ready_at`
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
- `releases`
  - `id`
  - `release_key`
  - `title`
  - `slug`
  - `status`
  - `workspace_path`
  - `branch_name`
  - `target_branch`，release 级 summary 字段；多 repo release 使用 `per-repo`，实际发布目标以 `release_repos.target_branch` 为准
  - `created_at`
  - `updated_at`
  - `integrated_at`
  - `published_at`
- `release_requirements`
  - `id`
  - `release_id`
  - `requirement_id`
  - `position`
  - `removed_at`，为空表示 active release association；非空表示该需求已软移出 release，只保留 membership history
  - `created_at`
  - `updated_at`
- `release_repos`
  - `id`
  - `release_id`
  - `repo_id`
  - `release_branch`
  - `worktree_path`
  - `publish_worktree_path`
  - `target_branch`
  - `integrated_base_sha`
  - `release_sha`
  - `published_sha`
  - `status`
  - `created_at`
  - `updated_at`
- `release_requirement_repos`
  - `id`
  - `release_requirement_id`
  - `release_id`
  - `requirement_id`
  - `repo_id`
  - `feature_branch`
  - `feature_sha`
  - `created_at`
  - `updated_at`
- `release_operation_logs`
  - `id`
  - `release_id`
  - `requirement_id`
  - `repo_id`
  - `operation`
  - `status`
  - `message`
  - `created_at`

索引和约束：

- `schema_migrations.version` 为主键，迁移按 version 升序执行。
- `release_requirements` 允许同一 requirement 被 remove 后再次加入同一 release，但同一时间只能有一个 active association；使用 partial unique index 约束 `release_id, requirement_id WHERE removed_at IS NULL`。
- `release_requirement_repos.release_requirement_id` 外键指向具体 `release_requirements.id`，用于区分同一需求 remove 后再次加入产生的不同 membership。
- `release_repos` 和 `release_requirement_repos` 是最新一次 integrate 的快照表，不作为 append-only audit 表；removed membership 历史只保留在 `release_requirements`。

迁移与兼容策略：

- store 层使用 `schema_migrations(version, name, applied_at)` 做 versioned migration，禁止只依赖 `CREATE TABLE IF NOT EXISTS` 隐式漂移 schema。
- 已发布 `v0.1.0` 数据库作为 baseline migration：`0001_baseline_v0_1_0`。旧库首次升级时，如果没有 `schema_migrations` 表，先创建该表，识别已有 v0.1 schema 后写入 baseline 记录，再执行后续迁移。
- Release 里程碑使用 `0002_ready_release_flow`：补 `requirements.ready_at`，创建 `releases`、`release_requirements`、`release_repos`、`release_requirement_repos`、`release_operation_logs`，并创建 active membership partial unique index。
- 每个 migration 在事务内执行；新增列前用 `PRAGMA table_info` 检查是否已存在，新增索引用 `CREATE INDEX IF NOT EXISTS` 或等价幂等检查，保证重复运行不会破坏数据。
- `0002_ready_release_flow` 必须兼容已有 `repos`、`requirements`、`requirement_repos`、`operation_logs` 数据，不重建旧表、不丢历史记录。
- 旧库中已有 `requirements.status='completed'` 的数据保持 legacy completed 语义，`completed_at`、`archived_at` 按旧数据保留；它表示历史版本已 finish/归档，不代表已经通过 release 发布到 base branch。
- legacy completed 不自动变 ready、不自动纳入 release、不自动标记 released，默认仍不可修改并按归档隐藏；未来如需补发布，必须另行设计显式导入或重开流程。
- 旧库中 `requirements.status='active'` 的数据保持 active，`ready_at` 回填为 `NULL`，不会自动推断为 ready。
- release 功能迁移一次性交付 `ready_at`、`req reopen`、`release create/integrate/publish` 和 stage 展示；当前 `FinishRequirement` 提交、推送、清理后写入 `ready_at`，最终 `completed_at`/`archived_at` 只由 release publish 成功路径写入。
- `FinishRequirement` 已从调用 completed 标记改为只写 `ready_at`；`MarkRequirementCompleted` 只能由 `PublishRelease` 全部 repo 发布成功路径调用。
- Go 层同步更新 `store.Requirement`，新增 `ReadyAt sql.NullTime`；所有 requirement SELECT、scan、CRUD、CLI `req list/show` 输出都必须纳入 `ready_at`，并输出推导 `stage`。

状态约定：

- requirement status：`active`、`completed`。`ready_at` 表示 feature 开发已完成且可进入 release；Release 里程碑上线后新的 `completed` 只能在 release publish 成功后写入。归档不是 status，通过 `archived_at` 是否为空判断。
- requirement stage：由 `status`、`ready_at`、`archived_at` 和 repo relation 状态推导，展示为 `active`、`cleanup-pending`、`ready`、`completed`；它不是数据库 status。
- released 不是 requirement status；只有 completed 需求存在 published release active association 时才推导为 `released=true`。active association 定义为 `release_requirements.removed_at IS NULL`；旧库 completed 没有 published release active association，展示为 legacy completed。
- release membership 以 `release_requirements.id` 为唯一身份；同一需求 remove 后重新加入同一 release 会生成新的 membership，新旧 membership 通过不同 `release_requirement_id` 区分。
- requirement repo status：`active`、`pushed`、`completed`、`cleanup_failed`。
- release status：`draft`、`integrated`、`stale`、`published`、`failed`。
- release repo status：`pending`、`integrated`、`stale`、`published`、`failed`。
- cleanup-pending 不是新的 requirement status，而是由任一 `requirement_repos.status in ('pushed', 'cleanup_failed')` 推导出的临时锁定态。
- publish-in-progress 不是新的 release status，而是由任一 `release_repos.status='published'` 且 release `status!='published'` 推导出的临时锁定态；它的优先级高于 stale。
- operation status：`success`、`failed`。
- stale release 不是用户手工编辑状态，而是已有集成结果的非 publish-in-progress release 因 active 集成范围变化、需求 feature SHA 变化或发布目标分支外部新 commit 推导并持久化；active 集成范围定义为 `release_requirements.removed_at IS NULL`。draft release 尚未产生集成结果，执行 add/remove/reopen 相关范围变化后仍保持 draft。已发布 repo 的目标分支 HEAD 等于该 repo `published_sha` 时不算 stale。publish-in-progress 期间出现这些变化时不自动改走 stale/reintegrate，而是展示需要人工处理。
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
- `DeleteLocalBranch(barePath, branch)`
- `ForcePushBranch(worktreePath, remote, branch, expectedSHA)`
- `RevParse(worktreePathOrBarePath, ref)`
- `Merge(worktreePath, ref)`
- `MergeNoFF(worktreePath, ref, message)`
- `Checkout(worktreePath, branch)`
- `ResetHard(worktreePath, ref)`
- `HasNewCommitsSince(barePath, remote, branch, oldSHA)`
- `CommitHasParentBare(barePath, commit, parent)`

策略：

- repo 注册时 clone 为 bare repo。
- 创建或扩展需求前先 fetch，更新托管 bare repo 的 remote tracking refs。
- feature 分支名统一为 `feature/<req-slug>`。
- 如果本地 feature 分支已存在且未被其他 worktree 占用，则直接基于该分支创建 worktree。
- 如果远端 feature 分支已存在但本地不存在，则从 `<remote>/<feature-branch>` 创建本地分支并创建 worktree。
- 如果本地和远端 feature 分支都不存在，则从最新的 `<remote>/<base_branch>` 创建本地 feature 分支；`base_branch` 来自 repo 注册配置，不固定为 `master`。
- 如果 feature 分支已被其他 worktree 占用，或目标 worktree path 已存在，则返回可恢复错误，不覆盖用户文件。
- push 使用 `git push <remote> HEAD:refs/heads/<feature-branch>`。
- finish 前先对所有存在变更的 repo 检查 `user.name` 和 `user.email`；缺失时在任何 commit、push、cleanup 前失败，并输出对应 repo 的修复命令。
- repo update 只传 `--url` 时，仅执行 `SetRemoteURL(barePath, currentRemote, newURL)`。
- repo update 只传 `--remote` 时，仅执行 `RenameRemote(barePath, oldRemote, newRemote)` 并保留原 URL。
- repo update 同时传 `--remote` 和 `--url` 时，先执行 `RenameRemote(barePath, oldRemote, newRemote)`，再执行 `SetRemoteURL(barePath, newRemote, newURL)`。
- `DeleteLocalBranch` 只删除未被 worktree 占用的本地分支；被占用时返回可恢复错误。
- `ForcePushBranch` 使用 `git push --force-with-lease=<ref>:<expected-sha>`，expected SHA 来自 integrate 前 fetch 到的远端 release branch；远端 release branch 不存在时使用空 expected 语义或等价的安全创建策略。
- `RevParse` 返回指定 ref 的 commit SHA，用于记录 feature SHA、base SHA、release SHA 和 `published_sha`。
- `Merge` 执行非 fast-forward 或普通 merge 时必须保留冲突现场，返回可诊断错误，不自动 abort。
- `Checkout` 和 `ResetHard` 只用于 release publish 的目标分支临时 worktree；不得对需求 feature worktree 做隐式 reset。
- `HasNewCommitsSince` 使用 commit graph 判断 `<remote>/<base_branch>` 相对旧 SHA 是否出现外部新 commit，不读取 MR/PR 平台记录。
- `CommitHasParentBare` 用于 publish 自愈：当目标分支已经被上一次 publish push 到包含 release SHA 的 merge commit，但本地 DB 没写入 published 状态时，确认当前目标分支 HEAD 的父提交包含本 release SHA 后才能补写 `published_sha`。

Release Git 策略：

- release branch 统一为 `release/<release-slug>`。
- integrate 前 fetch release 涉及的所有 repo，并读取目标分支 `<remote>/<base_branch>` 的 SHA。
- integrate 使用删除/重建语义：先删除旧 release worktree；删除前执行 dirty check，默认 dirty 时拒绝删除；只有 `--force` 表示丢弃 release worktree 的临时修改。如果本地 release branch 存在且未被其他 worktree 占用，则删除本地 release branch。
- integrate 先按 active requirements 计算 active repo union；通过 dirty guard 后，删除并重建该 release 的 `release_repos` 和 `release_requirement_repos` 快照，只保留最新一次 integrate 的 active repo union 和 feature SHA 记录。
- 每个 active repo 从最新 `<remote>/<base_branch>` 创建新的 release worktree，再按 `release_requirements.removed_at IS NULL` 的 active 集成范围和 `release_requirements.position` 顺序 merge 对应需求的 `feature/<req-slug>`。
- 如果某个需求不涉及某 repo，则该 repo 跳过该需求的 merge。
- merge 冲突时停止当前 repo 和整个 release 集成，release 标记为 `failed`，写入 release operation log，并保留 release worktree 诊断现场；用户必须回 feature 分支修复后重新 integrate，下一次 integrate 负责删除并重建旧 release worktree 和最新快照。
- 集成成功后记录每个 active repo 的 `integrated_base_sha`、`release_sha`，以及每个 active membership 在每个 repo 上的 `feature_sha`；`release_requirement_repos.release_requirement_id` 必须写入当前 active membership ID。
- 远端 release branch 使用强制更新语义：`git push --force-with-lease=<remote-release-ref>:<expected-sha> <remote> HEAD:refs/heads/release/<release-slug>`。release branch 是可重建产物，不作为长期可编辑分支。
- status/publish 前重新 fetch，并确认 active 集成范围内每个需求仍是 ready、每个 relation 仍处于 completed，再比较当前 feature SHA 与上次 integrate 记录；已有集成结果的非 publish-in-progress release 在任一 active 需求不再 ready、relation 不再 completed 或 feature 变化时标记为 `stale`。draft release 保持 draft，但在需求重新 ready 前不能成功 integrate。publish-in-progress release 出现这些变化时阻塞并要求人工处理，不允许重新 integrate 覆盖已发布 repo。
- publish 前先检查所有 active repo 的 release worktree 和 publish 临时 worktree 必须干净，包括 retry 时已经 `published` 的 repo；dirty 时返回可恢复错误，提示通过 `req reopen` 回 feature 分支修复后重新 integrate。
- publish 目标分支临时 worktree 路径为 `~/.workspace-cli/work/releases/<release-slug>/.publish/<repo>`，同时记录在 `release_repos.publish_worktree_path`。路径不存在时创建并 checkout/reset 到最新 `<remote>/<base_branch>`；路径存在且是干净 worktree 时原地 `reset --hard` 到最新 `<remote>/<base_branch>`，不删除重建；路径存在但 dirty 时拒绝 publish 并提示用户手动确认和清理；路径存在但不是 git worktree 时拒绝 publish 并提示移走该路径。publish 成功后可清理该目录，失败时保留诊断信息，下一次 publish 按同一规则恢复。
- publish 前先重新计算 active repo union，并校验它与当前 `release_repos` 完全一致；不一致表示 release 快照已过期，非 publish-in-progress release 返回 stale 并要求重新 integrate，publish-in-progress release 阻塞并要求人工处理。
- publish 前检查目标分支是否有外部新代码使用 commit graph，不读取 MR/PR 记录。对尚未发布的 active repo，以 `git rev-list <integrated_base_sha>..<remote>/<base_branch>` 是否为空判断；对已 `published` repo，先 fetch 并比较当前 `<remote>/<base_branch>` HEAD 是否等于 `published_sha`。
- 如果目标分支存在外部新 commit，且 release 尚未进入 publish-in-progress，publish 返回可恢复错误并要求重新 integrate 和重新测试。publish-in-progress release 出现目标分支偏离时阻塞并要求人工处理。
- status/publish 前必须校验尚未发布 repo 的远端 `release/<release-slug>` HEAD 仍等于上次 integrate 记录的 `release_repos.release_sha`；不一致表示 release 分支不是已测试版本，非 publish-in-progress release 标记 stale 并要求重新 integrate，publish-in-progress release 阻塞并要求人工处理。若目标分支已经包含该 `release_sha` 且本地 published 状态写入失败，则先走自愈补写 `published_sha`，不因 release branch 后续漂移误挡已完成的同一次发布。
- 如果目标分支无外部新 commit，publish 将 release branch 以 `--no-ff -m <message>` merge 到 publish 临时 worktree 的本地目标分支，push 到 `<remote>/<base_branch>`；未传 `-m` 时使用可读默认 release message；每个 repo 成功后记录 `release_repos.status=published`，并把成功 push 后 `<remote>/<base_branch>` 的 HEAD 记录为 `published_sha`。
- 如果上一次 publish 已成功 push 但在本地写入 `release_repos.status=published` 或 `published_sha` 时失败，retry 时会发现目标分支相对 `integrated_base_sha` 有新 commit；此时只有当当前目标分支 HEAD 的父提交包含本次 `release_sha`，才允许把该 HEAD 自愈写为 `published_sha` 并继续。若目标分支 HEAD 不是本 release 的发布 merge commit，则仍按外部新 commit 阻塞。
- publish retry 对 `release_repos.status=published` 的 repo 先 fetch：若目标分支 HEAD 等于 `published_sha`，跳过该 repo；若不相等，阻塞并要求人工处理，不允许重新 integrate 覆盖已发布 repo。retry 只继续处理 `integrated|failed` 且尚未成功发布的 repo；所有 active repo 都为 `published` 后再标记 release published。publish-in-progress 中任何 feature SHA、目标分支 HEAD 或 active 集成范围变化都进入人工处理提示，不走 stale/reintegrate 自动恢复。

## 6. 核心服务

`internal/workspace.Service` 负责：

- `AddRepo`：clone bare repo，探测 base branch，写入 repo 表。
- `SyncRepo` / `SyncAllRepos`：fetch 托管 repo，更新 `refs/remotes/<remote>/<base_branch>` 等 remote tracking refs；不修改已有需求 worktree，也不 rebase 已创建的 feature 分支。
- `UpdateRepo`：更新 URL、remote、base branch，并同步 bare repo remote 配置；只更新 URL 时调用 `SetRemoteURL`，只更新 remote 时调用 `RenameRemote` 并保留原 URL，同时更新 remote 和 URL 时先 `RenameRemote` 再 `SetRemoteURL`；repo 被普通 active、cleanup-pending、ready 需求引用，或被任何未 published release 引用时，禁止修改 URL、remote、base branch。released 与 legacy completed 历史展示使用绑定快照，不随 repo update 改变。
- `RemoveRepo`：检查 repo 未被普通 active、cleanup-pending、ready 需求和未 published release 引用后写入 `deleted_at`，保留历史记录和 bare repo 清理的后续扩展空间。
- `CreateRequirement`：要求至少绑定一个 repo；创建需求记录、为每个初始 repo 写入 `repo_name`、`repo_url`、`repo_remote`、`repo_base_branch` 绑定快照、准备 workspace、创建 worktree。
- `AddRepoToRequirement`：向普通 active 需求追加 repo、写入 repo 绑定快照并创建 worktree；普通 active 定义为 `status=active`、`ready_at` 为空、`archived_at` 为空且不是 cleanup-pending；非普通 active 需求不可追加 repo。
- `FinishRequirement`：普通 active 路径执行 commit 身份预检、提交、推送和 cleanup；cleanup-pending 重试路径不执行 commit/push，只执行 dirty guard、missing worktree 自愈和 cleanup 重试；全部完成后写入 `ready_at`，表示 feature 分支可进入 release 集成。
- `ReopenRequirement`：仅允许 `ready_at` 已写入、`status=active`、未 completed、非 cleanup-pending、且未包含在 publish-in-progress release active 集成范围中的 ready 需求；需求展示仍使用 `requirement_repos.repo_*` 快照，实际 Git 操作通过 `repo_id` 读取当前托管 repo 的 `bare_path`、`remote`、`base_branch`。reopen 先对所有绑定 repo 预检 repo 未软删、bare repo 存在、feature 分支可用、目标 worktree path 不存在；所有预检通过后再创建 worktree。任一 worktree 创建失败时，删除本次已创建 worktree，保持 `ready_at`、relation status 和 release stale 状态不变；全部 worktree 创建成功后才清空 `ready_at`，将 relation 状态重置为 `active`，并把 active 集成范围包含该需求且已有集成结果的非 publish-in-progress、未发布 release 标记为 `stale`，draft release 保持 draft。
- `ArchiveRequirement`：仅允许 `status=completed` 的 released 或 legacy completed 需求写入或保留 `archived_at`；completed 且已归档时返回成功 no-op；active、ready 或 cleanup-pending 需求返回可恢复错误。
- `UpdateRequirement`：仅普通 active 需求允许更新标题；普通 active 定义为 `status=active`、`ready_at` 为空、`archived_at` 为空且不是 cleanup-pending；非普通 active 需求不可修改。
- `CreateRelease`：创建 release，写入 release 记录和初始 `release_requirements`；只允许选择 `ready_at` 已写入且未 completed 的需求。创建 release 与初始 membership 写入必须是 all-or-nothing，中途失败时删除本次 release row、已写入 membership 和 release workspace 空目录。
- `AddRequirementToRelease`：向未 published 且非 publish-in-progress 的 release 添加 ready 需求，追加到 active 集成范围；draft release 保持 draft，已有集成结果的 release 标记为 `stale`。如果该 requirement 在同一 release 中已有 removed history，不复用旧 row，而是创建新的 `release_requirements` membership 和新的 position。
- `RemoveRequirementFromRelease`：从未 published 且非 publish-in-progress 的 release active 集成范围移出需求，写入 `removed_at`，不删除需求和 feature 分支；移出后该需求不参与后续 integrate、publish 或 released 推导；draft release 保持 draft，已有集成结果的 release 标记为 `stale`。
- `ShowRequirement` / `ListRequirements`：除生命周期 status 和 archived 外，输出推导 stage：`active|cleanup-pending|ready|completed`；completed 需求额外输出 completion：`released` 或 `legacy-completed`。
- `ShowRelease` / `ListReleases` / `ReleaseStatus`：展示 release 状态和推导 `publish-in-progress`；`release list` 至少输出 key、DB status、推导 phase 和 title，其中 phase 在发布中显示为 `publish-in-progress`，否则等于 status。`show/status` 进一步展示 active 集成范围、removed history、stale 原因、feature SHA、target/base SHA、release SHA、per-repo publish 状态和人工处理提示；`ReleaseStatus` 必须先刷新 stale 检测再输出，不能只读取旧 DB 状态；publish-in-progress 时展示已发布 repo、未发布 repo 和 `published_sha` 校验结果。status 检查只对 `removed_at IS NULL` 的 active requirements 计算 integrate/publish/released 相关状态，removed history 只用于审计展示。
- `IntegrateRelease`：允许从 `draft|integrated|stale|failed` 执行，禁止对 `published` 或 publish-in-progress release 执行；只处理 `removed_at IS NULL` 的 active 集成范围；fetch 相关 repo、dirty check 旧 release worktree、按 `--force` 语义删除旧 release worktree/branch、清理不再属于 active repo union 的 obsolete repo release worktree、重建 release branch、按 active repo union 删除并重建 `release_repos` 和 `release_requirement_repos` 最新快照、按 active 需求顺序 merge feature 分支、记录 base/release/feature SHA、force-with-lease 推送 release branch，并将 release 标记为 `integrated`。merge 冲突后的 `failed` release 保留诊断 worktree，下一次 integrate 负责按 dirty/force 规则删除并重建。
- `PublishRelease`：要求 `--tested`；只发布 `removed_at IS NULL` 的 active 集成范围涉及的 repo；publish 前校验当前 `release_repos` 与 active repo union 完全一致；检查所有 active repo 的 release worktree 和 `.publish/<repo>` 临时 worktree 干净；fetch 目标分支；为每个 repo 创建或原地重置 `release_repos.publish_worktree_path` 到最新 `<remote>/<base_branch>`；用 commit graph 检查目标分支是否有外部新 commit；非 publish-in-progress release 无外部新 commit 时将 release branch merge 到 publish 临时 worktree 的目标分支并 push；单 repo 成功后立即记录 `release_repos.status=published`，并把成功 push 后 `<remote>/<base_branch>` 的 HEAD 记录为 `published_sha`；retry 时对已 published repo 也先执行 dirty guard，再确认目标分支 HEAD 仍等于 `published_sha`，相等才跳过，不相等则阻塞并要求人工处理；publish-in-progress 期间若 active feature SHA、目标分支 HEAD 或 active 集成范围变化，阻塞并提示人工处理；所有 active repo 成功后通过 store 事务同时标记 release `published`，并仅将 `removed_at IS NULL` 的 active requirements 写入 `status=completed`、`completed_at` 和 `archived_at`。

错误原则：

- 任一 repo 的 fetch、worktree、relation、status、commit identity、commit、push、cleanup 失败，都写 operation log。
- finish 中任一 commit/push 失败，不删除任何 worktree，需求保持 `status=active`，repo 关系状态保持 `active` 或原状态，不持久写入本轮 `pushed`。
- 只有所有 repo push 全部成功后，才批量将 `requirement_repos.status` 记为 `pushed` 并进入 cleanup；删除 worktree 成功后记为 `completed`。
- 删除 worktree 只在所有 push 成功后执行。
- cleanup 失败时将失败 repo 关系记为 `cleanup_failed`，需求保持可重试且不写入 `archived_at`；再次 finish 时跳过已 `completed` 的 repo cleanup，继续处理 `pushed` 或 `cleanup_failed` 的 repo。
- cleanup-pending 重试时，对仍存在的 `pushed` 或 `cleanup_failed` worktree 先执行 `HasChanges(worktreePath)`；若 dirty，返回可恢复错误，不删除 worktree，不修改该 relation 状态。
- cleanup-pending 重试时，如果 `pushed` 或 `cleanup_failed` relation 的 worktree path 已不存在，视为清理已完成，将该 relation 标记为 `completed`。
- 所有 relation 都变为 `completed` 后，才能写入 requirement 的 `ready_at`；`status=completed`、`completed_at`、`archived_at` 只能由 release publish 成功写入。
- cleanup-pending 需求只允许 `show`、`list`、`finish`；`UpdateRequirement`、`AddRepoToRequirement`、`ArchiveRequirement` 必须返回可恢复错误。
- ready 需求可以被 release 选择，也可以通过 `ReopenRequirement` 清空 `ready_at`、恢复 feature worktree 并回到普通 active；ready 需求不能 update/add-repo/archive；已包含在 publish-in-progress release active 集成范围中的 ready 需求不能 reopen；completed、released 和 legacy completed 需求不可 update/add-repo/reopen；未归档 completed 需求允许 archive 写入 `archived_at`，已归档 completed 需求重复 archive 作为幂等 no-op 成功返回。
- `ReopenRequirement` 必须 all-or-nothing；只有全部 worktree 创建成功后才能在同一个 DB 事务中清空 `ready_at`、更新 relation 并标记 release stale，事务失败或 worktree 创建失败时必须移除本次已创建 worktree，并保持原 DB 状态。
- `CreateRelease` 必须 all-or-nothing；初始 membership 写入失败时不得留下 draft release 或部分 membership。
- release integrate 删除旧 release worktree 前必须执行 dirty check；dirty 且未传 `--force` 时返回可恢复错误，传 `--force` 表示丢弃 release worktree 临时修改。
- release integrate 的 repo/feature SHA 表是最新快照；每次非 publish-in-progress integrate 都按 active repo union 删除并重建 `release_repos` 和 `release_requirement_repos`，被移出需求独占的 repo 不再保留为待发布 repo，且其旧 release worktree 会在 dirty guard 后移除。
- release integrate 失败时不写入 published，不更新需求 completed 状态；已创建的 release worktree 保留用于诊断或由下一次 integrate 删除重建。
- release integrate 可从 failed 状态重试；published release 禁止重新 integrate。
- 非 publish-in-progress release stale 时禁止 publish；只能重新 integrate，且重新 integrate 后需要用户再次确认测试。
- publish-in-progress release 优先级高于 stale，只允许 `show`、`status` 和 `PublishRelease` retry；`AddRequirementToRelease`、`RemoveRequirementFromRelease`、`IntegrateRelease` 和相关需求 `ReopenRequirement` 必须返回可恢复错误。
- release publish 前必须检查待发布 release worktree 和 `.publish/<repo>` 临时 worktree 干净；dirty 时返回可恢复错误，不执行任何目标分支 push。
- publish-in-progress retry 中，已 `published` repo 也必须执行 release worktree 与 `.publish/<repo>` dirty guard，防止部分发布后继续发布剩余 repo 时忽略直接修改。
- release publish 前必须校验 `release_repos` 与 active repo union 一致；非 publish-in-progress release 不一致时标记 stale 并要求重新 integrate，publish-in-progress release 不一致时阻塞并要求人工处理。
- release status/publish 前必须校验尚未发布 repo 的远端 release branch HEAD 等于 `release_repos.release_sha`；不一致时非 publish-in-progress release 变 stale，publish-in-progress release 展示人工处理原因，且不能 merge/push 目标分支，避免发布未经测试的 release commit。
- release status/publish 前如果 active 集成范围中的需求已经不再 ready，例如已被另一个 release 发布为 completed，非 publish-in-progress release 必须标记 stale 并阻止发布；publish-in-progress release 必须阻塞并提示人工处理。
- release publish 中任一 repo 失败时停止后续 repo 发布，将失败 repo 记为 `release_repos.status=failed` 并写入 release operation log；如果尚无 repo 成功发布，release 保持 `integrated` 以便直接 retry；如果已有成功 push 的目标分支，v1 不自动回滚，已成功 repo 标记为 `published` 并记录 `published_sha`，release 进入 publish-in-progress，后续 retry 先校验目标分支 HEAD。retry 继续处理 `integrated|failed` 且尚未成功发布的 repo。
- 所有 repo 都发布成功后，release status 与 active requirement completed/archived 标记必须在同一个 DB 事务中完成；如果最终落库失败，不能出现部分需求 completed、release 仍未 published 的状态。
- release publish push 已经成功但写本地 `published` 状态失败时，retry 必须通过目标分支 HEAD 的父提交包含本 release SHA 来识别这是同一次 publish 的 merge commit，随后补写 `published_sha`；不能仅因为 release SHA 是祖先就自愈，否则会误吞后续外部提交。
- publish 前目标分支外部新 commit 判断只基于 commit SHA/commit graph，不依赖 MR/PR 平台记录；目标分支 HEAD 等于该 repo 已记录的 `published_sha` 时不算外部新 commit。
- publish retry 时，已 published repo 的目标分支 HEAD 不等于 `published_sha` 必须阻塞并要求人工处理，不能重新 integrate 覆盖已发布 repo。
- publish-in-progress 期间若 feature SHA、目标分支 HEAD 或 active 集成范围变化，不能把 release 自动标记为普通 stale 并要求重集成，必须阻塞并在 `release status/show` 中展示人工处理原因。

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
- `workspace dev` 和 `workspace ide` 启动前先读取 requirement stage；cleanup-pending 需求必须拒绝启动工具，提示继续执行 `workspace req finish <req>` 完成清理。
- stdin/stdout/stderr 继承当前终端。
- v1 不记录会话 ID，不管理会话恢复。

## 8. 测试方案

单元测试：

- config 默认路径和 init 输出。
- config 默认包含 `codex`、`claude`、`vscode`、`cursor`、`zed` 工具命令，且可覆盖 IDE 命令。
- slug 与 feature branch 生成。
- SQLite versioned migration 和基本 CRUD；从 `v0.1.0` 旧库升级时补 `requirements.ready_at`，旧 active 数据保持 active，旧 completed 数据保持 legacy completed。
- migration 测试确认 release 表新增不重建旧表、不丢已有 repo/requirement/relation/log 数据。
- migration 测试确认无 `schema_migrations` 的 v0.1 旧库会记录 `0001_baseline_v0_1_0`，再执行 `0002_ready_release_flow`；重复执行 migration 为 no-op。
- migration 测试确认 `0002_ready_release_flow` 创建 release 表、`release_requirement_id` 外键和 active membership partial unique index。
- `store.Requirement.ReadyAt`、所有 requirement SELECT、scanner、CRUD 和 CLI `req list/show` 输出都包含 `ready_at`；新增漏字段回归测试，防止 scanner/SELECT 未同步更新。
- requirement status、`ready_at` 与 `archived_at` 状态。
- requirement stage 推导覆盖 `active`、`cleanup-pending`、`ready`、`completed`。
- cleanup-pending 由 `requirement_repos.status` 推导，不新增 requirement status。
- `requirement_repos.status` 枚举不包含 `removed`。
- repo soft delete 的 `deleted_at` 行为。
- `CreateRequirement` 的初始 repo 绑定和 `AddRepoToRequirement` 的追加绑定都写入 `repo_name`、`repo_url`、`repo_remote`、`repo_base_branch` 快照。
- repo update/remove 被普通 active、cleanup-pending、ready 需求引用时禁止修改 URL、remote、base branch 或删除 repo。
- repo update/remove 被任何未 published release 引用时禁止修改或删除 repo。
- repo update 只传 URL、只传 remote、同时传 URL 和 remote 时分别触发正确的 bare repo remote 更新规则。
- completed 历史需求详情使用 repo 快照，不随 repo update 改变。
- legacy completed 不自动变 ready、不自动纳入 release、不自动标记 released，默认不可修改并按归档隐藏。
- released 与 legacy completed 通过 published release active association 推导，`req list --all` 与 `req show` 必须能区分 `released` 和 `legacy-completed`。
- released-but-unarchived、legacy completed 和已归档 completed 需求执行 update、add-repo 均被拒绝；已归档 completed 需求再次 archive 成功 no-op。
- active、ready 和 cleanup-pending 需求执行 archive 报错，released 与 legacy completed 需求可写入或保留 `archived_at`，已归档 completed 需求再次 archive 成功 no-op。
- home 解析优先级为 `--home` > `WORKSPACE_CLI_HOME` > 默认 home。
- `FinishRequirement` 全部 repo cleanup 后只写入 `ready_at`，不写入 `completed_at` 或 `archived_at`，且不调用 `MarkRequirementCompleted`。
- `MarkRequirementCompleted` 只能由 `PublishRelease` 全部 repo 发布成功路径调用。
- 当前 `FinishRequirement` 行为为提交、推送、清理后写入 `ready_at`；`MarkRequirementCompleted` 继续只允许由 release publish 成功路径调用。
- ready 需求不可 update/add-repo/archive，但可以加入 release。
- `ReopenRequirement` 只允许未包含在 publish-in-progress release active 集成范围中的 ready 需求，展示使用 repo 绑定快照，Git 操作使用 `repo_id` 指向的当前托管 repo；执行后清空 `ready_at`、恢复 relation 为 `active`，并将 active 集成范围包含该需求且已有集成结果的非 publish-in-progress、未发布 release 标记为 `stale`，draft release 保持 draft。
- `ReopenRequirement` 遇到 repo 已软删、bare repo 不存在、feature 分支被占用或目标 worktree path 已存在时返回可恢复错误。
- `ReopenRequirement` 中途 worktree 创建失败或 DB 事务失败时清理本次已创建 worktree，并保持 `ready_at`、relation status 和 release stale 状态不变。
- 普通 active、cleanup-pending、completed、legacy completed 和 publish-in-progress 中的 ready 需求执行 reopen 均报错。
- release status 枚举为 `draft|integrated|stale|published|failed`。
- `CreateRelease` 中途 DB 失败不会留下 partial release 或 partial membership。
- publish-in-progress 由 repo publish 状态推导，不新增 release status。
- release requirement 顺序、移出 `removed_at`、release repo SHA 快照、feature SHA 快照都可正确读写；`removed_at IS NULL` 被统一作为 active release association。
- 同一 release 中 remove 后 re-add 同一 requirement 会创建新的 `release_requirements.id`，`release_requirement_repos.release_requirement_id` 指向当前 membership；同一时间只能有一个 active association。
- `release_repos` 和 `release_requirement_repos` 作为 latest integrate 快照读写；重新 integrate 后 obsolete repo 和旧 feature SHA 快照不再参与 publish/status。
- 非 publish-in-progress release 中，已有集成结果的 release 在 active 集成范围变化、feature SHA 变化、目标分支出现外部新 commit 时会推导为 stale；draft release 的 add/remove/reopen 范围变化不置 stale。
- 非 publish-in-progress release 中，active 集成范围内需求不再 ready 或 relation 不再 completed 时，release status 推导为 stale，并在 status/show 中展示原因。
- publish-in-progress release 中，active 集成范围变化、feature SHA 变化、目标分支 HEAD 偏离或已发布 repo HEAD 偏离 `published_sha` 都会阻塞并展示人工处理提示，不允许 integrate。

Git 集成测试：

- 使用临时目录创建 bare remote。
- seed 一个 main 分支。
- `AddRepo` clone bare repo。
- `CreateRequirement` 创建 feature worktree。
- 本地 feature 分支、远端 feature 分支、base branch 三种创建路径。
- `CreateRequirement` 在 repo add 后远端 base branch 又有新提交时，会先 fetch 并从最新 `<remote>/<base_branch>` 创建 worktree。
- `SyncRepo` 能在远端 base branch 更新后刷新 bare repo 的 remote tracking ref。
- `IntegrateRelease` 能把多个 ready 需求按顺序 merge 到 `release/<release-slug>`。
- 同一个 release 涉及多个 repo 时，每个 repo 都从各自最新 `<remote>/<base_branch>` 重建 release branch。
- release integrate 成功后记录 `integrated_base_sha`、`release_sha` 和每个需求的 `feature_sha`。
- release integrate 按 active repo union 重建 `release_repos`；remove requirement 后重新 integrate，不再包含被移出需求独占的 repo，并移除该 repo 的旧 release worktree。
- release integrate 写入 `release_requirement_repos.release_requirement_id`，remove 后 re-add 的同一 requirement 可区分新旧 membership。
- release publish 在目标分支无外部新 commit 时将 release branch merge 到 `base_branch` 并 push。
- release publish 前发现远端 release branch HEAD 与 `release_repos.release_sha` 不一致时拒绝发布，不 push 任何目标分支。
- release publish 使用 `.publish/<repo>` 临时 worktree 创建或重置目标分支，成功后可清理，失败时保留诊断信息且下次可重建。
- release publish 对 `.publish/<repo>` 的四种路径状态都有明确行为：不存在则创建，干净 worktree 则原地 reset，dirty worktree 则拒绝，非 worktree 路径则拒绝。
- partial publish retry 会继续对已 published repo 执行 dirty guard；已发布 repo 变脏时阻止发布剩余 repo。
- release publish 最终状态落库失败时，不会部分完成 active requirements；修复 DB 问题后可 retry finalization。
- release publish 成功后，仅将 `removed_at IS NULL` 的 active requirements 写入 `status=completed`、`completed_at` 和 `archived_at`，并由 published release active association 推导为 `released=true`。
- 已写入 `removed_at` 的 requirement 不参与 integrate/publish，不被写入 completed/archived，也不被推导为 released。
- release publish 前发现 `release_repos` 与 active repo union 不一致时拒绝发布：非 publish-in-progress 标记 stale，publish-in-progress 阻塞人工处理。
- `ReopenRequirement` 能在 finish 清理 worktree 后恢复 feature worktree，修复后再次 finish 会产生新的 feature SHA。
- 修改 worktree 文件。
- `FinishRequirement` 在普通 active 路径 commit、push feature 分支、删除 worktree、标记 ready。

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
- 所有 relation 都变为 `completed` 后，需求只写入 `ready_at`，不写入 completed 或 archived 时间。
- repo remove 只写 `repos.deleted_at`，不写 `requirement_repos.status=removed`。
- repo update 的 URL only 场景调用 `SetRemoteURL`，remote only 场景调用 `RenameRemote` 并保留 URL，remote + URL 场景先 `RenameRemote` 再 `SetRemoteURL`。
- release integrate merge 冲突时标记 failed，保留诊断信息，不发布任何目标分支。
- 非 publish-in-progress release 测试后 feature 分支有新 commit 时，publish 前 status 检测为 stale，必须重新 integrate。
- 非 publish-in-progress release 的 active requirement 被其它 release 发布或不再 ready 时，status/publish 检测为 stale，必须人工移出、重开或重新设计 release 范围后再继续。
- 非 publish-in-progress 且已有集成结果的 release 执行 remove-req 后 status 为 stale，重新 integrate 后不再包含被移出的需求；draft release 执行 add-req/remove-req 后仍保持 draft。
- release remove-req 后再 add-req 同一需求会生成新 membership；旧 membership 保持 `removed_at` 历史，不参与 released 推导。
- remove requirement 后如果某 repo 不再属于 active repo union，重新 integrate 后该 repo 不再触发 publish 或 publish-in-progress。
- 非 publish-in-progress release 的目标分支出现外部新 commit 时阻塞 publish，提示重新 integrate 和重新测试。
- release publish 前发现 release worktree dirty 时拒绝发布，不 push 任何目标分支。
- release publish 前发现 `.publish/<repo>` 临时 worktree dirty 时拒绝发布，不 push 任何目标分支。
- release integrate 删除旧 release worktree 前发现 dirty 时默认拒绝；传 `--force` 时删除并重建。
- release publish 失败时记录失败 repo 为 `failed` 并写入 operation log；部分成功后记录成功 repo 为 `published` 和 `published_sha`；再次 publish 先校验已成功 repo 的目标分支 HEAD，并继续处理 `integrated|failed` 的未发布 repo。
- release publish 首个 repo 失败且尚无 repo 标记为 `published` 时，release 保持 `integrated` 并允许修复外部原因后直接 retry。
- 已成功发布 repo 的目标分支 HEAD 等于 `published_sha` 时，retry publish 跳过该 repo；不相等时阻塞并要求人工处理。
- publish-in-progress release 拒绝 add-req、remove-req、integrate 和相关 req reopen，只允许 show/status/publish retry。
- publish-in-progress 中 feature SHA、目标分支 HEAD 或 active 集成范围变化时，release status/show 展示人工处理原因，不提示自动重新 integrate。
- 已成功发布 repo 的目标分支 HEAD 不等于 `published_sha` 时，retry publish 阻塞并要求人工处理，不允许重新 integrate 覆盖已发布 repo。
- failed release 可以重新执行 integrate；published release 执行 integrate 报错。
- publish 不读取 MR/PR 记录；空 MR/PR 不影响 commit graph 判断。
- release worktree 有直接修改时不作为正式 bugfix 来源，重新 integrate 会删除并重建 release 分支。

CLI 测试：

- 命令缺参和重复资源返回错误。
- `repo list`、`req list`、`req show` 输出可读。
- `repo list` 默认隐藏 soft deleted repo，`repo list --all` 展示。
- `req list --all` 同时展示 lifecycle status、推导 stage、archived 状态和 completed 需求的 completion。
- `req show` 展示推导 stage，并在 completed 需求上展示 `released` 或 `legacy-completed` completion。
- `req reopen` 对 ready 需求恢复 feature worktree；对 active、cleanup-pending、completed 需求返回错误。
- `workspace --home <path> repo list` 等任意子命令都使用指定 home。
- `workspace repo sync <name>` 能刷新指定 repo 的 remote tracking ref。
- `release create/list/show/status` 输出 release 状态；`release list` 输出 DB status 与推导 phase，publish-in-progress release 可从列表直接识别；`release show/status` 输出需求集合、repo SHA、stale/manual 原因。
- `release integrate` 成功后输出 release branch 和 workspace path。
- `release publish --tested` 成功后输出 published release；缺少 `--tested` 时拒绝发布。
- 非 publish-in-progress 的已 integrated release 执行 `release add-req` 和 `release remove-req` 都会变为 stale；draft release 执行 add/remove/reopen 相关范围变化后仍保持 draft。
- cleanup-pending requirement 执行 `workspace dev` 或 `workspace ide` 时拒绝启动外部工具，并提示继续 finish cleanup。
- `dev` 对未知工具返回错误。
- `ide` 默认使用 `vscode`，把 requirement workspace path 作为最后一个参数传给 IDE 命令。
- `ide --tool cursor|zed` 使用对应配置命令。
- `ide` 对未知 IDE tool 返回 `unknown ide tool "<tool>"`。
