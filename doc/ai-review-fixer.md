# Darter · AI Review Fixer 全流程简介

Darter 是一个**闭环的 AI 代码审查修复工具**：喂给它一份 code review 报告 + 一个 git 仓库，它自动把报告拆成修复任务，AI 逐个修复、自审查、提交，做完再做宏观回归审查，然后交人工审查，最后把通过的修复归档到独立分支，每个任务恰好一个 commit，全程可追溯、可续跑、可回滚；归档卡点可人工介入，最终回归审查可自动闭环修复。

## 整体架构（一句话）

```
输入：review 报告 + git 仓库 + 项目约束
  ↓  AI 拆解 → 修复循环 → 宏观回归 → 人工审查 → 归档
输出：archive 分支（每个修复任务 = 一个独立 commit）
```

- **后端**：Go（`net/http` + JSON 文件存储，数据落在 `data/jobs/<id>/` 下）
- **AI 引擎**：本地 `claude` CLI 子进程（headless 模式，prompt 走 stdin 避免 `E2BIG`），模型由设置页配置
- **前端**：Vue3 + Naive UI，SSE 实时日志 + 历史回放
- **单一二进制**：前端 `go:embed` 内嵌，`make build` 一条命令打包

---

## 全流程状态机

**Job（整体任务）状态流转：**

```
draft → parsing → reviewing_tasks → ready → running
                                         │
                                   (全部 pending 跑完)
                                         ▼
                                    awaiting_review
                                         │
                               (人工 approve/reject)
                                         ▼
                              (标记审查完成) review_done ──(撤销标记)──► awaiting_review
                                         │
                                  (开始归档) ▼
                                      archiving
                                     ┌────┴────────────────────┐
                                     ▼                         ▼
                          awaiting_archive_confirm      archive_blocked(归档卡点)
                                     │                         │
                     (最终审查 pass，人工确认)  (resolve: retry/continue/skip)
                                     ▼                         ▼
                                 completed                重新 archiving
```

关键约束：

- **`completed` 只在归档流程里产生**——`archiving` 阶段发现「无待归档任务」时短路完成，或最终审查通过后人工「确认完成」。修复循环结束**永远进入 `awaiting_review`**，绝不直接判完成。
- **`archived`（手动归档冻结）**：对 `ready / awaiting_review / review_done / failed / cancelled / paused` 可一键冻结为归档态——不跑归档迁移，临时 fix/archive 分支保留，工作区切回 base 并清空未提交改动。与 `completed`（自动归档）同属「已归档」。
- **全任务重做**：`awaiting_review / review_done / paused / cancelled / failed` 下可触发——抛弃当前 fix 分支、基于 base 重建、保留任务列表、清空任务状态，回到 `ready`。
- `paused`（暂停）、`cancelled`（取消）、`failed`（出错）可在 `running` 及其后中间态进入，之后可重新开始修复。`awaiting_archive_confirm` 且最终审查未通过（fail）时可重新归档触发自动闭环修复。

**Task（单个修复任务）状态流转：**

```
pending → fixing → reviewing → passed(已提交) ──► approved(人工批准) → 归档
                            │                     │(取消审批，回到 passed)
                            │                     └─► passed
                            ├─► skipped(无需修改，已被顺带解决)
                            └─► failed(重试N次放弃，留作记录)

人工审查：passed ──批准──► approved；passed ──驳回──► failed(终态，不再回炉)
归档阶段：approved → archive_blocked(迁移受阻，人工三选一) ──► approved(已归档) / deferred(放弃，记为遗留)
```

---

## 阶段 0 · 前置准备（设置 + 约束模板）

**设置页**：配置 `claude_cmd`（默认 `claude`）、`claude_model`（可选 `opus`/`sonnet`）、`default_retries`（默认重试次数，新建任务时未指定则用它）。

**约束模板页**：维护可复用的「项目约束」文案。约束是贯穿全流程的**红线**——它会被注入到之后每一次 AI 调用里（修复、对抗审查、宏观回归审查、归档审查），确保 AI 每一步都遵守团队规范（如「不得改公共 API」「遵循某编码规范」）。新建任务时可以直接引用模板内容。

---

## 阶段 1 · 新建任务 + 加约束

在任务列表页新建，填写：

| 字段 | 说明 |
|---|---|
| `name` | 任务名（默认 `<repo>-fix`） |
| `repo_path` | git 仓库路径（**必须**是有效 `.git` 仓库，后端校验） |
| `base_branch` | 基准分支（修复分支/归档分支都从它切出）。**创建时后端会校验该分支真实存在**，分支名笔误立即报错 |
| `report_source` | 报告来源：`text`（直接粘贴）或 `file`（报告文件路径） |
| `constraints` | 项目约束（可引用约束模板） |
| `max_retries` | 每个任务的最大重试次数 |

提交后生成 `job_<时间戳>_<随机>`，状态 `draft`。

---

## 阶段 2 · 拆解报告（ParseReport + ReviewTasks）

