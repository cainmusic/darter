<script setup lang="ts">
import { onMounted, onBeforeUnmount, ref, computed, h } from 'vue'
import { useRoute } from 'vue-router'
import {
  NButton, NCard, NInput, NTag, NSpace, NAlert,
  NSpin, NEmpty, NModal, NSelect, useMessage, useDialog,
} from 'naive-ui'
import {
  getMigration, migrateCommit, migrateRange, resolveMigrationConflict,
  aiResolveMigrationConflict, cancelAiResolveConflict, autoMigrate, cancelAutoMigrate, regressionMigration, resetMigrationTarget, completeMigration,
  setMigrationCommitMark, ignoreMigrationCommit, ignoreMigrationRange, mergeMigrationCommits, setMigrationCommitTarget,
  listAiActivity, listConstraintTemplates, updateJobConstraints, concatTemplateContent, renderConstraintPreview,
  getMigrationRepoState, cleanMigrationRepo,
  type MigrationDetail, type CommitEntry, type CommitState, type CommitInfo, type ConflictRecord, type ConstraintTemplate, type MigrationRepoState,
} from '../api'
import StatusBadge from '../components/StatusBadge.vue'
import AiActivityButton from '../components/AiActivityButton.vue'
import ConstraintVarsEditor from '../components/ConstraintVarsEditor.vue'

const route = useRoute()
const message = useMessage()
const dialog = useDialog()
const id = route.params.id as string

const detail = ref<MigrationDetail | null>(null)
const loading = ref(false)
const initialized = ref(false)
const busy = ref(false)
const showDetail = ref(false)
const showRetarget = ref(false)
const retargetHash = ref<string | null>(null)
const retargetTarget = ref<string | null>(null)
const selectedTargetHash = ref<string | null>(null)
const showRangeAction = ref(false)
const rangeEndHash = ref<string | null>(null)
const hideSkipped = ref(false)
const scrollContainer = ref<HTMLElement | null>(null)
const showCommitDetail = ref(false)
const commitDetail = ref<{ hash: string; subject: string; message: string; author: string; date: string; skip_reason?: string } | null>(null)
const showConflictDetail = ref(false)
const conflictDetailTargetHash = ref<string | null>(null)
const showAiHistory = ref(false)

function openCommitDetail(c: { hash: string; subject: string; message: string; author: string; date: string; skip_reason?: string }) {
  commitDetail.value = c
  showCommitDetail.value = true
}

const startHash = ref<string | null>(null)
const conflictNote = ref('')

// 约束：查看/更换任务约束（同审查修复，支持引用模板或内联文本；任务进行中更换在后续 AI 调用生效）
const showConstraint = ref(false)
const constraintTemplates = ref<ConstraintTemplate[]>([])
const constraintTemplateId = ref<string | null>(null)
const constraintText = ref('')
const constraintVars = ref<Record<string, string>>({})
const savingConstraint = ref(false)
const showPreview = ref(false)

// 预览：把「迁移任务内置变量 + 自定义注入变量」替换进当前约束文本后展示。
const previewText = computed(() => {
  const base = currentConstraintTemplate.value
    ? concatTemplateContent(currentConstraintTemplate.value)
    : constraintText.value
  const builtin: Record<string, string> = {}
  const mig = job.value?.migration
  if (mig) {
    builtin['migration.source_branch'] = mig.source_branch
    builtin['migration.target_base'] = mig.target_base
    builtin['migration.target_branch'] = mig.target_branch
  }
  return renderConstraintPreview(base, { ...builtin, ...(constraintVars.value ?? {}) })
})

// 仓库状态：展示当前项目状态（当前分支/进行中操作/冲突文件/工作区），支持手动清理残留。
const showRepoState = ref(false)
const repoState = ref<MigrationRepoState | null>(null)
const loadingRepoState = ref(false)
const cleaningRepo = ref(false)

const repoHasPending = computed(() => {
  const s = repoState.value?.state
  return !!(s && (s.cherry_pick || s.merge || s.rebase))
})

async function loadRepoState() {
  loadingRepoState.value = true
  try { repoState.value = await getMigrationRepoState(id) } catch (e: any) { message.error(e.message) } finally { loadingRepoState.value = false }
}

async function openRepoState() {
  showRepoState.value = true
  await loadRepoState()
}

async function confirmCleanRepo() {
  if (!repoHasPending.value) { message.info('当前没有需要清理的残留操作'); return }
  const d = dialog.warning({
    title: '清理仓库残留',
    content: '将中止进行中的 cherry-pick / merge / rebase（若有）并清理未跟踪文件，把仓库恢复到干净、可重跑的状态。确认清理吗？',
    positiveText: '清理',
    negativeText: '取消',
    onPositiveClick: async () => {
      d.loading = true
      cleaningRepo.value = true
      try {
        repoState.value = await cleanMigrationRepo(id)
        message.success('已清理')
      } catch (e: any) { message.error(e.message) } finally {
        cleaningRepo.value = false
        d.loading = false
      }
    },
  })
}

const constraintTemplateOptions = computed(() =>
  constraintTemplates.value
    .filter((t) => {
      const sc = t.scene ?? 'common'
      return sc === 'common' || sc === 'migration'
    })
    .map((t) => ({ label: t.name, value: t.id })),
)
const currentConstraintTemplate = computed(() =>
  constraintTemplates.value.find((t) => t.id === constraintTemplateId.value) ?? null,
)

async function openConstraint() {
  if (!job.value) return
  constraintTemplateId.value = job.value.constraint_template_id || null
  constraintText.value = job.value.constraints || ''
  constraintVars.value = job.value.constraint_vars ?? {}
  showConstraint.value = true
  if (!constraintTemplates.value.length) {
    try { constraintTemplates.value = await listConstraintTemplates() } catch { /* ignore */ }
  }
}

function onConstraintTemplatePick(id: string | null) {
  constraintTemplateId.value = id
  // 用模板时清空内联文本，避免「引用模板 + 内联文本」并存造成歧义
  if (id) constraintText.value = ''
  constraintVars.value = {}
}

async function saveConstraint() {
  if (!job.value) return
  savingConstraint.value = true
  try {
    await updateJobConstraints(id, {
      constraint_template_id: constraintTemplateId.value ?? '',
      constraints: constraintText.value,
      constraint_vars: constraintVars.value,
    })
    await refresh()
    message.success('已更新约束')
    showConstraint.value = false
  } catch (e: any) { message.error(e.message) } finally { savingConstraint.value = false }
}

let pollTimer: number | undefined
let es: EventSource | null = null
let refreshTimer: number | undefined

