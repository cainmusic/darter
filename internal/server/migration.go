package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"darter/internal/ai"
	"darter/internal/git"
	"darter/internal/job"
	"darter/internal/store"
)

// ---- 代码迁移（kind=migration）----

// migrationDetail 迁移任务详情：job + 源 commit 迁移条目 + 冲突记录 + 目标分支 commit 列表 + 基底 commit。
type migrationDetail struct {
	Job           *job.Job             `json:"job"`
	Commits       []job.CommitEntry    `json:"commits"`
	Conflicts     []job.ConflictRecord `json:"conflicts"`
	TargetCommits []git.CommitInfo     `json:"target_commits"`
	BaseCommit    *git.CommitInfo      `json:"base_commit,omitempty"`     // 目标基底的 HEAD，列表底部锚点
	SourceHasBase bool                 `json:"source_has_base,omitempty"` // 源分支是否也包含该基底
}

func (a *App) migrationDetail(j *job.Job) migrationDetail {
	commits, _ := a.store.LoadMigrations(j.ID)
	conflicts, _ := a.store.LoadConflicts(j.ID)
	var targetCommits []git.CommitInfo
	var baseCommit *git.CommitInfo
	sourceHasBase := false
	if j.Migration != nil {
		g := git.New(j.RepoPath)
		targetCommits, _ = g.ListCommitsRange(j.Migration.TargetBase, j.Migration.TargetBranch)
		if bc, err := g.ResolveCommit(j.Migration.TargetBase); err == nil {
			baseCommit = &bc
			if anc, err := g.AncestorSet(j.Migration.SourceBranch); err == nil {
				sourceHasBase = anc[bc.Hash]
			}
		}
	}
	return migrationDetail{Job: j, Commits: commits, Conflicts: conflicts, TargetCommits: targetCommits, BaseCommit: baseCommit, SourceHasBase: sourceHasBase}
}

// loadMigration 加载并校验为迁移任务。
func (a *App) loadMigration(w http.ResponseWriter, r *http.Request) (*job.Job, bool) {
	j, ok := a.loadJob(w, r)
	if !ok {
		return nil, false
	}
	if j.Kind != job.KindMigration {
		writeErr(w, 400, "该任务不是代码迁移任务")
		return nil, false
	}
	return j, true
}

// createMigration 创建迁移任务：校验目录/分支、切出目标分支、载入 commit 列表。
func (a *App) createMigration(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name                 string `json:"name"`
		RepoPath             string `json:"repo_path"`
		SourceBranch         string `json:"source_branch"`
		TargetBase           string `json:"target_base"`
		TargetBranch         string `json:"target_branch"`
		Constraints          string `json:"constraints"`
		ConstraintTemplateID string `json:"constraint_template_id"`
		MaxRetries           int    `json:"max_retries"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "请求体解析失败: "+err.Error())
		return
	}
	if req.RepoPath == "" {
		writeErr(w, 400, "repo_path 不能为空")
		return
	}
	if req.SourceBranch == "" || req.TargetBase == "" || req.TargetBranch == "" {
		writeErr(w, 400, "源分支、目标基底、目标分支都必填")
		return
	}
	if req.SourceBranch == req.TargetBranch || req.TargetBase == req.TargetBranch {
		writeErr(w, 400, "目标分支不能与源分支/目标基底重名")
		return
	}
	if _, err := os.Stat(filepath.Join(req.RepoPath, ".git")); err != nil {
		writeErr(w, 400, fmt.Sprintf("repo_path 不是有效的 git 仓库: %v", err))
		return
	}
	g := git.New(req.RepoPath)
	if !g.BranchExists(req.SourceBranch) {
		writeErr(w, 400, fmt.Sprintf("源分支 %s 不存在于仓库 %s", req.SourceBranch, req.RepoPath))
		return
	}
	if !g.BranchExists(req.TargetBase) {
		writeErr(w, 400, fmt.Sprintf("目标基底 %s 不存在于仓库 %s", req.TargetBase, req.RepoPath))
		return
	}
	if g.BranchExists(req.TargetBranch) {
		writeErr(w, 400, fmt.Sprintf("目标分支 %s 已存在，请换一个名字或先删除它", req.TargetBranch))
		return
	}

	maxRetries := req.MaxRetries
	if maxRetries <= 0 {
		maxRetries = 5
	}

	j := &job.Job{
		ID:                   fmt.Sprintf("mig_%s_%s", time.Now().Format("20060102-150405"), randSuffix()),
		Kind:                 job.KindMigration,
		Name:                 req.Name,
		RepoPath:             req.RepoPath,
		Status:               job.MigStatusReady,
		Migration:            &job.MigrationSpec{SourceBranch: req.SourceBranch, TargetBase: req.TargetBase, TargetBranch: req.TargetBranch, MaxRetries: maxRetries},
		Constraints:          req.Constraints,
		ConstraintTemplateID: req.ConstraintTemplateID,
		ClaudeCmd:            a.settings.ClaudeCmd,
		ClaudeModel:          a.settings.ClaudeModel,
		CreatedAt:            time.Now(),
		UpdatedAt:            time.Now(),
	}
	if j.Name == "" {
		j.Name = filepath.Base(req.RepoPath) + "-migrate"
	}

	// 切出目标分支（持锁，避免与其它 job 争用同一仓库）
	release, err := a.tryAcquireRepo(req.RepoPath, j.ID)
	if err != nil {
		writeErr(w, 409, err.Error())
		return
	}
	// 若源分支 / 目标基底跟踪远端分支，先更新到最新（失败则立刻报错）。
	for _, b := range []string{req.SourceBranch, req.TargetBase} {
		if err := g.FetchUpdateBranch(b); err != nil {
			release()
			writeErr(w, 500, fmt.Sprintf("更新分支 %s 到远端最新失败: %v", b, err))
			return
		}
	}
	if err := g.CreateFixBranch(req.TargetBase, req.TargetBranch); err != nil {
		release()
		writeErr(w, 500, "切出目标分支失败: "+err.Error())
		return
	}
	release()

	if err := a.store.SaveJob(j); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if err := a.reloadMigCommits(j); err != nil {
		writeErr(w, 500, "载入 commit 列表失败: "+err.Error())
		return
	}
	a.publish("migration_created", j.ID, "", "迁移任务已创建", j)
	writeJSON(w, 201, j)
}

func (a *App) listMigrations(w http.ResponseWriter, r *http.Request) {
	jobs, err := a.store.ListJobs()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	out := make([]*job.Job, 0, len(jobs))
	for _, j := range jobs {
		if j.Kind == job.KindMigration {
			out = append(out, j)
		}
	}
	writeJSON(w, 200, out)
}

func (a *App) getMigration(w http.ResponseWriter, r *http.Request) {
	j, ok := a.loadMigration(w, r)
	if !ok {
		return
	}
	writeJSON(w, 200, a.migrationDetail(j))
}

// reloadMigCommits 载入源分支 commit 列表：过滤无内容 commit，标记已迁移，落盘。
// 已迁移判断：仅用迁移时记录的 target_commit 做祖先判断（精确、不依赖 message，reset/amend 都能正确感知）。
// 无迁移记录的 commit（工具外手动迁移/源新增）默认待迁移，由用户用「改目标」手动标记。保留已有的 skipped / mark。
func (a *App) reloadMigCommits(j *job.Job) error {
	if j.Migration == nil {
		return fmt.Errorf("job %s 缺少迁移配置", j.ID)
	}
	g := git.New(j.RepoPath)
	src, err := g.ListCommitsRange(j.Migration.TargetBase, j.Migration.SourceBranch)
	if err != nil {
		return err
	}
	// 目标分支当前全部祖先（含 HEAD），用于祖先判断「迁移 commit 是否仍在目标分支历史里」。
	ancestors, _ := g.AncestorSet(j.Migration.TargetBranch)

	// 保留已有的 skipped / mark / target_commit（迁移时精确记录）
	existing, _ := a.store.LoadMigrations(j.ID)
	skipped := map[string]bool{}
	mark := map[string]string{}
	targetCommit := map[string]string{}
	skipReason := map[string]string{}
	for _, e := range existing {
		if e.State == job.CommitSkipped {
			skipped[e.Hash] = true
		}
		if e.Mark != "" {
			mark[e.Hash] = e.Mark
		}
		if e.TargetCommit != "" {
			targetCommit[e.Hash] = e.TargetCommit
		}
		if e.SkipReason != "" {
			skipReason[e.Hash] = e.SkipReason
		}
	}

	commits := make([]job.CommitEntry, 0, len(src))
	for _, sc := range src {
		empty, err := g.IsEmptyCommit(sc.Hash)
		if err != nil {
			empty = false
		}
		if empty {
			continue // 过滤无内容 commit（如空 merge）
		}
		e := job.CommitEntry{
			Hash:    sc.Hash,
			Subject: sc.Subject,
			Message: sc.Message,
			Author:  sc.Author,
			Date:    sc.Date,
			IsMerge: sc.IsMerge,
			State:   job.CommitPending,
		}
		if th, ok := targetCommit[sc.Hash]; ok {
			// 有迁移记录：祖先判断是否仍被目标分支包含
			if ancestors[th] {
				e.State = job.CommitMigrated
				e.TargetCommit = th
			}
			// 不是祖先 → 迁移被撤销（reset/amend），回退 pending
		}
		// 无迁移记录 → pending（工具外手动迁移/源新增的 commit，用「改目标」手动标记已迁移）
		if skipped[sc.Hash] {
			e.State = job.CommitSkipped
			e.TargetCommit = ""
			e.SkipReason = skipReason[sc.Hash]
		}
		if m, ok := mark[sc.Hash]; ok {
			e.Mark = m
		}
		commits = append(commits, e)
	}
	return a.store.SaveMigrations(j.ID, commits)
}

// setCommitMark 设置某个源 commit 的标记类型（"" / star / triangle / square / circle）。
func (a *App) setCommitMark(w http.ResponseWriter, r *http.Request) {
	j, ok := a.loadMigration(w, r)
	if !ok {
		return
	}
	hash := r.PathValue("hash")
	var req struct {
		Mark string `json:"mark"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "请求体解析失败: "+err.Error())
		return
	}
	commits, _ := a.store.LoadMigrations(j.ID)
	idx := commitIndex(commits, hash)
	if idx < 0 {
		writeErr(w, 404, "commit 不存在: "+hash)
		return
	}
	commits[idx].Mark = req.Mark
	if err := a.store.SaveMigrations(j.ID, commits); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, a.migrationDetail(j))
}

