# Darter · 代码迁移全流程简介

Darter 的**代码迁移模块**：给定一个源分支和一个目标基底，从基底切出目标分支，把源分支上的 commit 按拓扑序逐个 cherry-pick 过去；冲突交给 AI 自动解决并审查，支持全自动迁移与全量回归审查，全程留档、可断点续跑、可中途停止。

## 整体架构（一句话）

```
输入：源分支 + 目标基底 + 目标分支 + 项目约束
  ↓  切出目标分支 → 两栏 diff 对照 → 逐 commit cherry-pick → 冲突 AI 解决 + 审查
  ↓  （可选）全自动迁移 → 全量回归审查
输出：目标分支（源 commit 按拓扑序迁移，每个源 commit 对应一个 target commit）
```

- **后端**：Go（`net/http` + JSON 文件存储，数据落在 `data/jobs/<id>/` 下）
- **AI 引擎**：本地 `claude` CLI 子进程，提示词走 stdin，模型由设置页配置
- **前端**：Vue3 + Naive UI，两栏 diff 对照 + SSE 实时刷新
- **单一二进制**：前端 `go:embed` 内嵌，`make build` 一条命令打包

---

## 全流程状态机

**Job（迁移任务）状态流转：**

```
mig_draft → mig_ready ──迁移──► mig_running(连续迁移) / mig_auto(全自动迁移)
                ▲                        │
                │                 ┌──────┴───────┐
                │                 ▼              ▼
                │           mig_conflict     mig_completed
                │           (冲突，AI 解决/跳过/中止后回 ready)
                └────────────────────────────────┘
```

关键约束：

- **`mig_running` / `mig_auto` 是瞬时态**：连续迁移（同步循环）与全自动迁移（后台异步）进行中；服务崩溃重启后复位为 `mig_ready`，可重新触发。
- **`mig_conflict` 停在冲突等处理**：AI 解决成功、AI 判定「应跳过」、或人工「中止/跳过」后回到 `mig_ready`；全自动迁移中 AI 重试耗尽也停下等人工。
- **`mig_completed` 是终态**：手动「完成」或全自动迁移全部迁移完成后自动置为完成。

**Commit（单个源 commit）迁移状态：**

```
pending ──cherry-pick──► migrated(已迁移，记录 target_commit)
   │                        ▲
   ├─► conflict ──AI 解决───┘
   │              └──AI 判定跳过 / 人工中止 / 跳过──► skipped
   ├─► skipped(忽略/空 commit/仅生成物)
   └─► failed(其它错误)
```

---

## 阶段 0 · 前置准备（约束模板）

**约束模板页**维护可复用的「项目约束」文案。约束会注入代码迁移的 AI 调用里——尤其是**冲突解决**：AI 依据约束判断「这个 commit 的改动是否全是生成物、该不该跳过」。例如在约束里写清「`composer.lock`、`vendor/`、编译产物等是生成物，不需要迁移」。

---

## 阶段 1 · 创建迁移任务

在代码迁移任务列表页新建，填写：

| 字段 | 说明 |
|---|---|
| `name` | 任务名（默认 `<repo>-migrate`） |
| `repo_path` | git 仓库路径（**必须**是有效 `.git` 仓库，后端校验） |
| `source_branch` | 源分支：要迁移的 commit 所在分支 |
| `target_base` | 目标基底：目标分支从它切出 |
| `target_branch` | 目标分支：接收 cherry-pick 的分支 |
| `constraints` | 项目约束（可引用约束模板） |

创建时后端校验：仓库是 git 仓库、源分支与目标基底真实存在、目标分支尚未存在（且不能与源分支/目标基底重名）；若源分支/目标基底跟踪远端，先 `git fetch` 更新到最新，再从 `target_base` 切出 `target_branch`。状态 `mig_ready`。

---

## 阶段 2 · 两栏 diff 对照

进入详情页，左右两栏对照展示：

- **左栏**：源分支待迁移的 commit 列表（按拓扑序），每条带状态（待迁移/已迁移/冲突/已跳过/失败）、序号、可切换的标记（★/▲/■/●）。
- **右栏**：目标分支当前 commit 列表，底部是基底 commit 锚点。
- **对齐方式**：用迁移时记录的 `target_commit` 做 LCS 精确对齐——源 commit 和它迁移到目标分支后的 commit 对齐到同一行；**不用 message 匹配**（message 会被手动 amend 改掉）。reset / amend / merge 后通过「目标 commit 是否仍在目标分支祖先里」重新判断状态，精确不漂移。

---

## 阶段 3 · 逐 commit 迁移

对单个源 commit 点「迁移」（或批量「自动迁移」），执行 `git cherry-pick`：

- **成功** → `migrated`，记录 `target_commit`（迁移到目标分支后的新 hash）。
- **空 commit**（改动已在目标分支，`git cherry-pick` 报 empty 且无冲突文件）→ 直接 `abort` + 标记 `skipped`，不提交空 commit。
- **真实冲突** → 进入阶段 4。

