# workspace-cli Review 记录

## Review 1: 文档计划终审

- Reviewer: Atlas
- 结论：Approved
- Blocking issues: 无
- Non-blocking note：测试方案可更明确写出 completed-but-unarchived 和已归档 completed 需求都拒绝 `update`、`add-repo`。
- 处理结果：已将测试方案描述改为显式覆盖 `update`、`add-repo` 拒绝和已归档 completed `archive` no-op。

## Review 2: Review 后文档优化复审

- Reviewer: Delta
- 结论：Approved
- Blocking issues: 无
- Non-blocking note：已归档 completed `archive` no-op 在测试方案相邻两行重复覆盖，但不冲突、无害。
- 处理结果：暂不改动语义；该重复属于可接受的 traceability 冗余。

## Review 3: 实现里程碑自审

- Reviewer: Codex local self-review
- 范围：当前 Go 实现、CLI 命令面、需求状态机、Git worktree 主流程、测试覆盖和文档记录。
- 结论：With fixes，已在本轮处理发现项。
- 发现项：
  - `FinishRequirement` 曾在单个 repo push 成功后立即写入 `pushed`，不符合“全部 push 成功后批量推进状态”；已新增失败集成测试并修复。
  - `operation_logs` 曾只建表不写入；已新增失败日志 API，并覆盖 finish 的状态检查、commit 身份、commit、push、cleanup 失败路径。
  - `DefaultBranch` 只存在于技术方案，代码未实现；已新增默认分支探测并在 `AddRepo` 未传 base 时使用。
- 验证：
  - `env GOCACHE=/private/tmp/workspace-cli-gocache GOMODCACHE=/private/tmp/workspace-cli-gomodcache GOSUMDB=off go test ./...`
  - `env GOCACHE=/private/tmp/workspace-cli-gocache GOMODCACHE=/private/tmp/workspace-cli-gomodcache GOSUMDB=off go vet ./...`
- 剩余风险：尚未由外部 reviewer 子代理审查本轮代码实现；当前为本地自审记录。

## Review 4: Echo 实现审查

- Reviewer: Echo
- 结论：Changes requested
- Blocking issues:
  - cleanup-pending 清理时，如果 `git status` 因非 worktree-missing 的原因失败，当前实现可能继续尝试删除 worktree；应记录失败并停止清理。
  - cleanup-pending retry 没有独立 cleanup-only 路径，混合 `active+pushed` relation 时仍可能提交/推送 active relation；应检测 cleanup-pending 并只执行清理恢复。
  - `FinishRequirement` 在 cleanup 循环后未重新确认所有 relation 都是 `completed` 就写入 requirement completed；应在完成前强校验。
- Non-blocking notes:
  - repo update 缺少 remote+URL 同时更新测试。
  - cleanup-pending 的 `update`、`add-repo`、`archive` 锁定测试还不够直接。
  - 缺少测试证明 cleanup-pending retry 不会 commit/push。
  - commit 身份错误消息缺少 repo/requirement 上下文和修复命令。
- 处理计划：按 TDD 先补失败测试，再修复 finish 状态机与错误消息，最后重新运行全量验证。

## Review 5: Delta 实现审查

- Reviewer: Delta the 2nd
- 结论：Changes requested
- Blocking issues:
  - `FinishRequirement` 仍缺少 cleanup-pending 的 cleanup-only 分支，混合 `active+pushed` relation 时会继续处理 active relation 并最终完成需求；本地 `go test ./...` 同步复现该失败。
  - cleanup retry 对非 missing-worktree 的 `HasChanges`/`git status` 失败没有停止删除，可能在 dirty guard 未成功执行时继续删除 worktree。
- Non-blocking notes:
  - completed-but-unarchived 修改拒绝、cleanup-pending 修改锁、cleanup-pending repo update 拒绝、remote+URL 同时更新仍需更直接测试。
  - SQLite status 列未加 schema-level enum 约束；当前 v1 先依赖 service 常量和测试保护。
- 处理计划：拆分 finish 普通 active 路径与 cleanup-pending 重试路径，补齐状态检查失败保护、完成前 relation 全 completed 校验，并补直接测试覆盖。

## Review 6: Atlas 复审

- Reviewer: Atlas the 2nd
- 结论：Approved
- Blocking issues: 无
- Non-blocking notes:
  - `TestFinishCleanupPendingStopsWhenStatusCheckFails` 已验证 `cleanup_failed` 和 worktree 保留，但未直接断言 `cleanup_status` operation log；实现已在 `service.go` 写入该日志。
  - `TestFinishCleanupPendingRejectsDirtyWorktree` 已验证拒绝清理和 worktree 保留，但未直接断言 relation 状态保持；实现未在 dirty 分支修改状态。