// setTargetCommit 手动设置某个源 commit 的迁移目标 hash（用于手动 amend/合并后纠正「源 → 目标」映射）。
// target_commit 为空表示清除映射、回退待迁移。
func (a *App) setTargetCommit(w http.ResponseWriter, r *http.Request) {
	j, ok := a.loadMigration(w, r)
	if !ok {
		return
	}
	if j.Migration == nil {
		writeErr(w, 500, "迁移配置缺失")
		return
	}
	hash := r.PathValue("hash")
	var req struct {
		TargetCommit string `json:"target_commit"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "请求体解析失败: "+err.Error())
		return
	}
	commits, _ := a.store.LoadMigrations(j.ID)
	idx := commitIndex(commits, hash)
	if idx < 0 {
		writeErr(w, 404, "commit 不存在: "+hash)
		return
	}

	if req.TargetCommit == "" {
		// 清除映射 → 待迁移
		commits[idx].TargetCommit = ""
		commits[idx].State = job.CommitPending
		commits[idx].SkipReason = ""
	} else {
		// 校验 target_commit 确实在目标分支历史里
		g := git.New(j.RepoPath)
		ancestors, err := g.AncestorSet(j.Migration.TargetBranch)
		if err != nil {
			writeErr(w, 500, "读取目标分支失败: "+err.Error())
			return
		}
		if !ancestors[req.TargetCommit] {
			writeErr(w, 400, fmt.Sprintf("目标 commit %s 不在目标分支 %s 上", req.TargetCommit, j.Migration.TargetBranch))
			return
		}
		commits[idx].TargetCommit = req.TargetCommit
		commits[idx].State = job.CommitMigrated
		commits[idx].LastError = ""
	}
	if err := a.store.SaveMigrations(j.ID, commits); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, a.migrationDetail(j))
}

// ignoreCommit 设置源 commit 的忽略状态：忽略（skipped，不可迁移、连续迁移跳过）/ 恢复（pending）。
func (a *App) ignoreCommit(w http.ResponseWriter, r *http.Request) {
	j, ok := a.loadMigration(w, r)
	if !ok {
		return
	}
	hash := r.PathValue("hash")
	var req struct {
		Ignored bool `json:"ignored"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "请求体解析失败: "+err.Error())
		return
	}
	commits, _ := a.store.LoadMigrations(j.ID)
	idx := commitIndex(commits, hash)
	if idx < 0 {
		writeErr(w, 404, "commit 不存在: "+hash)
		return
	}
	if commits[idx].State == job.CommitMigrated {
		writeErr(w, 400, "该 commit 已迁移，不能忽略")
		return
	}
	if req.Ignored {
		commits[idx].State = job.CommitSkipped
		commits[idx].LastError = ""
		commits[idx].SkipReason = "人工忽略"
	} else if commits[idx].State == job.CommitSkipped {
		commits[idx].State = job.CommitPending
		commits[idx].SkipReason = ""
	}
	if err := a.store.SaveMigrations(j.ID, commits); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, a.migrationDetail(j))
}

