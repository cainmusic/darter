package git

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

type Service struct {
	dir string
}

func New(dir string) *Service { return &Service{dir: dir} }

func (s *Service) run(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = s.dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func (s *Service) CurrentBranch() (string, error) {
	return s.run("rev-parse", "--abbrev-ref", "HEAD")
}

func (s *Service) Checkout(branch string) error {
	_, err := s.run("checkout", branch)
	return err
}

// ResetHard 丢弃工作区与暂存区的未提交改动（回退到 HEAD），用于清理中断的修复残留。
func (s *Service) ResetHard() error {
	_, err := s.run("reset", "--hard")
	return err
}

// darterIgnoreDir darter/claude 自身的工作目录（review 报告输入、claude 本地设置等）。
// 它不是「会卷进修复分支的用户源码改动」：EnsureClean 不应因它阻塞修复流程，
// CleanUntracked 不应删除它（否则会丢失报告输入数据），AddAll 也不应把它扫进 commit。
const darterIgnoreDir = ".claude"

// filterIgnorableUntracked 从 git status --porcelain 输出里移除 darter 自身工作目录产生的
// untracked 行（?? .claude/ 或 ?? .claude）。只过滤 untracked（??），已跟踪文件的改动仍然会保留并阻塞。
func filterIgnorableUntracked(status string) string {
	dirPrefix := "?? " + darterIgnoreDir + "/"
	exact := "?? " + darterIgnoreDir
	lines := strings.Split(status, "\n")
	kept := make([]string, 0, len(lines))
	for _, ln := range lines {
		if strings.HasPrefix(ln, dirPrefix) || ln == exact {
			continue
		}
		kept = append(kept, ln)
	}
	return strings.Join(kept, "\n")
}

// CleanUntracked 删除工作区里未跟踪、且未被 .gitignore 忽略的文件与目录。
// 与 git add -A 的暂存范围一致：每次尝试前清掉上一轮 AI/build 引入的 untracked 产物，
// 避免它们残留到下一轮、又被 git add -A 扫进 commit。不会触碰任何已跟踪文件，
// 也排除 darter 自身工作目录（.claude/），避免误删报告输入。
func (s *Service) CleanUntracked() error {
	_, err := s.run("clean", "-fd", "-e", darterIgnoreDir+"/")
	return err
}

func (s *Service) Status() (string, error) {
	return s.run("status", "--porcelain")
}

// EnsureClean 校验工作区无未提交改动，避免把用户改动卷进修复分支。
// darter 自身工作目录（.claude/）不计入脏改动。
func (s *Service) EnsureClean() error {
	st, err := s.Status()
	if err != nil {
		return err
	}
	st = filterIgnorableUntracked(st)
	if st != "" {
		return fmt.Errorf("repo %s 有未提交改动，请先处理:\n%s", s.dir, st)
	}
	return nil
}

// BranchExists 判断本地分支是否存在（仅本地分支，不含 remote-tracking 分支）。
func (s *Service) BranchExists(name string) bool {
	_, err := s.run("rev-parse", "--verify", "--quiet", "refs/heads/"+name)
	return err == nil
}

// CreateFixBranch 从 base 切出新的修复分支。base 不存在时给出明确错误，避免 checkout 报晦涩的 pathspec 错误。
func (s *Service) CreateFixBranch(base, fix string) error {
	if !s.BranchExists(base) {
		return fmt.Errorf("基础分支 %s 不存在（可用 git branch -a 查看实际分支）", base)
	}
	if _, err := s.run("checkout", base); err != nil {
		return err
	}
	if _, err := s.run("checkout", "-b", fix); err != nil {
		return err
	}
	return nil
}

// DeleteBranch 强制删除本地分支（即使未合并）。不可删除当前所在分支，调用方需先切走。
func (s *Service) DeleteBranch(name string) error {
	_, err := s.run("branch", "-D", name)
	return err
}

// RebuildFixBranch 全任务重做：抛弃旧修复分支，基于 base 重建一个全新的修复分支。
// 调用前需保证当前停在旧 fix 分支上（由调用方校验），且不保留旧 fix 分支的任何提交。
// 步骤：丢弃工作区改动 → 切回 base → 清理未跟踪残留 → 删除旧 fix 分支 → 从 base 切出新 fix 分支。
func (s *Service) RebuildFixBranch(base, oldFix, newFix string) error {
	if err := s.ResetHard(); err != nil {
		return fmt.Errorf("丢弃工作区改动失败: %w", err)
	}
	if err := s.Checkout(base); err != nil {
		return fmt.Errorf("切回基础分支 %s 失败: %w", base, err)
	}
	if err := s.CleanUntracked(); err != nil {
		return fmt.Errorf("清理未跟踪文件失败: %w", err)
	}
	if oldFix != "" && s.BranchExists(oldFix) {
		if err := s.DeleteBranch(oldFix); err != nil {
			return fmt.Errorf("删除旧修复分支 %s 失败: %w", oldFix, err)
		}
	}
	if _, err := s.run("checkout", "-b", newFix); err != nil {
		return fmt.Errorf("重建修复分支 %s 失败: %w", newFix, err)
	}
	return nil
}

// EnsureOnFixBranch 提交前的安全断言：绝不在基础分支上提交。
func (s *Service) EnsureOnFixBranch(fix string) error {
	cur, err := s.CurrentBranch()
	if err != nil {
		return err
	}
	if cur != fix {
		return fmt.Errorf("安全中止：当前分支 %q 不是修复分支 %q", cur, fix)
	}
	return nil
}

func (s *Service) Diff() (string, error) {
	return s.run("diff")
}

// DiffHead 返回工作区+暂存区相对 HEAD 的全部改动（含已暂存与未暂存）。
// 用于归档迁移：git apply --3way 会把结果暂存，普通 Diff()（仅未暂存）会漏掉。
func (s *Service) DiffHead() (string, error) {
	return s.run("diff", "HEAD")
}

// DiffCached 返回暂存区相对 HEAD 的完整改动（= 即将提交的内容，含 git add 进来的 untracked 新文件）。
// 是「审查/自检/提交检查」应当基于的口径：与提交前 git add -A 一致，
// 否则 untracked 文件对 Diff()/DiffHead() 不可见，会绕过审查直接被提交。
func (s *Service) DiffCached() (string, error) {
	return s.run("diff", "--cached")
}

// DiffRange 返回 base 与 head 两个 ref 之间的累积 diff（如 base..fix 分支整体改动）。
func (s *Service) DiffRange(base, head string) (string, error) {
	return s.run("diff", base+".."+head)
}

// ApplyPatch 用 git apply --3way 把一个 patch 应用到当前工作区（未暂存）。
// 三路合并能容忍多任务改同一文件导致的上下文偏移；失败时返回错误，工作区可能残留冲突态，由调用方 ResetHard 清理。
func (s *Service) ApplyPatch(patch string) error {
	cmd := exec.Command("git", "apply", "--3way")
	cmd.Dir = s.dir
	cmd.Stdin = strings.NewReader(patch)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git apply --3way: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// CherryPickNoCommit 用 git cherry-pick -n 把某提交的改动应用到工作区+暂存区，但不自动提交。
// 相比 apply patch，三路合并以提交对象的 parent 为 base，多任务改同一文件时还原度更高，能忠实还原已批准提交的内容。
// 失败（真实冲突）时返回错误，工作区会残留 CHERRY_PICK_HEAD 状态，需 CherryPickAbort 清理。
func (s *Service) CherryPickNoCommit(hash string) error {
	if _, err := s.run("cherry-pick", "-n", hash); err != nil {
		return fmt.Errorf("git cherry-pick -n %s: %w", hash, err)
	}
	return nil
}

// CherryPickAbort 取消一个未完成的 cherry-pick，清理 CHERRY_PICK_HEAD 等残留状态。
// 注意：ResetHard 只回退工作区/暂存区，清不掉 cherry-pick 的 sequencer 状态，冲突后必须用它清理。
func (s *Service) CherryPickAbort() error {
	_, err := s.run("cherry-pick", "--abort")
	return err
}

// LogRange 返回 base..head 之间的提交列表（含哈希/标题/正文），供最终归档回归审查核对 commit↔任务对应关系。
func (s *Service) LogRange(base, head string) (string, error) {
	return s.run("log", "--format=commit %H%nsubject: %s%nbody:%n%b", base+".."+head)
}

// AddAll 暂存全部改动（含 untracked 新文件），但排除 darter 自身工作目录（.claude/），
// 避免把 review 报告输入与 claude 本地设置误扫进修复/归档 commit。
// 注意：目标仓库若已把 .claude 写进 .gitignore，":!.claude/" 排除 pathspec 会触发 git 的
// "ignored by .gitignore" 报错并返回非零。故先探测 .claude 是否已被忽略：
// 已忽略则直接 git add -A（gitignore 已兜底排除），否则用排除 pathspec 显式排除。
func (s *Service) AddAll() error {
	if s.isIgnored(darterIgnoreDir + "/") {
		_, err := s.run("add", "-A")
		return err
	}
	_, err := s.run("add", "-A", "--", ":!"+darterIgnoreDir+"/")
	return err
}

// isIgnored 判断某路径是否被 .gitignore 等忽略规则命中（git check-ignore 退出码 0 表示已忽略）。
func (s *Service) isIgnored(path string) bool {
	_, err := s.run("check-ignore", "-q", path)
	return err == nil
}

// Commit 提交当前改动。subject 为标题行，body 为正文（如 Task-Id 等 trailer，可为空）。
func (s *Service) Commit(subject, body string) (string, error) {
	args := []string{"commit", "-m", subject}
	if body != "" {
		args = append(args, "-m", body)
	}
	if _, err := s.run(args...); err != nil {
		return "", err
	}
	return s.run("rev-parse", "HEAD")
}

// AmendNoEdit 把暂存区改动合入 HEAD 提交（保持提交信息不变），不新增 commit。
// 用于归档分支的最终修复：把 AI 修复增量合入最后一个归档提交，避免破坏「一任务一 commit」的对应关系。
func (s *Service) AmendNoEdit() error {
	_, err := s.run("commit", "--amend", "--no-edit")
	return err
}

// Show 返回某提交的 diff。
func (s *Service) Show(hash string) (string, error) {
	return s.run("show", "--format=", hash)
}

// HeadCommit 返回 HEAD 提交的哈希与完整提交信息（含正文）；分支无提交时返回空串。
// 用 NUL 分隔哈希与正文，避免正文首字符与分隔符冲突。
func (s *Service) HeadCommit() (hash, message string, err error) {
	out, err := s.run("log", "-1", "--format=%H%x00%B")
	if err != nil {
		return "", "", err
	}
	if out == "" {
		return "", "", nil
	}
	parts := strings.SplitN(out, "\x00", 2)
	hash = parts[0]
	if len(parts) == 2 {
		message = strings.TrimSpace(parts[1])
	}
	return hash, message, nil
}

// Grep 在仓库已跟踪文件里按字面量搜索，返回匹配行（格式 "file:lineno:content"）。
// 无匹配时返回空切片（不视为错误）。
func (s *Service) Grep(pattern string) ([]string, error) {
	cmd := exec.Command("git", "grep", "-n", "-I", "-e", pattern)
	cmd.Dir = s.dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
			return nil, nil // 无匹配
		}
		return nil, fmt.Errorf("git grep %s: %w: %s", pattern, err, strings.TrimSpace(string(out)))
	}
	var res []string
	for _, l := range strings.Split(string(out), "\n") {
		l = strings.TrimSpace(l)
		if l != "" {
			res = append(res, l)
		}
	}
	return res, nil
}