const job = computed(() => detail.value?.job)
const commits = computed(() => detail.value?.commits ?? [])
const conflicts = computed(() => detail.value?.conflicts ?? [])
// 当前查看的 commit 关联的冲突记录（source_hash 匹配，含 skip/abort 等无 target_commit 的记录）
const commitConflictRecords = computed(() => {
  if (!commitDetail.value) return []
  return conflicts.value.filter((c) => c.source_hash === commitDetail.value?.hash)
})
// AI 处理历史：按 commit 分组，commit 按「最近处理时间」倒序，组内按时间正序
const aiHistory = computed(() => {
  const byCommit = new Map<string, ConflictRecord[]>()
  for (const c of conflicts.value) {
    const arr = byCommit.get(c.source_hash) ?? []
    arr.push(c)
    byCommit.set(c.source_hash, arr)
  }
  const groups = [...byCommit.entries()].map(([hash, records]) => {
    records.sort((a, b) => (a.created_at || '').localeCompare(b.created_at || ''))
    const commit = commits.value.find((x) => x.hash === hash)
    return {
      hash,
      subject: commit?.subject ?? '',
      records,
      lastAt: records[records.length - 1]?.created_at || '',
    }
  })
  groups.sort((a, b) => b.lastAt.localeCompare(a.lastAt))
  return groups
})
const targetCommits = computed(() => detail.value?.target_commits ?? [])
const targetHead = computed(() => targetCommits.value[targetCommits.value.length - 1]?.hash ?? '')
const targetHashSet = computed(() => new Set(targetCommits.value.map((c) => c.hash)))

const retargetOptions = computed(() => {
  const list: CommitInfo[] = []
  if (baseCommit.value) list.push(baseCommit.value)
  list.push(...targetCommits.value)
  return list.map((c) => ({ label: `${short(c.hash)} ${c.subject}`, value: c.hash }))
})

const inConflict = computed(() => job.value?.status === 'mig_conflict')
const isAuto = computed(() => job.value?.status === 'mig_auto')
const isFinished = computed(() =>
  ['mig_completed', 'mig_cancelled', 'mig_failed'].includes(job.value?.status ?? ''),
)

// 当前进行中的任务标记（空串表示空闲）
const runningLabel = computed(() => {
  const st = job.value?.status ?? ''
  if (st === 'mig_auto') return '全自动迁移中'
  if (st === 'mig_running') return '迁移进行中'
  if (st === 'mig_conflict' && busy.value) return 'AI 解决冲突中'
  if (busy.value) return '处理中'
  return ''
})

// 可迁移的 commit（待迁移或失败）
const pendingCommits = computed(() =>
  commits.value.filter((c) => c.state === 'pending' || c.state === 'failed'),
)
const pendingCount = computed(() =>
  commits.value.filter((c) => c.state === 'pending' || c.state === 'failed' || c.state === 'conflict').length,
)

// 最后一个未解决的冲突
const activeConflict = computed(() => {
  for (let i = conflicts.value.length - 1; i >= 0; i--) {
    if (!conflicts.value[i].resolution) return conflicts.value[i]
  }
  return null
})

// 一条冲突记录是否代表「真正解决了冲突并继续迁移」（abort/skip 只是中止/跳过，不产生 target commit）。
function isResolvedConflict(r: ConflictRecord): boolean {
  return r.resolution === 'resolved' || r.resolution === 'continue'
}

// 当前查看的冲突详情：目标 commit hash → 该 hash 上的冲突解决记录（target_commit 精确匹配），按源 commit 分组。
const conflictDetail = computed(() => {
  const th = conflictDetailTargetHash.value
  if (!th) return null
  const recs = conflicts.value.filter((c) => c.target_commit === th && isResolvedConflict(c))
  if (!recs.length) return null
  const groups: { commit: CommitEntry | null; records: ConflictRecord[] }[] = []
  const byHash = new Map<string, ConflictRecord[]>()
  for (const r of recs) {
    const arr = byHash.get(r.source_hash) ?? []
    arr.push(r)
    byHash.set(r.source_hash, arr)
  }
  for (const [h, rs] of byHash) {
    groups.push({ commit: commits.value.find((c) => c.hash === h) ?? null, records: rs })
  }
  return { items: groups }
})

function handlerLabel(h: string) {
  if (h === 'ai') return 'AI 自动解决'
  if (h === 'human') return '人工解决'
  return h || '未记录'
}
function handlerStyle(h: string) {
  if (h === 'ai') return { background: 'var(--cy-cyan-bg)', color: 'var(--cy-cyan)' }
  if (h === 'human') return { background: 'var(--cy-amber-bg)', color: 'var(--cy-amber)' }
  return { background: 'var(--cy-muted-bg)', color: 'var(--cy-muted)' }
}
function resolutionLabel(r: string) {
  if (r === 'resolved' || r === 'continue') return '已解决'
  if (r === 'abort') return '中止'
  if (r === 'skip') return '跳过'
  return r || '未解决'
}
function resolutionStyle(r: string) {
  if (r === 'resolved' || r === 'continue') return { background: 'var(--cy-green-bg)', color: 'var(--cy-green)' }
  if (r === 'abort') return { background: 'var(--cy-red-bg)', color: 'var(--cy-red)' }
  if (r === 'skip') return { background: 'var(--cy-skip-bg)', color: 'var(--cy-skip)' }
  return { background: 'var(--cy-muted-bg)', color: 'var(--cy-muted)' }
}

const commitStateMeta: Record<CommitState, { label: string; key: string }> = {
  pending: { label: '待迁移', key: 'muted' },
  migrating: { label: '迁移中', key: 'amber' },
  migrated: { label: '已迁移', key: 'green' },
  conflict: { label: '冲突', key: 'red' },
  skipped: { label: '已跳过', key: 'skip' },
  failed: { label: '失败', key: 'red' },
}

// 已选起点的源 commit 下标（-1 = 未选）
const startIndex = computed(() => {
  if (!startHash.value) return -1
  return commits.value.findIndex((c) => c.hash === startHash.value)
})

// diff 比对：两个分支都严格拓扑序（后端 ListCommitsRange 已保证），
// 用 target_commit 精确匹配做 LCS 对齐——相同顺序的 commit 对齐到同一行，
// 顺序颠倒体现为增删行（一侧有、另一侧空），正确展示两个分支的顺序对应关系。
const diffRows = computed(() => {
  const src = hideSkipped.value
    ? commits.value.filter((c) => c.state !== 'skipped')
    : commits.value
  const dst = targetCommits.value
  const match = (s: CommitEntry, d: CommitInfo) =>
    s.target_commit !== undefined && s.target_commit !== '' && s.target_commit === d.hash
  const n = src.length
  const m = dst.length
  const dp: number[][] = Array.from({ length: n + 1 }, () => new Array<number>(m + 1).fill(0))
  for (let i = n - 1; i >= 0; i--) {
    for (let j = m - 1; j >= 0; j--) {
      dp[i][j] = match(src[i], dst[j]) ? dp[i + 1][j + 1] + 1 : Math.max(dp[i + 1][j], dp[i][j + 1])
    }
  }
  const rows: { source: CommitEntry | null; target: CommitInfo | null }[] = []
  let i = 0
  let j = 0
  while (i < n && j < m) {
    if (match(src[i], dst[j])) {
      rows.push({ source: src[i], target: dst[j] })
      i++; j++
    } else if (dp[i + 1][j] > dp[i][j + 1]) {
      rows.push({ source: src[i], target: null })
      i++
    } else {
      rows.push({ source: null, target: dst[j] })
      j++
    }
  }
  while (i < n) { rows.push({ source: src[i], target: null }); i++ }
  while (j < m) { rows.push({ source: null, target: dst[j] }); j++ }
  return rows
})

const baseCommit = computed(() => detail.value?.base_commit ?? null)
const sourceHasBase = computed(() => detail.value?.source_has_base ?? false)

