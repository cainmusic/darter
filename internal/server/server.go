package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"darter/internal/ai"
	"darter/internal/events"
	"darter/internal/git"
	"darter/internal/job"
	"darter/internal/runner"
	"darter/internal/store"
	"darter/internal/webui"
)

type App struct {
	store    *store.Store
	bus      *events.Bus
	mu       sync.Mutex
	cancels  map[string]context.CancelFunc
	wg       sync.WaitGroup
	settings store.Settings
	// repoLocks 任务级仓库锁：仓库路径（归一化后）→ 当前占用它的 job ID。
	// 防止两个 job 同时在同一仓库里跑（git checkout/commit/分支切换会互相破坏）。
	repoLocks map[string]string
}

func New(dataDir string) *App {
	s := store.New(dataDir)
	settings, _ := s.LoadSettings()
	a := &App{
		store:     s,
		bus:       events.New(),
		cancels:   map[string]context.CancelFunc{},
		settings:  settings,
		repoLocks: map[string]string{},
	}
	a.recover()
	a.cleanupOrphans()
	return a
}

// Shutdown 取消所有在跑任务（parse/start）的 context 并等待它们收尾退出。
// 取消 context 会让 exec.CommandContext 一并杀掉 claude 子进程，避免留下孤儿进程。
func (a *App) Shutdown() {
	a.mu.Lock()
	for _, cancel := range a.cancels {
		cancel()
	}
	a.mu.Unlock()
	a.wg.Wait()
}

// recover 在启动时把崩溃遗留的瞬时状态复位，保证进程重启后可以继续/重跑。
// 瞬时状态：parsing / reviewing_tasks（拆解中，重启后需重新拆解）、running（修复循环中，
// 重启后复位为 ready）。任务级的恢复（复位卡住的任务、回填已提交的 commit）由 runner 在下次
// 启动时统一处理——它需要访问 git，才能判断「commit 是否已经落盘」。
func (a *App) recover() {
	jobs, err := a.store.ListJobs()
	if err != nil {
		return
	}
	for _, j := range jobs {
		switch j.Status {
		case job.StatusParsing, job.StatusReviewingTasks:
			old := j.Status
			j.Status = job.StatusDraft
			j.CurrentTask = ""
			j.UpdatedAt = time.Now()
			if err := a.store.SaveJob(j); err == nil {
				log.Printf("恢复：job %s 由 %s 复位为 draft（进程曾在拆解报告时崩溃）", j.ID, old)
			}
		case job.StatusRunning:
			j.Status = job.StatusReady
			j.CurrentTask = ""
			j.UpdatedAt = time.Now()
			if err := a.store.SaveJob(j); err == nil {
				log.Printf("恢复：job %s 由 running 复位为 ready（进程曾在修复循环时崩溃）", j.ID)
			}
		case job.StatusArchiving:
			// 归档进行中崩溃：复位回待审查，可重新触发归档（归档本身幂等，已归档任务会跳过）
			j.Status = job.StatusAwaitingReview
			j.CurrentTask = ""
			j.UpdatedAt = time.Now()
			if err := a.store.SaveJob(j); err == nil {
				log.Printf("恢复：job %s 由 archiving 复位为 awaiting_review（进程曾在归档时崩溃）", j.ID)
			}
		case job.MigStatusRunning:
			j.Status = job.MigStatusReady
			j.UpdatedAt = time.Now()
			if err := a.store.SaveJob(j); err == nil {
				log.Printf("恢复：迁移 job %s 由 mig_running 复位为 mig_ready（进程曾在连续迁移时崩溃）", j.ID)
			}
		case job.MigStatusAuto:
			j.Status = job.MigStatusReady
			j.UpdatedAt = time.Now()
			if err := a.store.SaveJob(j); err == nil {
				log.Printf("恢复：迁移 job %s 由 mig_auto 复位为 mig_ready（进程曾在全自动迁移时崩溃）", j.ID)
			}
		}
	}
}

// cleanupOrphans 清理上次进程崩溃（如 kill -9）遗留的 claude 子进程。
// 记录里存了 PID + 启动时间 + 命令名，三重校验通过才 kill，避免 PID 复用误杀无关进程。
func (a *App) cleanupOrphans() {
	children, err := a.store.ListChildren()
	if err != nil {
		log.Printf("读取子进程记录失败: %v", err)
		return
	}
	for _, c := range children {
		if ai.KillOrphan(c.PID, c.Start, c.Cmd) {
			log.Printf("清理孤儿 claude 进程 pid=%d (job %s)", c.PID, c.JobID)
		}
		_ = a.store.DeleteChild(c.PID)
	}
}

// repoKey 归一化仓库路径，作为锁的 key（忽略末尾斜杠/./.. 等书写差异）。
func repoKey(path string) string {
	return filepath.Clean(strings.TrimSpace(path))
}

// tryAcquireRepo 尝试独占某仓库目录。同一 job 重复获取视为成功（幂等）；被其它 job 占用则返回错误。
// 成功后返回一个释放函数（仅当仍是该 job 持有时才删除，避免误删后来者的锁）。
func (a *App) tryAcquireRepo(path, jobID string) (func(), error) {
	key := repoKey(path)
	a.mu.Lock()
	defer a.mu.Unlock()
	if holder, ok := a.repoLocks[key]; ok && holder != jobID {
		return nil, fmt.Errorf("仓库 %s 正被任务 %s 占用，请等待其完成后再操作", path, holder)
	}
	a.repoLocks[key] = jobID
	return func() {
		a.mu.Lock()
		if a.repoLocks[key] == jobID {
			delete(a.repoLocks, key)
		}
		a.mu.Unlock()
	}, nil
}

// requireRepoBranch 校验仓库当前分支属于允许集合；否则返回人类可读错误（含当前分支与预期分支）。
// 用于拆解/修复/归档等操作入口做分支安全校验，避免在遗留的其它 job 临时分支（或用户其它分支）上误操作——
// 例如在旧 fix 分支上跑拆解，会让「该问题是否已存在/已修复」的判断被旧提交污染。
func requireRepoBranch(repoPath string, allowed ...string) error {
	g := git.New(repoPath)
	cur, err := g.CurrentBranch()
	if err != nil {
		return fmt.Errorf("读取仓库 %s 当前分支失败: %w", repoPath, err)
	}
	names := make([]string, 0, len(allowed))
	for _, b := range allowed {
		if b == "" {
			continue
		}
		names = append(names, b)
		if cur == b {
			return nil
		}
	}
	return fmt.Errorf("仓库 %s 当前在分支 %q，预期分支为 [%s]；请先 git checkout 到正确分支后再操作", repoPath, cur, strings.Join(names, " / "))
}

