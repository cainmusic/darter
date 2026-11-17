package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"darter/internal/ai"
	"darter/internal/git"
	"darter/internal/job"
	"darter/internal/store"
	"darter/internal/syntaxcheck"
)

// RunArchive 归档流程：人类审查完成后，把「修复通过且人工批准(approved)」的任务逐个迁移到归档分支，
// 每个任务恰好一个 commit；全部迁移完后做一次最终回归审查，等待人工确认。
// 幂等可续跑：已归档（Archived=true）的任务会被跳过，重复触发只补齐剩余任务并重做最终回归。
func (r *Runner) RunArchive(ctx context.Context, j *job.Job) error {
	j.Status = job.StatusArchiving
	j.CurrentTask = ""
	j.UpdatedAt = time.Now()
	r.saveJob(j)
	r.publish("archive_started", j.ID, "", "归档流程开始：迁移已批准任务到归档分支", nil)
	if r.AI != nil {
		r.AI.Log = func(line string) { r.publish("ai_log", j.ID, j.CurrentTask, line, nil) }
		r.AI.OnChild = func(pid int, start, cmd, phase string) {
			_ = r.Store.SaveChild(store.Child{PID: pid, Start: start, Cmd: cmd, JobID: j.ID, TaskID: j.CurrentTask, Phase: phase, StartedAt: time.Now()})
		}
		r.AI.OnChildExit = func(pid int) { _ = r.Store.DeleteChild(pid) }
	}

	tasks, err := r.Store.LoadTasks(j.ID)
	if err != nil {
		return r.fail(j, err)
	}

	// 待归档：状态为 approved（或归档卡点待处理）且尚未归档的任务
	approved := 0
	var pending []*job.Task
	for i := range tasks {
		if tasks[i].Status != job.TaskApproved && tasks[i].Status != job.TaskArchiveBlocked {
			continue
		}
		approved++
		if !tasks[i].Archived {
			pending = append(pending, &tasks[i])
		}
	}
	if approved == 0 {
		// 无待归档任务：人类审查已完成但没有批准任何任务，无需归档，直接判完成。
		j.Status = job.StatusCompleted
		j.CurrentTask = ""
		j.UpdatedAt = time.Now()
		r.saveJob(j)
		r.publish("archive_finished", j.ID, "", "无待归档任务，整体修复任务已完成", j.Status)
		return nil
	}

	g := git.New(j.RepoPath)
	if j.ArchiveBranch != "" {
		// 续跑：归档分支已存在，切过去并丢弃工具自身残留（上一轮中断的半成品）
		if err := g.Checkout(j.ArchiveBranch); err != nil {
			return r.fail(j, fmt.Errorf("切换到归档分支 %s 失败: %w", j.ArchiveBranch, err))
		}
		if cur, _ := g.CurrentBranch(); cur == j.ArchiveBranch {
			_ = g.ResetHard()
		}
	} else {
		if err := g.EnsureClean(); err != nil {
			return r.fail(j, err)
		}
		j.ArchiveBranch = fmt.Sprintf("archive/ai-review-%s", time.Now().Format("20060102-150405"))
		if err := g.CreateFixBranch(j.BaseBranch, j.ArchiveBranch); err != nil {
			return r.fail(j, fmt.Errorf("创建归档分支 %s 失败: %w", j.ArchiveBranch, err))
		}
	}
	r.saveJob(j)
	r.publish("archive_branch_created", j.ID, "", "归档分支 "+j.ArchiveBranch, j.ArchiveBranch)

	for _, t := range pending {
		if ctx.Err() != nil {
			return r.cancel(j)
		}
		constraints := r.currentConstraints(j.ID, j)
		j.CurrentTask = t.ID
		j.UpdatedAt = time.Now()
		r.saveJob(j)
		if !r.archiveTask(ctx, j, t, g, tasks, constraints) {
			// 归档卡点：该任务迁移受阻，等待人工指令（retry/continue/skip）。
			// job 状态已在 archiveTask 内切为 archive_blocked，此处正常返回，不再把整个 job 判失败。
			return nil
		}
	}

	return r.finishArchive(ctx, j, tasks, g, r.currentConstraints(j.ID, j))
}

