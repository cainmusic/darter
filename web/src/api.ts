export type JobStatus =
  | 'draft' | 'parsing' | 'reviewing_tasks' | 'ready' | 'running'
  | 'paused' | 'cancelled' | 'awaiting_review' | 'review_done' | 'completed' | 'archived' | 'failed'
  | 'archiving' | 'awaiting_archive_confirm' | 'archive_blocked'

export type TaskStatus =
  | 'pending' | 'fixing' | 'reviewing' | 'passed'
  | 'skipped' | 'failed' | 'approved' | 'rejected' | 'deferred' | 'archive_blocked'

export interface Job {
  id: string
  name: string
  repo_path: string
  base_branch: string
  fix_branch: string
  report_source: { type: string; value: string }
  constraints?: string
  constraint_template_id?: string
  status: JobStatus
  tasks_frozen: boolean
  current_task_id?: string
  max_retries: number
  claude_cmd: string
  claude_model?: string
  regression?: Review
  archive_branch?: string
  archive_review?: Review
  remaining_findings?: Finding[]
  decompose_ignored?: DecomposeNote[]
  decompose_removed?: DecomposeNote[]
  redecompose_reason?: string
  pause_requested?: boolean
  created_at: string
  updated_at: string
}

export interface Finding { file: string; line: number; severity: string; desc: string; commit?: string }
export interface Review { verdict: string; summary: string; findings: Finding[] }
export interface DecomposeNote { item: string; reason: string }
export interface HumanReview { verdict: string; note: string }
export interface Attempt { task_id: string; attempt: number; review: Review; diff: string; error?: string }
export interface AiReview { id: string; task_id: string; prompt?: string; review: Review; error?: string; created_at: string }
export interface AiActivity {
  pid: number
  phase: string
  task_id?: string
  task_title?: string
  cmd: string
  started_at?: string
  elapsed_seconds: number
}

export interface Task {
  id: string
  title: string
  description: string
  severity: string
  files: string[]
  status: TaskStatus
  retries: number
  commits: string[]
  review?: Review
  human?: HumanReview
  archived?: boolean
  archive_commit?: string
  note?: string
  suggestion?: string
  last_error?: string
  archive_action?: string
  archive_note?: string
}

export interface Evt {
  type: string
  job_id: string
  task_id?: string
  message?: string
  data?: any
  ts: string
}

export interface Settings {
  claude_cmd: string
  claude_model: string
}

export interface JobDetail { job: Job; tasks: Task[] }

export interface CreateJobInput {
  name: string
  repo_path: string
  base_branch: string
  report_source: { type: string; value: string }
  constraints?: string
  constraint_template_id?: string
  max_retries?: number
}

async function api<T>(path: string, opts: RequestInit = {}): Promise<T> {
  const res = await fetch(path, {
    headers: { 'Content-Type': 'application/json' },
    ...opts,
  })
  if (!res.ok) {
    let msg = res.statusText
    try {
      const b = await res.json()
      if (b?.error) msg = b.error
    } catch { /* ignore */ }
    throw new Error(msg)
  }
  return res.json()
}

export const listJobs = () => api<Job[]>('/api/jobs')
export const getJob = (id: string) => api<JobDetail>(`/api/jobs/${id}`)
export const getEvents = (id: string) => api<Evt[]>(`/api/jobs/${id}/history`)
export const createJob = (body: CreateJobInput) => api<Job>('/api/jobs', { method: 'POST', body: JSON.stringify(body) })
export const parseJob = (id: string) => api<Job>(`/api/jobs/${id}/parse`, { method: 'POST' })
export const redecomposeJob = (id: string, reason = '') => api<Job>(`/api/jobs/${id}/redecompose`, { method: 'POST', body: JSON.stringify({ reason }) })
export const startJob = (id: string) => api<Job>(`/api/jobs/${id}/start`, { method: 'POST' })
export const cancelJob = (id: string) => api<{ status: string }>(`/api/jobs/${id}/cancel`, { method: 'POST' })
export const pauseJob = (id: string) => api<{ status: string }>(`/api/jobs/${id}/pause`, { method: 'POST' })
export const unpauseJob = (id: string) => api<{ status: string }>(`/api/jobs/${id}/unpause`, { method: 'POST' })
export const markReviewComplete = (id: string, done: boolean) =>
  api<Job>(`/api/jobs/${id}/review-complete`, { method: 'POST', body: JSON.stringify({ done }) })
export const startArchive = (id: string) => api<Job>(`/api/jobs/${id}/archive`, { method: 'POST' })
export const archiveFreeze = (id: string) => api<Job>(`/api/jobs/${id}/archive-freeze`, { method: 'POST' })
export const redoAll = (id: string) => api<Job>(`/api/jobs/${id}/redo-all`, { method: 'POST' })
export const confirmArchive = (id: string) => api<Job>(`/api/jobs/${id}/archive/confirm`, { method: 'POST' })
export const resolveArchiveTask = (id: string, tid: string, action: 'retry' | 'continue' | 'skip', note = '') =>
  api<Job>(`/api/jobs/${id}/archive/tasks/${tid}/resolve`, { method: 'POST', body: JSON.stringify({ action, note }) })