func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/jobs", a.createJob)
	mux.HandleFunc("GET /api/jobs", a.listJobs)
	mux.HandleFunc("GET /api/jobs/{id}", a.getJob)
	mux.HandleFunc("PUT /api/jobs/{id}/constraints", a.updateJobConstraints)
	mux.HandleFunc("POST /api/jobs/{id}/parse", a.parseJob)
	mux.HandleFunc("POST /api/jobs/{id}/redecompose", a.redecomposeJob)
	mux.HandleFunc("POST /api/jobs/{id}/start", a.startJob)
	mux.HandleFunc("POST /api/jobs/{id}/cancel", a.cancelJob)
	mux.HandleFunc("POST /api/jobs/{id}/pause", a.pauseJob)
	mux.HandleFunc("POST /api/jobs/{id}/unpause", a.unpauseJob)
	mux.HandleFunc("POST /api/jobs/{id}/review-complete", a.reviewComplete)
	mux.HandleFunc("GET /api/jobs/{id}/events", a.events)
	mux.HandleFunc("GET /api/jobs/{id}/history", a.jobHistory)
	mux.HandleFunc("GET /api/jobs/{id}/tasks/{tid}", a.getTask)
	mux.HandleFunc("GET /api/jobs/{id}/problems", a.downloadProblems)
	mux.HandleFunc("POST /api/jobs/{id}/archive", a.startArchive)
	mux.HandleFunc("POST /api/jobs/{id}/archive/confirm", a.confirmArchive)
	mux.HandleFunc("POST /api/jobs/{id}/archive-freeze", a.archiveFreeze)
	mux.HandleFunc("POST /api/jobs/{id}/redo-all", a.redoAll)
	mux.HandleFunc("POST /api/jobs/{id}/archive/tasks/{tid}/resolve", a.resolveArchiveTask)
	mux.HandleFunc("POST /api/jobs/{id}/tasks/{tid}/approve", a.approveTask)
	mux.HandleFunc("POST /api/jobs/{id}/tasks/{tid}/reject", a.rejectTask)
	mux.HandleFunc("POST /api/jobs/{id}/tasks/{tid}/unapprove", a.unapproveTask)
	mux.HandleFunc("POST /api/jobs/{id}/tasks/{tid}/ai-review", a.aiReviewTask)
	mux.HandleFunc("GET /api/jobs/{id}/tasks/{tid}/ai-reviews", a.listAiReviews)
	mux.HandleFunc("GET /api/jobs/{id}/ai-activity", a.aiActivity)
	mux.HandleFunc("POST /api/jobs/{id}/tasks/{tid}/defer", a.deferTask)
	mux.HandleFunc("PUT /api/jobs/{id}/tasks/{tid}/annotation", a.annotateTask)
	mux.HandleFunc("GET /api/settings", a.getSettings)
	mux.HandleFunc("PUT /api/settings", a.putSettings)
	mux.HandleFunc("GET /api/prompts", a.listPrompts)
	mux.HandleFunc("GET /api/scenes", a.listScenes)
	mux.HandleFunc("GET /api/constraint-templates", a.listConstraintTemplates)
	mux.HandleFunc("POST /api/constraint-templates", a.createConstraintTemplate)
	mux.HandleFunc("GET /api/constraint-templates/{id}", a.getConstraintTemplate)
	mux.HandleFunc("POST /api/constraint-templates/{id}/copy", a.copyConstraintTemplate)
	mux.HandleFunc("PUT /api/constraint-templates/{id}", a.updateConstraintTemplate)
	mux.HandleFunc("DELETE /api/constraint-templates/{id}", a.deleteConstraintTemplate)
	mux.HandleFunc("GET /api/constraint-injectables", a.listConstraintInjectables)
	// 代码迁移模块
	mux.HandleFunc("POST /api/migrations", a.createMigration)
	mux.HandleFunc("GET /api/migrations", a.listMigrations)
	mux.HandleFunc("GET /api/migrations/{id}", a.getMigration)
	mux.HandleFunc("GET /api/migrations/{id}/repo-state", a.repoState)
	mux.HandleFunc("POST /api/migrations/{id}/repo-clean", a.cleanRepo)
	mux.HandleFunc("POST /api/migrations/{id}/commits/{hash}/migrate", a.migrateCommit)
	mux.HandleFunc("POST /api/migrations/{id}/commits/{hash}/mark", a.setCommitMark)
	mux.HandleFunc("POST /api/migrations/{id}/commits/{hash}/target", a.setTargetCommit)
	mux.HandleFunc("POST /api/migrations/{id}/commits/{hash}/ignore", a.ignoreCommit)
	mux.HandleFunc("POST /api/migrations/{id}/merge", a.mergeCommits)
	mux.HandleFunc("POST /api/migrations/{id}/range", a.migrateRange)
	mux.HandleFunc("POST /api/migrations/{id}/ignore-range", a.ignoreRange)
	mux.HandleFunc("POST /api/migrations/{id}/conflict/resolve", a.resolveConflict)
	mux.HandleFunc("POST /api/migrations/{id}/conflict/ai", a.aiResolveConflict)
	mux.HandleFunc("POST /api/migrations/{id}/conflict/cancel", a.cancelAiResolveConflict)
	mux.HandleFunc("POST /api/migrations/{id}/auto-migrate", a.autoMigrate)
	mux.HandleFunc("POST /api/migrations/{id}/auto-migrate/cancel", a.cancelAutoMigrate)
	mux.HandleFunc("POST /api/migrations/{id}/regression", a.regressionMigration)
	mux.HandleFunc("POST /api/migrations/{id}/reset", a.resetTarget)
	mux.HandleFunc("POST /api/migrations/{id}/complete", a.completeMigration)

	// 内嵌前端（go:embed），SPA 路由回退到 index.html
	if sub, err := webui.FS(); err == nil {
		mux.Handle("/", spaHandler(sub))
	} else {
		log.Printf("警告：未内嵌前端资源，请先执行 npm run build（%v）", err)
	}

	return cors(mux)
}

// ---- 创建/查询 ----

func (a *App) createJob(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name         string           `json:"name"`
		RepoPath     string           `json:"repo_path"`
		BaseBranch   string           `json:"base_branch"`
		ReportSource job.ReportSource `json:"report_source"`
		Constraints  string           `json:"constraints"`
		// ConstraintTemplateID 引用的约束模板 ID（可选）；与 Constraints 互斥，模板优先。
		ConstraintTemplateID string            `json:"constraint_template_id"`
		ConstraintVars       map[string]string `json:"constraint_vars"`
		MaxRetries           int               `json:"max_retries"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "请求体解析失败: "+err.Error())
		return
	}
	if req.RepoPath == "" {
		writeErr(w, 400, "repo_path 不能为空")
		return
	}
	if req.BaseBranch == "" {
		writeErr(w, 400, "base_branch 不能为空，请填写基础分支（如 master / dev / main）")
		return
	}
	if req.ReportSource.Value == "" {
		writeErr(w, 400, "报告内容不能为空")
		return
	}
	if _, err := os.Stat(filepath.Join(req.RepoPath, ".git")); err != nil {
		writeErr(w, 400, fmt.Sprintf("repo_path 不是有效的 git 仓库: %v", err))
		return
	}
	// 创建时校验配置的基础分支真实存在，尽早暴露分支名笔误（后续拆解/修复都依赖它）。
	if !git.New(req.RepoPath).BranchExists(req.BaseBranch) {
		writeErr(w, 400, fmt.Sprintf("基础分支 %s 不存在于仓库 %s（可用 git branch -a 查看实际分支）", req.BaseBranch, req.RepoPath))
		return
	}

	retries := req.MaxRetries
	if retries <= 0 {
		retries = 3
	}

	j := &job.Job{
		ID:                   fmt.Sprintf("job_%s_%s", time.Now().Format("20060102-150405"), randSuffix()),
		Kind:                 job.KindReviewFixer,
		Name:                 req.Name,
		RepoPath:             req.RepoPath,
		BaseBranch:           req.BaseBranch,
		ReportSource:         req.ReportSource,
		Constraints:          req.Constraints,
		ConstraintTemplateID: req.ConstraintTemplateID,
		ConstraintVars:       req.ConstraintVars,
		Status:               job.StatusDraft,
		MaxRetries:           retries,
		ClaudeCmd:            a.settings.ClaudeCmd,
		ClaudeModel:          a.settings.ClaudeModel,
		CreatedAt:            time.Now(),
		UpdatedAt:            time.Now(),
	}
	if j.Name == "" {
		j.Name = filepath.Base(req.RepoPath) + "-fix"
	}
	if err := a.store.SaveJob(j); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 201, j)
}

func (a *App) listJobs(w http.ResponseWriter, r *http.Request) {
	jobs, err := a.store.ListJobs()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	// 只返回 review-fixer 任务；迁移任务走 /api/migrations。
	out := make([]*job.Job, 0, len(jobs))
	for _, j := range jobs {
		if j.Kind != job.KindMigration {
			out = append(out, j)
		}
	}
	writeJSON(w, 200, out)
}

func (a *App) getJob(w http.ResponseWriter, r *http.Request) {
	j, ok := a.loadJob(w, r)
	if !ok {
		return
	}
	tasks, _ := a.store.LoadTasks(j.ID)
	a.mergeAnnotations(j.ID, tasks)
	writeJSON(w, 200, map[string]interface{}{"job": j, "tasks": tasks})
}

// updateJobConstraints 更换 job 的约束（可进行中更换）：改为引用某模板，或改用内联文本，或清空。
// runner 在下一个任务开始前重读并解析，因此不影响正在跑的 AI 调用。
func (a *App) updateJobConstraints(w http.ResponseWriter, r *http.Request) {
	j, ok := a.loadJob(w, r)
	if !ok {
		return
	}
	var req struct {
		ConstraintTemplateID string            `json:"constraint_template_id"`
		Constraints          string            `json:"constraints"`
		ConstraintVars       map[string]string `json:"constraint_vars"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "请求体解析失败: "+err.Error())
		return
	}
	req.ConstraintTemplateID = strings.TrimSpace(req.ConstraintTemplateID)
	req.Constraints = strings.TrimSpace(req.Constraints)
	// 若引用模板，校验模板存在，避免写入悬空引用。
	if req.ConstraintTemplateID != "" {
		ts, err := a.store.LoadConstraintTemplates()
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		found := false
		for _, t := range ts {
			if t.ID == req.ConstraintTemplateID {
				found = true
				break
			}
		}
		if !found {
			writeErr(w, 400, "约束模板不存在")
			return
		}
	}
	j.ConstraintTemplateID = req.ConstraintTemplateID
	j.Constraints = req.Constraints
	j.ConstraintVars = req.ConstraintVars
	j.UpdatedAt = time.Now()
	if err := a.store.SaveJob(j); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, j)
}

