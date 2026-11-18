package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"darter/internal/ai"
	"darter/internal/events"
	"darter/internal/git"
	"darter/internal/job"
	"darter/internal/store"
	"darter/internal/syntaxcheck"
)

type Runner struct {
	Store *store.Store
	AI    *ai.Client
	Bus   *events.Bus
}

// Run 串行执行修复循环：pending → fixing → reviewing → passed(commit)/failed(重试N次跳过)。
func (r *Runner) Run(ctx context.Context, j *job.Job) error {
	j.Status = job.StatusRunning
	j.UpdatedAt = time.Now()
	r.saveJob(j)
	r.publish("job_started", j.ID, "", "", j)
	// 把 claude 子进程的 stderr 实时透传到前端日志（当前任务上下文在调用时读取）
	if r.AI != nil {
		r.AI.Log = func(line string) {
			r.publish("ai_log", j.ID, j.CurrentTask, line, nil)
		}
		r.AI.OnChild = func(pid int, start, cmd, phase string) {
			_ = r.Store.SaveChild(store.Child{PID: pid, Start: start, Cmd: cmd, JobID: j.ID, TaskID: j.CurrentTask, Phase: phase, StartedAt: time.Now()})
		}
		r.AI.OnChildExit = func(pid int) {
			_ = r.Store.DeleteChild(pid)
		}
	}

	tasks, err := r.Store.LoadTasks(j.ID)
	if err != nil {
		return r.fail(j, err)
	}

	g := git.New(j.RepoPath)
	// 恢复上次中断：
	// 1) 先回填「commit 已成功但任务状态未落盘」的任务（补回丢失的 commit 哈希链接）；
	// 2) 再复位其余卡在 fixing/reviewing 的任务；若已在修复分支则丢弃工具自身留下的未提交改动。
	r.recoverCommittedTask(j, g, tasks)
	for i := range tasks {
		if tasks[i].Status == job.TaskFixing || tasks[i].Status == job.TaskReviewing {
			tasks[i].Status = job.TaskPending
		}
	}
	if err := r.Store.SaveTasks(j.ID, tasks); err != nil {
		return r.fail(j, err)
	}
	if j.FixBranch != "" {
		if cur, _ := g.CurrentBranch(); cur == j.FixBranch {
			_ = g.ResetHard()
		}
	}
	if err := g.EnsureClean(); err != nil {
		return r.fail(j, err)
	}
	if j.FixBranch == "" || !g.BranchExists(j.FixBranch) {
		// 分支未建过，或上次创建失败残留了不存在的分支名：重新创建。
		fix := fmt.Sprintf("fix/ai-review-%s", time.Now().Format("20060102-150405"))
		// 先建分支、成功后再落盘 FixBranch：若创建失败，FixBranch 保持为空，
		// 下次重跑会重新创建，而不是去 checkout 一个根本不存在的分支。
		if err := g.CreateFixBranch(j.BaseBranch, fix); err != nil {
			return r.fail(j, fmt.Errorf("基于 %s 创建修复分支失败: %w", j.BaseBranch, err))
		}
		j.FixBranch = fix
	} else {
		// 重跑：修复分支已存在，直接切换过去继续
		if err := g.Checkout(j.FixBranch); err != nil {
			return r.fail(j, fmt.Errorf("切换到修复分支 %s 失败: %w", j.FixBranch, err))
		}
	}
	r.saveJob(j)
	r.publish("branch_created", j.ID, "", "修复分支 "+j.FixBranch, j.FixBranch)

	for i := range tasks {
		if ctx.Err() != nil {
			return r.cancel(j)
		}
		t := &tasks[i]
		if t.Status != job.TaskPending {
			continue
		}
		// 暂停：用户请求「执行完当前任务后停止」，在开始下一个任务前消费该请求。
		if r.pauseRequested(j.ID) {
			return r.pause(j)
		}
		// 约束支持进行中更换：每个任务开始前重读并解析最新约束（下一任务生效，不打断当前 AI 调用）。
		constraints := r.currentConstraints(j.ID, j)
		j.CurrentTask = t.ID
		j.UpdatedAt = time.Now()
		r.saveJob(j)
		r.processTask(ctx, j, t, g, tasks, constraints)
		r.saveTasks(j.ID, tasks)
	}

	// 宏观回归审查：任务全部跑完后，对整条修复分支的累积改动做一次完整性检查。
	// 只针对通过（有提交）的任务；失败/跳过无提交，自然不在 diff 里。
	r.runRegressionReview(ctx, j, g, tasks, r.currentConstraints(j.ID, j))

	j.CurrentTask = ""
	// 修复循环结束统一进入待人类审查；「是否完成」统一交给归档流程判定（归档时无待归档任务才判完成），
	// 修复阶段绝不直接置 completed。
	j.Status = job.StatusAwaitingReview
	j.UpdatedAt = time.Now()
	r.saveJob(j)
	r.publish("job_finished", j.ID, "", "修复循环结束，等待人类审查", j.Status)
	return nil
}