- 覆盖确认：
  - `FinishRequirement` 已拆分普通 active 与 cleanup-pending cleanup-only 路径。
  - 普通 active 路径只在全部 push 成功后批量持久化 `pushed`。
  - cleanup-pending 重试不执行 commit/push，支持 missing worktree 自愈、dirty 拒绝清理、status 检查失败写 `cleanup_failed`。
  - 完成前会重新确认所有 relation 都为 `completed`。
  - remote+URL repo update、cleanup-pending mutation guard、completed-but-unarchived guard、push 失败不持久化 `pushed` 均已有测试覆盖。

## Review 7: Echo 完整目标复审

- Reviewer: Echo the 2nd
- 结论：Approved
- Blocking issues: 无
- Non-blocking notes:
  - `AddRepoToRequirement` 的 fetch failure 已实现日志写入，但当前只有 add-repo worktree failure 的直接测试；可补 focused add-repo fetch failure log 测试。
  - operation log 测试主要断言 operation/status，未在所有测试中直接断言 `requirement_id`、`repo_id`、`message` 字段；实现通过 `store.LogOperation` 写入这些字段。
- 覆盖确认：
  - `BranchInUse` 使用 `git worktree list --porcelain` 精确匹配本地分支 ref，`CreateWorktree` 会在本地 feature 分支被占用时返回可恢复错误。
  - 创建/追加需求的 fetch/worktree 失败路径会写入失败 operation log。
  - cleanup-pending finish 状态机仍符合已确认语义。
  - reviewer 复跑 `go test ./...` 和 `go vet ./...` 均通过。
- 处理计划：补 add-repo fetch failure 直接测试，并加强 operation log 字段断言。

## Review 8: Delta 测试增强复审

- Reviewer: Delta the 3rd
- 结论：Approved
- Blocking issues: 无
- Non-blocking notes: 无
- 覆盖确认：
  - Review 7 的非阻塞建议已闭环：已新增 `TestAddRepoToRequirementLogsFetchFailure`。
  - create/add-repo 的 fetch/worktree failure 测试已断言 `requirement_id`、`repo_id` 和非空 `message`。
  - 测试使用真实 service、SQLite store、临时 Git remote 和 `gitx.ExecRunner`，不是 mock-only 路径。
  - Step 15 已写入 `docs/implementation-steps.md`。

## Review 9: Codex 全面功能审查

- Reviewer: Codex local review
- 结论：Changes requested
- Blocking issues:
  - `CreateRequirement` 在 fetch/worktree/relation 失败时会留下 requirement 记录，极端情况下可留下 0 repo active 需求并允许后续 finish 标记 completed；同时同 key 重试会撞唯一约束。
- Non-blocking notes:
  - 文档“每个关键操作写入 operation log”与当前只记录失败操作不一致，需要明确 v1 失败日志语义。
  - CLI 层缺少 `repo list --all`、`req list --all`、`req archive`、`dev --tool unknown`、`WORKSPACE_CLI_HOME` 的直接测试证据。
- 验证：
  - `go test -count=1 ./...`、`go vet ./...`、`go build ./cmd/workspace` 均通过，但审查发现功能语义缺口。
- 处理计划：按 TDD 增加 create all-or-nothing 和 add-repo 补偿测试，补偿清理实现、文档澄清和 CLI 测试。

## Review 10: Atlas all-or-nothing 复审

- Reviewer: Atlas the 3rd
- 结论：Approved
- Blocking issues: 无
- Non-blocking notes:
  - 当前目录没有本地 `.git`，Reviewer 无法做 diff-level review，只能基于当前文件和计划文档审查。
  - 初始 `CreateRequirement` 的 relation insert 失败缺少 focused 测试；实现已覆盖该路径，但补充直接回归测试可提升可追踪性。
  - 补偿清理不是 crash-atomic；进程在 DB insert 和 cleanup 之间崩溃时仍可能留下半成品状态，该风险超出本轮修复计划。
- 覆盖确认：
  - `CreateRequirement` 已在 fetch/worktree/relation 失败时记录失败 operation log，删除已创建 worktree 和 relation，并删除 requirement 记录。
  - `AddRepoToRequirement` 已在 relation 写入失败时删除刚创建的 worktree，并保留原需求状态。
  - 文档已澄清 v1 记录关键失败 operation log，成功日志是后续增强。
  - CLI 测试覆盖 `repo list --all`、`req list --all`、`req archive`、未知 dev tool、`WORKSPACE_CLI_HOME`。
- Reviewer 验证：
  - `go test -count=1 ./...` 通过。
  - `go vet ./...` 通过。
  - `go build -o /private/tmp/workspace-cli-review-readonly/workspace ./cmd/workspace` 通过。