点「拆解」触发 `POST /parse`。拆解与预检都**只在 `base_branch` 上运行**（后端会校验当前分支，避免遗留的其它任务临时分支污染「问题是否已存在/已修复」的判断），分两步：

1. **`ParseReport`** —— AI 把报告拆成任务数组。每个任务含 `title`（一句话标题）、`description`（详情+期望修复方向）、`severity`（high/medium/low）、`files`（可能涉及的文件）。报告里无实质问题则返回空数组。
2. **`ReviewTasks`** —— AI 质检，做「查漏 / 去重 / 粒度调整 / 错误修正 / **冲突合并**」（多个任务改同一文件/函数时合并或排序，避免互相覆盖）。

完成后 `tasks_frozen=true`（任务列表冻结，不可重拆），状态 `ready`。这一步是唯一能「改任务列表」的环节，之后任务列表就是修复循环的固定输入。

---

## 阶段 3 · 修复循环（Fix → 自检 → 审查 → 提交）

点「开始」触发 `POST /start`，状态 `running`，从 `base_branch` 切出 `fix/ai-review-<时间戳>` 分支，然后**串行**处理每个 `pending` 任务（`processTask`）。开始前会校验仓库当前在 `base_branch` 或本任务自己的 fix 分支上，避免在别的任务遗留分支上误跑：

```
① AI 修复 (Fix, 允许自动编辑文件)
② 语法/编译自检 (syntaxcheck，硬性校验，不依赖模型判断)
③ 对抗性审查 (Review，AI 读全文件 + 全库 Grep 调用点核实)
④ 提交前检查 (CheckCommitScope，确认 diff 无混入无关改动)
⑤ git commit（一个任务一个 commit，正文带 Task-Id: trailer）
```

几个关键机制：

- **反馈闭环**：审查不通过（`fail`）时，审查结论 + 上一轮 diff 会作为反馈喂回下一轮 `Fix`，AI 判断意见是否成立再修正；自检失败、提交污染同理回炉。最多重试 `max_retries` 次。
- **无改动跳过**：某任务经 `Fix` 后 `git diff` 为空（问题已被之前的修复顺带解决），标记 `skipped`。
- **重试耗尽**：标记 `failed`，不再自动处理，留待人工审查决定。
- **崩溃恢复**：提交成功但状态未落盘的「窗口期」崩溃，重启后通过 commit 正文 `Task-Id:` trailer 识别并回填哈希；卡在 `fixing/reviewing` 的任务复位为 `pending`。
- **暂停/取消**：暂停是「执行完当前任务后停」，取消则回滚工作区并复位进行中任务，都可续跑。
- **结束状态**：所有 pending 任务处理完后（含宏观回归），**固定进入 `awaiting_review`**，绝不直接置 `completed`；「是否完成」统一交给归档流程判定。

---

## 阶段 4 · 宏观回归审查（ReviewBranch）

任务全部跑完后（只要存在至少一个「通过/已批准」任务），对整条修复分支 `base..fix` 的**累积 diff** 做一次宏观完整性审查：

- 重点查：**跨任务改动冲突**、**破坏下游调用**、**改了一半**、**无关改动**。
- **定位来源 commit**：AI 对每个问题用 `git log -S「关键代码」` / `git blame` 定位引入它的 commit，填到 finding 的 `commit` 字段（完整哈希）。
- 结论写入 `job.regression`，前端任务详情页每条 finding 会显示 `@<8位哈希>`。

这一步的结论**不在归档阶段修复**，而是作为**遗留问题清单**（`remaining_findings`）在归档终审时固化交付，交人工跟进。归档阶段只负责保证「迁移出来的分支能编译」+「commit 与任务一一对应」。

---

## 阶段 5 · 人工审查

修复循环结束（无论是否有通过任务），状态固定进入 `awaiting_review`，进入人工审查页（失败/驳回任务无需审批，直接留作失败记录，不显示在审查页）：

- **通过（approve）** → `approved`，等待归档。
- **驳回（reject）** → `failed`（终态，不再回炉重跑；人工意见保存在 `human` 字段留档，详情页会显示「该任务已被人工驳回」）。
- **取消审批（unapprove）** → `approved` 回退 `passed`（已归档的任务不可回退）。
- 可对任务写**备注/建议**（不影响状态，独立存储）。

审批意见与按钮**固定在页面底部**，长内容也能直接操作。全部处理完后，需**显式点「标记审查完成」**（状态 `review_done`）——这是归档前的门禁，避免在审查过程中误触归档；「开始归档」按钮只在 `review_done` 之后才可点（可撤销回 `awaiting_review`）。

---

## 阶段 6 · 归档（RunArchive）

点「归档」触发 `POST /archive`，状态 `archiving`，从 `base_branch` 切出 `archive/ai-review-<时间戳>` 分支，逐个迁移 `approved` 任务（幂等：已归档任务跳过，可续跑）。