func (r *Runner) processTask(ctx context.Context, j *job.Job, t *job.Task, g *git.Service, tasks []job.Task, constraints string) bool {
	maxRetries := j.MaxRetries
	if maxRetries <= 0 {
		maxRetries = 3
	}
	var feedback string
	if t.Human != nil && t.Human.Verdict == "rejected" {
		feedback = "人工审查驳回意见：" + t.Human.Note
	}
	for attempt := 0; attempt < maxRetries; attempt++ {
		if ctx.Err() != nil {
			return false // 取消：任务保持现状，由下次启动的恢复逻辑复位
		}

		// 每次尝试前把工作区回滚干净，避免上一次失败/其它任务残留的改动混入本次 diff；
		// 同时清掉 untracked 产物（AI/build 引入），与下方 git add -A 的暂存范围保持一致。
		if err := g.ResetHard(); err != nil {
			t.Retries++
			t.LastError = "回滚工作区失败: " + err.Error()
			r.saveAttempt(j.ID, t.ID, attempt+1, job.Review{}, "", t.LastError)
			r.saveTasks(j.ID, tasks)
			r.publish("task_fix_error", j.ID, t.ID, err.Error(), t)
			continue
		}
		if err := g.CleanUntracked(); err != nil {
			t.Retries++
			t.LastError = "清理未跟踪文件失败: " + err.Error()
			r.saveAttempt(j.ID, t.ID, attempt+1, job.Review{}, "", t.LastError)
			r.saveTasks(j.ID, tasks)
			r.publish("task_fix_error", j.ID, t.ID, err.Error(), t)
			continue
		}

		t.Status = job.TaskFixing
		r.saveTasks(j.ID, tasks)
		r.publish("task_started", j.ID, t.ID, fmt.Sprintf("开始修复 %s（第 %d 次尝试）", t.Title, attempt+1), t)

		summary, err := r.AI.Fix(ctx, j.RepoPath, *t, feedback, constraints)
		if err != nil {
			t.Retries++
			t.LastError = "修复调用失败: " + err.Error()
			feedback = "上一次修复调用失败：" + err.Error()
			r.saveAttempt(j.ID, t.ID, attempt+1, job.Review{}, "", t.LastError)
			r.saveTasks(j.ID, tasks)
			r.publish("task_fix_error", j.ID, t.ID, err.Error(), t)
			continue
		}

		// 先 git add -A 暂存全部改动（含 untracked 新文件），再用 git diff --cached 取「即将提交的内容」，
		// 保证审查/自检/提交检查的口径与提交一致，untracked 文件无法再绕过审查进 commit。
		if err := g.AddAll(); err != nil {
			t.Retries++
			t.LastError = "git add 失败: " + err.Error()
			r.saveAttempt(j.ID, t.ID, attempt+1, job.Review{}, "", t.LastError)
			r.saveTasks(j.ID, tasks)
			r.publish("task_fix_error", j.ID, t.ID, err.Error(), t)
			continue
		}
		diff, err := g.DiffCached()
		if err != nil {
			t.Retries++
			t.LastError = "获取 git diff 失败: " + err.Error()
			r.saveAttempt(j.ID, t.ID, attempt+1, job.Review{}, "", t.LastError)
			r.saveTasks(j.ID, tasks)
			r.publish("task_fix_error", j.ID, t.ID, err.Error(), t)
			continue
		}

		if strings.TrimSpace(diff) == "" {
			t.Review = &job.Review{Verdict: "pass", Summary: "无需修改（可能已在之前的修复中顺带解决）：" + summary}
			t.Status = job.TaskSkipped
			r.saveTasks(j.ID, tasks)
			r.publish("task_skipped", j.ID, t.ID, "无改动，无需单独修复（已跳过）", t)
			return true
		}

		// 硬性自检：基础语法/编译检查（不依赖模型判断），不通过直接反馈重试
		changed := ai.ChangedPaths(diff)
		if problems := syntaxcheck.Check(j.RepoPath, changed); len(problems) > 0 {
			t.Retries++
			t.LastError = "语法/编译自检未通过:\n" + strings.Join(problems, "\n")
			feedback = "语法/编译自检未通过，请修正：\n" + strings.Join(problems, "\n")
			r.saveAttempt(j.ID, t.ID, attempt+1, job.Review{}, diff, t.LastError)
			r.saveTasks(j.ID, tasks)
			r.publish("task_fix_error", j.ID, t.ID, "自检未通过: "+problems[0], t)
			continue
		}
		t.Status = job.TaskReviewing
		r.saveTasks(j.ID, tasks)
		r.publish("task_reviewing", j.ID, t.ID, "对抗性审查中", t)

		review, err := r.AI.Review(ctx, j.RepoPath, *t, diff, constraints, ai.NewPaths(diff))
		if err != nil {
			t.Retries++
			t.LastError = "审查调用失败: " + err.Error()
			feedback = "审查调用失败：" + err.Error()
			r.saveAttempt(j.ID, t.ID, attempt+1, job.Review{}, diff, t.LastError)
			r.saveTasks(j.ID, tasks)
			r.publish("task_review_error", j.ID, t.ID, err.Error(), t)
			continue
		}
		t.Review = &review

		if review.Verdict == "pass" {
			// 提交前最后一道检查：确认 diff 只含当前任务改动，无污染
			if reason, err := r.AI.CheckCommitScope(ctx, j.RepoPath, *t, diff, ai.NewPaths(diff)); err != nil {
				t.Retries++
				t.LastError = "提交检查调用失败: " + err.Error()
				feedback = "提交检查调用失败：" + err.Error()
				r.saveAttempt(j.ID, t.ID, attempt+1, job.Review{}, diff, t.LastError)
				r.saveTasks(j.ID, tasks)
				r.publish("task_commit_check_error", j.ID, t.ID, err.Error(), t)
				continue
			} else if reason != "" {
				t.Retries++
				t.LastError = "提交前检查发现 diff 混入无关改动: " + reason
				r.saveAttempt(j.ID, t.ID, attempt+1, review, diff, t.LastError)
				feedback = "提交前检查发现 diff 混入无关改动：" + reason + "，请只保留当前任务的改动。"
				t.Review = &review
				r.saveTasks(j.ID, tasks)
				r.publish("task_commit_polluted", j.ID, t.ID, reason, t)
				continue
			}

			if err := g.EnsureOnFixBranch(j.FixBranch); err != nil {
				t.Retries++
				t.LastError = "提交失败: " + err.Error()
				r.saveAttempt(j.ID, t.ID, attempt+1, job.Review{}, diff, t.LastError)
				r.saveTasks(j.ID, tasks)
				r.publish("task_commit_error", j.ID, t.ID, err.Error(), t)
				continue
			}
			// 改动已在上方 git add -A + git diff --cached 时暂存，这里直接提交，
			// 不再重复 add -A（避免把审查/自检之后新产生的 build 产物也扫进 commit）。
			hash, err := g.Commit("fix: "+t.Title, "Task-Id: "+t.ID)
			if err != nil {
				t.Retries++
				t.LastError = "git commit 失败: " + err.Error()
				r.saveAttempt(j.ID, t.ID, attempt+1, job.Review{}, diff, t.LastError)
				r.saveTasks(j.ID, tasks)
				r.publish("task_commit_error", j.ID, t.ID, err.Error(), t)
				continue
			}
			t.Commits = append(t.Commits, hash)
			t.Status = job.TaskPassed
			if patch, err := g.Show(hash); err == nil {
				_ = r.Store.SavePatch(j.ID, t.ID, patch)
			}
			r.saveTasks(j.ID, tasks)
			r.publish("task_passed", j.ID, t.ID, "审查通过并已提交 "+hash, t)
			return true
		}

		// 审查驳回：把本次 diff 与审查结论持久化，供重试反馈与后续分析复用（工作区下一轮会回滚）
		t.Retries++
		t.LastError = "审查不通过: " + review.Summary
		r.saveAttempt(j.ID, t.ID, attempt+1, review, diff, t.LastError)
		feedback = fmt.Sprintf(
			"审查员判定不通过。\n审查结论：%s\n具体问题：%s\n\n你上一轮的改动 diff（工作区已回滚，供参考）：\n%s\n\n请先判断上述审查意见是否成立：成立则据此修正；不成立请说明理由并保留正确改法。",
			review.Summary, findingsJSON(review.Findings), truncateStr(diff, 8000))
		r.saveTasks(j.ID, tasks)
		r.publish("task_review_failed", j.ID, t.ID, review.Summary, t)
	}

	t.Status = job.TaskFailed
	r.saveTasks(j.ID, tasks)
	r.publish("task_failed", j.ID, t.ID, fmt.Sprintf("重试 %d 次仍未通过，跳过", maxRetries), t)
	return false
}