// 目标 hash → 源 commit（用于「目标单独」行显示对应源 hash）
const sourceByTarget = computed(() => {
  const m = new Map<string, CommitEntry>()
  commits.value.forEach((c) => {
    if (c.target_commit) m.set(c.target_commit, c)
  })
  return m
})

function hasTargetConflict(targetHash: string): boolean {
  return conflicts.value.some((c) => c.target_commit === targetHash && isResolvedConflict(c))
}
function openTargetConflictDetail(targetHash: string) {
  if (!hasTargetConflict(targetHash)) return
  conflictDetailTargetHash.value = targetHash
  showConflictDetail.value = true
}

function short(h: string) { return h.slice(0, 8) }
function fmt(t: string) { return new Date(t).toLocaleString() }
function idxOf(hash: string) { return commits.value.findIndex((c) => c.hash === hash) }

async function refresh() {
  loading.value = true
  try { detail.value = await getMigration(id) } catch (e: any) { message.error(e.message) } finally { loading.value = false }
}

function applyDetail(d: MigrationDetail) {
  detail.value = d
}

async function run(op: Promise<MigrationDetail>) {
  if (busy.value) return // 重入保护：操作进行中不再触发
  busy.value = true
  try {
    const d = await op
    applyDetail(d)
    if (d.job.status === 'mig_conflict') {
      message.warning('遇到冲突，等待处理')
    }
  } catch (e: any) { message.error(e.message) } finally { busy.value = false }
}

function confirmMigrate(hash: string) {
  const c = commits.value.find((x) => x.hash === hash)
  const d = dialog.warning({
    title: '迁移 commit',
    content: `是否迁移该 commit？\n${c ? short(c.hash) + ' ' + c.subject : short(hash)}`,
    positiveText: '迁移',
    negativeText: '关闭',
    onPositiveClick: async () => {
      d.loading = true
      await run(migrateCommit(id, hash))
    },
  })
}

const MARK_ORDER = ['', 'star', 'triangle', 'square', 'circle']
const markMeta: Record<string, { glyph: string; color: string; bg: string }> = {
  star: { glyph: '★', color: 'var(--cy-amber)', bg: 'var(--cy-amber-bg)' },
  triangle: { glyph: '▲', color: 'var(--cy-red)', bg: 'var(--cy-red-bg)' },
  square: { glyph: '■', color: 'var(--cy-cyan)', bg: 'var(--cy-cyan-bg)' },
  circle: { glyph: '●', color: 'var(--cy-green)', bg: 'var(--cy-green-bg)' },
}

function cycleMark(hash: string, current: string) {
  const idx = MARK_ORDER.indexOf(current)
  const next = MARK_ORDER[(idx + 1) % MARK_ORDER.length]
  run(setMigrationCommitMark(id, hash, next))
}

function mergeToHere(hash: string) {
  const idx = targetCommits.value.findIndex((c) => c.hash === hash)
  if (idx < 0) return
  const list = targetCommits.value.slice(idx)
  if (list.length < 2) { message.warning('至少选择两个 commit 合并'); return }
  const hashes = list.map((c) => c.hash)
  const d = dialog.warning({
    title: '合并 commit',
    content: () => h('div', { style: 'line-height: 1.8' }, [
      h('div', `将合并以下 ${list.length} 个连续 commit 为一个：`),
      ...list.map((c) => h('div', `· ${short(c.hash)} ${c.subject}`)),
    ]),
    positiveText: '合并',
    negativeText: '关闭',
    onPositiveClick: async () => {
      d.loading = true
      await run(mergeMigrationCommits(id, hashes))
    },
  })
}

function toggleSelectTarget(hash: string) {
  selectedTargetHash.value = selectedTargetHash.value === hash ? null : hash
}

function scrollToLatest() {
  if (!targetHead.value) return
  document.querySelector(`[data-target-hash="${targetHead.value}"]`)?.scrollIntoView({ behavior: 'smooth', block: 'center' })
}

function scrollToTop() {
  scrollContainer.value?.scrollTo({ top: 0, behavior: 'smooth' })
}

function scrollToBottom() {
  const el = scrollContainer.value
  if (el) el.scrollTo({ top: el.scrollHeight, behavior: 'smooth' })
}

function confirmAutoMigrate() {
  const pending = commits.value.filter((c) => c.state === 'pending' || c.state === 'failed')
  if (!pending.length) { message.warning('没有待迁移的 commit'); return }
  const start = pending[0].hash
  const end = pending[pending.length - 1].hash
  const d = dialog.warning({
    title: '自动迁移',
    content: `将从源分支最上面的待迁移 commit 开始，逐个迁移（自动跳过已迁移和忽略的 commit），直到出现冲突或全部迁移完成。共 ${pending.length} 个待迁移 commit。确认吗？`,
    positiveText: '开始自动迁移',
    negativeText: '关闭',
    onPositiveClick: async () => {
      d.loading = true
      await run(migrateRange(id, start, end))
    },
  })
}

function confirmAutoMigrateAll() {
  const pending = commits.value.filter((c) => c.state === 'pending' || c.state === 'failed')
  if (!pending.length) { message.warning('没有待迁移的 commit'); return }
  const d = dialog.warning({
    title: '全自动迁移',
    content: () => h('div', { style: 'line-height: 1.8' }, [
      h('div', `将按列表依次迁移 ${pending.length} 个待迁移 commit，`),
      h('div', '遇到冲突时自动调用 AI 解决，解决后继续，直到全部完成。'),
      h('div', 'AI 连续 3 次解决失败时会停下，保留冲突现场等人工处理。'),
    ]),
    positiveText: '开始全自动迁移',
    negativeText: '关闭',
    onPositiveClick: async () => {
      d.loading = true
      try {
        await autoMigrate(id)
        busy.value = true
        message.info('全自动迁移已开始，稍后自动刷新')
        startAutoPolling()
      } catch (e: any) {
        message.error(e.message)
      } finally {
        d.loading = false
      }
    },
  })
}

function openRetarget(hash: string) {
  retargetHash.value = hash
  retargetTarget.value = null
  showRetarget.value = true
}

async function confirmRetarget() {
  if (!retargetHash.value || !retargetTarget.value) { message.warning('请选择目标 commit'); return }
  await run(setMigrationCommitTarget(id, retargetHash.value, retargetTarget.value))
  showRetarget.value = false
  retargetHash.value = null
}

function setIgnore(hash: string, ignored: boolean) {
  run(ignoreMigrationCommit(id, hash, ignored))
}

function setStart(hash: string) { startHash.value = hash }
function cancelStart() { startHash.value = null }

function openRangeAction(endHash: string) {
  if (!startHash.value) { message.warning('请先设置起点'); return }
  rangeEndHash.value = endHash
  showRangeAction.value = true
}

async function doIgnoreRange() {
  if (!startHash.value || !rangeEndHash.value) return
  await run(ignoreMigrationRange(id, startHash.value, rangeEndHash.value))
  rangeEndHash.value = null
  startHash.value = null
}

async function doMigrateRange() {
  if (!startHash.value || !rangeEndHash.value) return
  await run(migrateRange(id, startHash.value, rangeEndHash.value))
  rangeEndHash.value = null
  startHash.value = null
}

