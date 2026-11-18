package ai

import (
	"strings"

	"darter/internal/job"
)

// 约束模板场景：除内置的 SceneFixer（审查修复）/ SceneMigration（代码迁移）外，还有 common（通用约束）。
const SceneCommon = "common"

// ConstraintScenes 约束模板可选的场景（顺序即展示顺序）。
var ConstraintScenes = []Scene{
	{Key: SceneCommon, Name: "通用"},
	{Key: SceneFixer, Name: "审查修复"},
	{Key: SceneMigration, Name: "代码迁移"},
}

// InjectableField 约束文本里可注入的占位符定义：供前端「可注入字段」表展示，也是替换逻辑的依据。
// Placeholder 为完整占位符（如 {{migration.source_branch}}），写进约束文本后由运行时替换成真实值。
type InjectableField struct {
	Scene        string `json:"scene"`        // 所属场景：common / fixer / migration
	Placeholder  string `json:"placeholder"`  // 完整占位符，如 {{migration.source_branch}}
	Name         string `json:"name"`         // 中文名
	Description  string `json:"description"`  // 含义说明
	Availability string `json:"availability"` // 何时有值（全部调用 / 仅冲突解决与审查 等）
}

// InjectableFields 返回全部可注入占位符清单。
func InjectableFields() []InjectableField {
	return []InjectableField{
		// 代码迁移
		{Scene: SceneMigration, Placeholder: "{{migration.source_branch}}", Name: "源分支", Description: "要迁移的 commit 所在分支", Availability: "全部代码迁移 AI 调用"},
		{Scene: SceneMigration, Placeholder: "{{migration.target_base}}", Name: "目标基底", Description: "目标分支从其切出的基底分支", Availability: "全部代码迁移 AI 调用"},
		{Scene: SceneMigration, Placeholder: "{{migration.target_branch}}", Name: "目标分支", Description: "接收 cherry-pick 的目标工作分支", Availability: "全部代码迁移 AI 调用"},
		{Scene: SceneMigration, Placeholder: "{{migration.source_commit}}", Name: "源 commit 哈希", Description: "当前正在迁移的源 commit 完整哈希", Availability: "仅冲突解决与冲突审查"},
		{Scene: SceneMigration, Placeholder: "{{migration.source_commit_subject}}", Name: "源 commit 标题", Description: "当前源 commit 的标题行", Availability: "仅冲突解决与冲突审查"},
		{Scene: SceneMigration, Placeholder: "{{migration.source_commit_message}}", Name: "源 commit 提交信息", Description: "当前源 commit 的完整提交信息", Availability: "仅冲突解决与冲突审查"},
		{Scene: SceneMigration, Placeholder: "{{migration.conflict_files}}", Name: "冲突文件", Description: "当前冲突涉及的文件列表", Availability: "仅冲突解决与冲突审查"},
		// 审查修复
		{Scene: SceneFixer, Placeholder: "{{fixer.base_branch}}", Name: "基线分支", Description: "修复分支从其切出的基线分支", Availability: "全部审查修复 AI 调用"},
		{Scene: SceneFixer, Placeholder: "{{fixer.fix_branch}}", Name: "修复分支", Description: "当前修复任务所在分支", Availability: "全部审查修复 AI 调用"},
	}
}

// RenderConstraints 用 values 替换约束文本里的 {{name}} 占位符；无对应值或 values 为空时原样返回。
// 值构造统一走 MigrationConstraintValues / FixerConstraintValues，保证「已知占位符无值时替换为空串」而非残留字面量。
func RenderConstraints(constraints string, values map[string]string) string {
	if len(values) == 0 || strings.TrimSpace(constraints) == "" {
		return constraints
	}
	return render(constraints, values)
}

// MergeValues 把自定义注入变量合并进内置取值；自定义 key 覆盖同名内置 key。
// 用于把 Job.ConstraintVars 合并进 MigrationConstraintValues / FixerConstraintValues 的结果后再替换。
func MergeValues(base, custom map[string]string) map[string]string {
	if len(custom) == 0 {
		return base
	}
	out := make(map[string]string, len(base)+len(custom))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range custom {
		out[k] = v
	}
	return out
}

// MigrationConstraintValues 构造代码迁移约束的占位符取值。
// 三个分支永远填充；commit 非 nil 时填充 source_commit 系列；conflictFiles 非空时填充 conflict_files。
// 已知但无值的占位符（如全量回归阶段没有单一源 commit）填充空串，替换后整段留空而非残留字面量。
func MigrationConstraintValues(spec *job.MigrationSpec, commit *job.CommitEntry, conflictFiles []string) map[string]string {
	vals := map[string]string{
		"migration.source_branch":         "",
		"migration.target_base":           "",
		"migration.target_branch":         "",
		"migration.source_commit":         "",
		"migration.source_commit_subject": "",
		"migration.source_commit_message": "",
		"migration.conflict_files":        "",
	}
	if spec != nil {
		vals["migration.source_branch"] = spec.SourceBranch
		vals["migration.target_base"] = spec.TargetBase
		vals["migration.target_branch"] = spec.TargetBranch
	}
	if commit != nil {
		vals["migration.source_commit"] = commit.Hash
		vals["migration.source_commit_subject"] = commit.Subject
		vals["migration.source_commit_message"] = commit.Message
	}
	if len(conflictFiles) > 0 {
		vals["migration.conflict_files"] = strings.Join(conflictFiles, "\n")
	}
	return vals
}

// FixerConstraintValues 构造审查修复约束的占位符取值。
func FixerConstraintValues(j *job.Job) map[string]string {
	vals := map[string]string{"fixer.base_branch": "", "fixer.fix_branch": ""}
	if j != nil {
		vals["fixer.base_branch"] = j.BaseBranch
		vals["fixer.fix_branch"] = j.FixBranch
	}
	return vals
}