// ---- 解析报告 ----

func (a *App) parseJob(w http.ResponseWriter, r *http.Request) {
	j, ok := a.loadJob(w, r)
	if !ok {
		return
	}
	if j.TasksFrozen {
		writeErr(w, 409, "任务列表已冻结，不能重新拆解")
		return
	}
	releaseRepo, err := a.tryAcquireRepo(j.RepoPath, j.ID)
	if err != nil {
		writeErr(w, 409, err.Error())
		return
	}
	// 拆解/预检只读地在仓库里跑，必须基于 base 分支判断「问题是否已存在/已修复」，
	// 避免遗留的其它 job 临时分支污染判断（旧 fix 分支上已有的提交被误判为已修复）。
	if err := requireRepoBranch(j.RepoPath, j.BaseBranch); err != nil {
		releaseRepo()
		writeErr(w, 409, err.Error())
		return
	}
	j.Status = job.StatusParsing
	j.UpdatedAt = time.Now()
	_ = a.store.SaveJob(j)
	a.launchParse(j, releaseRepo)
	writeJSON(w, 202, j)
}

// redecomposeJob 打回重做：任务列表已生成但用户不满意，清空并重新拆解。
// 允许在「已冻结、且未在进行修复/归档」的状态下重做：ready（尚未开始）、
// failed/cancelled（修复循环已退出、任务列表仍保留）。避免在 running/archiving 等
// 进行中的状态重排任务造成混乱，也避免在已完成/待审查后误丢已提交成果。
func (a *App) redecomposeJob(w http.ResponseWriter, r *http.Request) {
	j, ok := a.loadJob(w, r)
	if !ok {
		return
	}
	if !j.TasksFrozen {
		writeErr(w, 409, "任务列表尚未生成，无需打回重做")
		return
	}
	switch j.Status {
	case job.StatusReady, job.StatusFailed, job.StatusCancelled:
	default:
		writeErr(w, 409, "当前状态无法打回重做（仅「待开始/失败/已取消」且任务列表已生成时可用）")
		return
	}
	releaseRepo, err := a.tryAcquireRepo(j.RepoPath, j.ID)
	if err != nil {
		writeErr(w, 409, err.Error())
		return
	}
	// 打回重做会重新拆解+预检，同样必须基于 base 分支判断，避免被遗留临时分支污染。
	if err := requireRepoBranch(j.RepoPath, j.BaseBranch); err != nil {
		releaseRepo()
		writeErr(w, 409, err.Error())
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	j.TasksFrozen = false
	j.CurrentTask = ""
	j.DecomposeIgnored = nil
	j.DecomposeRemoved = nil
	j.RedecomposeReason = strings.TrimSpace(req.Reason)
	j.Status = job.StatusParsing
	j.UpdatedAt = time.Now()
	_ = a.store.SaveJob(j)
	// 清空任务用空数组而非 nil：nil 会被序列化为 JSON null，前端拿到的 tasks 是 null 而非 []，导致 .find 报错。
	_ = a.store.SaveTasks(j.ID, []job.Task{})
	a.launchParse(j, releaseRepo)
	writeJSON(w, 202, j)
}

// launchParse 启动拆解 goroutine（parseJob 与 redecomposeJob 复用）。releaseRepo 为已获取的仓库锁释放函数。
func (a *App) launchParse(j *job.Job, releaseRepo func()) {
	client := ai.Client{
		Cmd:   j.ClaudeCmd,
		Model: j.ClaudeModel,
		Log: func(line string) {
			a.publish("ai_log", j.ID, "", line, nil)
		},
		OnChild: func(pid int, start, cmd, phase string) {
			_ = a.store.SaveChild(store.Child{PID: pid, Start: start, Cmd: cmd, JobID: j.ID, Phase: phase, StartedAt: time.Now()})
		},
		OnChildExit: func(pid int) {
			_ = a.store.DeleteChild(pid)
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	a.mu.Lock()
	if old, exists := a.cancels[j.ID]; exists {
		old()
	}
	a.cancels[j.ID] = cancel
	a.mu.Unlock()
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		defer cancel()
		defer releaseRepo()
		defer func() {
			a.mu.Lock()
			delete(a.cancels, j.ID)
			a.mu.Unlock()
		}()
		report, err := reportText(j.ReportSource)
		if err != nil {
			a.failJob(j, err)
			return
		}

		// 拆解/质检都在项目目录里只读进行，注入约束作为项目背景（判断问题归属的服务与文件）。
		constraints := a.store.ResolveConstraint(j.ConstraintTemplateID, j.Constraints)
		constraints = ai.RenderConstraints(constraints, ai.MergeValues(ai.FixerConstraintValues(j), j.ConstraintVars))
		// 打回重做时的人工说明，喂给拆解与复查 AI 改进，避免重复上次的问题。
		feedback := j.RedecomposeReason

		a.publish("parse_started", j.ID, "", "开始 AI 拆解报告", nil)
		tasks, ignored, err := client.ParseReport(ctx, j.RepoPath, report, constraints, feedback)
		if err != nil {
			a.failJob(j, err)
			return
		}
		j.DecomposeIgnored = ignored
		a.publish("job_parsed", j.ID, "", fmt.Sprintf("拆解出 %d 个修复任务，开始 AI 复查", len(tasks)), tasks)

		j.Status = job.StatusReviewingTasks
		j.UpdatedAt = time.Now()
		_ = a.store.SaveJob(j)

		a.publish("review_tasks_started", j.ID, "", "AI 复查任务列表中（查漏/去重/粒度 + 对抗性预检）", nil)
		final, removed, err := client.ReviewTasks(ctx, j.RepoPath, report, tasks, constraints, feedback)
		if err != nil {
			a.failJob(j, err)
			return
		}
		j.DecomposeRemoved = removed
		if err := a.store.SaveTasks(j.ID, final); err != nil {
			a.failJob(j, err)
			return
		}
		j.TasksFrozen = true
		j.Status = job.StatusReady
		j.UpdatedAt = time.Now()
		_ = a.store.SaveJob(j)
		a.publish("task_list_frozen", j.ID, "", fmt.Sprintf("AI 复查完成，任务列表已冻结（%d 个任务）", len(final)), final)
	}()
}

// ---- 开始/取消循环 ----

func (a *App) startJob(w http.ResponseWriter, r *http.Request) {
	j, ok := a.loadJob(w, r)
	if !ok {
		return
	}
	switch j.Status {
	case job.StatusReady, job.StatusFailed, job.StatusCancelled, job.StatusPaused:
	default:
		writeErr(w, 409, fmt.Sprintf("当前状态 %s 无法开始修复", j.Status))
		return
	}
	releaseRepo, err := a.tryAcquireRepo(j.RepoPath, j.ID)
	if err != nil {
		writeErr(w, 409, err.Error())
		return
	}
	// 开始修复前校验分支：只允许在 base（首次，尚未建 fix 分支）或本 job 自己的 fix 分支（续跑）上启动，
	// 拒绝在其它 job 遗留的临时分支上误跑。
	if err := requireRepoBranch(j.RepoPath, j.BaseBranch, j.FixBranch); err != nil {
		releaseRepo()
		writeErr(w, 409, err.Error())
		return
	}
	j.PauseRequest = false // 开始/继续前清除可能残留的暂停请求
	_ = a.store.SaveJob(j)
	ctx, cancel := context.WithCancel(context.Background())
	a.mu.Lock()
	if old, exists := a.cancels[j.ID]; exists {
		old() // 先取消旧循环
	}
	a.cancels[j.ID] = cancel
	a.mu.Unlock()

	rr := &runner.Runner{
		Store: a.store,
		AI:    &ai.Client{Cmd: j.ClaudeCmd, Model: j.ClaudeModel},
		Bus:   a.bus,
	}
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		defer releaseRepo()
		defer func() {
			a.mu.Lock()
			delete(a.cancels, j.ID)
			a.mu.Unlock()
		}()
		_ = rr.Run(ctx, j)
	}()
	writeJSON(w, 202, j)
}

func (a *App) cancelJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	// paused 状态下没有在跑的循环（runner 已返回），直接置为 cancelled。
	if j, err := a.store.LoadJob(id); err == nil && j.Status == job.StatusPaused {
		j.Status = job.StatusCancelled
		j.PauseRequest = false
		j.UpdatedAt = time.Now()
		_ = a.store.SaveJob(j)
		a.publish("job_cancelled", id, "", "已取消（暂停状态）", j.Status)
		writeJSON(w, 200, map[string]string{"status": "cancelled"})
		return
	}
	a.mu.Lock()
	cancel, exists := a.cancels[id]
	a.mu.Unlock()
	if !exists {
		writeErr(w, 404, "没有正在运行的循环")
		return
	}
	cancel()
	a.publish("job_cancelled", id, "", "已请求取消", nil)
	writeJSON(w, 200, map[string]string{"status": "cancelling"})
}

