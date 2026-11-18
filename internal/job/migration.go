package job

import "time"

// Job kind 取值。
const (
	KindReviewFixer = "review-fixer"
	KindMigration   = "migration"
)

// 迁移 job 状态（Status 为共享 string 类型；迁移用 mig_ 前缀避免与 review-fixer 状态冲突）。
const (
	MigStatusDraft     Status = "mig_draft"
	MigStatusReady     Status = "mig_ready"
	MigStatusRunning   Status = "mig_running"  // 连续迁移进行中（瞬时态，崩溃恢复时复位）
	MigStatusAuto      Status = "mig_auto"     // 全自动迁移进行中（瞬时态，崩溃恢复时复位）
	MigStatusConflict  Status = "mig_conflict" // 停在冲突，等待人工/AI 处理
	MigStatusCompleted Status = "mig_completed"
	MigStatusCancelled Status = "mig_cancelled"
	MigStatusFailed    Status = "mig_failed"
)

// MigrationSpec 代码迁移任务的配置（kind=migration）。
type MigrationSpec struct {
	SourceBranch string `json:"source_branch"`          // 源分支：要迁移的 commit 所在分支
	TargetBase   string `json:"target_base"`            // 目标基底：目标分支从其切出
	TargetBranch string `json:"target_branch"`          // 目标分支：从 TargetBase 切出，接收 cherry-pick
	StartCommit  string `json:"start_commit,omitempty"` // 连续迁移起点（源分支 commit hash）
	EndCommit    string `json:"end_commit,omitempty"`   // 连续迁移终点（源分支 commit hash）
	MaxRetries   int    `json:"max_retries,omitempty"`  // 冲突解决/回归修复的重试次数（默认 5）
}

// CommitState 单个源 commit 的迁移状态。
type CommitState string

const (
	CommitPending   CommitState = "pending"   // 待迁移
	CommitMigrating CommitState = "migrating" // 迁移中（瞬时态，崩溃恢复时复位为 pending）
	CommitMigrated  CommitState = "migrated"  // 已迁移到目标分支
	CommitConflict  CommitState = "conflict"  // 冲突，等待处理
	CommitSkipped   CommitState = "skipped"   // 人工跳过
	CommitFailed    CommitState = "failed"    // 失败（重试耗尽等）
)

// CommitEntry 源分支上一个 commit 的迁移条目。
type CommitEntry struct {
	Hash         string      `json:"hash"`
	Subject      string      `json:"subject"` // 标题行（迁移匹配的关键，cherry-pick 不改标题）
	Message      string      `json:"message"` // 完整提交信息
	Author       string      `json:"author"`
	Date         time.Time   `json:"date"`
	IsMerge      bool        `json:"is_merge,omitempty"`
	Empty        bool        `json:"empty,omitempty"` // 无内容 commit（如空 merge），列表过滤
	State        CommitState `json:"state"`
	TargetCommit string      `json:"target_commit,omitempty"` // 迁移到目标分支后的新 hash
	Mark         string      `json:"mark,omitempty"`          // 标记类型：""/star/triangle/square/circle，不同颜色区分不同用途
	SkipReason   string      `json:"skip_reason,omitempty"`   // 跳过原因（空 commit / 仅生成物 / 人工忽略 / 人工跳过）
	LastError    string      `json:"last_error,omitempty"`
}

// ConflictRecord 一次冲突处理的记录。
type ConflictRecord struct {
	ID           string    `json:"id"`
	SourceHash   string    `json:"source_hash"`
	Files        []string  `json:"files"`                   // 冲突文件
	Handler      string    `json:"handler"`                 // "human" | "ai"
	Resolution   string    `json:"resolution"`              // "resolved" | "continue" | "abort" | "skip"
	Note         string    `json:"note"`                    // 处理说明
	TargetCommit string    `json:"target_commit,omitempty"` // 解决后 continue 得到的目标 commit hash（abort/skip 为空）
	AiAttempts   []string  `json:"ai_attempts,omitempty"`   // AI 每次尝试的说明
	CreatedAt    time.Time `json:"created_at"`
	ResolvedAt   time.Time `json:"resolved_at,omitempty"`
}
