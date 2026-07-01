# workspace-cli

workspace-cli 是一个本地命令行工具，用来管理“需求开发空间”。它可以把一个需求涉及的多个 Git repo 以 `git worktree` 的方式集中到同一个 workspace 中，方便使用 Codex、Claude Code 或 IDE 做跨仓库开发。

项目当前聚焦 v1：管理本地开发流程，不接管 PR、CI、代码评审、issue 平台或权限体系。

## 核心能力

- 管理多个 Git repo，并以本地 bare clone 作为托管源。
- 创建需求并绑定一个或多个 repo。
- 为每个需求统一创建 `feature/<req-slug>` 分支。
- 将多个 repo 的 worktree 集中放进同一个需求 workspace。
- 从需求 workspace 启动 Codex、Claude Code，或用 VS Code/Cursor/Zed 打开 workspace。
- 完成需求开发时自动检查、提交、推送、清理 worktree，并让需求进入 ready/可集成阶段。
- 将多个 ready 需求集成到可重建的 `release/<release-slug>` 分支，测试通过后发布到各 repo 的 base branch。
- 使用 SQLite 记录 repo、需求、绑定快照、状态和失败操作日志。

## 安装与构建

一键安装最新 release：

```bash
curl -fsSL https://idefav.github.io/workspace-cli/install.sh | sh
```

默认安装到 `/usr/local/bin/workspace`。也可以指定用户态目录：

```bash
INSTALL_DIR="$HOME/.local/bin" sh -c "$(curl -fsSL https://idefav.github.io/workspace-cli/install.sh)"
```

从源码构建：

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
./workspace ide pay-flow

./workspace req finish pay-flow -m "feat: complete pay-flow"

./workspace release create "2026-07-01 发布" --key 2026-07-01 --req pay-flow
./workspace release integrate 2026-07-01
./workspace release publish 2026-07-01 --tested -m "release: 2026-07-01"
```

创建需求时，workspace-cli 会先同步每个 repo 配置的 base branch 最新代码，再创建 `feature/<req-slug>` worktree。也可以手动同步托管 repo：

```bash
workspace repo sync backend
workspace repo sync
```

当前版本中，`workspace req finish` 会完成需求 feature 分支的提交、推送和 worktree 清理，并将需求标记为 ready。需求最终 completed/archived 由 `workspace release publish --tested` 成功后写入。

## Release 流程

release 流程把多个 ready 需求集成到可重建的 release 分支；测试通过后，发布会把 release 分支合并到各 repo 的 base branch，并将 active 集成范围内的需求标记为 completed/archived。

历史版本中已经通过 `workspace req finish` 完成的需求会保留为 legacy completed，不会在当前 Release 流程中自动视为 released，也不会自动进入 release 集成范围。

```bash
workspace release create "2026-07-01 发布" --key 2026-07-01 --req pay-flow
workspace release integrate 2026-07-01
workspace release publish 2026-07-01 --tested -m "release: 2026-07-01"
```

release 测试修复流程可以使用 `workspace req reopen <key-or-slug>` 恢复 feature worktree，修复后重新 `req finish`，再重新 `release integrate` 和测试。

## 打开 IDE

默认使用 VS Code 打开需求 workspace：

```bash
workspace ide pay-flow
```

也可以选择其他内置 IDE：

```bash
workspace ide pay-flow --tool cursor
workspace ide pay-flow --tool zed
```

默认状态目录是 `~/.workspace-cli`。也可以通过全局参数或环境变量指定：

```bash
workspace --home /tmp/workspace-cli req list
WORKSPACE_CLI_HOME=/tmp/workspace-cli workspace repo list
```

## 更新 CLI

```bash
workspace version
workspace update --check
workspace update
```

`workspace update` 会查询 GitHub latest release，下载当前平台对应的包，校验 `checksums.txt` 后替换当前二进制。

## Shell Completion

workspace-cli 支持生成 bash、zsh、fish 和 PowerShell 的自动补全脚本。

zsh：

```zsh
mkdir -p "$HOME/.zsh/completions"
workspace completion zsh > "$HOME/.zsh/completions/_workspace"
grep -q '.zsh/completions' "$HOME/.zshrc" 2>/dev/null || \
  echo 'fpath=("$HOME/.zsh/completions" $fpath)' >> "$HOME/.zshrc"
exec zsh
```

bash：

```bash
workspace completion bash > /usr/local/etc/bash_completion.d/workspace
```

如果没有 `/usr/local/etc/bash_completion.d` 写权限，可以改写到用户目录，并在 shell 启动文件中 source：

```bash
mkdir -p "$HOME/.local/share/bash-completion/completions"
workspace completion bash > "$HOME/.local/share/bash-completion/completions/workspace"
```

fish：

```bash
mkdir -p "$HOME/.config/fish/completions"
workspace completion fish > "$HOME/.config/fish/completions/workspace.fish"
```

PowerShell：

```powershell
workspace completion powershell >> $PROFILE
```

也可以查看对应 shell 的内置说明：

```bash
workspace completion zsh --help
workspace completion bash --help
workspace completion fish --help
workspace completion powershell --help
```

## Release

推送符合 `v*.*.*` 的 tag 会触发 GitHub Actions 自动构建多平台包并发布 GitHub Release：

- `darwin-amd64`
- `darwin-arm64`
- `linux-amd64`
- `linux-arm64`
- `windows-amd64`

## 设计文档

- [需求规划](docs/requirements-planning.md)
- [技术实现方案](docs/technical-implementation-plan.md)
- [实现步骤记录](docs/implementation-steps.md)
- [Review 记录](docs/review-log.md)

## GitHub Pages

项目介绍页位于 [docs/index.html](docs/index.html)，发布后用于展示 workspace-cli 的定位、流程和命令示例。