// pauseJob 请求「执行完当前任务后暂停」：不取消 context（让当前任务跑完），
// 只落一个持久化标记，由 runner 在下一个任务开始前消费并进入 paused 状态。
func (a *App) pauseJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	j, ok := a.loadJob(w, r)
	if !ok {
		return
	}
	if j.Status != job.StatusRunning {
		writeErr(w, 409, fmt.Sprintf("当前状态 %s 无法暂停", j.Status))
		return
	}
	j.PauseRequest = true
	j.UpdatedAt = time.Now()
	if err := a.store.SaveJob(j); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	a.publish("pause_requested", id, "", "已请求暂停：当前任务完成后停止", nil)
	writeJSON(w, 200, map[string]string{"status": "pausing"})
}

// unpauseJob 撤销尚未生效的暂停请求：任务还在跑（running + PauseRequest），
// 用户反悔，把标记清掉继续运行。
func (a *App) unpauseJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	j, ok := a.loadJob(w, r)
	if !ok {
		return
	}
	if j.Status != job.StatusRunning || !j.PauseRequest {
		writeErr(w, 409, "当前没有待取消的暂停请求")
		return
	}
	j.PauseRequest = false
	j.UpdatedAt = time.Now()
	if err := a.store.SaveJob(j); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	a.publish("pause_cancelled", id, "", "已取消暂停请求，继续运行", nil)
	writeJSON(w, 200, map[string]string{"status": "running"})
}

// startArchive 开启归档流程：把 approved 任务逐个迁移到归档分支并做最终回归审查，之后等待人工确认。
func (a *App) startArchive(w http.ResponseWriter, r *http.Request) {
	j, ok := a.loadJob(w, r)
	if !ok {
		return
	}
	switch j.Status {
	case job.StatusReviewDone, job.StatusAwaitingArchiveConfirm:
	default:
		writeErr(w, 409, fmt.Sprintf("当前状态 %s 无法归档", j.Status))
		return
	}
	if err := a.launchArchive(j); err != nil {
		writeErr(w, 409, err.Error())
		return
	}
	writeJSON(w, 202, j)
}

// launchArchive 启动归档 runner（幂等续跑）。startArchive 与 resolveArchiveTask 复用。
// 返回错误表示仓库正被其它 job 占用，无法启动。
func (a *App) launchArchive(j *job.Job) error {
	releaseRepo, err := a.tryAcquireRepo(j.RepoPath, j.ID)
	if err != nil {
		return err
	}
	// 归档入口校验分支：修复循环正常结束停在 fix 分支，归档可能从 base / fix / 本 job 的 archive 分支续跑；
	// 拒绝在其它 job 遗留的临时分支上误跑。
	if err := requireRepoBranch(j.RepoPath, j.BaseBranch, j.FixBranch, j.ArchiveBranch); err != nil {
		releaseRepo()
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.mu.Lock()
	if old, exists := a.cancels[j.ID]; exists {
		old()
	}
	a.cancels[j.ID] = cancel
	a.mu.Unlock()

	rr := &runner.Runner{
		Store: a.store,
		AI:    &ai.Client{Cmd: j.ClaudeCmd, Model: j.ClaudeModel},
		Bus:   a.bus,
	}
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		defer releaseRepo()
		defer func() {
			a.mu.Lock()
			delete(a.cancels, j.ID)
			a.mu.Unlock()
		}()
		_ = rr.RunArchive(ctx, j)
	}()
	return nil
}

// resolveArchiveTask 处理归档卡点：对 blocked 任务给出人工指令（retry/continue/skip）后重新触发归档。
func (a *App) resolveArchiveTask(w http.ResponseWriter, r *http.Request) {
	j, ok := a.loadJob(w, r)
	if !ok {
		return
	}
	if j.Status != job.StatusArchiveBlocked {
		writeErr(w, 409, fmt.Sprintf("当前状态 %s 无法处理归档卡点", j.Status))
		return
	}
	tid := r.PathValue("tid")
	var req struct {
		Action string `json:"action"`
		Note   string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "请求体解析失败: "+err.Error())
		return
	}
	switch req.Action {
	case "retry", "continue", "skip":
	default:
		writeErr(w, 400, "action 必须是 retry/continue/skip 之一")
		return
	}
	tasks, err := a.store.LoadTasks(j.ID)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	found := false
	for i := range tasks {
		if tasks[i].ID != tid {
			continue
		}
		if tasks[i].Status != job.TaskArchiveBlocked {
			writeErr(w, 409, "该任务不在归档卡点状态")
			return
		}
		tasks[i].ArchiveAction = req.Action
		tasks[i].ArchiveNote = strings.TrimSpace(req.Note)
		found = true
		break
	}
	if !found {
		writeErr(w, 404, "任务不存在")
		return
	}
	if err := a.store.SaveTasks(j.ID, tasks); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	a.publish("archive_resolved", j.ID, tid, "归档卡点已处理："+req.Action, nil)
	if err := a.launchArchive(j); err != nil {
		// 指令已落盘，下次续跑会消费；此处仅提示仓库仍被占用。
		writeErr(w, 409, err.Error())
		return
	}
	writeJSON(w, 202, j)
}

