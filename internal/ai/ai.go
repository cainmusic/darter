package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"darter/internal/job"
)

// Client 封装本地 claude CLI，通过 os/exec 子进程调用，不依赖 API/key。
type Client struct {
	Cmd   string            // 默认 "claude"
	Model string            // 可选，如 "opus"/"sonnet"；空则用用户默认
	Log   func(line string) // 可选：实时透传子进程 stderr 进度（用于前端日志）
	// OnChild 在子进程启动后回调，用于记录 PID/启动时间/命令名/阶段，崩溃恢复时清理孤儿进程，
	// 并供前端跟踪「后台 AI 正在做什么」。phase 为阶段描述（如"修复任务"/"对抗审查"）。
	OnChild func(pid int, startTime, cmd, phase string)
	// OnChildExit 在子进程退出后回调，用于清除记录。
	OnChildExit func(pid int)
}

type envelope struct {
	Result  string `json:"result"`
	IsError bool   `json:"is_error"`
}

// lineWriter 按行把子进程输出转发给回调，用于实时透传 claude 的 stderr 进度。
type lineWriter struct {
	fn func(line string)
	b  bytes.Buffer
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.b.Write(p)
	data := w.b.Bytes()
	start := 0
	for {
		i := bytes.IndexByte(data[start:], '\n')
		if i < 0 {
			break
		}
		if line := strings.TrimSpace(string(data[start : start+i])); line != "" {
			w.fn(line)
		}
		start += i + 1
	}
	w.b.Reset()
	w.b.Write(data[start:])
	return len(p), nil
}

// run 执行一次 headless claude 调用，返回顶层 JSON 的 result 字段。
// permMode 为 claude 的 --permission-mode 值："acceptEdits"（允许自动编辑，用于 Fix）、
// "plan"（只读规划，用于审查类与拆解类：允许读代码、禁止写，避免误改仓库）、""（不指定，当前未使用）。
// phase 为本次调用的阶段描述（如"修复任务"/"对抗审查"），透传给 OnChild 供前端跟踪后台 AI 正在做什么。
func (c *Client) run(ctx context.Context, dir, prompt, permMode, phase string) (string, error) {
	cmdName := c.Cmd
	if cmdName == "" {
		cmdName = "claude"
	}
	args := []string{"-p", "--output-format", "json", "--permission-prompts", "none"}
	if permMode != "" {
		args = append(args, "--permission-mode", permMode)
	}
	if c.Model != "" {
		args = append(args, "--model", c.Model)
	}

	cmd := exec.CommandContext(ctx, cmdName, args...)
	cmd.Dir = dir
	// prompt 走 stdin 而非命令行参数：审查器携带完整文件上下文后提示词可能很大，
	// 作为单个 argv 传入会在 fork/exec 时触发 E2BIG（argument list too long），
	// 走 stdin 无 argv 长度限制，同时仍能避免交互态下 3s 无输入等待。
	cmd.Stdin = strings.NewReader(prompt)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	if c.Log != nil {
		cmd.Stderr = io.MultiWriter(&stderr, &lineWriter{fn: c.Log})
	} else {
		cmd.Stderr = &stderr
	}

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("claude 启动失败: %w", err)
	}
	pid := cmd.Process.Pid
	startTime := procStartTime(pid)
	if c.OnChild != nil {
		c.OnChild(pid, startTime, cmdName, phase)
	}
	if c.OnChildExit != nil {
		defer c.OnChildExit(pid)
	}
	if err := cmd.Wait(); err != nil {
		return "", fmt.Errorf("claude 调用失败: %w (stderr: %s)", err, strings.TrimSpace(stderr.String()))
	}

	var env envelope
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		return "", fmt.Errorf("解析 claude 输出失败: %w; 原文: %s", err, stdout.String())
	}
	if env.IsError {
		return "", errors.New("claude 返回错误: " + env.Result)
	}
	return env.Result, nil
}