// ignoreRange 把 [start, end] 区间内所有「未迁移」的 commit 都标记为忽略（skipped）。
func (a *App) ignoreRange(w http.ResponseWriter, r *http.Request) {
	j, ok := a.loadMigration(w, r)
	if !ok {
		return
	}
	var req struct {
		Start string `json:"start"`
		End   string `json:"end"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "请求体解析失败: "+err.Error())
		return
	}
	commits, _ := a.store.LoadMigrations(j.ID)
	startIdx := commitIndex(commits, req.Start)
	endIdx := commitIndex(commits, req.End)
	if startIdx < 0 || endIdx < 0 {
		writeErr(w, 404, "起点或终点 commit 不存在")
		return
	}
	if startIdx > endIdx {
		writeErr(w, 400, "起点必须在终点之前")
		return
	}
	ignored := 0
	for i := startIdx; i <= endIdx; i++ {
		if commits[i].State != job.CommitMigrated {
			commits[i].State = job.CommitSkipped
			commits[i].LastError = ""
			commits[i].SkipReason = "人工忽略"
			ignored++
		}
	}
	if err := a.store.SaveMigrations(j.ID, commits); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	a.publish("migration_ignore_range", j.ID, "", fmt.Sprintf("已批量忽略 %d 个 commit（%s..%s）", ignored, req.Start[:8], req.End[:8]), nil)
	writeJSON(w, 200, a.migrationDetail(j))
}

// mergeCommits 合并目标分支上「从某 commit 到 HEAD」的连续 commit 为一个（squash），
// 并把被合并 commit 对应的源 commit 的 TargetCommit 更新为合并后的新 commit，
// 明确记录「合并后的 commit 包含哪些源 commit」，reset 回退时能正确处理。
func (a *App) mergeCommits(w http.ResponseWriter, r *http.Request) {
	j, ok := a.loadMigration(w, r)
	if !ok {
		return
	}
	if j.Migration == nil {
		writeErr(w, 500, "迁移配置缺失")
		return
	}
	if j.Status == job.MigStatusCompleted || j.Status == job.MigStatusCancelled || j.Status == job.MigStatusFailed {
		writeErr(w, 400, "任务已结束，不能合并")
		return
	}
	var req struct {
		Hashes []string `json:"hashes"` // 目标分支上要合并的连续 commit hash（旧→新，最后一个必须是 HEAD）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "请求体解析失败: "+err.Error())
		return
	}
	if len(req.Hashes) < 2 {
		writeErr(w, 400, "至少选择两个 commit 合并")
		return
	}

	release, err := a.tryAcquireRepo(j.RepoPath, j.ID)
	if err != nil {
		writeErr(w, 409, err.Error())
		return
	}
	defer release()

	g := git.New(j.RepoPath)
	if err := ensureOnTarget(g, j.Migration); err != nil {
		writeErr(w, 409, err.Error())
		return
	}
	if g.InCherryPick() {
		writeErr(w, 409, "仓库存在未完成的 cherry-pick，请先处理冲突")
		return
	}

	head, err := g.RevParse(j.Migration.TargetBranch)
	if err != nil {
		writeErr(w, 500, "读取目标分支 HEAD 失败: "+err.Error())
		return
	}
	if req.Hashes[len(req.Hashes)-1] != head {
		writeErr(w, 400, "只能合并到 HEAD 为止的连续 commit")
		return
	}
	// 验证连续：hashes[i] 必须是 hashes[i+1] 的 parent
	for i := 0; i < len(req.Hashes)-1; i++ {
		p, err := g.RevParse(req.Hashes[i+1] + "^1")
		if err != nil || p != req.Hashes[i] {
			writeErr(w, 400, "选中的 commit 不连续")
			return
		}
	}

	// 拼接 message
	var subs []string
	for _, h := range req.Hashes {
		ci, err := g.ResolveCommit(h)
		if err != nil {
			writeErr(w, 500, "读取 commit 信息失败: "+err.Error())
			return
		}
		subs = append(subs, ci.Subject)
	}
	message := strings.Join(subs, "\n\n")

	// squash：reset --soft 到第一个 commit 的 parent，再重新 commit
	parent, err := g.RevParse(req.Hashes[0] + "^1")
	if err != nil {
		writeErr(w, 500, "读取父 commit 失败: "+err.Error())
		return
	}
	if err := g.ResetSoftTo(parent); err != nil {
		writeErr(w, 500, "git reset --soft 失败: "+err.Error())
		return
	}
	newHash, err := g.Commit(message, "")
	if err != nil {
		writeErr(w, 500, "提交合并结果失败: "+err.Error())
		return
	}

	// 更新映射：被合并 commit 对应的源 commit 的 TargetCommit → newHash
	hashSet := map[string]bool{}
	for _, h := range req.Hashes {
		hashSet[h] = true
	}
	commits, _ := a.store.LoadMigrations(j.ID)
	for i := range commits {
		if hashSet[commits[i].TargetCommit] {
			commits[i].TargetCommit = newHash
			commits[i].State = job.CommitMigrated
		}
	}
	if err := a.store.SaveMigrations(j.ID, commits); err != nil {
		writeErr(w, 500, err.Error())
		return
	}

	a.publish("migration_merge", j.ID, "", fmt.Sprintf("已合并 %d 个 commit 为 %s", len(req.Hashes), newHash[:8]), nil)
	writeJSON(w, 200, a.migrationDetail(j))
}

// commitIndex 在 commits 里按 hash 找下标，找不到返回 -1。
func commitIndex(commits []job.CommitEntry, hash string) int {
	for i, c := range commits {
		if c.Hash == hash {
			return i
		}
	}
	return -1
}

// cherryPickOne 迁移单个 commit（调用前已持锁、已切到目标分支、已确认无未完成 cherry-pick）。
// 返回更新后的条目、是否冲突。
func (a *App) cherryPickOne(g *git.Service, j *job.Job, e job.CommitEntry) (job.CommitEntry, bool) {
	newHash, err := g.CherryPick(e.Hash)
	if err == nil {
		e.State = job.CommitMigrated
		e.TargetCommit = newHash
		e.LastError = ""
		e.SkipReason = ""
		a.publish("migration_commit", j.ID, e.Hash, "已迁移 "+e.Subject, e)
		return e, false
	}
	// cherry-pick 失败：区分「真冲突」和「空 commit」
	files, _ := g.ConflictFiles()
	if len(files) == 0 {
		// 无 unmerged 文件
		if g.InCherryPick() {
			// 空 commit（改动已在目标分支存在）：git 报 empty 但保留 CHERRY_PICK_HEAD，直接跳过
			_ = g.CherryPickAbort()
			_ = g.CleanUntracked()
			e.State = job.CommitSkipped
			e.LastError = ""
			e.SkipReason = "空 commit（改动已在目标分支存在）"
			a.publish("migration_commit_empty", j.ID, e.Hash, "空 commit（改动已在目标分支存在），已跳过", e)
			return e, false
		}
		// 其它错误：无 cherry-pick 态，标记失败
		e.State = job.CommitFailed
		e.LastError = err.Error()
		a.publish("migration_commit_error", j.ID, e.Hash, "迁移失败: "+err.Error(), e)
		return e, false
	}
	// 真冲突
	e.State = job.CommitConflict
	e.LastError = err.Error()
	rec := job.ConflictRecord{
		ID:         fmt.Sprintf("conf_%s_%s", time.Now().Format("150405"), randSuffix()),
		SourceHash: e.Hash,
		Files:      files,
		CreatedAt:  time.Now(),
	}
	cs, _ := a.store.LoadConflicts(j.ID)
	cs = append(cs, rec)
	_ = a.store.SaveConflicts(j.ID, cs)
	a.publish("migration_conflict", j.ID, e.Hash, "迁移 "+e.Subject+" 遇到冲突", map[string]interface{}{"files": files})
	return e, true
}

// activeConflictIndex 返回最后一个未解决冲突在列表里的下标，无则 -1。
func activeConflictIndex(cs []job.ConflictRecord) int {
	for i := len(cs) - 1; i >= 0; i-- {
		if cs[i].Resolution == "" {
			return i
		}
	}
	return -1
}

// ensureOnTarget 确保当前分支为目标分支（迁移/冲突处理前调用）。
func ensureOnTarget(g *git.Service, spec *job.MigrationSpec) error {
	cur, err := g.CurrentBranch()
	if err != nil {
		return fmt.Errorf("读取当前分支失败: %w", err)
	}
	if cur != spec.TargetBranch {
		return fmt.Errorf("当前分支 %q 不是目标分支 %q，请先切到目标分支", cur, spec.TargetBranch)
	}
	return nil
}

// migrateCommit 迁移单个 commit（同步：cherry-pick 快，无 AI）。
func (a *App) migrateCommit(w http.ResponseWriter, r *http.Request) {
	j, ok := a.loadMigration(w, r)
	if !ok {
		return
	}
	if j.Migration == nil {
		writeErr(w, 500, "迁移配置缺失")
		return
	}
	if j.Status == job.MigStatusCompleted || j.Status == job.MigStatusCancelled || j.Status == job.MigStatusFailed {
		writeErr(w, 400, "任务已结束，不能再迁移")
		return
	}
	hash := r.PathValue("hash")
	commits, _ := a.store.LoadMigrations(j.ID)
	idx := commitIndex(commits, hash)
	if idx < 0 {
		writeErr(w, 404, "commit 不存在: "+hash)
		return
	}
	if commits[idx].State == job.CommitMigrated {
		writeErr(w, 400, "该 commit 已迁移")
		return
	}

	release, err := a.tryAcquireRepo(j.RepoPath, j.ID)
	if err != nil {
		writeErr(w, 409, err.Error())
		return
	}
	defer release()

	g := git.New(j.RepoPath)
	if err := g.Checkout(j.Migration.TargetBranch); err != nil {
		writeErr(w, 500, "切到目标分支失败: "+err.Error())
		return
	}
	if g.InCherryPick() {
		writeErr(w, 409, "仓库存在未完成的 cherry-pick，请先处理冲突")
		return
	}

	updated, conflict := a.cherryPickOne(g, j, commits[idx])
	commits[idx] = updated
	if err := a.store.SaveMigrations(j.ID, commits); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if conflict {
		j.Status = job.MigStatusConflict
	} else {
		j.Status = job.MigStatusReady
	}
	j.UpdatedAt = time.Now()
	_ = a.store.SaveJob(j)
	writeJSON(w, 200, a.migrationDetail(j))
}

// migrateRange 连续迁移 [start, end] 区间（含两端），遇冲突即停，等待处理。
func (a *App) migrateRange(w http.ResponseWriter, r *http.Request) {
	j, ok := a.loadMigration(w, r)
	if !ok {
		return
	}
	if j.Migration == nil {
		writeErr(w, 500, "迁移配置缺失")
		return
	}
	if j.Status == job.MigStatusCompleted || j.Status == job.MigStatusCancelled || j.Status == job.MigStatusFailed {
		writeErr(w, 400, "任务已结束，不能再迁移")
		return
	}
	var req struct {
		Start string `json:"start"`
		End   string `json:"end"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "请求体解析失败: "+err.Error())
		return
	}
	commits, _ := a.store.LoadMigrations(j.ID)
	startIdx := commitIndex(commits, req.Start)
	endIdx := commitIndex(commits, req.End)
	if startIdx < 0 || endIdx < 0 {
		writeErr(w, 404, "起点或终点 commit 不存在")
		return
	}
	if startIdx > endIdx {
		writeErr(w, 400, "起点必须在终点之前（按源分支时间顺序）")
		return
	}

	release, err := a.tryAcquireRepo(j.RepoPath, j.ID)
	if err != nil {
		writeErr(w, 409, err.Error())
		return
	}
	defer release()

	g := git.New(j.RepoPath)
	if err := g.Checkout(j.Migration.TargetBranch); err != nil {
		writeErr(w, 500, "切到目标分支失败: "+err.Error())
		return
	}
	if g.InCherryPick() {
		writeErr(w, 409, "仓库存在未完成的 cherry-pick，请先处理冲突")
		return
	}

	// 记录本次选择
	j.Migration.StartCommit = req.Start
	j.Migration.EndCommit = req.End

	j.Status = job.MigStatusRunning
	_ = a.store.SaveJob(j)

	conflict := false
	for i := startIdx; i <= endIdx; i++ {
		c := commits[i]
		if c.State == job.CommitMigrated || c.State == job.CommitSkipped {
			continue
		}
		updated, cf := a.cherryPickOne(g, j, c)
		commits[i] = updated
		if cf {
			conflict = true
			break
		}
	}
	if err := a.store.SaveMigrations(j.ID, commits); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if conflict {
		j.Status = job.MigStatusConflict
	} else {
		j.Status = job.MigStatusReady
	}
	j.UpdatedAt = time.Now()
	_ = a.store.SaveJob(j)
	writeJSON(w, 200, a.migrationDetail(j))
}