// confirmArchive 人工确认最终回归审查通过，完成整体修复任务。
func (a *App) confirmArchive(w http.ResponseWriter, r *http.Request) {
	j, ok := a.loadJob(w, r)
	if !ok {
		return
	}
	if j.Status != job.StatusAwaitingArchiveConfirm {
		writeErr(w, 409, fmt.Sprintf("当前状态 %s 无法确认归档", j.Status))
		return
	}
	// 最终回归审查未通过（commit↔任务对应关系有问题）时，阻断确认，避免把有问题的归档误判为完成。
	if j.ArchiveReview != nil && j.ArchiveReview.Verdict == "fail" {
		writeErr(w, 409, "最终回归审查未通过（"+j.ArchiveReview.Summary+"），请先处理后再确认")
		return
	}
	j.Status = job.StatusCompleted
	j.UpdatedAt = time.Now()
	if err := a.store.SaveJob(j); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	a.publish("archive_confirmed", j.ID, "", "归档已人工确认，整体修复任务完成", j.Status)
	writeJSON(w, 200, j)
}

// archiveFreeze 手动归档冻结：不跑归档迁移流程，直接把任务冻结为归档态（archived）。
// 与 completed（自动归档）区分：completed 是归档流程迁移 commit 后确认完成；
// archived 是人工直接冻结——临时 fix/archive 分支保留，仅把工作区切回基础分支并清空未提交改动。
func (a *App) archiveFreeze(w http.ResponseWriter, r *http.Request) {
	j, ok := a.loadJob(w, r)
	if !ok {
		return
	}
	switch j.Status {
	case job.StatusReady, job.StatusAwaitingReview, job.StatusReviewDone, job.StatusFailed, job.StatusCancelled, job.StatusPaused:
	default:
		writeErr(w, 409, fmt.Sprintf("当前状态 %s 无法归档冻结", j.Status))
		return
	}
	releaseRepo, err := a.tryAcquireRepo(j.RepoPath, j.ID)
	if err != nil {
		writeErr(w, 409, err.Error())
		return
	}
	defer releaseRepo()

	// 清空工作区：若当前停在 fix/archive 分支，丢弃未提交改动并切回基础分支（分支本身保留，不删除）。
	g := git.New(j.RepoPath)
	if cur, err := g.CurrentBranch(); err == nil && cur != j.BaseBranch {
		_ = g.ResetHard()
		_ = g.CleanUntracked()
		_ = g.Checkout(j.BaseBranch)
	}

	j.Status = job.StatusArchived
	j.CurrentTask = ""
	j.UpdatedAt = time.Now()
	if err := a.store.SaveJob(j); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	a.publish("archive_frozen", j.ID, "", "已手动归档冻结（临时分支保留，工作区已清空）", j.Status)
	writeJSON(w, 200, j)
}

// redoAll 全任务重做：抛弃当前 fix 分支，基于 base 重建 fix 分支，保留任务列表、清除任务状态。
// 可在「开始修复之后、开始归档之前」触发（awaiting_review/review_done/paused/cancelled/failed）。
// 注意：deferred（手动跳过）等状态也会一并重置为 pending，重做即「从零开始」。
func (a *App) redoAll(w http.ResponseWriter, r *http.Request) {
	j, ok := a.loadJob(w, r)
	if !ok {
		return
	}
	switch j.Status {
	case job.StatusAwaitingReview, job.StatusReviewDone, job.StatusPaused, job.StatusCancelled, job.StatusFailed:
	default:
		writeErr(w, 409, fmt.Sprintf("当前状态 %s 无法全任务重做（仅限开始修复后、开始归档前）", j.Status))
		return
	}
	if j.FixBranch == "" {
		writeErr(w, 409, "当前尚无修复分支，无需重做（可直接开始修复）")
		return
	}
	releaseRepo, err := a.tryAcquireRepo(j.RepoPath, j.ID)
	if err != nil {
		writeErr(w, 409, err.Error())
		return
	}
	defer releaseRepo()
	// 只允许停在旧 fix 分支上重建，避免误丢 base 或其它分支的工作区改动。
	if err := requireRepoBranch(j.RepoPath, j.FixBranch); err != nil {
		writeErr(w, 409, err.Error())
		return
	}

	oldFix := j.FixBranch
	newFix := fmt.Sprintf("fix/ai-review-%s", time.Now().Format("20060102-150405"))
	if err := git.New(j.RepoPath).RebuildFixBranch(j.BaseBranch, oldFix, newFix); err != nil {
		writeErr(w, 500, err.Error())
		return
	}

	// 保留任务列表（id/title/description/severity/files 不变），重置修复/归档状态为 pending。
	tasks, err := a.store.LoadTasks(j.ID)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	for i := range tasks {
		tasks[i].Status = job.TaskPending
		tasks[i].Retries = 0
		tasks[i].Commits = nil
		tasks[i].Review = nil
		tasks[i].Human = nil
		tasks[i].Archived = false
		tasks[i].ArchiveCommit = ""
		tasks[i].LastError = ""
		tasks[i].ArchiveAction = ""
		tasks[i].ArchiveNote = ""
	}
	if err := a.store.SaveTasks(j.ID, tasks); err != nil {
		writeErr(w, 500, err.Error())
		return
	}

	j.FixBranch = newFix
	j.ArchiveBranch = ""
	j.ArchiveReview = nil
	j.Regression = nil
	j.RemainingFindings = nil
	j.RedecomposeReason = ""
	j.CurrentTask = ""
	j.Status = job.StatusReady
	j.UpdatedAt = time.Now()
	if err := a.store.SaveJob(j); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	a.publish("redo_all", j.ID, "", fmt.Sprintf("已全任务重做：重建修复分支 %s，任务状态已清除", newFix), j.Status)
	writeJSON(w, 200, j)
}

// ---- 任务操作 ----