// archiveTask 把一个已批准任务迁移到归档分支。
// 首选 cherry-pick 修复分支上该任务的 commit（忠实还原人工批准的内容，不重复审查）；
// 冲突时退化为 apply 已批准的 patch；仍失败或无从迁移时交给 AI 重新实现（AI 产生的新内容走完整审查链）；
// 最终恰好提交一个 commit。
func (r *Runner) archiveTask(ctx context.Context, j *job.Job, t *job.Task, g *git.Service, tasks []job.Task, constraints string) bool {
	maxRetries := j.MaxRetries
	if maxRetries <= 0 {
		maxRetries = 3
	}
	patch, _ := r.Store.LoadPatch(j.ID, t.ID)

	// —— 归档卡点人工指令 ——
	// skip：人工选择放弃该任务，不再归档（记为遗留）。
	if t.ArchiveAction == "skip" {
		t.Status = job.TaskDeferred
		t.ArchiveAction = ""
		t.ArchiveNote = ""
		t.LastError = "归档卡点：人工选择放弃，该任务不再归档"
		r.saveTasks(j.ID, tasks)
		r.publish("archive_task_skipped", j.ID, t.ID, t.LastError, t)
		return true
	}
	// continue：人已在归档分支工作区手动改好，直接校验并提交，不跑 AI。
	if t.ArchiveAction == "continue" {
		return r.commitManualArchive(j, t, g, tasks)
	}

	baseFeedback := archiveFeedback(j, t)
	// retry：把人工补充意见并入反馈，供 AI 重新实现时参考。
	if t.ArchiveAction == "retry" && t.ArchiveNote != "" {
		baseFeedback += "\n\n人工补充意见：" + t.ArchiveNote
	}
	t.ArchiveAction = "" // 消费指令

	for attempt := 0; attempt < maxRetries; attempt++ {
		if ctx.Err() != nil {
			return false
		}
		// 每轮从干净状态开始：先清理 cherry-pick 残留，再回滚工作区，最后清掉 untracked 产物
		_ = g.CherryPickAbort()
		if err := g.ResetHard(); err != nil {
			t.LastError = "回滚归档工作区失败: " + err.Error()
			r.publish("archive_task_error", j.ID, t.ID, t.LastError, t)
			continue
		}
		if err := g.CleanUntracked(); err != nil {
			t.LastError = "清理归档未跟踪文件失败: " + err.Error()
			r.publish("archive_task_error", j.ID, t.ID, t.LastError, t)
			continue
		}

		// 首选：cherry-pick 修复分支上该任务的 commit（内容 = 人工已批准的版本）
		migrated := false
		if len(t.Commits) > 0 {
			if err := g.CherryPickNoCommit(t.Commits[len(t.Commits)-1]); err != nil {
				_ = g.CherryPickAbort()
				_ = g.ResetHard()
			} else {
				migrated = true
			}
		}
		// 回退：cherry-pick 真实冲突时，退化为 apply 已批准的 patch
		if !migrated && patch != "" {
			if err := g.ApplyPatch(patch); err != nil {
				_ = g.ResetHard()
			} else {
				migrated = true
			}
		}

		// 先暂存再取 staged diff，保证自检/审查口径与提交一致（untracked 新文件也能被看到）。
		if err := g.AddAll(); err != nil {
			t.LastError = "归档 git add 失败: " + err.Error()
			r.publish("archive_task_error", j.ID, t.ID, t.LastError, t)
			continue
		}
		diff, err := g.DiffCached()
		if err != nil {
			t.LastError = "获取归档 diff 失败: " + err.Error()
			r.publish("archive_task_error", j.ID, t.ID, t.LastError, t)
			continue
		}

		// AI 兜底：无改动可迁移（cherry-pick/apply 均失败，或任务本无 commit），交给 AI 在归档分支上重新实现。
		// 这是唯一产生「未经人工批准的新内容」的路径，后续需走完整审查链。
		aiProduced := false
		if strings.TrimSpace(diff) == "" {
			feedback := baseFeedback
			feedback += "\n\n原修复无法干净迁移到归档分支（cherry-pick/apply 冲突，或多任务修改同一文件，或任务无现成 commit），请根据任务描述在归档分支上重新实现修复。"
			r.publish("archive_task_fixing", j.ID, t.ID, "无现成改动，AI 重新修复中", t)
			if _, err := r.AI.Fix(ctx, j.RepoPath, *t, feedback, constraints); err != nil {
				t.LastError = "归档修复调用失败: " + err.Error()
				r.publish("archive_task_error", j.ID, t.ID, t.LastError, t)
				continue
			}
			if err := g.AddAll(); err != nil {
				t.LastError = "归档 git add 失败: " + err.Error()
				r.publish("archive_task_error", j.ID, t.ID, t.LastError, t)
				continue
			}
			diff, err = g.DiffCached()
			if err != nil {
				t.LastError = "获取归档 diff 失败: " + err.Error()
				r.publish("archive_task_error", j.ID, t.ID, t.LastError, t)
				continue
			}
			aiProduced = true
		}
		if strings.TrimSpace(diff) == "" {
			t.LastError = "归档未产生任何改动（无法迁移且 AI 判定无需修改）"
			r.publish("archive_task_error", j.ID, t.ID, t.LastError, t)
			continue
		}

		// 硬性自检：基础语法/编译检查（所有路径都做，兜底 cherry-pick/交叉合并引入的编译错误）
		changed := ai.ChangedPaths(diff)
		if problems := syntaxcheck.Check(j.RepoPath, changed); len(problems) > 0 {
			t.LastError = "归档语法/编译自检未通过:\n" + strings.Join(problems, "\n")
			r.publish("archive_task_error", j.ID, t.ID, "自检未通过: "+problems[0], t)
			continue
		}

		// 对抗审查 + 提交检查：仅对 AI 兜底产生的新内容执行；忠实迁移的内容已人工批准，跳过重复审查。
		if aiProduced {
			r.publish("archive_task_reviewing", j.ID, t.ID, "归档审查中", t)
			review, err := r.AI.Review(ctx, j.RepoPath, *t, diff, constraints, ai.NewPaths(diff))
			if err != nil {
				t.LastError = "归档审查调用失败: " + err.Error()
				r.publish("archive_task_error", j.ID, t.ID, t.LastError, t)
				continue
			}
			if review.Verdict != "pass" {
				t.LastError = "归档审查不通过: " + review.Summary
				r.publish("archive_task_review_failed", j.ID, t.ID, review.Summary, t)
				continue
			}

			// 提交前最后一道检查：确认 diff 只含当前任务改动
			if reason, err := r.AI.CheckCommitScope(ctx, j.RepoPath, *t, diff, ai.NewPaths(diff)); err != nil {
				t.LastError = "归档提交检查调用失败: " + err.Error()
				r.publish("archive_task_error", j.ID, t.ID, t.LastError, t)
				continue
			} else if reason != "" {
				t.LastError = "归档提交前检查发现无关改动: " + reason
				r.publish("archive_task_error", j.ID, t.ID, t.LastError, t)
				continue
			}
		}

		if err := g.EnsureOnFixBranch(j.ArchiveBranch); err != nil {
			t.LastError = "归档提交失败: " + err.Error()
			r.publish("archive_task_error", j.ID, t.ID, t.LastError, t)
			continue
		}
		// 改动已在上方暂存，直接提交，不再重复 add -A。
		hash, err := g.Commit("fix: "+t.Title, "")
		if err != nil {
			t.LastError = "归档 git commit 失败: " + err.Error()
			r.publish("archive_task_error", j.ID, t.ID, t.LastError, t)
			continue
		}
		t.Archived = true
		t.ArchiveCommit = hash
		t.Status = job.TaskApproved
		t.ArchiveAction = ""
		t.ArchiveNote = ""
		t.LastError = ""
		r.saveTasks(j.ID, tasks)
		r.publish("archive_task_done", j.ID, t.ID, "已归档提交 "+hash, t)
		return true
	}

	// 进入卡点前清理工作区残留（cherry-pick 状态与半成品改动），确保人工「继续」时从干净起点开始。
	_ = g.CherryPickAbort()
	_ = g.ResetHard()
	// 保留最后一次的详细错误，让归档卡点直接显示真实原因（如 commitlint 拒绝、自检失败等），而非笼统的「重试未通过」。
	if t.LastError != "" {
		t.LastError = fmt.Sprintf("归档重试 %d 次仍未通过，需人工介入。\n最后错误：%s", maxRetries, t.LastError)
	} else {
		t.LastError = fmt.Sprintf("归档重试 %d 次仍未通过，需人工介入", maxRetries)
	}
	return r.blockArchiveTask(j, t, tasks)
}