// CherryPick 迁移单个提交到当前分支：保留原提交信息（不加 -x，不改 message）。
// 成功返回新提交 hash；冲突时返回错误，工作区残留 CHERRY_PICK_HEAD 状态，等待人工/AI 处理后 CherryPickContinue。
func (s *Service) CherryPick(hash string) (string, error) {
	if _, err := s.run("cherry-pick", hash); err != nil {
		return "", fmt.Errorf("git cherry-pick %s: %w", hash, err)
	}
	return s.run("rev-parse", "HEAD")
}

// CherryPickContinue 冲突解决（已 git add）后继续完成 cherry-pick，返回新提交 hash。
// 设 GIT_EDITOR=true 避免 --continue 打开交互式编辑器卡住。
func (s *Service) CherryPickContinue() (string, error) {
	cmd := exec.Command("git", "cherry-pick", "--continue")
	cmd.Dir = s.dir
	cmd.Env = append(os.Environ(), "GIT_EDITOR=true")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git cherry-pick --continue: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return s.run("rev-parse", "HEAD")
}

// InCherryPick 判断当前是否处于未完成的 cherry-pick 状态（存在 CHERRY_PICK_HEAD）。
func (s *Service) InCherryPick() bool {
	_, err := s.run("rev-parse", "--verify", "--quiet", "CHERRY_PICK_HEAD")
	return err == nil
}