// procStartTime 返回进程启动时间字符串（ps -o lstart=），进程不存在或查询失败时返回空串。
func procStartTime(pid int) string {
	out, err := exec.Command("ps", "-o", "lstart=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// procCommand 返回进程完整命令行（ps -o command=），进程不存在或查询失败时返回空串。
func procCommand(pid int) string {
	out, err := exec.Command("ps", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// KillOrphan 校验并清理遗留的 claude 子进程。返回是否真正执行了 kill。
// 三重校验防止 PID 复用误杀：PID 仍存活 + 启动时间完全一致 + 命令行包含命令名。
func KillOrphan(pid int, start, cmdName string) bool {
	if pid <= 0 || start == "" {
		return false
	}
	if procStartTime(pid) != start {
		return false // 进程已不存在，或该 PID 已被复用（启动时间不一致）
	}
	if cmdName != "" {
		cmdline := procCommand(pid)
		if cmdline == "" || !strings.Contains(cmdline, filepath.Base(cmdName)) {
			return false
		}
	}
	return syscall.Kill(pid, syscall.SIGKILL) == nil
}

// ParseReport 把 review 报告拆解为任务列表。
// repoDir 为项目目录：拆解时只读浏览代码，把报告里的模块名/服务名映射成真实文件路径。
// constraints 为项目约束/背景（用于判断问题归属的服务与文件，不是修复约束）。
// feedback 为人工提示（打回重做原因），据此改进拆解，可空。
// 返回的 ignored 为报告里没有成为任务的条目及原因（不静默丢弃，保证透明度）。
func (c *Client) ParseReport(ctx context.Context, repoDir, report, constraints, feedback string) ([]job.Task, []job.DecomposeNote, error) {
	prompt := render(c.prompt(PromptParseReport), map[string]string{
		"project_background": section("项目背景（用于判断问题归属的服务与文件，不是修复约束）：", constraints),
		"feedback":           section("人工提示（打回重做的原因，请据此改进本次拆解，避免上次的问题）：", feedback),
		"report":             report,
	})

	raw, err := c.run(ctx, repoDir, prompt, "plan", "拆解报告")
	if err != nil {
		return nil, nil, err
	}
	out, err := parseDecompose(raw)
	if err != nil {
		return nil, nil, err
	}
	for i := range out.Tasks {
		out.Tasks[i].ID = fmt.Sprintf("task_%03d", i+1)
		out.Tasks[i].Status = job.TaskPending
	}
	return out.Tasks, out.Ignored, nil
}

// Fix 在 repoDir 中修复任务，返回模型总结（真实改动以 git diff 为准）。
func (c *Client) Fix(ctx context.Context, repoDir string, t job.Task, feedback, constraints string) (string, error) {
	prompt := render(c.prompt(PromptFix), map[string]string{
		"task":        taskBlock(t),
		"constraints": section("项目约束（修复时必须遵守）：", constraints),
		"feedback":    section("上一次修复未通过对抗性审查，审查员指出以下问题，请据此修正：", feedback),
	})

	return c.run(ctx, repoDir, prompt, "acceptEdits", "修复任务")
}

// Review 对抗性审查：看任务 + diff，其余信息由审查器自行在仓库（当前工作目录）里核实。
// newFiles 为本次改动的新增文件清单（可空），供审查器对新建文件做更严格的必要性核实。
func (c *Client) Review(ctx context.Context, repoDir string, t job.Task, diff string, constraints string, newFiles []string) (job.Review, error) {
	constr := constraints
	if constr == "" {
		constr = "（无）"
	}
	prompt := render(c.prompt(PromptReview), map[string]string{
		"task":        taskBlock(t),
		"constraints": constr,
		"diff":        diff,
		"new_files":   newFilesBlock(newFiles),
	})

	raw, err := c.run(ctx, repoDir, prompt, "plan", "对抗审查")
	if err != nil {
		return job.Review{}, err
	}
	var rv job.Review
	if err := json.Unmarshal(extractJSON(raw), &rv); err != nil {
		return job.Review{}, fmt.Errorf("解析审查结果失败: %w; 模型输出: %s", err, raw)
	}
	if rv.Verdict == "" {
		rv.Verdict = "fail"
		rv.Summary = "审查结果无法解析，按不通过处理"
	}
	return rv, nil
}

// AdhocReview 人工发起的一次性 AI 审查：对某个已通过/已审批任务的修复内容做独立复查。
// humanPrompt 为人类审查者附加的审查方向/疑问（可为空，则仅用默认审查标准）。
// 与自动对抗审查(Review)的区别：这是人类主动、不限次数、可定向的复查，结果单独落盘供回溯。
func (c *Client) AdhocReview(ctx context.Context, repoDir string, t job.Task, diff, constraints, humanPrompt string, newFiles []string) (job.Review, error) {
	prompt := render(c.prompt(PromptAdhocReview), map[string]string{
		"task":         taskBlock(t),
		"constraints":  section("项目约束（修复必须满足）：", constraints),
		"diff":         diff,
		"new_files":    newFilesBlock(newFiles),
		"human_prompt": section("人类审查者补充的审查方向/疑问（请重点针对这些给出明确结论）：", humanPrompt),
	})

	raw, err := c.run(ctx, repoDir, prompt, "plan", "AI 审查（人工发起）")
	if err != nil {
		return job.Review{}, err
	}
	var rv job.Review
	if err := json.Unmarshal(extractJSON(raw), &rv); err != nil {
		return job.Review{}, fmt.Errorf("解析审查结果失败: %w; 模型输出: %s", err, raw)
	}
	if rv.Verdict == "" {
		rv.Verdict = "fail"
		rv.Summary = "审查结果无法解析，按不通过处理"
	}
	return rv, nil
}

// ReviewBranch 宏观回归审查：任务全部跑完后，对整条修复分支的累积改动做一次完整性检查，
// 只报告「明显有问题」的地方（跨任务冲突、破坏下游、改了一半、无关改动），不逐行吹毛求疵。
// 同时为每个问题定位它来源于哪个 commit（供归档阶段定点处理）。
func (c *Client) ReviewBranch(ctx context.Context, repoDir, diff, log, constraints string) (job.Review, error) {
	constr := constraints
	if constr == "" {
		constr = "（无）"
	}
	if log == "" {
		log = "（无）"
	}
	prompt := render(c.prompt(PromptReviewBranch), map[string]string{
		"constraints": constr,
		"diff":        diff,
		"log":         log,
	})

	raw, err := c.run(ctx, repoDir, prompt, "plan", "宏观回归审查")
	if err != nil {
		return job.Review{}, err
	}
	var rv job.Review
	if err := json.Unmarshal(extractJSON(raw), &rv); err != nil {
		return job.Review{}, fmt.Errorf("解析宏观回归审查结果失败: %w; 模型输出: %s", err, raw)
	}
	if rv.Verdict == "" {
		rv.Verdict = "fail"
		rv.Summary = "宏观回归审查结果无法解析，按有问题处理"
	}
	return rv, nil
}

// ReviewArchive 归档完成后的最终回归审查：核对归档分支 commit 与已归档任务的对应关系（每个任务恰好一个 commit）。
// 宏观回归审查发现的问题不在归档阶段处理，故此处不再检查它们（它们作为遗留问题清单另行交付）。
func (c *Client) ReviewArchive(ctx context.Context, repoDir, diff, log, tasksJSON, constraints string) (job.Review, error) {
	constr := constraints
	if constr == "" {
		constr = "（无）"
	}
	prompt := render(c.prompt(PromptReviewArchive), map[string]string{
		"tasks_json":  tasksJSON,
		"diff":        diff,
		"log":         log,
		"constraints": constr,
	})

	raw, err := c.run(ctx, repoDir, prompt, "plan", "归档回归审查")
	if err != nil {
		return job.Review{}, err
	}
	var rv job.Review
	if err := json.Unmarshal(extractJSON(raw), &rv); err != nil {
		return job.Review{}, fmt.Errorf("解析归档回归审查结果失败: %w; 模型输出: %s", err, raw)
	}
	if rv.Verdict == "" {
		rv.Verdict = "fail"
		rv.Summary = "归档回归审查结果无法解析，按有问题处理"
	}
	return rv, nil
}

// FixArchive 在归档分支上修复最终回归审查发现的问题（无交互，只改文件，提交由调用方处理）。
// feedback 由调用方拼接：编译/语法自检问题 + 最终回归审查 findings。
func (c *Client) FixArchive(ctx context.Context, repoDir, feedback, constraints string) (string, error) {
	constraintsSection := ""
	if strings.TrimSpace(constraints) != "" {
		constraintsSection = "项目约束（必须遵守）：\n" + constraints + "\n\n"
	}
	prompt := render(c.prompt(PromptFixArchive), map[string]string{
		"constraints": constraintsSection,
		"feedback":    feedback,
	})

	return c.run(ctx, repoDir, prompt, "acceptEdits", "归档修复")
}

// ReviewTasks 对拆解出的任务列表做一次 AI 复查（查漏/去重/粒度 + 对抗性预检剔除误报），返回修正后的最终列表。
// repoDir 为项目目录：质检时只读浏览代码，用于核实/修正涉及文件、判断冲突合并、验证任务真实性。
// constraints 为项目约束/背景（用于判断问题归属的服务与文件，不是修复约束）。
// feedback 为人工提示（打回重做原因），据此改进复查，可空。
// 返回的 removed 为对抗性预检剔除的误报任务及原因（不进任务列表，但保留记录供人工核对）。
func (c *Client) ReviewTasks(ctx context.Context, repoDir, report string, tasks []job.Task, constraints, feedback string) ([]job.Task, []job.DecomposeNote, error) {
	tasksJSON, _ := json.Marshal(tasks)
	prompt := render(c.prompt(PromptReviewTasks), map[string]string{
		"project_background": section("项目背景（用于判断问题归属的服务与文件，不是修复约束）：", constraints),
		"feedback":           section("人工提示（打回重做的原因，请据此改进本次复查，避免上次的问题）：", feedback),
		"report":             report,
		"tasks_json":         string(tasksJSON),
	})

	raw, err := c.run(ctx, repoDir, prompt, "plan", "任务质检")
	if err != nil {
		return nil, nil, err
	}
	out, err := parseDecompose(raw)
	if err != nil {
		return nil, nil, err
	}
	for i := range out.Tasks {
		out.Tasks[i].ID = fmt.Sprintf("task_%03d", i+1)
		out.Tasks[i].Status = job.TaskPending
	}
	return out.Tasks, out.Removed, nil
}

// ResolveConflict 解决 git 合并冲突：仓库当前处于 cherry-pick 冲突态，工作区文件带冲突标记。
// claude 读冲突文件、合并两侧改动、移除冲突标记；只改文件，不执行 git 操作（暂存/--continue 由调用方处理）。
// constraints 为项目约束（如不引入第三方依赖/保持兼容性等），解决冲突时带入 AI。
// ConflictResolution AI 解决冲突的结果。
type ConflictResolution struct {
	Action  string   // "resolved"（正常解决）| "skip"（判定该 commit 应跳过迁移）
	Summary string   // resolved 时的解决说明；skip 时的跳过原因
	Files   []string // 已解决的文件相对路径（resolved 时）
}

func (c *Client) ResolveConflict(ctx context.Context, repoDir string, files []string, constraints, feedback string) (ConflictResolution, error) {
	prompt := render(c.prompt(PromptResolveConflict), map[string]string{
		"constraints": section("项目约束（解决冲突时必须遵守）：", constraints),
		"feedback":    section("上一轮解决未通过审查，审查员指出以下问题，请据此修正：", feedback),
		"files":       strings.Join(files, "\n"),
	})

	raw, err := c.run(ctx, repoDir, prompt, "acceptEdits", "解决冲突")
	if err != nil {
		return ConflictResolution{}, err
	}
	return parseConflictResolution(raw), nil
}

// parseConflictResolution 解析 AI 解决冲突的输出；action 缺省或非法时按 resolved 处理（兼容旧格式）。
func parseConflictResolution(raw string) ConflictResolution {
	var out struct {
		Action     string   `json:"action"`
		Summary    string   `json:"summary"`
		SkipReason string   `json:"skip_reason"`
		Files      []string `json:"files"`
	}
	if err := json.Unmarshal(extractJSON(raw), &out); err != nil {
		return ConflictResolution{Action: "resolved", Summary: strings.TrimSpace(raw)}
	}
	if out.Action == "skip" {
		reason := strings.TrimSpace(out.SkipReason)
		if reason == "" {
			reason = strings.TrimSpace(out.Summary)
		}
		return ConflictResolution{Action: "skip", Summary: reason}
	}
	return ConflictResolution{Action: "resolved", Summary: strings.TrimSpace(out.Summary), Files: out.Files}
}

// ReviewConflict 审查 AI 解决冲突的结果：看解决后的完整改动 diff（相对 cherry-pick 前）是否合理。
func (c *Client) ReviewConflict(ctx context.Context, repoDir string, files []string, diff, constraints string) (job.Review, error) {
	constr := ""
	if strings.TrimSpace(constraints) != "" {
		constr = "\n\n项目约束（解决冲突时必须遵守）：\n" + constraints
	}
	prompt := render(c.prompt(PromptReviewConflict), map[string]string{
		"files":       strings.Join(files, "\n"),
		"diff":        diff,
		"constraints": constr,
	})

	raw, err := c.run(ctx, repoDir, prompt, "plan", "冲突解决审查")
	if err != nil {
		return job.Review{}, err
	}
	var rv job.Review
	if err := json.Unmarshal(extractJSON(raw), &rv); err != nil {
		return job.Review{}, fmt.Errorf("解析冲突解决审查结果失败: %w; 模型输出: %s", err, raw)
	}
	if rv.Verdict == "" {
		rv.Verdict = "fail"
		rv.Summary = "审查结果无法解析，按不通过处理"
	}
	return rv, nil
}

// extractSummary 从 claude 输出的 JSON（{"summary":"...","files":[...]}）里提取 summary 字段；
// 解析失败（AI 未按约定格式输出）时原样返回，保证不丢信息。
func extractSummary(raw string) string {
	var out struct {
		Summary string `json:"summary"`
	}
	if err := json.Unmarshal(extractJSON(raw), &out); err == nil && strings.TrimSpace(out.Summary) != "" {
		return strings.TrimSpace(out.Summary)
	}
	return strings.TrimSpace(raw)
}

// taskBlock 序列化任务为 JSON 文本，供提示词内嵌。
func taskBlock(t job.Task) string {
	b, _ := json.MarshalIndent(t, "", "  ")
	return string(b)
}

// decomposeOutput 拆解/质检 AI 的结构化输出：任务列表 + 被忽略/剔除的内容及原因。
type decomposeOutput struct {
	Tasks   []job.Task          `json:"tasks"`
	Ignored []job.DecomposeNote `json:"ignored"`
	Removed []job.DecomposeNote `json:"removed"`
}

// parseDecompose 解析拆解/质检 AI 的 JSON 输出。优先按结构化对象解析；
// 若 AI 只输出了裸任务数组（旧格式），回退按数组解析，保证兼容。
func parseDecompose(raw string) (decomposeOutput, error) {
	bs := extractJSON(raw)
	var out decomposeOutput
	if err := json.Unmarshal(bs, &out); err == nil && out.Tasks != nil {
		return out, nil
	}
	var tasks []job.Task
	if err := json.Unmarshal(bs, &tasks); err != nil {
		return out, fmt.Errorf("解析拆解输出失败: %w; 模型输出: %s", err, raw)
	}
	out.Tasks = tasks
	return out, nil
}

// ChangedPaths 从 git diff 中提取被修改/新增的文件相对路径（去除 ---/+++ 前缀与 /dev/null）。
func ChangedPaths(diff string) []string {
	seen := map[string]bool{}
	var paths []string
	for _, line := range strings.Split(diff, "\n") {
		var p string
		switch {
		case strings.HasPrefix(line, "+++ b/"):
			p = strings.TrimPrefix(line, "+++ b/")
		case strings.HasPrefix(line, "--- a/"):
			p = strings.TrimPrefix(line, "--- a/")
		default:
			continue
		}
		p = strings.TrimSpace(p)
		if p == "" || p == "/dev/null" || seen[p] {
			continue
		}
		seen[p] = true
		paths = append(paths, p)
	}
	return paths
}

// NewPaths 从 diff 文本中提取「新增文件」（new file mode 标记）的相对路径。
// 与 ChangedPaths（所有改动文件）不同，它只返回新建文件，供审查提示词显式列出、逐一核实必要性。
// 适用于任意 unified diff（git diff --cached / git show 的输出都带 new file mode 标记）。
func NewPaths(diff string) []string {
	var paths []string
	var cur string
	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, "diff --git ") {
			if i := strings.Index(line, " b/"); i >= 0 {
				cur = strings.TrimSpace(line[i+3:])
			}
			continue
		}
		if strings.HasPrefix(line, "new file mode ") && cur != "" {
			paths = append(paths, cur)
			cur = "" // 同一文件块只计一次
		}
	}
	return paths
}

// newFilesBlock 若存在新增文件，返回一段提示词，让审查器逐一核实其必要性。
func newFilesBlock(newFiles []string) string {
	if len(newFiles) == 0 {
		return ""
	}
	return "\n\n本次改动新增的文件（请逐一核实其必要性：是否确实是本任务所需、内容是否最小必要）：\n- " + strings.Join(newFiles, "\n- ")
}

// CheckCommitScope 提交前最后一道检查：确认 diff 是否只包含当前任务相关改动。
// 返回 reason（空串表示干净、可提交），err 表示调用失败。
func (c *Client) CheckCommitScope(ctx context.Context, repoDir string, t job.Task, diff string, newFiles []string) (reason string, err error) {
	prompt := render(c.prompt(PromptCheckCommitScope), map[string]string{
		"task":      taskBlock(t),
		"diff":      diff,
		"new_files": newFilesBlock(newFiles),
	})

	raw, err := c.run(ctx, repoDir, prompt, "plan", "提交前检查")
	if err != nil {
		return "", err
	}
	var rv struct {
		Verdict string `json:"verdict"`
		Reason  string `json:"reason"`
	}
	if err := json.Unmarshal(extractJSON(raw), &rv); err != nil {
		return "", fmt.Errorf("解析提交检查结果失败: %w; 模型输出: %s", err, raw)
	}
	if rv.Verdict == "polluted" {
		return strings.TrimSpace(rv.Reason), nil
	}
	return "", nil
}

// ReviewMigration 代码迁移全量回归审查：审查目标分支相对 target_base 的累积改动，只挑明显问题。
func (c *Client) ReviewMigration(ctx context.Context, repoDir, diff, log, constraints string) (job.Review, error) {
	constr := constraints
	if constr == "" {
		constr = "（无）"
	}
	if log == "" {
		log = "（无）"
	}
	prompt := render(c.prompt(PromptReviewMigration), map[string]string{
		"constraints": constr,
		"diff":        diff,
		"log":         log,
	})

	raw, err := c.run(ctx, repoDir, prompt, "plan", "全量回归审查")
	if err != nil {
		return job.Review{}, err
	}
	var rv job.Review
	if err := json.Unmarshal(extractJSON(raw), &rv); err != nil {
		return job.Review{}, fmt.Errorf("解析全量回归审查结果失败: %w; 模型输出: %s", err, raw)
	}
	if rv.Verdict == "" {
		rv.Verdict = "fail"
		rv.Summary = "全量回归审查结果无法解析，按有问题处理"
	}
	return rv, nil
}

// FixMigrationRev 在迁移目标分支上修复全量回归审查发现的问题，返回修复说明。
func (c *Client) FixMigrationRev(ctx context.Context, repoDir, feedback, constraints string) (title, summary string, err error) {
	constraintsSection := ""
	if strings.TrimSpace(constraints) != "" {
		constraintsSection = "项目约束（必须遵守）：\n" + constraints + "\n\n"
	}
	prompt := render(c.prompt(PromptFixMigrationRev), map[string]string{
		"constraints": constraintsSection,
		"feedback":    feedback,
	})

	raw, err := c.run(ctx, repoDir, prompt, "acceptEdits", "回归修复")
	if err != nil {
		return "", "", err
	}
	title, summary = parseFixResult(raw)
	return title, summary, nil
}

// parseFixResult 解析修复输出的 title + summary（title 为提交信息的简短标题，summary 为改动说明）。
func parseFixResult(raw string) (title, summary string) {
	var out struct {
		Title   string   `json:"title"`
		Summary string   `json:"summary"`
		Files   []string `json:"files"`
	}
	if err := json.Unmarshal(extractJSON(raw), &out); err != nil {
		s := strings.TrimSpace(raw)
		return s, s
	}
	title = strings.TrimSpace(out.Title)
	summary = strings.TrimSpace(out.Summary)
	if title == "" {
		title = summary
	}
	return title, summary
}

// ReviewMigrationFix 审查回归修复的 diff 是否正确。
func (c *Client) ReviewMigrationFix(ctx context.Context, repoDir, diff, constraints string) (job.Review, error) {
	constr := ""
	if strings.TrimSpace(constraints) != "" {
		constr = "\n\n项目约束（修复必须满足）：\n" + constraints
	}
	prompt := render(c.prompt(PromptReviewMigrationFix), map[string]string{
		"diff":        diff,
		"constraints": constr,
	})

	raw, err := c.run(ctx, repoDir, prompt, "plan", "回归修复审查")
	if err != nil {
		return job.Review{}, err
	}
	var rv job.Review
	if err := json.Unmarshal(extractJSON(raw), &rv); err != nil {
		return job.Review{}, fmt.Errorf("解析回归修复审查结果失败: %w; 模型输出: %s", err, raw)
	}
	if rv.Verdict == "" {
		rv.Verdict = "fail"
		rv.Summary = "回归修复审查结果无法解析，按不通过处理"
	}
	return rv, nil
}

// extractJSON 从可能带围栏/前后文字的输出中提取 JSON。
func extractJSON(s string) []byte {
	s = strings.TrimSpace(s)
	// 去除 ```json / ``` 围栏
	if strings.HasPrefix(s, "```") {
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = s[i+1:]
		}
		if i := strings.LastIndex(s, "```"); i >= 0 {
			s = s[:i]
		}
		s = strings.TrimSpace(s)
	}
	if json.Valid([]byte(s)) {
		return []byte(s)
	}
	for _, open := range []byte{'{', '['} {
		close := byte('}')
		if open == '[' {
			close = ']'
		}
		i := strings.IndexByte(s, open)
		j := strings.LastIndexByte(s, close)
		if i >= 0 && j > i {
			sub := s[i : j+1]
			if json.Valid([]byte(sub)) {
				return []byte(sub)
			}
		}
	}
	return []byte(s)
}