// commitManualArchive 处理人工「continue」指令：人已在归档分支工作区手动改好，校验后直接提交，不跑 AI。
func (r *Runner) commitManualArchive(j *job.Job, t *job.Task, g *git.Service, tasks []job.Task) bool {
	_ = g.CherryPickAbort() // 清理可能残留的 cherry-pick 状态
	// 人工可能新建文件，先暂存再取 staged diff，保证自检口径与提交一致。
	if err := g.AddAll(); err != nil {
		t.LastError = "归档 git add 失败: " + err.Error()
		r.publish("archive_task_error", j.ID, t.ID, t.LastError, t)
		return r.blockArchiveTask(j, t, tasks)
	}
	diff, err := g.DiffCached()
	if err != nil {
		t.LastError = "获取人工改动 diff 失败: " + err.Error()
		r.publish("archive_task_error", j.ID, t.ID, t.LastError, t)
		return r.blockArchiveTask(j, t, tasks)
	}
	if strings.TrimSpace(diff) == "" {
		t.LastError = "人工继续：工作区无改动，请确认已修改文件后重试"
		r.publish("archive_task_error", j.ID, t.ID, t.LastError, t)
		return r.blockArchiveTask(j, t, tasks)
	}
	changed := ai.ChangedPaths(diff)
	if problems := syntaxcheck.Check(j.RepoPath, changed); len(problems) > 0 {
		t.LastError = "人工改动语法/编译自检未通过:\n" + strings.Join(problems, "\n")
		r.publish("archive_task_error", j.ID, t.ID, "自检未通过: "+problems[0], t)
		return r.blockArchiveTask(j, t, tasks)
	}
	if err := g.EnsureOnFixBranch(j.ArchiveBranch); err != nil {
		t.LastError = "归档提交失败: " + err.Error()
		r.publish("archive_task_error", j.ID, t.ID, t.LastError, t)
		return r.blockArchiveTask(j, t, tasks)
	}
	// 改动已在上方暂存，直接提交。
	hash, err := g.Commit("fix: "+t.Title, "")
	if err != nil {
		t.LastError = "归档 git commit 失败: " + err.Error()
		r.publish("archive_task_error", j.ID, t.ID, t.LastError, t)
		return r.blockArchiveTask(j, t, tasks)
	}
	t.Archived = true
	t.ArchiveCommit = hash
	t.Status = job.TaskApproved
	t.ArchiveAction = ""
	t.ArchiveNote = ""
	t.LastError = ""
	r.saveTasks(j.ID, tasks)
	r.publish("archive_task_done", j.ID, t.ID, "已归档提交 "+hash+"（人工继续）", t)
	return true
}