func (a *App) getTask(w http.ResponseWriter, r *http.Request) {
	id, tid := r.PathValue("id"), r.PathValue("tid")
	tasks, err := a.store.LoadTasks(id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	a.mergeAnnotations(id, tasks)
	for _, t := range tasks {
		if t.ID == tid {
			patch, _ := a.store.LoadPatch(id, tid)
			writeJSON(w, 200, map[string]interface{}{"task": t, "patch": patch, "attempts": a.taskAttempts(id, tid)})
			return
		}
	}
	writeErr(w, 404, "任务不存在")
}

// taskAttempts 返回某任务的历次失败尝试记录（修复 diff 与审查结论成对），供详情页展示。
func (a *App) taskAttempts(jobID, taskID string) []store.Attempt {
	all, err := a.store.LoadAttempts(jobID)
	if err != nil {
		return []store.Attempt{}
	}
	var out []store.Attempt
	for _, at := range all {
		if at.TaskID == taskID {
			out = append(out, at)
		}
	}
	return out
}

func (a *App) approveTask(w http.ResponseWriter, r *http.Request) {
	a.humanReview(w, r, job.TaskApproved, "approved")
}

func (a *App) rejectTask(w http.ResponseWriter, r *http.Request) {
	// 驳回即判失败：不再回炉重跑，本次修复内不再处理，留作失败记录（人工意见仍在 human 字段）。
	a.humanReview(w, r, job.TaskFailed, "rejected")
}

// unapproveTask 取消审批：把已审批(approved)的任务回退为通过(passed)，清除人工审批记录。
// 已归档的任务不可回退——归档分支已经迁移了它的改动，回退会让任务状态与归档分支脱节。
func (a *App) unapproveTask(w http.ResponseWriter, r *http.Request) {
	id, tid := r.PathValue("id"), r.PathValue("tid")
	tasks, err := a.store.LoadTasks(id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	for i := range tasks {
		if tasks[i].ID != tid {
			continue
		}
		if tasks[i].Status != job.TaskApproved {
			writeErr(w, 409, "该任务不是已审批状态，无需取消审批")
			return
		}
		if tasks[i].Archived || tasks[i].ArchiveCommit != "" {
			writeErr(w, 409, "该任务已归档，无法取消审批")
			return
		}
		tasks[i].Status = job.TaskPassed
		tasks[i].Human = nil
		if err := a.store.SaveTasks(id, tasks); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		a.publish("task_unapproved", id, tid, "已取消审批，回到待审", &tasks[i])
		writeJSON(w, 200, &tasks[i])
		return
	}
	writeErr(w, 404, "任务不存在")
}

// aiReviewTask 人工发起一次 AI 审查：对某个 passed/approved 任务的修复内容做定向复查。
// 人类可附加提示词（审查方向/疑问）；不限次数，每次的提示词与 AI 结论都落盘记录。
func (a *App) aiReviewTask(w http.ResponseWriter, r *http.Request) {
	id, tid := r.PathValue("id"), r.PathValue("tid")
	var req struct {
		Prompt string `json:"prompt"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	j, err := a.store.LoadJob(id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	tasks, err := a.store.LoadTasks(id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	var t *job.Task
	for i := range tasks {
		if tasks[i].ID == tid {
			t = &tasks[i]
			break
		}
	}
	if t == nil {
		writeErr(w, 404, "任务不存在")
		return
	}
	if t.Status != job.TaskPassed && t.Status != job.TaskApproved {
		writeErr(w, 409, "只有通过(passed)或已审批(approved)的任务可以发起 AI 审查")
		return
	}
	// 修复内容 = 该任务保存的 patch（commit 后由 runner 落盘）。
	patch, _ := a.store.LoadPatch(id, tid)
	if strings.TrimSpace(patch) == "" {
		writeErr(w, 409, "该任务无可用改动内容，无法审查")
		return
	}

	// 异步执行：AI 审查可能耗时较长，立即返回、后台跑完落盘，前端通过轮询感知结果。
	// 不用 r.Context()（请求已返回），改用 Background + 超时，超时/失败都会落一条带 error 的记录。
	rec := store.AiReview{
		ID:        fmt.Sprintf("r_%d", time.Now().UnixNano()),
		TaskID:    tid,
		Prompt:    req.Prompt,
		CreatedAt: time.Now(),
	}
	cmd, model := j.ClaudeCmd, j.ClaudeModel
	constraints, repoPath, prompt := a.store.ResolveConstraint(j.ConstraintTemplateID, j.Constraints), j.RepoPath, req.Prompt
	constraints = ai.RenderConstraints(constraints, ai.MergeValues(ai.FixerConstraintValues(j), j.ConstraintVars))
	taskCopy := *t

	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()

		client := ai.Client{
			Cmd:   cmd,
			Model: model,
			Log: func(line string) {
				a.publish("ai_log", id, tid, line, nil)
			},
			OnChild: func(pid int, start, cmds, phase string) {
				_ = a.store.SaveChild(store.Child{PID: pid, Start: start, Cmd: cmds, JobID: id, TaskID: tid, Phase: phase, StartedAt: time.Now()})
			},
			OnChildExit: func(pid int) {
				_ = a.store.DeleteChild(pid)
			},
		}

		review, err := client.AdhocReview(ctx, repoPath, taskCopy, patch, constraints, prompt, ai.NewPaths(patch))
		if err != nil {
			rec.Error = err.Error()
			rec.Review = job.Review{Verdict: "fail", Summary: "AI 审查调用失败：" + err.Error()}
		} else {
			rec.Review = review
		}
		if _, serr := a.store.AppendAiReview(id, rec); serr != nil {
			a.publish("ai_review_error", id, tid, "保存审查记录失败: "+serr.Error(), nil)
			return
		}
		a.publish("ai_review_done", id, tid, "AI 审查完成 ["+rec.Review.Verdict+"] "+rec.Review.Summary, &rec)
	}()

	a.publish("ai_review_started", id, tid, "人工发起 AI 审查中", nil)
	writeJSON(w, 200, map[string]interface{}{"started": true})
}

// listAiReviews 返回某任务的全部 AI 审查记录（人工发起，不限次数）。
func (a *App) listAiReviews(w http.ResponseWriter, r *http.Request) {
	id, tid := r.PathValue("id"), r.PathValue("tid")
	all, err := a.store.LoadAiReviews(id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	out := []store.AiReview{}
	for _, rec := range all {
		if rec.TaskID == tid {
			out = append(out, rec)
		}
	}
	writeJSON(w, 200, out)
}

// aiActivity 返回当前 job 下仍在运行的 AI 子进程状态，供前端跟踪「后台 AI 正在做什么」。
// 每条含阶段描述、所属任务（标题）、PID、启动时间与已运行时长；空数组表示当前没有在跑的 AI。
func (a *App) aiActivity(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	children, err := a.store.ListChildren()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	tasks, _ := a.store.LoadTasks(id)
	titleByID := map[string]string{}
	for _, t := range tasks {
		titleByID[t.ID] = t.Title
	}

	type item struct {
		PID            int       `json:"pid"`
		Phase          string    `json:"phase"`
		TaskID         string    `json:"task_id,omitempty"`
		TaskTitle      string    `json:"task_title,omitempty"`
		Cmd            string    `json:"cmd"`
		StartedAt      time.Time `json:"started_at,omitempty"`
		ElapsedSeconds int64     `json:"elapsed_seconds"`
	}

	out := []item{}
	for _, c := range children {
		if c.JobID != id {
			continue
		}
		elapsed := int64(0)
		if !c.StartedAt.IsZero() {
			elapsed = int64(time.Since(c.StartedAt).Seconds())
		}
		out = append(out, item{
			PID:            c.PID,
			Phase:          c.Phase,
			TaskID:         c.TaskID,
			TaskTitle:      titleByID[c.TaskID],
			Cmd:            c.Cmd,
			StartedAt:      c.StartedAt,
			ElapsedSeconds: elapsed,
		})
	}
	writeJSON(w, 200, out)
}

func (a *App) humanReview(w http.ResponseWriter, r *http.Request, status job.TaskStatus, verdict string) {
	id, tid := r.PathValue("id"), r.PathValue("tid")
	var req struct {
		Note string `json:"note"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	tasks, err := a.store.LoadTasks(id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	for i := range tasks {
		if tasks[i].ID == tid {
			tasks[i].Human = &job.HumanReview{Verdict: verdict, Note: req.Note}
			tasks[i].Status = status
			if err := a.store.SaveTasks(id, tasks); err != nil {
				writeErr(w, 500, err.Error())
				return
			}
			a.publish("task_human_"+verdict, id, tid, req.Note, &tasks[i])
			writeJSON(w, 200, &tasks[i])
			return
		}
	}
	writeErr(w, 404, "任务不存在")
}

// deferTask 手动跳过任务：修复循环会跳过它，且之后不再处理（不可重开）。
func (a *App) deferTask(w http.ResponseWriter, r *http.Request) {
	id, tid := r.PathValue("id"), r.PathValue("tid")
	tasks, err := a.store.LoadTasks(id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	for i := range tasks {
		if tasks[i].ID == tid {
			tasks[i].Status = job.TaskDeferred
			if err := a.store.SaveTasks(id, tasks); err != nil {
				writeErr(w, 500, err.Error())
				return
			}
			a.publish("task_deferred", id, tid, "已跳过，不再处理", &tasks[i])
			writeJSON(w, 200, &tasks[i])
			return
		}
	}
	writeErr(w, 404, "任务不存在")
}

// annotateTask 更新任务的人工备注/建议（独立存储，不影响任务状态与修复循环）。
func (a *App) annotateTask(w http.ResponseWriter, r *http.Request) {
	id, tid := r.PathValue("id"), r.PathValue("tid")
	var req struct {
		Note       string `json:"note"`
		Suggestion string `json:"suggestion"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "请求体解析失败: "+err.Error())
		return
	}
	tasks, err := a.store.LoadTasks(id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	found := false
	for _, t := range tasks {
		if t.ID == tid {
			found = true
			break
		}
	}
	if !found {
		writeErr(w, 404, "任务不存在")
		return
	}

	anns, err := a.store.LoadAnnotations(id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	idx := -1
	for i := range anns {
		if anns[i].TaskID == tid {
			idx = i
			break
		}
	}
	if idx == -1 {
		idx = len(anns)
		anns = append(anns, store.Annotation{TaskID: tid})
	}
	anns[idx].Note = strings.TrimSpace(req.Note)
	anns[idx].Suggestion = strings.TrimSpace(req.Suggestion)

	if anns[idx].Note == "" && anns[idx].Suggestion == "" {
		anns = append(anns[:idx], anns[idx+1:]...)
		if err := a.store.SaveAnnotations(id, anns); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		a.publish("task_annotated", id, tid, "已清空人工备注/建议", nil)
		writeJSON(w, 200, map[string]string{"status": "cleared"})
		return
	}

	if err := a.store.SaveAnnotations(id, anns); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	a.publish("task_annotated", id, tid, "已更新人工备注/建议", &anns[idx])
	writeJSON(w, 200, &anns[idx])
}

// annotationMap 加载某 job 的全部备注/建议，按 taskID 建索引。
func (a *App) annotationMap(jobID string) map[string]store.Annotation {
	anns, err := a.store.LoadAnnotations(jobID)
	if err != nil {
		return nil
	}
	m := make(map[string]store.Annotation, len(anns))
	for _, an := range anns {
		m[an.TaskID] = an
	}
	return m
}

// mergeAnnotations 把备注/建议合并进任务对象（用于 getJob/getTask 返回）。
func (a *App) mergeAnnotations(jobID string, tasks []job.Task) {
	m := a.annotationMap(jobID)
	if len(m) == 0 {
		return
	}
	for i := range tasks {
		if an, ok := m[tasks[i].ID]; ok {
			tasks[i].Note = an.Note
			tasks[i].Suggestion = an.Suggestion
		}
	}
}

// reviewComplete 标记/撤销「人类审查完成」：归档前必须显式标记审查完成，避免在人类审查过程中误触归档。
func (a *App) reviewComplete(w http.ResponseWriter, r *http.Request) {
	j, ok := a.loadJob(w, r)
	if !ok {
		return
	}
	var req struct {
		Done bool `json:"done"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	if req.Done {
		if j.Status != job.StatusAwaitingReview {
			writeErr(w, 409, fmt.Sprintf("当前状态 %s 无法标记审查完成", j.Status))
			return
		}
		j.Status = job.StatusReviewDone
	} else {
		if j.Status != job.StatusReviewDone {
			writeErr(w, 409, fmt.Sprintf("当前状态 %s 无法撤销审查完成", j.Status))
			return
		}
		j.Status = job.StatusAwaitingReview
	}
	j.UpdatedAt = time.Now()
	if err := a.store.SaveJob(j); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if req.Done {
		a.publish("review_completed", j.ID, "", "人类审查已完成，可开始归档", j.Status)
	} else {
		a.publish("review_reopened", j.ID, "", "已撤销审查完成，重新进入人类审查", j.Status)
	}
	writeJSON(w, 200, j)
}

// ---- 事件流 ----

func (a *App) events(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	fl, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, 500, "不支持流式响应")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	ch := a.bus.Subscribe(id)
	defer a.bus.Unsubscribe(id, ch)
	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-ch:
			b, _ := json.Marshal(ev)
			fmt.Fprintf(w, "data: %s\n\n", b)
			fl.Flush()
		}
	}
}

// jobHistory 返回该 job 的历史事件，供前端进页面时回放日志（实时 SSE 只推订阅之后的事件）。
func (a *App) jobHistory(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	evts, err := a.store.LoadEvents(id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, evts)
}

// ---- 设置 ----

func (a *App) getSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, a.settings)
}

func (a *App) putSettings(w http.ResponseWriter, r *http.Request) {
	var s store.Settings
	if err := json.NewDecoder(r.Body).Decode(&s); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if err := a.store.SaveSettings(s); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	a.settings = s
	writeJSON(w, 200, s)
}

// ---- AI 提示词模板 ----

// promptItem 一个 AI 任务的提示词模板（场景/能力 + 当前生效内容）。
type promptItem struct {
	Scene        string   `json:"scene"`
	Function     string   `json:"function"`
	Key          string   `json:"key"`
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Placeholders []string `json:"placeholders"`
	Content      string   `json:"content"` // 当前生效内容（内置默认；历史覆盖值仍会生效）
}

// promptItems 返回所有内置提示词模板 + 内置默认内容。
func (a *App) promptItems() ([]promptItem, error) {
	items := make([]promptItem, 0, len(ai.PromptDefs()))
	for _, d := range ai.PromptDefs() {
		items = append(items, promptItem{
			Scene:        d.Scene,
			Function:     d.Function,
			Key:          d.Key,
			Name:         ai.FunctionName(d.Function),
			Description:  d.Description,
			Placeholders: d.Placeholders,
			Content:      ai.DefaultPrompt(d.Key),
		})
	}
	return items, nil
}

func (a *App) listPrompts(w http.ResponseWriter, r *http.Request) {
	items, err := a.promptItems()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, items)
}