export const getTask = (id: string, tid: string) => api<{ task: Task; patch: string; attempts: Attempt[] }>(`/api/jobs/${id}/tasks/${tid}`)
export const approveTask = (id: string, tid: string, note = '') =>
  api<Task>(`/api/jobs/${id}/tasks/${tid}/approve`, { method: 'POST', body: JSON.stringify({ note }) })
export const rejectTask = (id: string, tid: string, note = '') =>
  api<Task>(`/api/jobs/${id}/tasks/${tid}/reject`, { method: 'POST', body: JSON.stringify({ note }) })
export const unapproveTask = (id: string, tid: string) =>
  api<Task>(`/api/jobs/${id}/tasks/${tid}/unapprove`, { method: 'POST' })
export const listAiReviews = (id: string, tid: string) =>
  api<AiReview[]>(`/api/jobs/${id}/tasks/${tid}/ai-reviews`)
export const aiReviewTask = (id: string, tid: string, prompt = '') =>
  api<{ started: boolean }>(`/api/jobs/${id}/tasks/${tid}/ai-review`, { method: 'POST', body: JSON.stringify({ prompt }) })
export const listAiActivity = (id: string) => api<AiActivity[]>(`/api/jobs/${id}/ai-activity`)
export const deferTask = (id: string, tid: string) => api<Task>(`/api/jobs/${id}/tasks/${tid}/defer`, { method: 'POST' })
export const annotateTask = (id: string, tid: string, note: string, suggestion: string) =>
  api<{ status?: string; task_id?: string; note?: string; suggestion?: string }>(`/api/jobs/${id}/tasks/${tid}/annotation`, { method: 'PUT', body: JSON.stringify({ note, suggestion }) })
export const getSettings = () => api<Settings>('/api/settings')
export const saveSettings = (s: Settings) => api<Settings>('/api/settings', { method: 'PUT', body: JSON.stringify(s) })

export interface ConstraintModule { id: string; name: string; content: string }
export interface ConstraintTemplate { id: string; name: string; modules: ConstraintModule[] }

export interface TemplateUser { id: string; name: string; status: string }
export interface ConstraintTemplateDetail { template: ConstraintTemplate; used_by: TemplateUser[] }

export const listConstraintTemplates = () => api<ConstraintTemplate[]>('/api/constraint-templates')
export const getConstraintTemplate = (id: string) => api<ConstraintTemplateDetail>(`/api/constraint-templates/${id}`)
export const copyConstraintTemplate = (id: string) =>
  api<ConstraintTemplate>(`/api/constraint-templates/${id}/copy`, { method: 'POST' })
export const createConstraintTemplate = (name: string, modules: ConstraintModule[]) =>
  api<ConstraintTemplate>('/api/constraint-templates', { method: 'POST', body: JSON.stringify({ name, modules }) })
export const updateConstraintTemplate = (id: string, name: string, modules: ConstraintModule[]) =>
  api<ConstraintTemplate>(`/api/constraint-templates/${id}`, { method: 'PUT', body: JSON.stringify({ name, modules }) })
export const deleteConstraintTemplate = (id: string) =>
  api<{ status: string }>(`/api/constraint-templates/${id}`, { method: 'DELETE' })

// 把模板各模块详情按顺序拼成一份约束文本（用于只读预览/展示）。
export function concatTemplateContent(t: ConstraintTemplate): string {
  return (t.modules ?? []).map((m) => m.content).filter((c) => c.trim() !== '').join('\n\n')
}

// ---- AI 提示词模板 ----

export interface Scene {
  key: string
  name: string
}

export interface PromptItem {
  scene: string
  function: string
  key: string
  name: string
  description: string
  placeholders: string[]
  content: string
}

export const listPrompts = () => api<PromptItem[]>('/api/prompts')
export const listScenes = () => api<Scene[]>('/api/scenes')

export const updateJobConstraints = (id: string, body: { constraint_template_id: string; constraints: string }) =>
  api<Job>(`/api/jobs/${id}/constraints`, { method: 'PUT', body: JSON.stringify(body) })

// 下载「问题描述」md（group: 'passed'|'failed'|'skipped'，空串为全部问题），触发浏览器下载。
export async function downloadProblems(id: string, group = ''): Promise<void> {
  const q = group ? `?group=${group}` : ''
  const res = await fetch(`/api/jobs/${id}/problems${q}`)
  if (!res.ok) throw new Error(res.statusText)
  const blob = await res.blob()
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = group ? `problems-${group}.md` : 'problems.md'
  document.body.appendChild(a)
  a.click()
  a.remove()
  URL.revokeObjectURL(url)
}

// ---- 代码迁移（kind=migration）----

