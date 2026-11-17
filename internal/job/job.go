package job

import "time"

// Job 是一次 review 报告修复的顶层对象。
type Job struct {
	ID           string       `json:"id"`
	Kind         string       `json:"kind"` // "review-fixer"（默认）| "migration"；区分工具模块
	Name         string       `json:"name"`
	RepoPath     string       `json:"repo_path"`
	BaseBranch   string       `json:"base_branch"`
	FixBranch    string       `json:"fix_branch"`
	ReportSource ReportSource `json:"report_source"`
	Constraints  string       `json:"constraints,omitempty"`
	// ConstraintTemplateID 引用的约束模板 ID（空 = 无模板引用，用内联 Constraints）。
	// 约束文本在运行时按此 ID 从模板「每次读最新内容」，因此改模板/换模板都会在后续任务生效。
	ConstraintTemplateID string  `json:"constraint_template_id,omitempty"`
	Status               Status  `json:"status"`
	TasksFrozen          bool    `json:"tasks_frozen"`
	CurrentTask          string  `json:"current_task_id,omitempty"`
	MaxRetries           int     `json:"max_retries"`
	ClaudeCmd            string  `json:"claude_cmd"`
	ClaudeModel          string  `json:"claude_model,omitempty"`
	Regression           *Review `json:"regression,omitempty"`     // 宏观回归审查结论（任务全跑完后对整条分支的完整性检查）
	ArchiveBranch        string  `json:"archive_branch,omitempty"` // 归档分支名（基于 base 分支，逐个迁移 approved 任务）
	ArchiveReview        *Review `json:"archive_review,omitempty"` // 归档完成后的最终回归审查结论
	// RemainingFindings 遗留问题清单：宏观回归审查发现、但归档阶段不处理的问题（归档终审时固化，交人工跟进）。
	RemainingFindings []Finding `json:"remaining_findings,omitempty"`
	// DecomposeIgnored 拆解阶段被忽略的报告内容（未拆成任务）及原因，供前端展示，保证「丢弃有据可查」。
	DecomposeIgnored []DecomposeNote `json:"decompose_ignored,omitempty"`
	// DecomposeRemoved 对抗性预检阶段被剔除的误报任务及原因（不进任务列表，但保留记录供人工核对）。
	DecomposeRemoved []DecomposeNote `json:"decompose_removed,omitempty"`
	// RedecomposeReason 最近一次打回重做时的人工说明（喂给 AI 改进拆解，也供前端展示）。
	RedecomposeReason string `json:"redecompose_reason,omitempty"`
	PauseRequest      bool   `json:"pause_requested,omitempty"` // 请求「执行完当前任务后暂停」；runner 在任务边界消费并置回 false
	// Migration kind=migration 时的专属配置；其余 kind 为 nil。
	Migration *MigrationSpec `json:"migration,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

type ReportSource struct {
	Type  string `json:"type"` // "text" | "file"
	Value string `json:"value"`
}

type Status string

const (
	StatusDraft          Status = "draft"
	StatusParsing        Status = "parsing"
	StatusReviewingTasks Status = "reviewing_tasks"
	StatusReady          Status = "ready"
	StatusRunning        Status = "running"
	StatusPaused         Status = "paused" // 执行完当前任务后暂停，可继续（startJob 恢复）
	StatusCancelled      Status = "cancelled"
	StatusAwaitingReview Status = "awaiting_review"
	StatusReviewDone     Status = "review_done" // 人类审查完成（人工显式标记），归档前的门禁，避免误触归档
	StatusCompleted      Status = "completed"
	StatusArchived       Status = "archived" // 手动归档冻结：不迁移 commit，直接冻结为归档态（临时 fix/archive 分支保留，工作区清空）
	StatusFailed         Status = "failed"
	// 归档阶段：人类审查完成后，把 approved 任务迁移到归档分支并做最终回归审查。
	StatusArchiving              Status = "archiving"                // 归档进行中（迁移任务 + 最终回归）
	StatusAwaitingArchiveConfirm Status = "awaiting_archive_confirm" // 归档+最终回归完成，等待人工确认
	StatusArchiveBlocked         Status = "archive_blocked"          // 归档卡点：某任务迁移受阻，等待人工介入（retry/continue/skip）
)

type TaskStatus string

const (
	TaskPending   TaskStatus = "pending"
	TaskFixing    TaskStatus = "fixing"
	TaskReviewing TaskStatus = "reviewing"
	TaskPassed    TaskStatus = "passed"
	TaskSkipped   TaskStatus = "skipped"  // 无需单独修复：已被之前的修复顺带解决 / 本就无需改动
	TaskDeferred  TaskStatus = "deferred" // 手动跳过：不再处理
	TaskFailed    TaskStatus = "failed"
	TaskApproved  TaskStatus = "approved"
	TaskRejected  TaskStatus = "rejected"
	// 归档卡点：任务在归档迁移时重试耗尽，等待人工指令（retry/continue/skip）。
	TaskArchiveBlocked TaskStatus = "archive_blocked"
)

// Finished 判断 job 是否已完结（完结后不再产生 AI 调用，约束模板的 live 变更/删除不再影响它）。
func (s Status) Finished() bool {
	return s == StatusCompleted || s == StatusArchived || s == StatusCancelled || s == StatusFailed
}

type Task struct {
	ID            string       `json:"id"`
	Title         string       `json:"title"`
	Description   string       `json:"description"`
	Severity      string       `json:"severity"`
	Files         []string     `json:"files"`
	Status        TaskStatus   `json:"status"`
	Retries       int          `json:"retries"`
	Commits       []string     `json:"commits,omitempty"`
	Review        *Review      `json:"review,omitempty"`
	Human         *HumanReview `json:"human,omitempty"`
	Archived      bool         `json:"archived,omitempty"`       // 是否已归档：归档流程中迁移到归档分支后标记为 true
	ArchiveCommit string       `json:"archive_commit,omitempty"` // 归档分支上该任务对应的 commit 哈希
	Note          string       `json:"note,omitempty"`           // 人工备注：不参与处理流程
	Suggestion    string       `json:"suggestion,omitempty"`     // 人工建议：特别标记，后续可能据此重审/重修
	LastError     string       `json:"last_error,omitempty"`     // 最近一次失败原因（修复/自检/审查/提交等），供前端排查
	// 归档卡点指令：任务迁移受阻后的人工处理指令，取值 "" | "retry" | "continue" | "skip"；
	// 由 resolve 接口写入，归档 runner 在下一次续跑时消费并清空。
	ArchiveAction string `json:"archive_action,omitempty"` // 归档卡点人工指令
	ArchiveNote   string `json:"archive_note,omitempty"`   // 归档卡点人工补充意见（retry 时喂给 AI）
}

type Review struct {
	Verdict  string    `json:"verdict"` // "pass" | "fail"
	Summary  string    `json:"summary"`
	Findings []Finding `json:"findings"`
}

type Finding struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	Severity string `json:"severity"`
	Desc     string `json:"desc"`
	Commit   string `json:"commit,omitempty"` // 问题来源 commit（宏观回归审查时定位，供归档定点处理）
}

type HumanReview struct {
	Verdict string `json:"verdict"` // "approved" | "rejected"
	Note    string `json:"note"`
}

// DecomposeNote 拆解/质检阶段被忽略或剔除的一项内容及原因，供前端展示。
type DecomposeNote struct {
	Item   string `json:"item"`   // 被忽略的报告片段 / 被剔除的任务标题（可含描述）
	Reason string `json:"reason"` // 原因说明
}