// listScenes 返回提示词场景命名配置（前缀 + 中文名）。
func (a *App) listScenes(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, ai.Scenes)
}

// ---- 约束模板 ----

func (a *App) listConstraintTemplates(w http.ResponseWriter, r *http.Request) {
	ts, err := a.store.LoadConstraintTemplates()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, ts)
}

// listConstraintInjectables 返回约束模板的场景列表 + 可注入占位符清单（供约束模板页「可注入字段」表展示）。
func (a *App) listConstraintInjectables(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]interface{}{
		"scenes": ai.ConstraintScenes,
		"fields": ai.InjectableFields(),
	})
}

// getConstraintTemplate 返回模板详情 + 正在使用它的未完结 job 列表（供「修改前提示」与「反向找 job」）。
func (a *App) getConstraintTemplate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ts, err := a.store.LoadConstraintTemplates()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	var tpl *store.ConstraintTemplate
	for i := range ts {
		if ts[i].ID == id {
			tpl = &ts[i]
			break
		}
	}
	if tpl == nil {
		writeErr(w, 404, "模板不存在")
		return
	}

	type usedBy struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Status string `json:"status"`
	}
	users := []usedBy{}
	if jobs, jerr := a.store.ListJobs(); jerr == nil {
		for _, j := range jobs {
			if j.ConstraintTemplateID == id && !j.Status.Finished() {
				users = append(users, usedBy{ID: j.ID, Name: j.Name, Status: string(j.Status)})
			}
		}
	}
	writeJSON(w, 200, map[string]interface{}{"template": tpl, "used_by": users})
}