// recoverCommittedTask 处理崩溃窗口：任务在 commit 成功后、状态落盘前崩溃时，
// 磁盘上的任务仍是 reviewing，但修复分支 HEAD 已经是该任务的提交。
// 这里通过提交正文里的 Task-Id: trailer 识别并回填哈希，把任务直接标记为 passed，
// 避免重跑时重复修复，同时补回丢失的 commit 哈希链接。
func (r *Runner) recoverCommittedTask(j *job.Job, g *git.Service, tasks []job.Task) {
	if j.FixBranch == "" {
		return
	}
	cur, err := g.CurrentBranch()
	if err != nil || cur != j.FixBranch {
		return
	}
	hash, message, err := g.HeadCommit()
	if err != nil || hash == "" {
		return
	}
	taskID := taskIDFromMessage(message)
	if taskID == "" {
		return
	}
	for i := range tasks {
		if tasks[i].ID != taskID || tasks[i].Status != job.TaskReviewing {
			continue
		}
		tasks[i].Commits = append(tasks[i].Commits, hash)
		tasks[i].Status = job.TaskPassed
		tasks[i].Review = &job.Review{Verdict: "pass", Summary: "崩溃恢复：检测到该任务已提交 " + hash}
		if patch, err := g.Show(hash); err == nil {
			_ = r.Store.SavePatch(j.ID, tasks[i].ID, patch)
		}
		r.publish("task_recovered", j.ID, tasks[i].ID, "崩溃恢复：回填已提交 commit "+hash, &tasks[i])
	}
}