// resolveConflict 人工处理冲突：continue（已手动解决并 git add）/ abort / skip。
func (a *App) resolveConflict(w http.ResponseWriter, r *http.Request) {
	j, ok := a.loadMigration(w, r)
	if !ok {
		return
	}
	if j.Migration == nil {
		writeErr(w, 500, "迁移配置缺失")
		return
	}
	var req struct {
		Action string `json:"action"` // continue | abort | skip
		Note   string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "请求体解析失败: "+err.Error())
		return
	}
	switch req.Action {
	case "continue", "abort", "skip":
	default:
		writeErr(w, 400, "action 必须为 continue / abort / skip")
		return
	}

	release, err := a.tryAcquireRepo(j.RepoPath, j.ID)
	if err != nil {
		writeErr(w, 409, err.Error())
		return
	}
	defer release()

	g := git.New(j.RepoPath)
	if err := ensureOnTarget(g, j.Migration); err != nil {
		writeErr(w, 409, err.Error())
		return
	}

	commits, _ := a.store.LoadMigrations(j.ID)
	idx := commitIndex(commits, conflictSourceHash(commits))
	cs, _ := a.store.LoadConflicts(j.ID)
	ci := activeConflictIndex(cs)

	// 无未完成 cherry-pick：可能已被外部手动 --continue，重载匹配。
	if !g.InCherryPick() {
		_ = a.reloadMigCommits(j)
		commits, _ = a.store.LoadMigrations(j.ID)
		idx = commitIndex(commits, conflictSourceHash(commits))
		if idx >= 0 && commits[idx].State == job.CommitMigrated {
			j.Status = job.MigStatusReady
			j.UpdatedAt = time.Now()
			_ = a.store.SaveJob(j)
			writeJSON(w, 200, a.migrationDetail(j))
			return
		}
		writeErr(w, 409, "当前没有未完成的 cherry-pick，且未找到已迁移的对应 commit")
		return
	}

	if idx < 0 {
		writeErr(w, 500, "找不到处于冲突态的 commit")
		return
	}

	var resolvedHash string // continue 得到的新 commit hash（abort/skip 时为空）
	switch req.Action {
	case "continue":
		if err := g.AddAll(); err != nil {
			writeErr(w, 500, "暂存解决结果失败: "+err.Error())
			return
		}
		resolvedHash, err = g.CherryPickContinue()
		if err != nil {
			writeErr(w, 500, "继续失败（可能仍有未解决冲突）: "+err.Error())
			return
		}
		commits[idx].State = job.CommitMigrated
		commits[idx].TargetCommit = resolvedHash
		commits[idx].LastError = ""
		commits[idx].SkipReason = ""
	case "abort":
		if err := g.CherryPickAbort(); err != nil {
			writeErr(w, 500, "中止 cherry-pick 失败: "+err.Error())
			return
		}
		if err := g.CleanUntracked(); err != nil {
			writeErr(w, 500, "清理未跟踪文件失败: "+err.Error())
			return
		}
		commits[idx].State = job.CommitPending
		commits[idx].LastError = ""
		commits[idx].SkipReason = ""
	case "skip":
		if err := g.CherryPickAbort(); err != nil {
			writeErr(w, 500, "中止 cherry-pick 失败: "+err.Error())
			return
		}
		if err := g.CleanUntracked(); err != nil {
			writeErr(w, 500, "清理未跟踪文件失败: "+err.Error())
			return
		}
		commits[idx].State = job.CommitSkipped
		commits[idx].LastError = ""
		commits[idx].SkipReason = req.Note
		if commits[idx].SkipReason == "" {
			commits[idx].SkipReason = "人工跳过"
		}
	}

	if ci >= 0 {
		cs[ci].Handler = "human"
		cs[ci].Resolution = req.Action
		cs[ci].Note = req.Note
		cs[ci].TargetCommit = resolvedHash
		cs[ci].ResolvedAt = time.Now()
		_ = a.store.SaveConflicts(j.ID, cs)
	}
	if err := a.store.SaveMigrations(j.ID, commits); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	j.Status = job.MigStatusReady
	j.UpdatedAt = time.Now()
	_ = a.store.SaveJob(j)
	writeJSON(w, 200, a.migrationDetail(j))
}