// copyConstraintTemplate 复制模板出新模板：新 ID，名称加「 (副本)」后缀，内容照搬。
func (a *App) copyConstraintTemplate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ts, err := a.store.LoadConstraintTemplates()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	var src *store.ConstraintTemplate
	for i := range ts {
		if ts[i].ID == id {
			src = &ts[i]
			break
		}
	}
	if src == nil {
		writeErr(w, 404, "模板不存在")
		return
	}
	mods := make([]store.ConstraintModule, len(src.Modules))
	copy(mods, src.Modules)
	t := store.ConstraintTemplate{
		ID:        fmt.Sprintf("tpl_%s_%s", time.Now().Format("20060102-150405"), randSuffix()),
		Name:      src.Name + " (副本)",
		Scene:     src.Scene,
		Modules:   mods,
		Variables: src.Variables,
	}
	ts = append(ts, t)
	if err := a.store.SaveConstraintTemplates(ts); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 201, t)
}

// constraintTemplateReq 是创建/更新模板的请求体。新结构传 modules（分段），
// 兼容旧前端/旧调用传整段 content（自动包装成单模块）。
type constraintTemplateReq struct {
	Name      string                     `json:"name"`
	Scene     string                     `json:"scene"`
	Content   string                     `json:"content"`
	Modules   []store.ConstraintModule   `json:"modules"`
	Variables []store.ConstraintVariable `json:"variables"`
}

// normalizeScene 归一化约束模板场景：仅允许 common / fixer / migration，其余（含空）回落 common。
func normalizeScene(s string) string {
	switch strings.TrimSpace(s) {
	case ai.SceneFixer, ai.SceneMigration, ai.SceneCommon:
		return strings.TrimSpace(s)
	default:
		return ai.SceneCommon
	}
}

// normalizeVariables 归一化自定义注入变量：过滤空 key/空字段名，按 key 去重。
func normalizeVariables(vars []store.ConstraintVariable) []store.ConstraintVariable {
	out := make([]store.ConstraintVariable, 0, len(vars))
	seen := map[string]bool{}
	for _, v := range vars {
		v.Name = strings.TrimSpace(v.Name)
		v.Key = strings.TrimSpace(v.Key)
		v.Description = strings.TrimSpace(v.Description)
		if v.Key == "" || v.Name == "" || seen[v.Key] {
			continue
		}
		seen[v.Key] = true
		out = append(out, v)
	}
	return out
}

// normalizeModules 把请求体规整成有序模块列表：
// - 旧字段 content 非空且未传 modules 时包装成单模块（名「内容」）；
// - 过滤掉内容为空的模块（空白段无意义）；
// - 给空 ID 的模块补一个稳定 ID。
// 规整后无任何有效模块时返回错误。
func normalizeModules(req constraintTemplateReq) ([]store.ConstraintModule, error) {
	mods := req.Modules
	if len(mods) == 0 && strings.TrimSpace(req.Content) != "" {
		mods = []store.ConstraintModule{{ID: "m0", Name: "内容", Content: strings.TrimSpace(req.Content)}}
	}
	out := make([]store.ConstraintModule, 0, len(mods))
	for _, m := range mods {
		m.Name = strings.TrimSpace(m.Name)
		m.Content = strings.TrimSpace(m.Content)
		if m.Content == "" {
			continue
		}
		if m.ID == "" {
			m.ID = fmt.Sprintf("m%d", len(out))
		}
		out = append(out, m)
	}
	if len(out) == 0 {
		return nil, errors.New("模板内容不能为空")
	}
	return out, nil
}

func (a *App) createConstraintTemplate(w http.ResponseWriter, r *http.Request) {
	var req constraintTemplateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "请求体解析失败: "+err.Error())
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeErr(w, 400, "模板名称不能为空")
		return
	}
	mods, err := normalizeModules(req)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	ts, err := a.store.LoadConstraintTemplates()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	t := store.ConstraintTemplate{
		ID:        fmt.Sprintf("tpl_%s_%s", time.Now().Format("20060102-150405"), randSuffix()),
		Name:      req.Name,
		Scene:     normalizeScene(req.Scene),
		Modules:   mods,
		Variables: normalizeVariables(req.Variables),
	}
	ts = append(ts, t)
	if err := a.store.SaveConstraintTemplates(ts); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 201, t)
}

func (a *App) updateConstraintTemplate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req constraintTemplateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "请求体解析失败: "+err.Error())
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeErr(w, 400, "模板名称不能为空")
		return
	}
	mods, err := normalizeModules(req)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	ts, err := a.store.LoadConstraintTemplates()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	for i := range ts {
		if ts[i].ID == id {
			ts[i].Name = req.Name
			ts[i].Scene = normalizeScene(req.Scene)
			ts[i].Modules = mods
			ts[i].Variables = normalizeVariables(req.Variables)
			ts[i].Content = ""
			if err := a.store.SaveConstraintTemplates(ts); err != nil {
				writeErr(w, 500, err.Error())
				return
			}
			writeJSON(w, 200, ts[i])
			return
		}
	}
	writeErr(w, 404, "模板不存在")
}

func (a *App) deleteConstraintTemplate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ts, err := a.store.LoadConstraintTemplates()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	// 守卫：有未完结 job 在引用该模板则禁止删除，避免留下悬空引用（已完结 job 引用无所谓）。
	if jobs, jerr := a.store.ListJobs(); jerr == nil {
		for _, j := range jobs {
			if j.ConstraintTemplateID == id && !j.Status.Finished() {
				writeErr(w, 409, fmt.Sprintf("模板正在被未完结任务「%s」使用，不能删除", j.Name))
				return
			}
		}
	}
	out := make([]store.ConstraintTemplate, 0, len(ts))
	for _, t := range ts {
		if t.ID == id {
			continue
		}
		out = append(out, t)
	}
	if err := a.store.SaveConstraintTemplates(out); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"status": "deleted"})
}

// ---- 内部辅助 ----

func (a *App) loadJob(w http.ResponseWriter, r *http.Request) (*job.Job, bool) {
	j, err := a.store.LoadJob(r.PathValue("id"))
	if err != nil {
		writeErr(w, 404, "job 不存在: "+err.Error())
		return nil, false
	}
	return j, true
}

func (a *App) failJob(j *job.Job, err error) {
	j.Status = job.StatusFailed
	j.UpdatedAt = time.Now()
	_ = a.store.SaveJob(j)
	a.publish("job_failed", j.ID, "", err.Error(), j)
}

func (a *App) publish(typ, jobID, taskID, msg string, data interface{}) {
	ev := events.Event{Type: typ, JobID: jobID, TaskID: taskID, Message: msg, Data: data, TS: time.Now()}
	a.bus.Publish(jobID, ev)
	_ = a.store.AppendEvent(jobID, ev)
}

func reportText(rs job.ReportSource) (string, error) {
	if rs.Type == "file" {
		b, err := os.ReadFile(rs.Value)
		if err != nil {
			return "", fmt.Errorf("读取报告文件失败: %w", err)
		}
		return string(b), nil
	}
	return rs.Value, nil
}

func randSuffix() string {
	return fmt.Sprintf("%04x", time.Now().UnixNano()&0xffff)
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// spaHandler 托管内嵌前端，未知路径回退到 index.html（配合 vue-router history 模式）。
// 用 ServeFileFS 直接服务文件，避免 FileServer 对 /index.html 的 301 规范化重定向。
func spaHandler(sub fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" {
			p = "index.html"
		}
		if _, err := fs.Stat(sub, p); err != nil {
			p = "index.html" // SPA 回退：未知路由返回前端入口
		}
		http.ServeFileFS(w, r, sub, p)
	})
}