// ConflictFiles 返回当前未合并（冲突）的文件列表。
func (s *Service) ConflictFiles() ([]string, error) {
	out, err := s.run("diff", "--name-only", "--diff-filter=U")
	if err != nil {
		return nil, err
	}
	var res []string
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		if l != "" {
			res = append(res, l)
		}
	}
	return res, nil
}

// RepoState 仓库当前状态：供任务页展示「当前项目状态」，并在残留进行中操作时手动清理。
type RepoState struct {
	Branch     string   `json:"branch"`      // 当前分支
	CherryPick bool     `json:"cherry_pick"` // cherry-pick 进行中
	Merge      bool     `json:"merge"`       // merge 进行中
	Rebase     bool     `json:"rebase"`      // rebase 进行中
	Conflicts  []string `json:"conflicts"`   // 未合并冲突文件
	Dirty      bool     `json:"dirty"`       // 工作区脏（含已跟踪改动，不含 .claude）
}

// refExists 判断某 git 引用是否存在（CHERRY_PICK_HEAD / MERGE_HEAD / REBASE_HEAD 等）。
func (s *Service) refExists(ref string) bool {
	_, err := s.run("rev-parse", "--verify", "--quiet", ref)
	return err == nil
}

// RepoState 返回仓库当前状态。
func (s *Service) RepoState() RepoState {
	st := RepoState{}
	if b, err := s.CurrentBranch(); err == nil {
		st.Branch = b
	}
	st.CherryPick = s.refExists("CHERRY_PICK_HEAD")
	st.Merge = s.refExists("MERGE_HEAD")
	st.Rebase = s.refExists("REBASE_HEAD")
	if st.CherryPick || st.Merge || st.Rebase {
		st.Conflicts, _ = s.ConflictFiles()
	}
	status, _ := s.Status()
	st.Dirty = filterIgnorableUntracked(status) != ""
	return st
}