// conflictSourceHash 返回当前处于 conflict 态的源 commit hash（无则空串）。
func conflictSourceHash(commits []job.CommitEntry) string {
	for _, c := range commits {
		if c.State == job.CommitConflict {
			return c.Hash
		}
	}
	return ""
}

// aiResolveConflict 用 AI 解决当前冲突（异步：claude 调用慢）。
func (a *App) aiResolveConflict(w http.ResponseWriter, r *http.Request) {
	j, ok := a.loadMigration(w, r)
	if !ok {
		return
	}
	if j.Status != job.MigStatusConflict {
		writeErr(w, 400, "当前没有待解决的冲突")
		return
	}
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		a.runAiResolveConflict(j)
	}()
	writeJSON(w, 200, map[string]bool{"started": true})
}

// cancelAiResolveConflict 停止正在后台运行的 AI 冲突解决（取消 context 杀掉 claude 子进程）。
// 取消后由 runAiResolveConflict 感知并清理工作区（中止 cherry-pick、commit 回退 pending）。
func (a *App) cancelAiResolveConflict(w http.ResponseWriter, r *http.Request) {
	j, ok := a.loadMigration(w, r)
	if !ok {
		return
	}
	if j.Status != job.MigStatusConflict {
		writeErr(w, 400, "当前没有待解决的冲突")
		return
	}
	a.mu.Lock()
	cancel, exists := a.cancels[j.ID]
	a.mu.Unlock()
	if !exists {
		writeErr(w, 404, "没有正在运行的 AI 冲突解决")
		return
	}
	cancel()
	writeJSON(w, 200, map[string]bool{"stopped": true})
}

// resolveConflictWithAI 同步解决当前 cherry-pick 冲突（调用方已持锁、已 ensureOnTarget、已 InCherryPick）。
// 每轮：AI 解决冲突 → 审查解决结果（diff 相对 cherry-pick 前）→ 审查驳回则恢复冲突态重试。
// AI 可反馈「该 commit 应跳过」（空 commit / 仅有生成物），此时返回 skipped=true，由调用方 abort+跳过。
// 单次 AI 解决带 30 分钟超时，最多 3 轮。成功返回 continue 得到的新 hash 与 AI 说明；ctx 取消立即中止。
func (a *App) resolveConflictWithAI(ctx context.Context, g *git.Service, j *job.Job) (string, string, bool, error) {
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
	constraints := a.store.ResolveConstraint(j.ConstraintTemplateID, j.Constraints)

	recordAttempt := func(msg string) {
		cs, _ := a.store.LoadConflicts(j.ID)
		ci := activeConflictIndex(cs)
		if ci >= 0 {
			cs[ci].AiAttempts = append(cs[ci].AiAttempts, msg)
			_ = a.store.SaveConflicts(j.ID, cs)
		}
	}

	// 冲突文件列表（初始冲突态，后续轮次用 RestoreConflicts 恢复）
	files, err := g.ConflictFiles()
	if err != nil {
		return "", "", false, fmt.Errorf("获取冲突文件失败: %w", err)
	}
	if len(files) == 0 {
		if g.InCherryPick() {
			// 空 commit（改动已在目标分支存在），跳过
			return "", "cherry-pick 结果为空（改动已在目标分支存在），跳过", true, nil
		}
		return "", "", false, fmt.Errorf("没有冲突文件")
	}

	recordAttempt("AI 开始尝试解决冲突")

	maxAttempts := j.Migration.MaxRetries
	if maxAttempts <= 0 {
		maxAttempts = 5
	}
	var lastErr error
	var feedback string
	var summary string
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			// 上一轮被审查驳回：恢复冲突态，让 AI 基于原始冲突重新解决
			if err := g.RestoreConflicts(files); err != nil {
				lastErr = fmt.Errorf("恢复冲突态失败: %w", err)
				recordAttempt(lastErr.Error())
				break
			}
		}

		// 单次 AI 解决超时 30 分钟，避免单个冲突拖垮整个全自动流程
		actx, acancel := context.WithTimeout(ctx, 30*time.Minute)
		resolution, aerr := client.ResolveConflict(actx, j.RepoPath, files, constraints, feedback)
		acancel()
		if aerr != nil {
			if ctx.Err() == context.Canceled {
				return "", "", false, ctx.Err()
			}
			lastErr = aerr
			recordAttempt(fmt.Sprintf("AI 解决失败（第 %d/%d 次）: %s", attempt, maxAttempts, aerr))
			continue
		}

		// AI 判定该 commit 应跳过（空 commit / 仅有生成物）
		if resolution.Action == "skip" {
			return "", resolution.Summary, true, nil
		}

		summary = resolution.Summary

		// 审查解决结果：diff = 相对 cherry-pick 前的完整改动（被迁移提交 + AI 解决）
		diff, derr := g.DiffHead()
		if derr != nil {
			lastErr = fmt.Errorf("获取解决 diff 失败: %w", derr)
			recordAttempt(lastErr.Error())
			continue
		}
		review, rerr := client.ReviewConflict(ctx, j.RepoPath, files, diff, constraints)
		if rerr != nil {
			if ctx.Err() == context.Canceled {
				return "", "", false, ctx.Err()
			}
			lastErr = rerr
			feedback = "上一次审查调用失败：" + rerr.Error()
			recordAttempt(fmt.Sprintf("冲突解决审查失败（第 %d/%d 次）: %s", attempt, maxAttempts, rerr))
			continue
		}
		if review.Verdict != "pass" {
			lastErr = fmt.Errorf("冲突解决审查不通过: %s", review.Summary)
			fb, _ := json.Marshal(review.Findings)
			feedback = fmt.Sprintf("审查员判定不通过。\n审查结论：%s\n具体问题：%s", review.Summary, string(fb))
			recordAttempt(fmt.Sprintf("冲突解决审查不通过（第 %d/%d 次）: %s", attempt, maxAttempts, review.Summary))
			continue
		}

		// 审查通过：暂存并继续
		if err := g.AddAll(); err != nil {
			lastErr = fmt.Errorf("暂存解决结果失败: %w", err)
			recordAttempt(lastErr.Error())
			continue
		}
		newHash, cerr := g.CherryPickContinue()
		if cerr != nil {
			lastErr = fmt.Errorf("继续失败（可能仍有冲突标记残留）: %w", cerr)
			recordAttempt(lastErr.Error())
			continue
		}
		return newHash, summary, false, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("AI 解决冲突失败")
	}
	return "", "", false, lastErr
}