**迁移单个任务（archiveTask）——三级降级策略：**

```
① 优先 cherry-pick 修复分支上该任务的 commit（忠实还原人工批准的内容，不重复审查）
② cherry-pick 真实冲突 → 退化为 apply 已批准的 patch
③ 仍失败 / 无现成改动 → AI 在归档分支上重新实现（唯一产生「未经人工批准新内容」的路径）
④ 语法自检（所有路径都做，兜底 cherry-pick/交叉合并引入的编译错误）
⑤ 仅 AI 兜底的新内容走对抗审查 + 提交前检查；忠实迁移的内容已人工批准，跳过重复审查
⑥ 恰好提交一个 commit（archive: <标题>，正文带 Task-Id）
```

**归档卡点（层 2）**：某任务迁移重试 `max_retries` 次仍未通过时，**不再把整个 job 判失败**，而是进入 `archive_blocked` 卡点，工作台弹交互区三选一：

- **让 AI 重试**（`retry`）——可附补充意见，喂回下一次 AI 兜底。
- **我已手动改好，继续**（`continue`）——校验工作区改动后直接提交，不跑 AI。
- **放弃该任务**（`skip`）——记为 `deferred`，作为遗留不再归档。

处理完自动续跑归档（`POST /archive/tasks/{tid}/resolve`）。

**最终回归审查 + 自动闭环（层 1）**：全部迁移完后进入 `finishArchive`：

```
① 整条归档分支编译自检（多任务交叉合并最易引入编译错误）
② ReviewArchive 核对 commit↔任务一一对应
③ 编译自检或最终审查未通过 → 派无交互 AI（FixArchive）在归档分支修复，
   amend 合入最后一个 commit（不新增 commit，保住「一任务一 commit」），循环重试
④ 通过 → awaiting_archive_confirm
   仍不通过（重试耗尽）→ 停在 awaiting_archive_confirm(fail)，展示报告，可重新归档再试
```

宏观回归审查的问题**不在归档阶段处理**，作为遗留问题清单（`remaining_findings`）固化交付。

---

## 阶段 7 · 结束

最终回归审查通过（verdict=pass）后，人工确认（`POST /archive/confirm`），状态 `completed`。若最终审查未通过（fail），「确认完成」按钮被门禁阻断（409），需先重新归档触发自动闭环修复。

归档分支 `archive/ai-review-<时间戳>` 即最终交付物——每个修复任务一个独立、干净、经过多重审查的 commit，可直接 review / cherry-pick / 合并。

同时可下载归档报告（`GET /archive`，支持按 passed/failed/skipped 分组导出 md）。

---

## 阶段 8 · 归档冻结（手动，不跑归档迁移）

不想走完整归档迁移（或任务不适合迁移 commit）时，可对 `ready / awaiting_review / review_done / failed / cancelled / paused` 一键「归档冻结」（`POST /archive-freeze`）：

- 状态置为 `archived`，与 `completed` 同属「已归档」，之后不再产生 AI 调用。
- 不迁移 commit，**保留**临时 fix/archive 分支，仅把工作区切回 base 分支并清空未提交改动。
- 适用于「放弃本轮修复、冻结现场留档」的场景，与自动归档（`completed`）区分。

## 阶段 9 · 全任务重做（抛弃 fix 分支从头再来）

修复结果不满意时，可在 `awaiting_review / review_done / paused / cancelled / failed` 下「全任务重做」（`POST /redo-all`）：

- **抛弃当前 fix 分支**（强删），基于 `base_branch` 重建一条全新的 `fix/ai-review-<时间戳>` 分支。
- **保留任务列表**（id/title/description/severity/files 不变）。
- **清除任务状态**：所有任务（含 passed/approved/rejected/failed/skipped/deferred）重置回 `pending`，清空 commits/review/human/retries 等修复与归档痕迹。
- job 层清空回归审查/归档分支/遗留清单，状态回到 `ready`，可重新点「开始修复」。

需先停止/暂停在跑的修复循环（`running` 态不可重做），且仓库需停在当前 fix 分支上。

---

## 贯穿全程的机制

| 机制 | 作用 |
|---|---|
| **约束红线** | `constraints` 注入修复、审查、回归、归档每次 AI 调用 |
| **实时日志** | SSE 推送 + 历史回放，每个事件带时间戳，claude 子进程 stderr 实时透传 |
| **崩溃恢复** | 启动时复位瞬时状态、回填已提交 commit、清理遗留 claude 孤儿进程（PID+启动时间+命令三重校验防误杀） |
| **幂等续跑** | 修复循环 / 归档都能中断后重新触发，已完成的步骤自动跳过 |
| **分支校验** | 创建时校验 base 分支存在；拆解/修复/归档入口校验仓库当前分支，防止在其它任务遗留的临时分支上误操作 |
| **多道审查** | 每任务：语法自检 → 对抗审查 → 提交检查；整体：宏观回归 → 人工审查 → 归档回归（自动闭环）→ 人工确认 |