function confirmIgnoreRange() {
  if (!startHash.value || !rangeEndHash.value) return
  showRangeAction.value = false
  const d = dialog.warning({
    title: '确认忽略',
    content: '将把起点到终点的所有未迁移 commit 标记为忽略，此操作较重。确认吗？',
    positiveText: '确认忽略',
    negativeText: '关闭',
    onPositiveClick: async () => {
      d.loading = true
      await doIgnoreRange()
    },
  })
}

function confirmMigrateRange() {
  if (!startHash.value || !rangeEndHash.value) return
  showRangeAction.value = false
  const d = dialog.warning({
    title: '确认迁移',
    content: '将按顺序连续迁移起点到终点的 commit，此操作较重。确认吗？',
    positiveText: '确认迁移',
    negativeText: '关闭',
    onPositiveClick: async () => {
      d.loading = true
      await doMigrateRange()
    },
  })
}

async function resolveNow(action: 'continue' | 'abort' | 'skip') {
  await run(resolveMigrationConflict(id, action, conflictNote.value))
  conflictNote.value = ''
}

function startAiPolling() {
  if (pollTimer) clearInterval(pollTimer)
  pollTimer = window.setInterval(async () => {
    try {
      const d = await getMigration(id)
      applyDetail(d)
      if (d.job.status !== 'mig_conflict') {
        clearInterval(pollTimer)
        pollTimer = undefined
        busy.value = false
        message.success('冲突已解决')
      }
    } catch { /* ignore */ }
  }, 3000)
}

// 轮询全自动迁移进度，直到状态离开 mig_auto。
function startAutoPolling() {
  if (pollTimer) clearInterval(pollTimer)
  pollTimer = window.setInterval(async () => {
    try {
      const d = await getMigration(id)
      applyDetail(d)
      if (d.job.status !== 'mig_auto') {
        clearInterval(pollTimer)
        pollTimer = undefined
        busy.value = false
        if (d.job.status === 'mig_completed') message.success('全自动迁移完成')
        else if (d.job.status === 'mig_conflict') message.warning('全自动迁移遇到无法解决的冲突，已停下等人工处理')
        else message.info('全自动迁移已结束')
      }
    } catch { /* ignore */ }
  }, 3000)
}

// 离开页面再回来时，检测是否有 AI 仍在后台解决冲突；有则恢复 busy + 轮询，避免重复触发 AI 操作。
async function detectRunningAi() {
  try {
    const act = await listAiActivity(id)
    if (act.length > 0) {
      busy.value = true
      startAiPolling()
    }
  } catch { /* ignore */ }
}

async function aiResolve() {
  if (busy.value) return // 重入保护
  busy.value = true
  try {
    await aiResolveMigrationConflict(id)
    message.info('AI 正在后台解决冲突，稍后自动刷新')
    startAiPolling()
  } catch (e: any) {
    busy.value = false
    message.error(e.message)
  }
}

// 停止正在后台运行的 AI 冲突解决：后端取消 context 并中止 cherry-pick，轮询会自动感知状态变化。
async function stopAi() {
  try {
    await cancelAiResolveConflict(id)
    message.info('已请求停止 AI，正在清理工作区…')
  } catch (e: any) {
    message.error(e.message)
  }
}

// 停止正在后台运行的全自动迁移：后端取消 context，轮询会自动感知状态变化。
async function stopAutoMigrate() {
  try {
    await cancelAutoMigrate(id)
    message.info('已请求停止全自动迁移，正在清理…')
  } catch (e: any) {
    message.error(e.message)
  }
}

// 全量回归审查：审查迁移累积改动，发现问题走最多 3 轮「修复 + 审查」（异步后台，靠 AI 活动检测完成）。
function confirmRegression() {
  const d = dialog.warning({
    title: '全量回归审查',
    content: () => h('div', { style: 'line-height: 1.8' }, [
      h('div', '对迁移目标分支的累积改动做一次完整性审查，只挑明显问题。'),
      h('div', '若发现问题，自动走最多 3 轮「修复 + 审查」。'),
    ]),
    positiveText: '开始回归审查',
    negativeText: '关闭',
    onPositiveClick: async () => {
      d.loading = true
      try {
        await regressionMigration(id)
        busy.value = true
        message.info('全量回归审查已开始，稍后自动刷新')
        startRegressionPolling()
      } catch (e: any) {
        message.error(e.message)
      } finally {
        d.loading = false
      }
    },
  })
}

// 轮询 AI 活动：回归审查是纯后台 AI 任务，活动消失即完成。
function startRegressionPolling() {
  if (pollTimer) clearInterval(pollTimer)
  pollTimer = window.setInterval(async () => {
    try {
      const act = await listAiActivity(id)
      if (act.length === 0) {
        clearInterval(pollTimer)
        pollTimer = undefined
        busy.value = false
        await refresh()
        const reg = detail.value?.job.regression
        if (reg) {
          if (reg.verdict === 'pass') message.success('全量回归审查通过')
          else message.warning('全量回归审查发现问题，请查看结果')
        }
      }
    } catch { /* ignore */ }
  }, 3000)
}

function confirmReset(hash: string, subject: string) {
  const d = dialog.warning({
    title: '重置到该 commit',
    content: () => h('div', { style: 'line-height: 1.8' }, [
      h('div', `将目标分支硬重置到 ${short(hash)}（${subject}）。`),
      h('div', '注意：重置会清掉比它更新的全部历史迁移记录，'),
      h('div', '对应源 commit 的「已迁移」状态会回退为「待迁移」。'),
      h('div', '请自行取舍后确认。'),
    ]),
    positiveText: '确认重置',
    negativeText: '关闭',
    onPositiveClick: async () => {
      d.loading = true
      await run(resetMigrationTarget(id, hash))
    },
  })
}

function confirmComplete() {
  const d = dialog.warning({
    title: '完成迁移任务',
    content: `该操作不可逆。${pendingCount.value > 0 ? `还有 ${pendingCount.value} 个 commit 未迁移。` : ''}确认完成吗？`,
    positiveText: '确认完成',
    negativeText: '关闭',
    onPositiveClick: async () => {
      d.loading = true
      await run(completeMigration(id))
    },
  })
}

onMounted(async () => {
  await refresh()
  await detectRunningAi()
  if (job.value?.status === 'mig_auto') {
    busy.value = true
    startAutoPolling()
  }
  initialized.value = true
  // 订阅实时事件：任何 migration 事件都防抖刷新列表（批量迁移/忽略、AI 冲突解决等都会推事件）
  es = new EventSource(`/api/jobs/${id}/events`)
  es.onmessage = (e) => {
    try {
      const ev = JSON.parse(e.data)
      // busy 时（本页正在执行操作）跳过，避免和 run() 的 applyDetail 竞态/闪烁；
      // 非 busy（其它页面/标签页操作、后台 AI 之外的事件）才实时刷新。
      if (ev?.type && ev.type.startsWith('migration_') && !busy.value) {
        if (refreshTimer) clearTimeout(refreshTimer)
        refreshTimer = window.setTimeout(() => { refresh() }, 300)
      }
    } catch { /* ignore */ }
  }
})
onBeforeUnmount(() => {
  if (pollTimer) clearInterval(pollTimer)
  if (refreshTimer) clearTimeout(refreshTimer)
  es?.close()
})
</script>