// runAiResolveConflict 后台执行 AI 冲突解决：读冲突文件→AI 解决→git add→--continue。
func (a *App) runAiResolveConflict(j *job.Job) {
	release, err := a.tryAcquireRepo(j.RepoPath, j.ID)
	if err != nil {
		a.publish("ai_conflict_error", j.ID, "", err.Error(), nil)
		return
	}
	defer release()

	g := git.New(j.RepoPath)
	if err := ensureOnTarget(g, j.Migration); err != nil {
		a.publish("ai_conflict_error", j.ID, "", err.Error(), nil)
		return
	}
	if !g.InCherryPick() {
		a.publish("ai_conflict_error", j.ID, "", "当前没有未完成的 cherry-pick", nil)
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// 注册取消入口，供「停止 AI」接口调用；退出时清理。
	a.mu.Lock()
	if old, exists := a.cancels[j.ID]; exists {
		old() // 防御：先取消旧的
	}
	a.cancels[j.ID] = cancel
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		delete(a.cancels, j.ID)
		a.mu.Unlock()
	}()

	newHash, summary, skipped, err := a.resolveConflictWithAI(ctx, g, j)
	if err != nil {
		if ctx.Err() == context.Canceled {
			// 用户手动停止：中止 cherry-pick 清理工作区，commit 回退 pending，任务回到就绪
			_ = g.CherryPickAbort()
			_ = g.CleanUntracked()
			commits, _ := a.store.LoadMigrations(j.ID)
			idx := commitIndex(commits, conflictSourceHash(commits))
			if idx >= 0 {
				commits[idx].State = job.CommitPending
				commits[idx].LastError = ""
			}
			_ = a.store.SaveMigrations(j.ID, commits)
			cs, _ := a.store.LoadConflicts(j.ID)
			ci := activeConflictIndex(cs)
			if ci >= 0 {
				cs[ci].Handler = "human"
				cs[ci].Resolution = "abort"
				cs[ci].Note = "用户手动停止 AI 解决"
				cs[ci].ResolvedAt = time.Now()
				_ = a.store.SaveConflicts(j.ID, cs)
			}
			j.Status = job.MigStatusReady
			j.UpdatedAt = time.Now()
			_ = a.store.SaveJob(j)
			a.publish("ai_conflict_cancelled", j.ID, "", "已停止 AI 解决并中止冲突", nil)
			return
		}
		a.publish("ai_conflict_error", j.ID, "", "AI 解决冲突失败: "+err.Error(), nil)
		return
	}

	if skipped {
		// AI 判定该 commit 应跳过：中止 cherry-pick，commit 标记为 skipped
		_ = g.CherryPickAbort()
		_ = g.CleanUntracked()
		commits, _ := a.store.LoadMigrations(j.ID)
		idx := commitIndex(commits, conflictSourceHash(commits))
		if idx >= 0 {
			commits[idx].State = job.CommitSkipped
			commits[idx].LastError = ""
			commits[idx].SkipReason = summary
		}
		_ = a.store.SaveMigrations(j.ID, commits)
		cs, _ := a.store.LoadConflicts(j.ID)
		ci := activeConflictIndex(cs)
		if ci >= 0 {
			cs[ci].Handler = "ai"
			cs[ci].Resolution = "skip"
			cs[ci].Note = summary
			cs[ci].ResolvedAt = time.Now()
			_ = a.store.SaveConflicts(j.ID, cs)
		}
		j.Status = job.MigStatusReady
		j.UpdatedAt = time.Now()
		_ = a.store.SaveJob(j)
		a.publish("migration_conflict_skipped", j.ID, "", "AI 判定该 commit 应跳过： "+summary, nil)
		return
	}

	// 标记该 commit 已迁移
	commits, _ := a.store.LoadMigrations(j.ID)
	idx := commitIndex(commits, conflictSourceHash(commits))
	if idx >= 0 {
		commits[idx].State = job.CommitMigrated
		commits[idx].TargetCommit = newHash
		commits[idx].LastError = ""
	}
	_ = a.store.SaveMigrations(j.ID, commits)

	// 记录 AI 解决成功
	cs, _ := a.store.LoadConflicts(j.ID)
	ci := activeConflictIndex(cs)
	if ci >= 0 {
		cs[ci].Handler = "ai"
		cs[ci].Resolution = "resolved"
		cs[ci].Note = summary
		cs[ci].TargetCommit = newHash
		cs[ci].ResolvedAt = time.Now()
		_ = a.store.SaveConflicts(j.ID, cs)
	}

	j.Status = job.MigStatusReady
	j.UpdatedAt = time.Now()
	_ = a.store.SaveJob(j)
	a.publish("migration_conflict_resolved", j.ID, "", "AI 已解决冲突并继续迁移", map[string]string{"commit": newHash})
}

// autoMigrate 发起全自动迁移（异步）：按列表依次迁移，遇冲突自动 AI 解决后继续，直到全部完成。
func (a *App) autoMigrate(w http.ResponseWriter, r *http.Request) {
	j, ok := a.loadMigration(w, r)
	if !ok {
		return
	}
	if j.Migration == nil {
		writeErr(w, 500, "迁移配置缺失")
		return
	}
	if j.Status == job.MigStatusCompleted || j.Status == job.MigStatusCancelled || j.Status == job.MigStatusFailed {
		writeErr(w, 400, "任务已结束，不能自动迁移")
		return
	}
	if j.Status == job.MigStatusAuto {
		writeErr(w, 409, "全自动迁移已在进行中")
		return
	}
	if j.Status == job.MigStatusConflict {
		writeErr(w, 409, "存在未处理的冲突，请先解决或中止")
		return
	}
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		a.runAutoMigrate(j)
		// 全自动迁移全部完成后，自动触发全量回归审查（此时 runAutoMigrate 已释放仓库锁）
		if j.Status == job.MigStatusCompleted {
			a.wg.Add(1)
			go func() {
				defer a.wg.Done()
				a.runMigrationRegression(j)
			}()
		}
	}()
	writeJSON(w, 200, map[string]bool{"started": true})
}

