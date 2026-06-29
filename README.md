# workspace-cli

workspace-cli 是一个本地命令行工具，用来管理“需求开发空间”。它可以把一个需求涉及的多个 Git repo 以 `git worktree` 的方式集中到同一个 workspace 中，方便使用 Codex 或 Claude Code 做跨仓库开发。

项目当前聚焦 v1：管理本地开发流程，不接管 PR、CI、代码评审、issue 平台或权限体系。

## 核心能力

- 管理多个 Git repo，并以本地 bare clone 作为托管源。
- 创建需求并绑定一个或多个 repo。
- 为每个需求统一创建 `feature/<req-slug>` 分支。
- 将多个 repo 的 worktree 集中放进同一个需求 workspace。
- 从需求 workspace 启动 Codex 或 Claude Code。
- 完成需求时自动检查、提交、推送、清理 worktree 并归档需求。
- 使用 SQLite 记录 repo、需求、绑定快照、状态和失败操作日志。

## 安装与构建

```bash
go build -o workspace ./cmd/workspace
```

## 快速开始

```bash
./workspace init

./workspace repo add backend git@github.com:example/backend.git --base main
./workspace repo add frontend git@github.com:example/frontend.git --base main

./workspace req create "支付链路优化" --key pay-flow --repo backend --repo frontend
./workspace dev pay-flow --tool codex

./workspace req finish pay-flow -m "feat: complete pay-flow"
```

默认状态目录是 `~/.workspace-cli`。也可以通过全局参数或环境变量指定：

```bash
workspace --home /tmp/workspace-cli req list
WORKSPACE_CLI_HOME=/tmp/workspace-cli workspace repo list
```

## 设计文档

- [需求规划](docs/requirements-planning.md)
- [技术实现方案](docs/technical-implementation-plan.md)
- [实现步骤记录](docs/implementation-steps.md)
- [Review 记录](docs/review-log.md)

## GitHub Pages

项目介绍页位于 [docs/index.html](docs/index.html)，发布后用于展示 workspace-cli 的定位、流程和命令示例。