export type MigStatus =
  | 'mig_draft' | 'mig_ready' | 'mig_running' | 'mig_auto' | 'mig_conflict'
  | 'mig_completed' | 'mig_cancelled' | 'mig_failed'

export type CommitState = 'pending' | 'migrating' | 'migrated' | 'conflict' | 'skipped' | 'failed'

export interface MigrationSpec {
  source_branch: string
  target_base: string
  target_branch: string
  start_commit?: string
  end_commit?: string
  max_retries?: number
}

export interface MigJob {
  id: string
  kind: string
  name: string
  repo_path: string
  status: MigStatus
  migration: MigrationSpec
  regression?: Review
  created_at: string
  updated_at: string
}

export interface CommitEntry {
  hash: string
  subject: string
  message: string
  author: string
  date: string
  is_merge?: boolean
  empty?: boolean
  state: CommitState
  target_commit?: string
  mark?: string
  skip_reason?: string
  last_error?: string
}

export interface ConflictRecord {
  id: string
  source_hash: string
  files: string[]
  handler: string
  resolution: string
  note: string
  target_commit?: string
  ai_attempts?: string[]
  created_at: string
  resolved_at?: string
}

export interface CommitInfo {
  hash: string
  subject: string
  message: string
  author: string
  date: string
  parents?: string[]
  is_merge?: boolean
}

export interface MigrationDetail {
  job: MigJob
  commits: CommitEntry[]
  conflicts: ConflictRecord[]
  target_commits: CommitInfo[]
  base_commit?: CommitInfo
  source_has_base?: boolean
}

export interface CreateMigrationInput {
  name: string
  repo_path: string
  source_branch: string
  target_base: string
  target_branch: string
  constraints?: string
  constraint_template_id?: string
  max_retries?: number
}

export const listMigrations = () => api<MigJob[]>('/api/migrations')
export const getMigration = (id: string) => api<MigrationDetail>(`/api/migrations/${id}`)
export const createMigration = (body: CreateMigrationInput) =>
  api<MigJob>('/api/migrations', { method: 'POST', body: JSON.stringify(body) })
export const migrateCommit = (id: string, hash: string) =>
  api<MigrationDetail>(`/api/migrations/${id}/commits/${hash}/migrate`, { method: 'POST' })
export const setMigrationCommitMark = (id: string, hash: string, mark: string) =>
  api<MigrationDetail>(`/api/migrations/${id}/commits/${hash}/mark`, { method: 'POST', body: JSON.stringify({ mark }) })
export const ignoreMigrationCommit = (id: string, hash: string, ignored: boolean) =>
  api<MigrationDetail>(`/api/migrations/${id}/commits/${hash}/ignore`, { method: 'POST', body: JSON.stringify({ ignored }) })
export const mergeMigrationCommits = (id: string, hashes: string[]) =>
  api<MigrationDetail>(`/api/migrations/${id}/merge`, { method: 'POST', body: JSON.stringify({ hashes }) })
export const setMigrationCommitTarget = (id: string, hash: string, targetCommit: string) =>
  api<MigrationDetail>(`/api/migrations/${id}/commits/${hash}/target`, { method: 'POST', body: JSON.stringify({ target_commit: targetCommit }) })
export const migrateRange = (id: string, start: string, end: string) =>
  api<MigrationDetail>(`/api/migrations/${id}/range`, { method: 'POST', body: JSON.stringify({ start, end }) })
export const ignoreMigrationRange = (id: string, start: string, end: string) =>
  api<MigrationDetail>(`/api/migrations/${id}/ignore-range`, { method: 'POST', body: JSON.stringify({ start, end }) })
export const resolveMigrationConflict = (id: string, action: 'continue' | 'abort' | 'skip', note = '') =>
  api<MigrationDetail>(`/api/migrations/${id}/conflict/resolve`, { method: 'POST', body: JSON.stringify({ action, note }) })
export const aiResolveMigrationConflict = (id: string) =>
  api<{ started: boolean }>(`/api/migrations/${id}/conflict/ai`, { method: 'POST' })
export const cancelAiResolveConflict = (id: string) =>
  api<{ stopped: boolean }>(`/api/migrations/${id}/conflict/cancel`, { method: 'POST' })
export const autoMigrate = (id: string) =>
  api<{ started: boolean }>(`/api/migrations/${id}/auto-migrate`, { method: 'POST' })
export const cancelAutoMigrate = (id: string) =>
  api<{ stopped: boolean }>(`/api/migrations/${id}/auto-migrate/cancel`, { method: 'POST' })
export const regressionMigration = (id: string) =>
  api<{ started: boolean }>(`/api/migrations/${id}/regression`, { method: 'POST' })
export const resetMigrationTarget = (id: string, ref: string) =>
  api<MigrationDetail>(`/api/migrations/${id}/reset`, { method: 'POST', body: JSON.stringify({ ref }) })
export const completeMigration = (id: string) =>
  api<MigrationDetail>(`/api/migrations/${id}/complete`, { method: 'POST' })