// cancelAutoMigrate 停止正在后台运行的全自动迁移（取消 context 杀掉 claude 子进程）。
// 取消后由 runAutoMigrate 感知并清理当前冲突（中止 cherry-pick、commit 回退 pending）。
func (a *App) cancelAutoMigrate(w http.ResponseWriter, r *http.Request) {
	j, ok := a.loadMigration(w, r)
	if !ok {
		return
	}
	if j.Status != job.MigStatusAuto {
		writeErr(w, 400, "当前没有进行中的全自动迁移")
		return
	}
	a.mu.Lock()
	cancel, exists := a.cancels[j.ID]
	a.mu.Unlock()
	if !exists {
		writeErr(w, 404, "没有正在运行的全自动迁移")
		return
	}
	cancel()
	writeJSON(w, 200, map[string]bool{"stopped": true})
}

// runAutoMigrate 后台执行全自动迁移：依次迁移待迁移 commit，遇冲突自动 AI 解决后继续。
// AI 重试耗尽（3 次仍失败）则停下、保留冲突现场（job → mig_conflict）等人工；被取消则回退到 mig_ready。
func (a *App) runAutoMigrate(j *job.Job) {
	release, err := a.tryAcquireRepo(j.RepoPath, j.ID)
	if err != nil {
		a.publish("ai_conflict_error", j.ID, "", err.Error(), nil)
		return
	}
	defer release()

	g := git.New(j.RepoPath)
	if err := g.Checkout(j.Migration.TargetBranch); err != nil {
		a.publish("ai_conflict_error", j.ID, "", "切到目标分支失败: "+err.Error(), nil)
		return
	}
	if g.InCherryPick() {
		a.publish("ai_conflict_error", j.ID, "", "仓库存在未完成的 cherry-pick，请先处理冲突", nil)
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.mu.Lock()
	if old, exists := a.cancels[j.ID]; exists {
		old()
	}
	a.cancels[j.ID] = cancel
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		delete(a.cancels, j.ID)
		a.mu.Unlock()
	}()

	j.Status = job.MigStatusAuto
	j.UpdatedAt = time.Now()
	_ = a.store.SaveJob(j)
	a.publish("migration_auto_started", j.ID, "", "全自动迁移开始", nil)

	commits, _ := a.store.LoadMigrations(j.ID)
	defer func() { _ = a.store.SaveMigrations(j.ID, commits) }() // 兜底：任何退出路径都落盘最终状态

	stopped := false
	for i := range commits {
		if ctx.Err() != nil {
			stopped = true
			break
		}
		c := commits[i]
		if c.State != job.CommitPending && c.State != job.CommitFailed {
			continue
		}

		updated, conflict := a.cherryPickOne(g, j, c)
		commits[i] = updated
		if conflict {
			newHash, summary, skipped, aerr := a.resolveConflictWithAI(ctx, g, j)
			if aerr != nil {
				if ctx.Err() == context.Canceled {
					// 被停止：中止冲突、回退 pending
					_ = g.CherryPickAbort()
					_ = g.CleanUntracked()
					commits[i].State = job.CommitPending
					commits[i].LastError = ""
					cs, _ := a.store.LoadConflicts(j.ID)
					ci := activeConflictIndex(cs)
					if ci >= 0 {
						cs[ci].Handler = "human"
						cs[ci].Resolution = "abort"
						cs[ci].Note = "全自动迁移被停止"
						cs[ci].ResolvedAt = time.Now()
						_ = a.store.SaveConflicts(j.ID, cs)
					}
					stopped = true
					break
				}
				// AI 重试耗尽：停下，保留冲突现场等人工
				commits[i].State = job.CommitConflict
				commits[i].LastError = aerr.Error()
				stopped = true
				break
			}
			if skipped {
				// AI 判定该 commit 应跳过：中止冲突，标记 skipped，继续下一个
				_ = g.CherryPickAbort()
				_ = g.CleanUntracked()
				commits[i].State = job.CommitSkipped
				commits[i].LastError = ""
				commits[i].SkipReason = summary
				cs, _ := a.store.LoadConflicts(j.ID)
				ci := activeConflictIndex(cs)
				if ci >= 0 {
					cs[ci].Handler = "ai"
					cs[ci].Resolution = "skip"
					cs[ci].Note = summary
					cs[ci].ResolvedAt = time.Now()
					_ = a.store.SaveConflicts(j.ID, cs)
				}
				a.publish("migration_conflict_skipped", j.ID, c.Hash, "AI 判定该 commit 应跳过： "+summary, nil)
			} else {
				// AI 解决成功：标记 migrated + 记录 resolved
				commits[i].State = job.CommitMigrated
				commits[i].TargetCommit = newHash
				commits[i].LastError = ""
				cs, _ := a.store.LoadConflicts(j.ID)
				ci := activeConflictIndex(cs)
				if ci >= 0 {
					cs[ci].Handler = "ai"
					cs[ci].Resolution = "resolved"
					cs[ci].Note = summary
					cs[ci].TargetCommit = newHash
					cs[ci].ResolvedAt = time.Now()
					_ = a.store.SaveConflicts(j.ID, cs)
				}
				a.publish("migration_conflict_resolved", j.ID, c.Hash, "AI 已解决冲突并继续迁移", map[string]string{"commit": newHash})
			}
		}

		// 每个 commit 处理后落盘，切走再切回能看到实时进度
		_ = a.store.SaveMigrations(j.ID, commits)
	}

	var finalStatus job.Status
	switch {
	case stopped && ctx.Err() == context.Canceled:
		finalStatus = job.MigStatusReady
	case stopped:
		finalStatus = job.MigStatusConflict // AI 重试耗尽，停下等人工
	default:
		finalStatus = job.MigStatusCompleted // 全部完成
	}
	j.Status = finalStatus
	j.UpdatedAt = time.Now()
	_ = a.store.SaveJob(j)
	a.publish("migration_auto_finished", j.ID, "", "全自动迁移结束", map[string]string{"status": string(finalStatus)})
}

// regressionMigration 手动触发全量回归审查（异步）：审查迁移目标分支累积改动，发现问题走 3 轮「修复+审查」。
func (a *App) regressionMigration(w http.ResponseWriter, r *http.Request) {
	j, ok := a.loadMigration(w, r)
	if !ok {
		return
	}
	if j.Migration == nil {
		writeErr(w, 500, "迁移配置缺失")
		return
	}
	if j.Status == job.MigStatusConflict || j.Status == job.MigStatusAuto || j.Status == job.MigStatusRunning {
		writeErr(w, 409, "当前有迁移/冲突处理进行中，请先完成或停止")
		return
	}
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		a.runMigrationRegression(j)
	}()
	writeJSON(w, 200, map[string]bool{"started": true})
}

// findingsText 序列化问题清单，供 feedback 传给 AI。
func findingsText(fs []job.Finding) string {
	b, _ := json.Marshal(fs)
	return string(b)
}