<template>
  <div style="padding: 8px; height: 100%; box-sizing: border-box; display: flex; flex-direction: column; gap: 8px">
    <template v-if="initialized">
      <div style="display:flex;align-items:center;justify-content:space-between;flex-wrap:wrap;gap:8px">
        <div style="display:flex;align-items:center;gap:10px;min-width:0">
          <span class="font-display cy-title" style="font-size:18px">{{ job?.name }}</span>
          <StatusBadge :status="job?.status ?? ''" />
        </div>
        <n-space>
          <span v-if="runningLabel" style="display:inline-flex;align-items:center;gap:6px;font-size:12px;color:var(--cy-amber)">
            <span class="pulse-dot" style="width:7px;height:7px;border-radius:50%;background:var(--cy-amber);box-shadow:0 0 6px var(--cy-amber)"></span>
            {{ runningLabel }}
          </span>
          <n-button size="small" @click="refresh">刷新</n-button>
          <n-button size="small" @click="openConstraint">约束</n-button>
          <n-button size="small" @click="showAiHistory = true">AI 处理历史</n-button>
          <n-button size="small" @click="openRepoState">仓库状态</n-button>
          <AiActivityButton :job-id="id" />
          <n-button size="small" @click="showDetail = true">详情</n-button>
          <n-button size="small" type="error" :disabled="isFinished || busy" @click="confirmComplete">完成</n-button>
        </n-space>
      </div>

      <!-- 冲突处理 -->
      <n-alert v-if="inConflict" type="error" :bordered="false">
        <div style="font-size:13px;margin-bottom:6px">
          迁移遇到冲突（commit <span class="glow-cyan">{{ short(activeConflict?.source_hash ?? '') }}</span>），冲突文件：
          <span v-for="f in activeConflict?.files" :key="f" class="glow-amber" style="margin-right:6px">{{ f }}</span>
        </div>
        <div style="font-size:12px;color:var(--cy-muted);margin-bottom:8px">
          在本地解决冲突并 git add 后点「继续」；或让 AI 自动解决；「中止」回到待迁移，「跳过」标记跳过。
        </div>
        <n-space>
          <n-input v-model:value="conflictNote" placeholder="处理说明（可选）" style="width:240px" />
          <n-button size="small" type="primary" :disabled="busy" @click="resolveNow('continue')">继续（已手动解决）</n-button>
          <n-button size="small" type="info" :disabled="busy" @click="aiResolve">AI 解决</n-button>
          <n-button v-if="busy" size="small" type="error" @click="stopAi">停止 AI</n-button>
          <n-button size="small" :disabled="busy" @click="resolveNow('abort')">中止</n-button>
          <n-button size="small" :disabled="busy" @click="resolveNow('skip')">跳过</n-button>
        </n-space>
      </n-alert>

      <!-- diff 式左右对照：按 commit msg 对齐，每行一行内容，撑满剩余高度滚动 -->
      <n-card class="cy-card" :bordered="false"
        style="flex:1; min-height:0; display:flex; flex-direction:column"
        content-style="padding:0; flex:1; min-height:0; display:flex; flex-direction:column">
        <div ref="scrollContainer" style="flex:1; min-height:0; overflow-y:auto">
          <div style="display:flex;border-bottom:1px solid var(--cy-border);color:var(--cy-muted);font-size:11px;font-weight:600;position:sticky;top:0;background:var(--cy-panel);z-index:1">
            <div style="flex:6;padding:6px 8px;border-right:1px solid var(--cy-border);display:flex;align-items:center;gap:8px">
              <span>源分支（待迁移）</span>
              <n-button size="tiny" quaternary @click="hideSkipped = !hideSkipped">{{ hideSkipped ? '显示已跳过' : '隐藏已跳过' }}</n-button>
              <n-button size="tiny" quaternary :disabled="isFinished || busy" @click="confirmAutoMigrate">自动迁移</n-button>
              <n-button size="tiny" quaternary :disabled="isFinished || busy || isAuto" @click="confirmAutoMigrateAll">全自动迁移</n-button>
              <n-button v-if="isAuto" size="tiny" quaternary type="error" @click="stopAutoMigrate">停止全自动</n-button>
              <n-button size="tiny" quaternary :disabled="busy || inConflict || isAuto" @click="confirmRegression">全量回归审查</n-button>
            </div>
            <div style="flex:4;padding:6px 8px;display:flex;align-items:center;gap:8px">
              <span>目标分支（已迁移）</span>
              <n-button size="tiny" quaternary @click="scrollToLatest">最新</n-button>
              <n-button size="tiny" quaternary @click="scrollToTop">最上</n-button>
              <n-button size="tiny" quaternary @click="scrollToBottom">最下</n-button>
            </div>
          </div>

          <n-empty v-if="!diffRows.length" description="无 commit" style="padding:20px 0" />

          <div v-if="baseCommit" style="display:flex;border-bottom:1px solid var(--cy-border)">
            <div style="flex:6;min-width:0;padding:4px 8px;border-right:1px solid var(--cy-border);display:flex;align-items:center;gap:8px">
              <template v-if="sourceHasBase">
                <span style="flex-shrink:0;font-size:11px;color:var(--cy-muted);font-family:var(--font-mono);min-width:22px;text-align:right">0</span>
                <span style="flex-shrink:0;padding:0 4px;color:var(--cy-muted);font-size:12px">○</span>
                <span style="font-size:12px;color:var(--cy-muted);flex-shrink:0;font-family:var(--font-mono)">{{ short(baseCommit.hash) }}</span>
                <n-tag size="tiny" :bordered="false" style="flex-shrink:0;background:var(--cy-muted-bg);color:var(--cy-muted)">基底</n-tag>
                <span style="flex:1;min-width:0;font-size:13px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis" :title="baseCommit.subject + ' · ' + baseCommit.author">{{ baseCommit.subject }}</span>
                <n-button size="tiny" quaternary style="flex-shrink:0;padding:0 4px" @click="openCommitDetail(baseCommit)">详情</n-button>
              </template>
              <span v-else style="color:var(--cy-muted);font-size:12px">—</span>
            </div>
            <div style="flex:4;min-width:0;padding:4px 8px;display:flex;align-items:center;gap:8px">
              <span style="font-size:12px;color:var(--cy-green);flex-shrink:0;font-family:var(--font-mono)">{{ short(baseCommit.hash) }}</span>
              <n-tag size="tiny" :bordered="false" style="flex-shrink:0;background:var(--cy-muted-bg);color:var(--cy-muted)">基底</n-tag>
              <span style="flex:1;min-width:0;font-size:13px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis" :title="baseCommit.subject + ' · ' + baseCommit.author">{{ baseCommit.subject }}</span>
              <n-button size="tiny" quaternary style="flex-shrink:0;padding:0 4px" @click="openCommitDetail(baseCommit)">详情</n-button>
              <n-button size="tiny" type="warning" style="flex-shrink:0" :disabled="isFinished || busy" :loading="busy" @click="confirmReset(baseCommit.hash, baseCommit.subject)">重置至此</n-button>
            </div>
          </div>

          <div v-for="(r, ri) in diffRows" :key="ri" style="display:flex;border-bottom:1px solid var(--cy-border)">
            <div :style="r.source?.mark ? { background: markMeta[r.source.mark]?.bg } : undefined"
              style="flex:6;min-width:0;padding:4px 8px;border-right:1px solid var(--cy-border);display:flex;align-items:center;gap:8px">
              <template v-if="r.source">
                <span style="flex-shrink:0;font-size:11px;color:var(--cy-muted);font-family:var(--font-mono);min-width:22px;text-align:right">{{ idxOf(r.source.hash) + 1 }}</span>
                <span :style="`flex-shrink:0;padding:0 4px;font-size:12px;color:${markMeta[r.source.mark ?? '']?.color ?? 'var(--cy-muted)'}${isFinished ? '' : ';cursor:pointer'}`"
                  :title="r.source.mark ? `标记：${r.source.mark}（点击切换）` : '点击标记'"
                  @click="!isFinished && cycleMark(r.source.hash, r.source.mark ?? '')">{{ markMeta[r.source.mark ?? '']?.glyph ?? '○' }}</span>
                <span style="font-size:12px;color:var(--cy-cyan);flex-shrink:0;font-family:var(--font-mono)">{{ short(r.source.hash) }}</span>
                <n-tag size="tiny" :bordered="false" style="flex-shrink:0"
                  :style="{ background: `var(--cy-${commitStateMeta[r.source.state].key}-bg)`, color: `var(--cy-${commitStateMeta[r.source.state].key})` }">
                  {{ commitStateMeta[r.source.state].label }}
                </n-tag>
                <n-button v-if="r.source.state !== 'skipped'" size="tiny" quaternary style="flex-shrink:0;padding:0 4px" :disabled="isFinished" title="改迁移目标" @click="openRetarget(r.source.hash)">改目标</n-button>
                <n-button size="tiny" quaternary style="flex-shrink:0;padding:0 4px" @click="openCommitDetail(r.source)">详情</n-button>
                <span style="flex:1;min-width:0;font-size:13px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis"
                  :title="r.source.last_error ? r.source.subject + ' · ' + r.source.author + ' — ' + r.source.last_error : r.source.subject + ' · ' + r.source.author">{{ r.source.subject }}</span>
                <template v-if="r.source.state === 'pending' || r.source.state === 'failed'">
                  <n-button v-if="!startHash" size="tiny" style="flex-shrink:0" :disabled="isFinished || busy" @click="setStart(r.source.hash)">设为起点</n-button>
                  <n-button v-else-if="idxOf(r.source.hash) === startIndex" size="tiny" type="warning" style="flex-shrink:0" :disabled="isFinished || busy" @click="cancelStart">取消起点</n-button>
                  <n-button v-else-if="idxOf(r.source.hash) > startIndex" size="tiny" style="flex-shrink:0" :disabled="isFinished || busy" @click="openRangeAction(r.source.hash)">设为终点</n-button>
                  <n-button size="tiny" style="flex-shrink:0" :disabled="isFinished || busy" @click="setIgnore(r.source.hash, true)">忽略</n-button>
                  <n-button size="tiny" type="primary" style="flex-shrink:0" :disabled="isFinished || busy" :loading="busy" @click="confirmMigrate(r.source.hash)">迁移</n-button>
                </template>
                <n-button v-else-if="r.source.state === 'skipped'" size="tiny" style="flex-shrink:0" :disabled="isFinished || busy" @click="setIgnore(r.source.hash, false)">恢复</n-button>
              </template>
              <span v-else-if="r.target" style="flex-shrink:0;font-size:11px;color:var(--cy-muted);font-family:var(--font-mono)">← {{ sourceByTarget.get(r.target.hash) ? short(sourceByTarget.get(r.target.hash)!.hash) : '?' }}</span>
              <span v-else style="color:var(--cy-muted);font-size:12px">—</span>
            </div>
            <div :style="(r.target?.hash && r.target.hash === selectedTargetHash) ? { background: 'color-mix(in srgb, var(--cy-amber) 32%, transparent)' } : undefined"
              style="flex:4;min-width:0;padding:4px 8px;display:flex;align-items:center;gap:8px">
              <template v-if="r.target">
                <span :data-target-hash="r.target.hash" style="font-size:12px;color:var(--cy-green);flex-shrink:0;font-family:var(--font-mono);cursor:pointer" @click="toggleSelectTarget(r.target.hash)">{{ short(r.target.hash) }}</span>
                <n-button size="tiny" quaternary style="flex-shrink:0;padding:0 4px" @click="openCommitDetail(r.target)">详情</n-button>
                <n-button v-if="hasTargetConflict(r.target.hash)" size="tiny" quaternary style="flex-shrink:0;padding:0 4px;color:var(--cy-red)" title="查看冲突解决详情" @click="openTargetConflictDetail(r.target.hash)">冲突详情</n-button>
                <span style="flex:1;min-width:0;font-size:13px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis" :title="r.target.subject + ' · ' + r.target.author">{{ r.target.subject }}</span>
                <n-button v-if="r.target.hash !== targetHead" size="tiny" style="flex-shrink:0" :disabled="isFinished || busy" @click="mergeToHere(r.target.hash)">合并至此</n-button>
                <n-button v-if="r.target.hash !== targetHead" size="tiny" type="warning" style="flex-shrink:0" :disabled="isFinished || busy" :loading="busy" @click="confirmReset(r.target.hash, r.target.subject)">重置至此</n-button>
              </template>
              <span v-else-if="r.source && r.source.target_commit" style="flex-shrink:0;font-size:11px;font-family:var(--font-mono);cursor:pointer"
                :style="{ color: targetHashSet.has(r.source.target_commit) ? 'var(--cy-green)' : 'var(--cy-red)', background: selectedTargetHash === r.source.target_commit ? 'color-mix(in srgb, var(--cy-amber) 32%, transparent)' : 'transparent' }"
                @click="toggleSelectTarget(r.source.target_commit)">→ {{ short(r.source.target_commit) }}{{ targetHashSet.has(r.source.target_commit) ? '' : ' (不在目标列表)' }}</span>
              <span v-else style="color:var(--cy-muted);font-size:12px">—</span>
            </div>
          </div>
        </div>
      </n-card>
    </template>

    <n-spin v-else style="margin-top:80px" />

    <n-modal v-model:show="showDetail" preset="card" title="任务详情" style="max-width:640px">
      <div style="font-size:13px;line-height:2.1">
        <div>名称：{{ job?.name || '—' }}</div>
        <div>仓库：{{ job?.repo_path || '—' }}</div>
        <div>源分支：<span class="glow-cyan">{{ job?.migration?.source_branch || '—' }}</span></div>
        <div>目标基底：{{ job?.migration?.target_base || '—' }}</div>
        <div>目标分支：<span class="glow-green">{{ job?.migration?.target_branch || '—' }}</span></div>
        <div>创建：{{ job ? fmt(job.created_at) : '—' }}</div>
        <div v-if="job?.regression" style="margin-top:8px;border-top:1px solid var(--cy-border);padding-top:8px">
          <div style="display:flex;align-items:center;gap:6px">
            <span>全量回归审查：</span>
            <n-tag size="tiny" :bordered="false"
              :style="job.regression.verdict === 'pass' ? { background: 'var(--cy-green-bg)', color: 'var(--cy-green)' } : { background: 'var(--cy-red-bg)', color: 'var(--cy-red)' }">
              {{ job.regression.verdict === 'pass' ? '通过' : '发现问题' }}
            </n-tag>
          </div>
          <div style="font-size:12px;color:var(--cy-muted)">{{ job.regression.summary }}</div>
          <div v-for="(f, i) in job.regression.findings" :key="i" style="font-size:12px;color:var(--cy-amber);margin-top:4px">
            · {{ f.file }}<span v-if="f.line">:{{ f.line }}</span> [{{ f.severity }}] {{ f.desc }}
          </div>
        </div>
      </div>
      <template #footer>
        <n-space justify="end">
          <n-button @click="showDetail = false">关闭</n-button>
        </n-space>
      </template>
    </n-modal>

    <n-modal v-model:show="showRetarget" preset="card" title="改迁移目标" style="max-width:480px">
      <div style="font-size:13px;line-height:2.1;margin-bottom:12px">
        为源 commit <span class="glow-cyan">{{ short(retargetHash ?? '') }}</span> 手动指定它在目标分支上的迁移结果：
      </div>
      <n-select v-model:value="retargetTarget" :options="retargetOptions" placeholder="选择目标分支上的 commit" filterable />
      <template #footer>
        <n-space justify="end">
          <n-button @click="showRetarget = false">取消</n-button>
          <n-button type="primary" :disabled="!retargetTarget" @click="confirmRetarget">确认</n-button>
        </n-space>
      </template>
    </n-modal>

    <n-modal v-model:show="showRangeAction" preset="card" title="选择操作" style="max-width:480px">
      <div style="font-size:13px;line-height:2">
        <div>起点：<span class="glow-cyan">{{ startHash ? short(startHash) : '—' }}</span></div>
        <div>终点：<span class="glow-green">{{ rangeEndHash ? short(rangeEndHash) : '—' }}</span></div>
        <div style="color:var(--cy-muted);font-size:12px;margin-top:8px">「忽略」将把起点到终点的所有未迁移 commit 标记为忽略；「迁移」将按顺序连续迁移。</div>
      </div>
      <template #footer>
        <div style="display:flex;align-items:center;justify-content:space-between">
          <n-button :disabled="busy" @click="confirmIgnoreRange">忽略</n-button>
          <n-space>
            <n-button @click="showRangeAction = false">关闭</n-button>
            <n-button type="primary" :disabled="busy" :loading="busy" @click="confirmMigrateRange">迁移</n-button>
          </n-space>
        </div>
      </template>
    </n-modal>

    <n-modal v-model:show="showCommitDetail" preset="card" title="Commit 详情" style="max-width:640px">
      <div v-if="commitDetail" style="font-size:13px;line-height:1.8">
        <div>Hash：<span style="font-family:var(--font-mono)">{{ commitDetail.hash }}</span></div>
        <div>作者：{{ commitDetail.author }}</div>
        <div>时间：{{ fmt(commitDetail.date) }}</div>
        <div v-if="commitDetail.skip_reason" style="margin-top:4px;color:var(--cy-amber)">跳过原因：{{ commitDetail.skip_reason }}</div>
        <div v-for="rec in commitConflictRecords" :key="rec.id"
          style="margin-top:8px;border:1px solid var(--cy-border);border-radius:4px;padding:8px 10px;background:var(--cy-hover-bg)">
          <div style="font-size:12px;color:var(--cy-muted)">冲突处理（{{ resolutionLabel(rec.resolution) }}）</div>
          <div v-if="rec.note" style="white-space:pre-wrap;word-break:break-all;font-size:12px;margin-top:4px">{{ rec.note }}</div>
          <div v-if="rec.ai_attempts?.length" style="margin-top:6px">
            <div v-for="(a, ai) in rec.ai_attempts" :key="ai" style="font-size:12px;color:var(--cy-muted);white-space:pre-wrap;word-break:break-all">{{ a }}</div>
          </div>
        </div>
        <div style="margin-top:8px">Message：</div>
        <div style="white-space:pre-wrap;word-break:break-all;font-size:12px;background:var(--cy-hover-bg);border:1px solid var(--cy-border);border-radius:4px;padding:8px 10px">{{ commitDetail.message }}</div>
      </div>
      <template #footer>
        <n-space justify="end">
          <n-button @click="showCommitDetail = false">关闭</n-button>
        </n-space>
      </template>
    </n-modal>

    <n-modal v-model:show="showAiHistory" preset="card" title="AI 处理历史" style="max-width:760px">
      <div style="max-height:70vh;overflow:auto;padding-right:4px">
        <n-empty v-if="!aiHistory.length" description="暂无处理记录" style="margin:24px 0" />
        <div v-for="g in aiHistory" :key="g.hash"
          style="margin-bottom:12px;border:1px solid var(--cy-border);border-radius:4px;padding:10px 12px">
          <div style="display:flex;align-items:center;gap:8px">
            <span style="font-family:var(--font-mono);color:var(--cy-cyan)">{{ short(g.hash) }}</span>
            <span style="font-size:13px;font-weight:600;overflow:hidden;text-overflow:ellipsis;white-space:nowrap">{{ g.subject }}</span>
          </div>
          <div v-for="rec in g.records" :key="rec.id"
            style="margin-top:8px;padding-left:12px;border-left:2px solid var(--cy-border)">
            <div style="display:flex;align-items:center;gap:8px;flex-wrap:wrap">
              <n-tag size="tiny" :bordered="false" :style="handlerStyle(rec.handler)">{{ handlerLabel(rec.handler) }}</n-tag>
              <n-tag size="tiny" :bordered="false" :style="resolutionStyle(rec.resolution)">{{ resolutionLabel(rec.resolution) }}</n-tag>
              <span style="font-size:12px;color:var(--cy-muted)">{{ fmt(rec.created_at) }}</span>
            </div>
            <div v-if="rec.note" style="font-size:12px;white-space:pre-wrap;word-break:break-all;margin-top:4px">{{ rec.note }}</div>
            <div v-if="rec.ai_attempts?.length" style="margin-top:4px">
              <div v-for="(a, ai) in rec.ai_attempts" :key="ai" style="font-size:12px;color:var(--cy-muted);white-space:pre-wrap;word-break:break-all">{{ a }}</div>
            </div>
          </div>
        </div>
      </div>
      <template #footer>
        <n-space justify="end">
          <n-button @click="showAiHistory = false">关闭</n-button>
        </n-space>
      </template>
    </n-modal>

    <n-modal v-model:show="showConflictDetail" preset="card" title="冲突解决详情" style="max-width:640px">
      <div v-if="conflictDetail" style="font-size:13px;line-height:1.8">
        <div v-for="(item, ii) in conflictDetail.items" :key="ii">
          <div style="margin:8px 0 4px;padding:6px 8px;border-radius:4px;background:var(--cy-panel)">
            源 commit：<span class="glow-cyan" style="font-family:var(--font-mono)">{{ item.commit ? short(item.commit.hash) : '?' }}</span>
            <span style="color:var(--cy-muted)">　{{ item.commit?.subject ?? '' }}</span>
          </div>
          <div v-for="rec in item.records" :key="rec.id"
          style="margin-top:10px;border:1px solid var(--cy-border);border-radius:4px;padding:10px;background:var(--cy-hover-bg)">
          <div style="display:flex;align-items:center;gap:8px;flex-wrap:wrap">
            <n-tag size="tiny" :bordered="false" :style="handlerStyle(rec.handler)">{{ handlerLabel(rec.handler) }}</n-tag>
            <n-tag size="tiny" :bordered="false" :style="resolutionStyle(rec.resolution)">{{ resolutionLabel(rec.resolution) }}</n-tag>
            <span style="font-size:12px;color:var(--cy-muted)">{{ fmt(rec.created_at) }}</span>
            <span v-if="rec.resolved_at" style="font-size:12px;color:var(--cy-muted)">→ 解决于 {{ fmt(rec.resolved_at) }}</span>
          </div>
          <div v-if="rec.files?.length" style="margin-top:8px">
            <span style="color:var(--cy-muted);font-size:12px">冲突文件：</span>
            <span v-for="f in rec.files" :key="f" class="glow-amber" style="margin-right:6px;font-size:12px">{{ f }}</span>
          </div>
          <div v-if="rec.note" style="margin-top:8px">
            <div style="color:var(--cy-muted);font-size:12px">{{ rec.handler === 'ai' ? 'AI 解决说明' : '处理说明' }}：</div>
            <div style="white-space:pre-wrap;word-break:break-all;font-size:12px;margin-top:4px;background:var(--cy-panel);border-radius:4px;padding:8px 10px">{{ rec.note }}</div>
          </div>
          <div v-if="rec.ai_attempts?.length" style="margin-top:8px">
            <div style="color:var(--cy-muted);font-size:12px">AI 尝试记录：</div>
            <div v-for="(a, ai) in rec.ai_attempts" :key="ai" style="white-space:pre-wrap;word-break:break-all;font-size:12px;color:var(--cy-muted);margin-top:2px">{{ a }}</div>
          </div>
          </div>
        </div>
      </div>
      <template #footer>
        <n-space justify="end">
          <n-button @click="showConflictDetail = false">关闭</n-button>
        </n-space>
      </template>
    </n-modal>

    <!-- 约束查看/更换弹窗 -->
    <n-modal v-model:show="showConstraint" preset="card" title="任务约束" style="max-width:640px">
      <div style="font-size:12px;color:var(--cy-muted);margin-bottom:12px">
        约束随模板实时读取；任务进行中更换后在后续 AI 调用（冲突解决/回归审查）生效。
      </div>
      <n-select v-model:value="constraintTemplateId" :options="constraintTemplateOptions"
        placeholder="选择约束模板（留空则使用下方内联文本）" clearable
        style="margin-bottom:12px" @update:value="onConstraintTemplatePick" />
      <template v-if="constraintTemplateId">
        <n-alert type="info" :bordered="false" style="margin-bottom:12px">
          <div style="font-size:12px;color:var(--cy-muted);margin-bottom:4px">
            已引用模板：{{ currentConstraintTemplate?.name ?? constraintTemplateId }}
          </div>
          <span style="white-space:pre-wrap;font-size:13px">{{ currentConstraintTemplate ? concatTemplateContent(currentConstraintTemplate) : '（模板内容加载中…）' }}</span>
        </n-alert>
        <ConstraintVarsEditor :variables="currentConstraintTemplate?.variables ?? []" v-model:values="constraintVars" />
      </template>
      <template v-else>
        <div style="font-size:12px;color:var(--cy-muted);margin-bottom:6px">内联约束文本</div>
        <n-input v-model:value="constraintText" type="textarea" :rows="6"
          placeholder="如：不引入第三方依赖 / 保持兼容性 / 遵循某代码规范..." />
      </template>
      <div v-if="showPreview" style="margin-top:12px;border-top:1px solid var(--cy-border);padding-top:12px">
        <div style="font-size:12px;color:var(--cy-muted);margin-bottom:6px">约束预览（已注入任务变量与自定义变量）</div>
        <div style="max-height:40vh;overflow:auto;white-space:pre-wrap;word-break:break-all;font-size:13px;background:var(--cy-panel);border:1px solid var(--cy-border);border-radius:4px;padding:10px 12px">{{ previewText }}</div>
      </div>
      <template #footer>
        <n-space justify="space-between">
          <n-button @click="showPreview = !showPreview">{{ showPreview ? '收起预览' : '预览' }}</n-button>
          <n-space>
            <n-button @click="showConstraint = false">取消</n-button>
            <n-button type="primary" :loading="savingConstraint" @click="saveConstraint">保存</n-button>
          </n-space>
        </n-space>
      </template>
    </n-modal>

    <!-- 仓库状态弹窗 -->
    <n-modal v-model:show="showRepoState" preset="card" title="仓库状态" style="max-width:640px">
      <div v-if="loadingRepoState" style="text-align:center;padding:20px 0;color:var(--cy-muted)">加载中…</div>
      <template v-else-if="repoState">
        <div style="font-size:13px;line-height:2">
          <div>当前分支：<span class="glow-cyan">{{ repoState.state.branch || '—' }}</span></div>
          <div>目标分支：<span :style="repoState.state.branch === repoState.target_branch ? 'color:var(--cy-green,#18a058)' : 'color:var(--cy-amber)'">{{ repoState.target_branch }}</span>
            <span v-if="repoState.state.branch !== repoState.target_branch" style="color:var(--cy-amber);font-size:12px">　（当前不在目标分支）</span>
          </div>
          <div>进行中操作：
            <span v-if="repoState.state.cherry_pick" class="glow-amber">cherry-pick　</span>
            <span v-if="repoState.state.merge" class="glow-amber">merge　</span>
            <span v-if="repoState.state.rebase" class="glow-amber">rebase　</span>
            <span v-if="!repoState.state.cherry_pick && !repoState.state.merge && !repoState.state.rebase" style="color:var(--cy-muted)">无</span>
          </div>
          <div v-if="repoState.state.conflicts?.length" style="margin-top:6px">
            <div style="color:var(--cy-muted);font-size:12px">冲突文件：</div>
            <div v-for="f in repoState.state.conflicts" :key="f" class="glow-amber" style="font-size:12px">{{ f }}</div>
          </div>
          <div>工作区：<span :style="repoState.state.dirty ? 'color:var(--cy-amber)' : 'color:var(--cy-green,#18a058)'">{{ repoState.state.dirty ? '有未提交改动' : '干净' }}</span></div>
        </div>
        <n-alert v-if="repoHasPending" type="warning" :bordered="false" style="margin-top:12px">
          检测到残留的进行中操作，可能导致迁移无法启动，可点击下方「清理残留」恢复。
        </n-alert>
      </template>
      <template #footer>
        <n-space justify="space-between">
          <n-button type="warning" :disabled="!repoHasPending" :loading="cleaningRepo" @click="confirmCleanRepo">清理残留</n-button>
          <n-space>
            <n-button @click="loadRepoState" :loading="loadingRepoState">刷新</n-button>
            <n-button @click="showRepoState = false">关闭</n-button>
          </n-space>
        </n-space>
      </template>
    </n-modal>
  </div>
</template>