// AbortPending 中止任何进行中的 cherry-pick / merge / rebase，并清理未跟踪文件，
// 把仓库从卡住的进行中状态恢复到干净、可重跑的状态。
func (s *Service) AbortPending() error {
	if s.refExists("CHERRY_PICK_HEAD") {
		if _, err := s.run("cherry-pick", "--abort"); err != nil {
			return fmt.Errorf("中止 cherry-pick 失败: %w", err)
		}
	}
	if s.refExists("MERGE_HEAD") {
		if _, err := s.run("merge", "--abort"); err != nil {
			return fmt.Errorf("中止 merge 失败: %w", err)
		}
	}
	if s.refExists("REBASE_HEAD") {
		if _, err := s.run("rebase", "--abort"); err != nil {
			return fmt.Errorf("中止 rebase 失败: %w", err)
		}
	}
	return s.CleanUntracked()
}

// RestoreConflicts 把冲突文件恢复到冲突态（重新生成 <<<<<<< / ======= / >>>>>>> 标记）。
// 用于冲突解决审查驳回后，让 AI 基于原始冲突重新解决。依赖 index 里仍保留的冲突三方信息（stage 1/2/3）。
func (s *Service) RestoreConflicts(files []string) error {
	if len(files) == 0 {
		return nil
	}
	args := append([]string{"checkout", "--conflict=merge", "--"}, files...)
	_, err := s.run(args...)
	return err
}