「自动迁移」= 按顺序连续 cherry-pick，遇冲突即停（不自动 AI 解决）。

---

## 阶段 4 · 冲突 AI 解决 + 审查

冲突时记录冲突文件，进入 AI 解决流程（单次「AI 解决」或全自动迁移内部），最多 3 轮：

```
① AI 解决冲突 (ResolveConflict) —— 读冲突文件、合并两侧、移除冲突标记
    ├─ AI 判定「应跳过」(空 commit / 改动全是生成物) → abort + skipped
    └─ 正常解决
② 冲突解决审查 (ReviewConflict) —— 审查解决后的 diff 是否残留冲突标记、丢失意图、破坏下游、引入无关改动
    ├─ 不通过 → 恢复冲突态，feedback 回下一轮重试
    └─ 通过 → git add + cherry-pick --continue → migrated
③ 3 轮仍不通过 → 停下保留冲突现场，等人工
```

- **单次 AI 解决**：用户在冲突面板点「AI 解决」，后台异步跑，可「停止 AI」。
- **可停止**：取消会杀掉 claude 子进程并 `abort` 清理工作区，commit 回退 `pending`。
- **冲突详情留档**：每条冲突记录带 `target_commit`（这次解决产生的 commit），前端目标 commit 行可查看「冲突如何解决的」，避免 reset / merge 后反推产生「假冲突」。

---

## 阶段 5 · 全自动迁移

「全自动迁移」= 自动迁移 + 冲突自动 AI 解决：后台异步，按列表逐个 cherry-pick，冲突自动走「解决 + 审查」后继续，直到全部完成：

- 状态 `mig_auto`，过程中每个 commit 处理完落盘（切走再回来看得到进度），SSE 实时推送。
- 空 commit / 仅生成物 → 自动跳过，继续下一个。
- AI 重试耗尽（3 轮）→ 停下 `mig_conflict`，保留冲突现场等人工。
- 全部完成 → `mig_completed`，并**自动触发全量回归审查**（阶段 6）。
- 可中途「停止」，停止后当前冲突中止回退、已迁移的保留。

---

## 阶段 6 · 全量回归审查

迁移完成后，对目标分支相对 `target_base` 的**累积改动 diff + 提交日志**做一次完整性审查（`ReviewMigration`），只挑明显问题（冲突解决不当、破坏下游、迁了一半、无关改动）：

- **无问题** → 通过，结论写入 `job.regression`。
- **有问题** → 走最多 3 轮「修复 → 审查」：
  - `FixMigrationRev` 修复问题 → `git commit`（**单独成 commit**，不 amend 到迁移 commit，commit message 用 AI 给的简短标题 `fix: <title>`）
  - `ReviewMigrationFix` 审查修复 → 不通过则 feedback 回下一轮
  - 3 轮仍不通过 → 停下，展示报告等人工

修复**单独成 commit** 而非合入迁移 commit，是为了让你后续逐个评估「这个修复应该合并回源分支的哪个 commit」。

---

## 阶段 7 · 精细控制

- **手动迁移**：单个 commit 单独迁移。
- **忽略 / 恢复**：单条忽略（`skipped`，不迁移、连续迁移跳过）或区间忽略。
- **合并（squash）**：把目标分支上「某 commit 到 HEAD」的连续 commit 合并为一个，并更新对应源 commit 的 `target_commit`。
- **重置**：把目标分支 `reset --hard` 到某个祖先 commit（只允许回退到目标分支自身祖先），作废更新的迁移记录。
- **改迁移目标**：手动指定某个源 commit 在目标分支上的迁移结果（纠正手动 amend / 合并后的映射）。
- **标记**：给源 commit 打 ★/▲/■/● 标记，区分不同用途。
- **完成**：手动置 `mig_completed`（终态）。

---

## 阶段 8 · 结束

目标分支即最终交付物——源 commit 按拓扑序迁移，冲突经 AI 解决并审查，空/生成物 commit 已跳过，全量回归审查发现的问题已修复成独立 commit，可直接 review / 合并 / 推远端。

---

## 贯穿全程的机制

| 机制 | 作用 |
|---|---|
| **状态精确判断** | 迁移状态用 `target_commit` + 祖先判断，不用 message 匹配（message 会被 amend 改掉） |
| **冲突详情留档** | 每条冲突记录带 `target_commit`，精确关联「哪次解决产生哪个 commit」 |
| **约束红线** | `constraints` 注入冲突解决、冲突审查、回归审查每次 AI 调用 |
| **可停止** | AI 冲突解决、全自动迁移都可中途停止，停止后清理工作区（含未跟踪文件） |
| **工作区清理** | 回退/中止/停止/重置后清空未跟踪文件；推进/提交类操作不清 |
| **仓库并发锁** | 操作同一 git 仓库走仓库锁，防止多任务并发互踩 |
| **崩溃恢复** | `mig_auto` / `mig_running` 崩溃后复位 `mig_ready`，可重新触发 |
| **实时刷新** | SSE 订阅 migration 事件，批量迁移/忽略/AI 解决都实时刷新列表 |
