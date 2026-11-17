# Darter — 工具集合

> GET THINGS DONE

Darter 是一个 AI 驱动的任务工具集合，目前包含两个模块：

- **审查修复**：输入一份 review 报告 + 代码库，自动拆解为修复任务、串行修复、多重对抗性审查、逐任务提交到修复分支，交人类逐条审批后归档到独立分支（一任务一 commit）交付。
- **代码迁移**：把源分支的 commit 按拓扑序 cherry-pick 到目标分支，冲突由 AI 自动解决并审查，支持全自动迁移与全量回归审查，全程留档、可断点续跑。

配套通用能力：**约束模板**（项目约束注入每次 AI 调用）、**提示词库**（内置提示词统一管理）。

## 技术栈

Go 后端（标准库 + `os/exec` 调用本地 `claude` CLI）+ Vue3 + Naive UI（赛博主题）+ JSON 文件存储，单二进制交付（前端 `go:embed` 内嵌）。

## 快速开始

```bash
# 单可执行文件交付（前端已 go:embed 内嵌）：一键构建
make build          # 先 npm run build 再 go build，产出 ./darter
./darter --data-dir ./data --port 8080    # 访问 http://localhost:8080

# 开发模式（前后端分离，Vite 代理 /api → :8080）
go run ./cmd/server                          # 后端 :8080
cd web && npm install && npm run dev          # 前端 :5173
```

## 模块

### 审查修复（review-fixer）

```
报告 → 拆解 → 质检 → 修复循环（修复 → 审查 → 提交）→ 宏观回归 → 人工审批 → 归档（一任务一 commit）→ 最终审查
```

详见 [doc/ai-review-fixer.md](doc/ai-review-fixer.md)。

### 代码迁移（migration）

```
创建任务（源分支 / 目标基底 / 目标分支）→ 两栏 diff 对照 → 逐 commit 迁移 → 冲突 AI 解决 + 审查 → 全自动迁移 / 全量回归审查
```

详见 [doc/code-migration.md](doc/code-migration.md)。

## 通用能力

- **约束模板**：预存项目约束，新建任务时一键引用；约束注入每一次 AI 调用（修复、审查、冲突解决、回归审查等）。
- **提示词库**：内置提示词统一在页面只读展示，按「场景.能力」命名空间组织（`fixer.*` / `migration.*`）；修改走开发流程。

## 文档

- 开发约束：[CLAUDE.md](CLAUDE.md)
- 审查修复全流程：[doc/ai-review-fixer.md](doc/ai-review-fixer.md)
- 代码迁移全流程：[doc/code-migration.md](doc/code-migration.md)
- 特色功能：[doc/features.md](doc/features.md)