// CommitInfo 某分支上单个提交的元信息，供代码迁移模块展示与过滤。
type CommitInfo struct {
	Hash    string    `json:"hash"`
	Subject string    `json:"subject"`
	Message string    `json:"message"` // 完整 message（含 subject + body + trailer，用于 -x 来源匹配）
	Author  string    `json:"author"`
	Date    time.Time `json:"date"`
	Parents []string  `json:"parents,omitempty"`
	IsMerge bool      `json:"is_merge,omitempty"`
}

// ListCommitsRange 按时间从旧到新列出 base..head 之间的提交（head 有而 base 没有的提交）。
// format 用 \x1f 分隔字段、\x1e 分隔记录（提交信息中不会出现的控制字符），避免解析歧义。
func (s *Service) ListCommitsRange(base, head string) ([]CommitInfo, error) {
	// --topo-order 保证拓扑序（parent 先于 child），翻转后得到「旧→新」的拓扑序，符合迁移依赖顺序。
	// --no-merges 过滤所有 merge commit：merge 只是合并点，不创造新代码，其改动由两侧普通 commit 迁移；
	// 线性化 cherry-pick 时 merge 冲突会重现，交给冲突处理流程（人工/AI）解决。
	out, err := s.run("log", "--topo-order", "--no-merges", "--format=%H%x1f%s%x1f%an%x1f%aI%x1f%P%x1f%B%x1e", base+".."+head)
	if err != nil {
		return nil, err
	}
	var res []CommitInfo
	for _, rec := range strings.Split(out, "\x1e") {
		rec = strings.TrimSpace(rec)
		if rec == "" {
			continue
		}
		fields := strings.Split(rec, "\x1f")
		if len(fields) < 4 {
			continue
		}
		ci := CommitInfo{Hash: fields[0], Subject: fields[1], Author: fields[2]}
		if t, err := time.Parse(time.RFC3339, fields[3]); err == nil {
			ci.Date = t
		}
		if len(fields) >= 5 && strings.TrimSpace(fields[4]) != "" {
			for _, p := range strings.Fields(fields[4]) {
				ci.Parents = append(ci.Parents, p)
			}
		}
		if len(fields) >= 6 {
			ci.Message = strings.TrimSpace(fields[5])
		}
		ci.IsMerge = len(ci.Parents) > 1
		res = append(res, ci)
	}
	// git log 默认从新到旧，翻转为从旧到新（迁移顺序）。
	for i, j := 0, len(res)-1; i < j; i, j = i+1, j-1 {
		res[i], res[j] = res[j], res[i]
	}
	return res, nil
}

// ResolveCommit 返回 ref（分支名或 commit hash）指向的单个 commit 信息。
// 用于获取「基底 commit」——目标基底的 HEAD，作为 diff 列表底部的锚点（可 reset 到根部）。
func (s *Service) ResolveCommit(ref string) (CommitInfo, error) {
	out, err := s.run("log", "-1", "--format=%H%x1f%s%x1f%an%x1f%aI%x1f%P%x1f%B", ref)
	if err != nil {
		return CommitInfo{}, err
	}
	fields := strings.Split(strings.TrimSpace(out), "\x1f")
	if len(fields) < 4 {
		return CommitInfo{}, fmt.Errorf("解析 commit %s 失败", ref)
	}
	ci := CommitInfo{Hash: fields[0], Subject: fields[1], Author: fields[2]}
	if t, err := time.Parse(time.RFC3339, fields[3]); err == nil {
		ci.Date = t
	}
	if len(fields) >= 5 && strings.TrimSpace(fields[4]) != "" {
		for _, p := range strings.Fields(fields[4]) {
			ci.Parents = append(ci.Parents, p)
		}
	}
	if len(fields) >= 6 {
		ci.Message = strings.TrimSpace(fields[5])
	}
	ci.IsMerge = len(ci.Parents) > 1
	return ci, nil
}