// runMigrationRegression 执行全量回归审查：审查迁移目标分支累积改动，只挑明显问题；
// 发现问题则走最多 3 轮「AI 修复 → amend 到 HEAD → 审查修复」。
func (a *App) runMigrationRegression(j *job.Job) {
	release, err := a.tryAcquireRepo(j.RepoPath, j.ID)
	if err != nil {
		a.publish("migration_regression_error", j.ID, "", err.Error(), nil)
		return
	}
	defer release()

	g := git.New(j.RepoPath)
	if err := g.Checkout(j.Migration.TargetBranch); err != nil {
		a.publish("migration_regression_error", j.ID, "", "切到目标分支失败: "+err.Error(), nil)
		return
	}
	if g.InCherryPick() {
		a.publish("migration_regression_error", j.ID, "", "仓库存在未完成的 cherry-pick，请先处理冲突", nil)
		return
	}

	diff, err := g.DiffRange(j.Migration.TargetBase, j.Migration.TargetBranch)
	if err != nil {
		a.publish("migration_regression_error", j.ID, "", "获取累积 diff 失败: "+err.Error(), nil)
		return
	}
	logText, _ := g.LogRange(j.Migration.TargetBase, j.Migration.TargetBranch)

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
	constraints := a.store.ResolveConstraint(j.ConstraintTemplateID, j.Constraints)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.mu.Lock()
	if old, exists := a.cancels[j.ID]; exists {
		old()
	}
	a.cancels[j.ID] = cancel
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		delete(a.cancels, j.ID)
		a.mu.Unlock()
	}()

	a.publish("migration_regression_started", j.ID, "", "全量回归审查开始", nil)

	// 第一步：全量回归审查
	review, err := client.ReviewMigration(ctx, j.RepoPath, diff, logText, constraints)
	if err != nil {
		if ctx.Err() == context.Canceled {
			return
		}
		a.publish("migration_regression_error", j.ID, "", "全量回归审查失败: "+err.Error(), nil)
		return
	}
	j.Regression = &review
	if review.Verdict == "pass" {
		j.UpdatedAt = time.Now()
		_ = a.store.SaveJob(j)
		a.publish("migration_regression_pass", j.ID, "", "全量回归审查通过", nil)
		return
	}

	// 有问题：走「修复 → 提交独立 commit → 审查」循环，重试次数用迁移任务的 max_retries（默认 5）。
	// 每轮修复单独提交 commit（不 amend 到迁移 commit，来源可能不同，便于后续评估合并）。
	feedback := findingsText(review.Findings)
	maxAttempts := j.Migration.MaxRetries
	if maxAttempts <= 0 {
		maxAttempts = 5
	}
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if ctx.Err() == context.Canceled {
			return
		}
		actx, acancel := context.WithTimeout(ctx, 20*time.Minute)
		title, _, ferr := client.FixMigrationRev(actx, j.RepoPath, feedback, constraints)
		acancel()
		if ferr != nil {
			feedback = "上一次修复调用失败：" + ferr.Error()
			continue
		}
		if err := g.AddAll(); err != nil {
			feedback = "暂存修复结果失败：" + err.Error()
			continue
		}
		fixHash, err := g.Commit("fix: "+title, "")
		if err != nil {
			feedback = "提交修复失败：" + err.Error()
			continue
		}

		fixDiff, _ := g.Show(fixHash)
		fixReview, rerr := client.ReviewMigrationFix(ctx, j.RepoPath, fixDiff, constraints)
		if rerr != nil {
			if ctx.Err() == context.Canceled {
				return
			}
			feedback = "上一次审查调用失败：" + rerr.Error()
			continue
		}
		j.Regression = &fixReview
		if fixReview.Verdict == "pass" {
			j.UpdatedAt = time.Now()
			_ = a.store.SaveJob(j)
			a.publish("migration_regression_pass", j.ID, "", "全量回归审查问题已修复", nil)
			return
		}
		feedback = "审查员判定不通过。\n审查结论：" + fixReview.Summary + "\n具体问题：" + findingsText(fixReview.Findings)
	}

	j.UpdatedAt = time.Now()
	_ = a.store.SaveJob(j)
	a.publish("migration_regression_fail", j.ID, "", "全量回归审查问题未全部修复，请人工处理", nil)
}

// resetTarget 把目标分支 reset --hard 到指定 ref（只允许回退到目标分支自身祖先）。
func (a *App) resetTarget(w http.ResponseWriter, r *http.Request) {
	j, ok := a.loadMigration(w, r)
	if !ok {
		return
	}
	if j.Migration == nil {
		writeErr(w, 500, "迁移配置缺失")
		return
	}
	if j.Status == job.MigStatusCompleted || j.Status == job.MigStatusCancelled {
		writeErr(w, 400, "任务已结束，不能重置")
		return
	}
	var req struct {
		Ref string `json:"ref"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "请求体解析失败: "+err.Error())
		return
	}
	if req.Ref == "" {
		writeErr(w, 400, "ref 不能为空")
		return
	}

	release, err := a.tryAcquireRepo(j.RepoPath, j.ID)
	if err != nil {
		writeErr(w, 409, err.Error())
		return
	}
	defer release()

	g := git.New(j.RepoPath)
	if err := ensureOnTarget(g, j.Migration); err != nil {
		writeErr(w, 409, err.Error())
		return
	}
	// 护栏：只允许回退到目标分支自身的祖先
	if !g.IsAncestor(req.Ref, j.Migration.TargetBranch) {
		writeErr(w, 400, fmt.Sprintf("ref %s 不是目标分支 %s 的祖先，拒绝重置", req.Ref, j.Migration.TargetBranch))
		return
	}
	if err := g.ResetHardTo(req.Ref); err != nil {
		writeErr(w, 500, "git reset 失败: "+err.Error())
		return
	}
	if err := g.CleanUntracked(); err != nil {
		writeErr(w, 500, "清理未跟踪文件失败: "+err.Error())
		return
	}
	// reset 后重载：作废比该点更新的迁移映射
	if err := a.reloadMigCommits(j); err != nil {
		writeErr(w, 500, "重载 commit 列表失败: "+err.Error())
		return
	}
	j.Status = job.MigStatusReady
	j.UpdatedAt = time.Now()
	_ = a.store.SaveJob(j)
	a.publish("migration_reset", j.ID, "", "目标分支已重置到 "+req.Ref, nil)
	writeJSON(w, 200, a.migrationDetail(j))
}

// completeMigration 手动完成迁移任务（不可逆，前端二次确认后调用）。
func (a *App) completeMigration(w http.ResponseWriter, r *http.Request) {
	j, ok := a.loadMigration(w, r)
	if !ok {
		return
	}
	if j.Status == job.MigStatusCompleted {
		writeErr(w, 400, "任务已完成")
		return
	}
	if j.Status == job.MigStatusConflict {
		writeErr(w, 400, "存在未处理的冲突，请先解决或中止后再完成")
		return
	}
	j.Status = job.MigStatusCompleted
	j.UpdatedAt = time.Now()
	_ = a.store.SaveJob(j)
	a.publish("migration_completed", j.ID, "", "迁移任务已完成", nil)
	writeJSON(w, 200, a.migrationDetail(j))
}