// taskIDFromMessage 从提交信息的 Task-Id: trailer 中解析任务 ID；无则返回空串。
func taskIDFromMessage(msg string) string {
	for _, line := range strings.Split(msg, "\n") {
		line = strings.TrimSpace(line)
		if v, ok := strings.CutPrefix(line, "Task-Id:"); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// runRegressionReview 宏观回归审查：对整条修复分支的累积改动做一次完整性检查。
// 仅当存在「通过（有提交）」的任务时才执行；结果写入 j.Regression 并落盘。
func (r *Runner) runRegressionReview(ctx context.Context, j *job.Job, g *git.Service, tasks []job.Task, constraints string) {
	if r.AI == nil || j.FixBranch == "" || j.BaseBranch == "" {
		return
	}
	passed := false
	for i := range tasks {
		if tasks[i].Status == job.TaskPassed || tasks[i].Status == job.TaskApproved {
			passed = true
			break
		}
	}
	if !passed {
		return
	}
	fullDiff, err := g.DiffRange(j.BaseBranch, j.FixBranch)
	if err != nil {
		r.publish("regression_error", j.ID, "", "获取修复分支整体 diff 失败: "+err.Error(), nil)
		return
	}
	if strings.TrimSpace(fullDiff) == "" {
		return
	}
	logText, err := g.LogRange(j.BaseBranch, j.FixBranch)
	if err != nil {
		r.publish("regression_error", j.ID, "", "获取修复分支 commit 日志失败: "+err.Error(), nil)
		return
	}
	r.publish("regression_reviewing", j.ID, "", "宏观回归审查中（整条修复分支）", nil)
	reg, err := r.AI.ReviewBranch(ctx, j.RepoPath, fullDiff, logText, constraints)
	if err != nil {
		r.publish("regression_error", j.ID, "", "宏观回归审查失败: "+err.Error(), nil)
		return
	}
	j.Regression = &reg
	r.saveJob(j)
	r.publish("regression_reviewed", j.ID, "", "宏观回归审查完成: "+reg.Summary, &reg)
}

func (r *Runner) fail(j *job.Job, err error) error {
	j.Status = job.StatusFailed
	j.UpdatedAt = time.Now()
	r.saveJob(j)
	r.publish("job_failed", j.ID, "", err.Error(), j)
	return err
}

// pause 处理「执行完当前任务后暂停」：置为 paused 状态并消费暂停请求，等待用户「继续」。
// 工作区清理交给下次 startJob 的 Run()（其开头的 EnsureClean/ResetHard 会复位）。
func (r *Runner) pause(j *job.Job) error {
	j.Status = job.StatusPaused
	j.PauseRequest = false // 消费本次暂停请求
	j.CurrentTask = ""
	j.UpdatedAt = time.Now()
	r.saveJob(j)
	r.publish("job_paused", j.ID, "", "已暂停：当前任务已完成，可随时继续", j.Status)
	return nil
}

// pauseRequested 从存储读取最新的暂停请求（server 在另一 goroutine 写入）。
func (r *Runner) pauseRequested(jobID string) bool {
	cur, err := r.Store.LoadJob(jobID)
	if err != nil {
		return false
	}
	return cur.PauseRequest
}

// currentConstraints 从存储读取 job 最新的约束字段并解析为生效文本（live）。
// 复用 LoadJob 模式：每个任务开始前重读，使「进行中更换约束 / 编辑模板」在下一任务生效。
func (r *Runner) currentConstraints(jobID string, fallback *job.Job) string {
	cur, err := r.Store.LoadJob(jobID)
	if err != nil {
		cur = fallback
	}
	constraints := r.Store.ResolveConstraint(cur.ConstraintTemplateID, cur.Constraints)
	// 用审查修复上下文替换约束里的 {{fixer.*}} 占位符（基线分支 / 修复分支）+ 自定义注入变量。
	return ai.RenderConstraints(constraints, ai.MergeValues(ai.FixerConstraintValues(cur), cur.ConstraintVars))
}

// cancel 处理用户取消：清理中断残留并把进行中任务复位为可恢复状态。
func (r *Runner) cancel(j *job.Job) error {
	g := git.New(j.RepoPath)
	if cur, _ := g.CurrentBranch(); cur == j.FixBranch {
		_ = g.ResetHard()
	}
	tasks, _ := r.Store.LoadTasks(j.ID)
	for i := range tasks {
		if tasks[i].Status == job.TaskFixing || tasks[i].Status == job.TaskReviewing {
			tasks[i].Status = job.TaskPending
		}
	}
	_ = r.Store.SaveTasks(j.ID, tasks)
	j.Status = job.StatusCancelled
	j.CurrentTask = ""
	j.UpdatedAt = time.Now()
	r.saveJob(j)
	r.publish("job_cancelled", j.ID, "", "已取消，可重新开始", j.Status)
	return nil
}

func (r *Runner) saveJob(j *job.Job) {
	if err := r.Store.SaveJob(j); err != nil {
		r.publish("error", j.ID, "", "保存 job 失败: "+err.Error(), nil)
	}
}

func (r *Runner) saveTasks(jobID string, tasks []job.Task) {
	if err := r.Store.SaveTasks(jobID, tasks); err != nil {
		r.publish("error", jobID, "", "保存任务失败: "+err.Error(), nil)
	}
}

func (r *Runner) publish(typ, jobID, taskID, msg string, data interface{}) {
	ev := events.Event{Type: typ, JobID: jobID, TaskID: taskID, Message: msg, Data: data, TS: time.Now()}
	r.Bus.Publish(jobID, ev)
	_ = r.Store.AppendEvent(jobID, ev)
}

func findingsJSON(fs []job.Finding) string {
	b, _ := json.Marshal(fs)
	return string(b)
}

// saveAttempt 持久化一次修复尝试的 diff、审查结论与失败原因。
// errMsg 记录该次尝试失败的原因（审查驳回/污染/修复错误/自检失败/提交错误等）；空串表示无附加原因。
func (r *Runner) saveAttempt(jobID, taskID string, attempt int, review job.Review, diff, errMsg string) {
	as, err := r.Store.LoadAttempts(jobID)
	if err != nil {
		as = []store.Attempt{}
	}
	as = append(as, store.Attempt{TaskID: taskID, Attempt: attempt, Review: review, Diff: diff, Error: errMsg})
	if err := r.Store.SaveAttempts(jobID, as); err != nil {
		r.publish("error", jobID, taskID, "保存尝试记录失败: "+err.Error(), nil)
	}
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n…（已截断）"
}