// IsEmptyCommit 判断提交是否「无内容」（普通空提交）：其 tree 与第一 parent 的 tree 相同。
// ListCommitsRange 已用 --no-merges 过滤掉所有 merge commit，故这里只需兜底过滤普通空提交。
// root commit（无 parent）保守返回 false（不判为空，宁可保留）。
func (s *Service) IsEmptyCommit(hash string) (bool, error) {
	tree, err := s.run("rev-parse", hash+"^{tree}")
	if err != nil {
		return false, err
	}
	parentTree, err := s.run("rev-parse", hash+"^1^{tree}")
	if err != nil {
		return false, nil // 无第一 parent（root commit）
	}
	return tree == parentTree, nil
}

// ResetHardTo 把当前分支硬重置到指定 ref（用于迁移目标分支回退）。ref 可为提交 hash 或分支名。
func (s *Service) ResetHardTo(ref string) error {
	_, err := s.run("reset", "--hard", ref)
	return err
}

// ResetSoftTo 把当前分支软重置到 ref（保留改动在暂存区），用于 squash 连续 commit。
func (s *Service) ResetSoftTo(ref string) error {
	_, err := s.run("reset", "--soft", ref)
	return err
}

// RevParse 返回 ref（分支名/commit hash）解析后的完整 commit hash。
func (s *Service) RevParse(ref string) (string, error) {
	return s.run("rev-parse", ref)
}

// BranchUpstream 返回分支跟踪的远端 ref（如 origin/master），无跟踪时返回空串。
func (s *Service) BranchUpstream(branch string) (string, error) {
	out, err := s.run("rev-parse", "--abbrev-ref", branch+"@{upstream}")
	if err != nil {
		return "", nil // 无 upstream（fatal: no upstream...），返回空，不视为错误
	}
	return out, nil
}

// FetchUpdateBranch 拉取远端并更新本地分支到远端最新（fast-forward，拒绝 non-fast-forward）。
// 无 upstream 时跳过（返回 nil）。更新失败返回错误。
func (s *Service) FetchUpdateBranch(branch string) error {
	upstream, err := s.BranchUpstream(branch)
	if err != nil || upstream == "" {
		return err
	}
	parts := strings.SplitN(upstream, "/", 2)
	if len(parts) != 2 {
		return fmt.Errorf("无法解析 upstream %s", upstream)
	}
	remote, remoteBranch := parts[0], parts[1]
	if _, err := s.run("fetch", remote, remoteBranch+":refs/heads/"+branch); err != nil {
		return fmt.Errorf("git fetch %s %s:refs/heads/%s: %w", remote, remoteBranch, branch, err)
	}
	return nil
}

// IsAncestor 判断 candidate 是否为 ref 的祖先（用于 reset 护栏：只允许回退到目标分支自身的祖先）。
func (s *Service) IsAncestor(candidate, ref string) bool {
	_, err := s.run("merge-base", "--is-ancestor", candidate, ref)
	return err == nil
}

// AncestorSet 返回 ref（含自身）全部祖先的 hash 集合，用于批量判断「某 commit 是否仍在某分支历史里」。
func (s *Service) AncestorSet(ref string) (map[string]bool, error) {
	out, err := s.run("rev-list", ref)
	if err != nil {
		return nil, err
	}
	set := map[string]bool{}
	for _, h := range strings.Fields(out) {
		set[h] = true
	}
	return set, nil
}