// blockArchiveTask 把任务置为归档卡点状态，并把 job 切到 archive_blocked，等待人工介入。
func (r *Runner) blockArchiveTask(j *job.Job, t *job.Task, tasks []job.Task) bool {
	t.Status = job.TaskArchiveBlocked
	r.saveTasks(j.ID, tasks)
	j.Status = job.StatusArchiveBlocked
	j.CurrentTask = ""
	j.UpdatedAt = time.Now()
	r.saveJob(j)
	r.publish("archive_blocked", j.ID, t.ID, t.LastError, t)
	return false
}

// finishArchive 全部迁移完成后做最终回归审查，并对未通过的情况自动闭环修复：
// 1) 先对整条归档分支做编译自检（单任务各自编译通过 ≠ 合并后能编译）；
// 2) 再让 AI 核对 commit↔任务对应关系；
// 3) 若编译自检或最终审查未通过，派无交互 AI 在归档分支上修复（amend 合入，不新增 commit），循环重试；
// 4) 仍不通过则停在 awaiting_archive_confirm（fail），展示报告交人工跟进；通过则固化遗留问题清单。
func (r *Runner) finishArchive(ctx context.Context, j *job.Job, tasks []job.Task, g *git.Service, constraints string) error {
	maxRetries := j.MaxRetries
	if maxRetries <= 0 {
		maxRetries = 3
	}

	var archived []job.Task
	for _, t := range tasks {
		if t.Archived {
			archived = append(archived, t)
		}
	}
	tasksJSON, _ := json.Marshal(archived)

	var lastReg job.Review
	haveReg := false
	var lastErr error

	for attempt := 0; attempt < maxRetries; attempt++ {
		if ctx.Err() != nil {
			return r.cancel(j)
		}

		fullDiff, err := g.DiffRange(j.BaseBranch, j.ArchiveBranch)
		if err != nil {
			return r.fail(j, fmt.Errorf("获取归档分支整体 diff 失败: %w", err))
		}
		changed := ai.ChangedPaths(fullDiff)
		problems := syntaxcheck.Check(j.RepoPath, changed)

		logText, err := g.LogRange(j.BaseBranch, j.ArchiveBranch)
		if err != nil {
			return r.fail(j, fmt.Errorf("获取归档分支 commit 日志失败: %w", err))
		}

		r.publish("archive_reviewing", j.ID, "", fmt.Sprintf("归档后最终回归审查中（第 %d/%d 次）", attempt+1, maxRetries), nil)
		reg, err := r.AI.ReviewArchive(ctx, j.RepoPath, fullDiff, logText, string(tasksJSON), constraints)
		if err != nil {
			lastErr = err
		} else {
			lastReg = reg
			haveReg = true
		}

		// 通过：编译自检无问题且最终审查 pass。
		if len(problems) == 0 && err == nil && reg.Verdict == "pass" {
			j.ArchiveReview = &reg
			if j.Regression != nil && len(j.Regression.Findings) > 0 {
				j.RemainingFindings = j.Regression.Findings
			}
			j.Status = job.StatusAwaitingArchiveConfirm
			j.CurrentTask = ""
			j.UpdatedAt = time.Now()
			r.saveJob(j)
			r.publish("archive_reviewed", j.ID, "", "最终回归审查完成 [pass] "+reg.Summary, &reg)
			return nil
		}

		// 未通过：派无交互 AI 在归档分支上解决最后的问题，amend 合入后重新审查。
		feedback := buildArchiveFixFeedback(problems, err, reg)
		_ = g.CherryPickAbort()
		_ = g.ResetHard() // 从干净的归档分支 HEAD 开始，避免上一轮残留改动混入本次修复
		_ = g.CleanUntracked()
		r.publish("archive_fixing", j.ID, "", fmt.Sprintf("最终审查发现问题，AI 自动修复中（第 %d/%d 次）", attempt+1, maxRetries), nil)
		summary, fixErr := r.AI.FixArchive(ctx, j.RepoPath, feedback, constraints)
		if fixErr != nil {
			lastErr = fixErr
			r.publish("archive_fix_error", j.ID, "", "AI 自动修复调用失败: "+fixErr.Error(), nil)
			continue
		}
		if err := g.AddAll(); err != nil {
			lastErr = err
			continue
		}
		fixDiff, derr := g.DiffCached()
		if derr != nil {
			lastErr = derr
			continue
		}
		if strings.TrimSpace(fixDiff) == "" {
			lastErr = fmt.Errorf("AI 未产生改动，无法自动修复：%s", summary)
			r.publish("archive_fix_error", j.ID, "", lastErr.Error(), nil)
			continue
		}
		if p := syntaxcheck.Check(j.RepoPath, ai.ChangedPaths(fixDiff)); len(p) > 0 {
			lastErr = fmt.Errorf("AI 修复自检未通过: %s", strings.Join(p, "; "))
			r.publish("archive_fix_error", j.ID, "", lastErr.Error(), nil)
			continue
		}
		if err := g.AmendNoEdit(); err != nil {
			lastErr = err
			continue
		}
		r.publish("archive_fixed", j.ID, "", "AI 已修复并合入归档分支，重新审查中", nil)
	}

	// 循环耗尽仍未通过：停在 awaiting_archive_confirm（fail），展示报告，人工可重新归档再试。
	if !haveReg {
		lastReg = job.Review{Verdict: "fail", Summary: "最终回归审查未通过：" + errText(lastErr)}
	}
	j.ArchiveReview = &lastReg
	if j.Regression != nil && len(j.Regression.Findings) > 0 {
		j.RemainingFindings = j.Regression.Findings
	}
	j.Status = job.StatusAwaitingArchiveConfirm
	j.CurrentTask = ""
	j.UpdatedAt = time.Now()
	r.saveJob(j)
	r.publish("archive_reviewed", j.ID, "", "最终回归审查未通过 [fail] "+j.ArchiveReview.Summary, j.ArchiveReview)
	return nil
}

// buildArchiveFixFeedback 把编译自检问题与最终审查结论拼成给 AI 的修复反馈。
func buildArchiveFixFeedback(problems []string, regErr error, reg job.Review) string {
	var parts []string
	if len(problems) > 0 {
		parts = append(parts, "归档分支编译/语法自检未通过：\n"+strings.Join(problems, "\n"))
	}
	if regErr != nil {
		parts = append(parts, "最终回归审查调用失败："+regErr.Error())
	}
	if reg.Verdict == "fail" {
		parts = append(parts, "最终回归审查发现问题：\n"+reg.Summary+"\n"+findingsJSON(reg.Findings))
	}
	return strings.Join(parts, "\n\n")
}

func errText(err error) string {
	if err == nil {
		return "未知原因"
	}
	return err.Error()
}

// archiveFeedback 把人工审查意见拼成反馈，供 AI 在归档分支上重新实现/修复时参考。
// 宏观回归审查发现的问题不在归档阶段处理（作为遗留问题清单另行交付），故此处只保留人工意见。
func archiveFeedback(j *job.Job, t *job.Task) string {
	if t.Human != nil && t.Human.Note != "" {
		return "人工审查意见：" + t.Human.Note
	}
	return ""
}
