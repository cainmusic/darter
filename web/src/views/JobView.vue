<script setup lang="ts">
import { onMounted, onBeforeUnmount, ref, computed, nextTick, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { NButton, NSpace, NSpin, NEmpty, NAlert, NTag, NInput, NModal, NRadioGroup, NRadioButton, NSelect, useMessage, useDialog } from 'naive-ui'
import { getJob, parseJob, redecomposeJob, startJob, cancelJob, pauseJob, unpauseJob, getTask, getEvents, deferTask, annotateTask, downloadProblems, startArchive, archiveFreeze, redoAll, confirmArchive, resolveArchiveTask, listConstraintTemplates, updateJobConstraints, concatTemplateContent, type Job, type Task, type Evt, type Attempt, type ConstraintTemplate } from '../api'
import StatusBadge from '../components/StatusBadge.vue'
import TerminalPanel from '../components/TerminalPanel.vue'
import DiffView from '../components/DiffView.vue'
import AiReviewModal from '../components/AiReviewModal.vue'
import AiActivityButton from '../components/AiActivityButton.vue'
import ConstraintVarsEditor from '../components/ConstraintVarsEditor.vue'
import { GAP } from '../layout'

const route = useRoute()
const router = useRouter()
const message = useMessage()
const dialog = useDialog()
const id = route.params.id as string

const job = ref<Job | null>(null)
const tasks = ref<Task[]>([])
const selected = ref<Task | null>(null)
const patch = ref('')
const attempts = ref<Attempt[]>([])
const showAttempts = ref(false)
// 人工发起的 AI 审查弹窗
const aiReviewTarget = ref<Task | null>(null)
const showAiReview = ref(false)
function openAiReview(t: Task) {
  aiReviewTarget.value = t
  showAiReview.value = true
}
const loading = ref(false)
const busy = ref(false)
const logLines = ref<string[]>([])
const noteText = ref('')
const suggestionText = ref('')
const savingAnnotation = ref(false)

// 宏观回归审查（任务全部跑完后对整条分支的完整性检查）
const regression = computed(() => job.value?.regression)
// 宏观回归审查默认收起：仅当审查完成、实时收到 regression_reviewed 事件（第一次展示）时展开。
const regressionCollapsed = ref(true)

// 拆解说明：忽略的报告内容 + 对抗性预检剔除的误报任务（默认收起）
const decomposeCollapsed = ref(true)
const hasDecomposeNotes = computed(() =>
  !!(job.value?.decompose_ignored?.length || job.value?.decompose_removed?.length || job.value?.redecompose_reason),
)

// 状态详情面板：把拆解说明/回归审查/遗留问题等「参考性」信息收进一个可折叠区，
// 避免顶部堆叠太多状态栏把任务列表和详情挤没。默认收起，仅保留一行摘要。
const showStatus = ref(false)
// 实时日志右栏收起状态：收起时右侧只留一条窄边，点开再展开。
const logCollapsed = ref(false)
const noticeSummary = computed(() => {
  const parts: string[] = []
  if (hasDecomposeNotes.value) parts.push('拆解说明')
  if (regression.value) parts.push('回归审查')
  if (job.value?.remaining_findings?.length) parts.push('遗留问题')
  return parts.join(' / ')
})

// 归档分类筛选：通过 / 失败 / 跳过（通过含 passed+approved，跳过含 skipped+deferred）
const filter = ref<'all' | 'passed' | 'failed' | 'skipped'>('all')
const filterLabel = computed(() => ({ all: '全部', passed: '通过', failed: '失败', skipped: '跳过' }[filter.value]))
const filteredTasks = computed(() => {
  const f = filter.value
  if (f === 'all') return tasks.value
  return tasks.value.filter((t) => {
    if (f === 'passed') return t.status === 'passed' || t.status === 'approved'
    if (f === 'failed') return t.status === 'failed'
    return t.status === 'skipped' || t.status === 'deferred'
  })
})

// 当前正在执行的任务（运行中），用于「回当前」快捷定位
const currentTask = computed(() => currentTaskOf(tasks.value, job.value?.current_task_id))

async function jumpToCurrent() {
  const t = currentTask.value
  if (!t) { message.info('当前没有正在执行的任务'); return }
  // 若被当前分类筛选过滤掉，先切回「全部」，否则列表里找不到该任务元素
  if (filter.value !== 'all' && !filteredTasks.value.some((x) => x.id === t.id)) {
    filter.value = 'all'
  }
  await nextTick()
  itemEls.get(t.id)?.scrollIntoView({ block: 'nearest' })
}
async function downloadMd() {
  try {
    await downloadProblems(id, filter.value === 'all' ? '' : filter.value)
    message.success('已下载问题描述 md')
  } catch (e: any) {
    message.error(e.message)
  }
}

let es: EventSource | null = null

// 时间戳格式化：YYYY-MM-DD HH:mm:ss（本地时区）
function fmtTs(input: string | number | Date): string {
  const d = new Date(input)
  if (isNaN(d.getTime())) return String(input)
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`
}

function log(s: string, ts?: string | number | Date) { logLines.value.push(`[${fmtTs(ts ?? new Date())}] ${s}`) }

// 任务项 DOM 引用，用于选中后自动滚动到可见区域
const itemEls = new Map<string, HTMLElement>()
let lastCurrentId = ''

function setItemRef(id: string) {
  return (el: unknown) => {
    if (el) itemEls.set(id, el as HTMLElement)
    else itemEls.delete(id)
  }
}

async function loadDetail(t: Task) {
  patch.value = ''
  attempts.value = []
  try {
    const r = await getTask(id, t.id)
    patch.value = r.patch ?? ''
    attempts.value = r.attempts ?? []
  } catch { /* ignore */ }
}

async function scrollSelectedIntoView() {
  await nextTick()
  itemEls.get(selected.value?.id ?? '')?.scrollIntoView({ block: 'nearest' })
}

async function select(t: Task) {
  selected.value = t
  showAttempts.value = false
  await loadDetail(t)
  await scrollSelectedIntoView()
}

// 当前正在处理的任务（运行中）
function currentTaskOf(tasks: Task[], currentId?: string): Task | undefined {
  return tasks.find((t) => t.id === currentId)
    || tasks.find((t) => t.status === 'fixing' || t.status === 'reviewing')
}

async function refresh() {
  const d = await getJob(id)
  job.value = d.job
  tasks.value = d.tasks ?? []

  const current = currentTaskOf(tasks.value, d.job.current_task_id)
  const currentId = current?.id ?? ''

  // 运行中自动跟随当前任务（仅在当前任务切换时切换，尊重用户临时点选别的任务）
  if (d.job.status === 'running' && current && currentId !== lastCurrentId) {
    lastCurrentId = currentId
    await select(current)
    return
  }
  if (d.job.status !== 'running') lastCurrentId = currentId

  if (selected.value) {
    const prev = selected.value
    const found = d.tasks.find((t) => t.id === prev.id)
    if (found) {
      const changed =
        found.status !== prev.status ||
        JSON.stringify(found.commits) !== JSON.stringify(prev.commits)
      selected.value = found
      if (changed) await loadDetail(found)
    }
  }
  if (!selected.value && d.tasks.length) select(d.tasks[0])
}

// 拆解报告：首次拆解（draft）或拆解失败后重试（failed 且任务列表尚未冻结）。
// 任务已冻结的 failed 状态应走「打回重做」而非拆解报告（拆解报告会因冻结被后端 409 拒绝）。
const canParse = computed(() => ['draft', 'failed'].includes(job.value?.status ?? '') && !job.value?.tasks_frozen)
// 打回重做：任务列表已生成（frozen）、且未在进行修复/归档时可重做——ready / failed / cancelled。
const canRedecompose = computed(() =>
  !!job.value?.tasks_frozen && ['ready', 'failed', 'cancelled'].includes(job.value?.status ?? ''),
)
const canStart = computed(() =>
  ['ready', 'failed', 'cancelled', 'paused'].includes(job.value?.status ?? '') &&
  tasks.value.some((t) => t.status === 'pending'),
)
// 开始/停止修复：待开始/已暂停→「开始修复」（点击即继续），运行→「停止修复」（四字等长）
const canStop = computed(() => job.value?.status === 'running')
const canStartStop = computed(() => canStart.value || canStop.value)
// 暂停/取消暂停：running（未请求）→「暂停」；running（已请求暂停、任务仍在跑）→「取消」（撤销暂停请求）
// 两字等长，避免按钮宽度跳变
const pauseState = computed<'pause' | 'cancel-pause' | ''>(() => {
  if (job.value?.status === 'running') return job.value?.pause_requested ? 'cancel-pause' : 'pause'
  return ''
})
const pauseLabel = computed(() => pauseState.value === 'cancel-pause' ? '取消' : '暂停')
const pauseType = computed<'warning' | 'default'>(() => pauseState.value === 'cancel-pause' ? 'default' : 'warning')
const canReview = computed(() => ['awaiting_review', 'review_done', 'completed'].includes(job.value?.status ?? ''))
// 归档：只有显式「标记审查完成」(review_done) 后才可开启（也允许归档完成后重新归档补齐剩余任务）。
// awaiting_review 期间不可归档，避免在人类审查过程中误触归档。
// 不再要求「存在待归档任务」：审查完成后即使任务列表为空也开放归档按钮，
// 由归档流程判定「无待归档任务」并直接判完成。
const canArchive = computed(() =>
  ['review_done', 'awaiting_archive_confirm'].includes(job.value?.status ?? ''),
)
// 归档冻结：不跑归档迁移流程，直接把任务冻结为归档态（archived）。
// 与 completed（自动归档）区分；仅对非归档、非运行中的中间态开放（与后端 archiveFreeze 允许的状态一致）。
const canArchiveFreeze = computed(() =>
  ['ready', 'awaiting_review', 'review_done', 'failed', 'cancelled', 'paused'].includes(job.value?.status ?? ''),
)
// 全任务重做：开始修复之后、开始归档之前（含审查完成但尚未归档），抛弃当前 fix 分支从头重跑。
const canRedoAll = computed(() =>
  ['awaiting_review', 'review_done', 'paused', 'cancelled', 'failed'].includes(job.value?.status ?? ''),
)
const canConfirmArchive = computed(() => job.value?.status === 'awaiting_archive_confirm')
// 最终回归审查通过才允许确认完成；未通过（commit↔任务对应有问题）时阻断确认
const archiveReviewPassed = computed(() => job.value?.archive_review?.verdict === 'pass')

// 归档卡点：某任务迁移受阻，等待人工指令（retry/continue/skip）
const archiveBlocked = computed(() => job.value?.status === 'archive_blocked')
const blockedTask = computed(() => tasks.value.find((t) => t.status === 'archive_blocked'))
const resolveNote = ref('')

// 选中任务详情里的错误提示文案：人工驳回（human.verdict === 'rejected'）虽状态也是 failed，
// 但那是人工决定，不是「重试多次仍未通过」，需要单独措辞。
const selectedError = computed(() => {
  const t = selected.value
  if (!t) return ''
  if (t.human?.verdict === 'rejected') return t.human?.note || '该任务已被人工驳回。'
  return t.last_error || '重试多次仍未通过，请人工介入。'
})

// 任务详情：查看任务基本信息（分支、仓库、报告来源等）
const showJobDetail = ref(false)
function openJobDetail() {
  showJobDetail.value = true
}

// 约束：查看/更换任务约束（支持引用模板或内联文本；任务进行中也可更换，下一任务生效）
const showConstraint = ref(false)
const constraintTemplates = ref<ConstraintTemplate[]>([])
const constraintTemplateId = ref<string | null>(null)
const constraintText = ref('')
const constraintVars = ref<Record<string, string>>({})
const savingConstraint = ref(false)
// 预览：把「任务内置变量 + 自定义注入变量」替换进当前约束文本后展示。

const constraintTemplateOptions = computed(() =>
  constraintTemplates.value.map((t) => ({ label: t.name, value: t.id })),
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
    const j = await updateJobConstraints(id, {
      constraint_template_id: constraintTemplateId.value ?? '',
      constraints: constraintText.value,
      constraint_vars: constraintVars.value,
    })
    job.value = j
    message.success('已更新约束')
    showConstraint.value = false
  } catch (e: any) { message.error(e.message) } finally { savingConstraint.value = false }
}

async function doParse() {
  busy.value = true
  try { await parseJob(id); log('>>> 开始拆解报告'); message.info('已开始拆解') } catch (e: any) { message.error(e.message) } finally { busy.value = false }
}
// 打回重做：弹出带原因输入的弹窗，人工说明会喂给 AI 改进下次拆解/复查。
const showRedecompose = ref(false)
const redecomposeReason = ref('')
const redecomposing = ref(false)
function doRedecompose() {
  redecomposeReason.value = job.value?.redecompose_reason ?? ''
  showRedecompose.value = true
}
async function confirmRedecompose() {
  redecomposing.value = true
  try {
    const j = await redecomposeJob(id, redecomposeReason.value.trim())
    job.value = j
    tasks.value = []
    showRedecompose.value = false
    log('>>> 打回重做，重新拆解报告' + (redecomposeReason.value.trim() ? '（已附说明）' : ''))
    message.info('已打回重做，重新拆解中')
  } catch (e: any) { message.error(e.message) } finally { redecomposing.value = false }
}
async function doStart() {
  busy.value = true
  try { await startJob(id); log('>>> 开始修复循环'); message.info('已开始修复') } catch (e: any) { message.error(e.message) } finally { busy.value = false }
}
async function doCancel() {
  try { await cancelJob(id); log('>>> 已停止修复'); message.warning('已停止修复') } catch (e: any) { message.error(e.message) }
}
async function doPause() {
  try { await pauseJob(id); log('>>> 已请求暂停（当前任务完成后停止）'); message.info('将在当前任务完成后暂停') } catch (e: any) { message.error(e.message) }
}
async function doCancelPause() {
  try { await unpauseJob(id); log('>>> 已取消暂停请求'); message.info('已取消暂停，继续运行') } catch (e: any) { message.error(e.message) }
}
function doPauseAction() {
  if (pauseState.value === 'cancel-pause') return doCancelPause()
  return doPause()
}
function doStartStop() {
  if (canStop.value) doCancel()
  else doStart()
}
async function doArchive() {
  busy.value = true
  try { await startArchive(id); log('>>> 开始归档流程'); message.info('已开始归档') } catch (e: any) { message.error(e.message) } finally { busy.value = false }
}
function doArchiveFreeze() {
  dialog.warning({
    title: '归档冻结',
    content: '将直接冻结为归档态（不迁移 commit）：临时 fix/archive 分支保留，工作区切回基础分支并清空未提交改动。确定吗？',
    positiveText: '确认冻结',
    negativeText: '取消',
    onPositiveClick: async () => {
      try { await archiveFreeze(id); log('>>> 已归档冻结'); message.success('已归档冻结'); await refresh() } catch (e: any) { message.error(e.message) }
    },
  })
}
function doRedoAll() {
  dialog.warning({
    title: '全任务重做',
    content: '将抛弃当前修复分支、基于基础分支重建，并清除所有任务状态（含已通过/已驳回/已跳过，全部回到待处理）。任务列表保留。此操作不可撤销，确定吗？',
    positiveText: '确认重做',
    negativeText: '取消',
    onPositiveClick: async () => {
      try { await redoAll(id); log('>>> 已全任务重做'); message.success('已全任务重做，回到待开始'); await refresh() } catch (e: any) { message.error(e.message) }
    },
  })
}
function doConfirmArchive() {
  dialog.success({
    title: '确认完成归档',
    content: '确认最终回归审查通过并完成整体修复任务吗？',
    positiveText: '确认完成',
    negativeText: '取消',
    onPositiveClick: async () => {
      try { await confirmArchive(id); message.success('已确认，整体修复任务完成'); await refresh() } catch (e: any) { message.error(e.message) }
    },
  })
}
// 处理归档卡点：skip 不可逆，先确认；retry/continue 直接提交。
function resolveArchive(action: 'retry' | 'continue' | 'skip') {
  const t = blockedTask.value
  if (!t) return
  if (action === 'skip') {
    dialog.warning({
      title: '放弃该任务',
      content: `确定放弃 ${t.id}「${t.title}」吗？该任务将不再归档，作为遗留问题记录。`,
      positiveText: '确认放弃',
      negativeText: '取消',
      onPositiveClick: () => doResolveArchive(t, action),
    })
    return
  }
  doResolveArchive(t, action)
}
async function doResolveArchive(t: Task, action: 'retry' | 'continue' | 'skip') {
  busy.value = true
  try {
    await resolveArchiveTask(id, t.id, action, resolveNote.value)
    resolveNote.value = ''
    message.success('已提交处理，重新归档中')
    await refresh()
  } catch (e: any) { message.error(e.message) } finally { busy.value = false }
}
function doDefer(t: Task) {
  dialog.warning({
    title: '跳过任务',
    content: `确定跳过 ${t.id}「${t.title}」吗？跳过后该任务将不再处理。`,
    positiveText: '确认跳过',
    negativeText: '取消',
    onPositiveClick: async () => {
      try { await deferTask(id, t.id); message.success('已跳过，不再处理'); await refresh() } catch (e: any) { message.error(e.message) }
    },
  })
}

async function saveAnnotation() {
  if (!selected.value) return
  savingAnnotation.value = true
  try {
    await annotateTask(id, selected.value.id, noteText.value, suggestionText.value)
    message.success('已保存备注/建议')
    await refresh()
  } catch (e: any) { message.error(e.message) } finally { savingAnnotation.value = false }
}

// 手动刷新当前选中任务的最新信息（状态/重试/提交/审查/备注/建议/diff），
// 独立于 SSE 事件触发，覆盖事件未覆盖到的变化。
async function refreshCurrentTask() {
  if (!selected.value) return
  try {
    const r = await getTask(id, selected.value.id)
    selected.value = r.task
    patch.value = r.patch ?? ''
    attempts.value = r.attempts ?? []
    noteText.value = r.task.note ?? ''
    suggestionText.value = r.task.suggestion ?? ''
    message.success('已刷新当前任务')
  } catch (e: any) { message.error(e.message) }
}

watch(() => selected.value?.id, (newId, oldId) => {
  if (newId === oldId) return
  noteText.value = selected.value?.note ?? ''
  suggestionText.value = selected.value?.suggestion ?? ''
})

function formatEvent(ev: Evt): string {
  let line = ev.type
  if (ev.task_id) line += ` ${ev.task_id}`
  if (ev.message) line += ` — ${ev.message}`
  return line
}

function onEvent(ev: Evt) {
  log(formatEvent(ev), ev.ts)
  // 宏观回归审查完成后的第一次展示展开；其它场景（重进页面/后续刷新）保持收起
  if (ev.type === 'regression_reviewed') regressionCollapsed.value = false
  refresh().catch(() => {})
}

function sevColor(s: string) {
  return ({ high: 'var(--cy-red)', medium: 'var(--cy-amber)', low: 'var(--cy-cyan)' } as Record<string, string>)[s] ?? 'var(--cy-muted)'
}
function sevBg(s: string) {
  return ({ high: 'var(--cy-red-bg)', medium: 'var(--cy-amber-bg)', low: 'var(--cy-cyan-bg)' } as Record<string, string>)[s] ?? 'var(--cy-muted-bg)'
}

onMounted(async () => {
  // 先回放历史事件（避免进页面日志为空），再订阅实时 SSE
  try {
    const evts = await getEvents(id)
    logLines.value = evts.map((ev) => `[${fmtTs(ev.ts)}] ${formatEvent(ev)}`)
  } catch { /* ignore */ }
  refresh().catch((e) => message.error(e.message))
  es = new EventSource(`/api/jobs/${id}/events`)
  es.onmessage = (e) => { try { onEvent(JSON.parse(e.data)) } catch { /* ignore */ } }
})
onBeforeUnmount(() => es?.close())
</script>

<template>
  <div style="height:100%;display:flex;flex-direction:column;padding:8px">
    <!-- 头部 -->
    <div style="display:flex;align-items:center;justify-content:space-between;margin-bottom:12px">
      <div style="display:flex;align-items:center;gap:12px">
        <span class="font-display" style="font-size:16px">{{ job?.name || '...' }}</span>
        <StatusBadge v-if="job" :status="job.status" />
      </div>
      <n-space>
        <AiActivityButton :job-id="id" />
        <n-button size="small" secondary :disabled="!job" @click="openJobDetail">任务详情</n-button>
        <n-button size="small" secondary :disabled="!job" @click="openConstraint">约束</n-button>
        <n-button size="small" :disabled="!canParse" :loading="busy" @click="doParse">拆解报告</n-button>
        <n-button size="small" :disabled="!canRedecompose" @click="doRedecompose">打回重做</n-button>
        <n-button size="small" :type="canStop ? 'error' : 'primary'" :disabled="!canStartStop" @click="doStartStop">{{ canStop ? '停止修复' : '开始修复' }}</n-button>
        <n-button size="small" :type="pauseType" :disabled="!pauseState" @click="doPauseAction">{{ pauseLabel }}</n-button>
        <n-button size="small" type="success" :disabled="!canReview" @click="router.push(`/jobs/${id}/review`)">人类审查</n-button>
        <n-button size="small" type="primary" :disabled="!canArchive" @click="doArchive">开始归档</n-button>
        <n-button size="small" type="warning" :disabled="!canArchiveFreeze" @click="doArchiveFreeze">归档冻结</n-button>
        <n-button size="small" type="error" :disabled="!canRedoAll" @click="doRedoAll">全任务重做</n-button>
      </n-space>
    </div>

    <!-- 状态详情：拆解说明 / 回归审查 / 遗留问题 等参考信息，折叠成一行，避免挤占任务列表 -->
    <div v-if="noticeSummary" style="margin-bottom:12px">
      <div @click="showStatus = !showStatus"
        style="display:flex;align-items:center;gap:8px;cursor:pointer;font-size:12px;color:var(--cy-muted);padding:4px 2px;user-select:none">
        <span style="font-size:11px">{{ showStatus ? '▾' : '▸' }}</span>
        <span style="font-weight:600">状态详情</span>
        <span v-if="!showStatus" style="opacity:.7">{{ noticeSummary }}</span>
      </div>
      <div v-if="showStatus" style="max-height:36vh;overflow-y:auto;padding-right:4px;margin-top:6px">
        <n-alert v-if="hasDecomposeNotes" type="info" style="margin-bottom:12px">
          <template #header>
            <div style="display:flex;align-items:center;gap:8px">
              <span>拆解说明</span>
              <n-button size="tiny" quaternary @click="decomposeCollapsed = !decomposeCollapsed">
                {{ decomposeCollapsed ? '展开' : '收起' }}
              </n-button>
            </div>
          </template>
          <template v-if="!decomposeCollapsed">
            <div v-if="job?.redecompose_reason" style="margin-top:4px;font-size:12px;padding:6px 8px;background:var(--cy-findings-bg);border-left:2px solid var(--cy-amber);border-radius:2px">
              <span style="color:var(--cy-amber)">打回重做原因：</span>{{ job.redecompose_reason }}
            </div>
            <div v-if="job?.decompose_ignored?.length" style="margin-top:4px">
              <div style="font-size:12px;color:var(--cy-muted);margin:6px 0 4px">忽略的报告内容（{{ job.decompose_ignored.length }}）</div>
              <div v-for="(n, i) in job.decompose_ignored" :key="'ig' + i"
                style="font-size:12px;padding:6px 8px;margin-top:4px;background:var(--cy-findings-bg);border-left:2px solid var(--cy-muted);border-radius:2px">
                <div>{{ n.item }}</div>
                <div style="color:var(--cy-muted)">原因：{{ n.reason }}</div>
              </div>
            </div>
            <div v-if="job?.decompose_removed?.length" style="margin-top:8px">
              <div style="font-size:12px;color:var(--cy-muted);margin:6px 0 4px">剔除的误报任务（{{ job.decompose_removed.length }}）</div>
              <div v-for="(n, i) in job.decompose_removed" :key="'rm' + i"
                style="font-size:12px;padding:6px 8px;margin-top:4px;background:var(--cy-findings-bg);border-left:2px solid var(--cy-amber);border-radius:2px">
                <div>{{ n.item }}</div>
                <div style="color:var(--cy-muted)">原因：{{ n.reason }}</div>
              </div>
            </div>
          </template>
        </n-alert>

        <n-alert v-if="regression" :type="regression.verdict === 'pass' ? 'success' : 'warning'" style="margin-bottom:12px">
          <template #header>
            <div style="display:flex;align-items:center;gap:8px">
              <span>宏观回归审查 [{{ regression.verdict }}]</span>
              <n-button size="tiny" quaternary @click="regressionCollapsed = !regressionCollapsed">
                {{ regressionCollapsed ? '展开' : '收起' }}
              </n-button>
            </div>
          </template>
          <template v-if="!regressionCollapsed">
            <div v-if="regression.summary" style="margin-top:4px;font-size:13px">{{ regression.summary }}</div>
            <div v-if="regression.findings?.length" style="margin-top:4px">
              <div v-for="(f, i) in regression.findings" :key="i"
                style="font-size:12px;padding:6px 8px;margin-top:4px;background:var(--cy-findings-bg);border-left:2px solid var(--cy-amber);border-radius:2px">
                <span :style="{ color: sevColor(f.severity) }">[{{ f.severity }}]</span>
                {{ f.file }}:{{ f.line }} — {{ f.desc }}
                <span v-if="f.commit" class="glow-cyan" style="font-size:11px" :title="f.commit"> @{{ f.commit.slice(0, 8) }}</span>
              </div>
            </div>
          </template>
        </n-alert>

        <n-alert v-if="job?.remaining_findings?.length" type="warning" style="margin-bottom:0">
          <template #header>
            <div>遗留问题清单（宏观回归审查发现，归档未处理，请人工跟进）</div>
          </template>
          <div v-for="(f, i) in job!.remaining_findings" :key="i"
            style="font-size:12px;padding:6px 8px;margin-top:4px;background:var(--cy-findings-bg);border-left:2px solid var(--cy-amber);border-radius:2px">
            <span :style="{ color: sevColor(f.severity) }">[{{ f.severity }}]</span>
            {{ f.file }}:{{ f.line }} — {{ f.desc }}
            <span v-if="f.commit" class="glow-cyan" style="font-size:11px" :title="f.commit"> @{{ f.commit.slice(0, 8) }}</span>
          </div>
        </n-alert>
      </div>
    </div>

    <!-- 归档卡点（可操作，常显，错误原因直接展示） -->
    <n-alert v-if="archiveBlocked && blockedTask" type="error" style="margin-bottom:12px">
      <template #header>
        <div>归档卡点：{{ blockedTask.id }}「{{ blockedTask.title }}」迁移受阻，需人工介入</div>
      </template>
      <div style="font-size:12px;white-space:pre-wrap;margin-top:4px">{{ blockedTask.last_error || '该任务重试多次仍未归档成功。' }}</div>
      <n-input v-model:value="resolveNote" type="textarea" :rows="2" placeholder="（可选）给 AI 的补充意见，仅「让 AI 重试」时生效" style="margin-top:8px" />
      <n-space style="margin-top:8px">
        <n-button size="tiny" type="primary" :loading="busy" @click="resolveArchive('retry')">让 AI 重试</n-button>
        <n-button size="tiny" type="success" :loading="busy" @click="resolveArchive('continue')">我已手动改好，继续</n-button>
        <n-button size="tiny" type="warning" :loading="busy" @click="resolveArchive('skip')">放弃该任务</n-button>
      </n-space>
    </n-alert>

    <!-- 归档分支 + 最终回归审查（含确认完成按钮，常显） -->
    <n-alert v-if="job?.archive_branch || job?.archive_review"
      :type="job?.archive_review?.verdict === 'pass' ? 'success' : 'warning'"
      :title="`归档分支 @${job?.archive_branch || '—'} — 最终回归审查 [${job?.archive_review?.verdict ?? '进行中'}]`"
      style="margin-bottom:12px">
      <div v-if="job?.archive_review?.summary" style="margin-top:4px;font-size:13px">{{ job.archive_review.summary }}</div>
      <div v-if="job?.archive_review?.findings?.length" style="margin-top:4px">
        <div v-for="(f, i) in job!.archive_review!.findings" :key="i"
          style="font-size:12px;padding:6px 8px;margin-top:4px;background:var(--cy-findings-bg);border-left:2px solid var(--cy-amber);border-radius:2px">
          <span :style="{ color: sevColor(f.severity) }">[{{ f.severity }}]</span>
          {{ f.file }}:{{ f.line }} — {{ f.desc }}
        </div>
      </div>
      <n-button v-if="canConfirmArchive" size="small" type="success" :disabled="!archiveReviewPassed" style="margin-top:8px" @click="doConfirmArchive">确认完成整体修复任务</n-button>
      <n-alert v-if="canConfirmArchive && !archiveReviewPassed" type="error" style="margin-top:8px">
        最终回归审查未通过，无法确认完成。请处理 commit↔任务对应问题后重新归档。
      </n-alert>
    </n-alert>

    <n-spin :show="loading" style="flex:1;min-height:0;display:flex" content-style="flex:1;min-height:0;display:flex">
      <div :style="`flex:1;display:flex;gap:${GAP}px;min-height:0`">
        <!-- 任务列表 -->
        <div class="cy-card" style="width:260px;flex-shrink:0;display:flex;flex-direction:column;padding:8px;min-height:0">
          <div style="flex-shrink:0">
            <div style="display:flex;align-items:center;justify-content:space-between;padding:4px 6px 8px">
              <div class="cy-title">任务 ({{ tasks.length }})</div>
              <n-button size="tiny" tertiary :disabled="!currentTask" @click="jumpToCurrent">回当前</n-button>
            </div>
            <div style="margin-bottom:8px">
              <n-radio-group v-model:value="filter" size="small">
                <n-radio-button value="all">全部</n-radio-button>
                <n-radio-button value="passed">通过</n-radio-button>
                <n-radio-button value="failed">失败</n-radio-button>
                <n-radio-button value="skipped">跳过</n-radio-button>
              </n-radio-group>
            </div>
            <div style="margin-bottom:8px">
              <n-button size="tiny" block @click="downloadMd">下载 {{ filterLabel }} md</n-button>
            </div>
          </div>
          <div style="flex:1;overflow-y:auto;overflow-x:hidden;min-height:0">
            <n-empty v-if="!tasks.length" description="尚未拆解" style="margin-top:40px" />
            <n-empty v-else-if="!filteredTasks.length" description="该分类暂无任务" style="margin-top:24px" />
            <div v-for="t in filteredTasks" :key="t.id" :ref="setItemRef(t.id)"
              @click="select(t)"
              :style="{
                padding: '8px 10px', marginBottom: 6, borderRadius: 4, cursor: 'pointer',
                border: selected?.id === t.id ? '1px solid var(--cy-cyan)' : '1px solid transparent',
                background: selected?.id === t.id ? 'var(--cy-selected-bg)' : 'var(--cy-hover-bg)',
              }">
              <div style="display:flex;justify-content:space-between;align-items:center">
                <div style="display:flex;align-items:center;gap:6px">
                  <span style="font-size:12px;color:var(--cy-muted)">{{ t.id }}</span>
                  <n-button v-if="t.status === 'pending'" size="tiny" type="warning" secondary @click.stop="doDefer(t)">跳过</n-button>
                </div>
                <div style="display:flex;align-items:center;gap:4px">
                  <n-tag v-if="t.archived" size="tiny" :bordered="false" :style="{ color: 'var(--cy-green)', background: 'var(--cy-green-bg)' }">已归档</n-tag>
                  <n-tag v-if="t.suggestion" size="tiny" :bordered="false" :style="{ color: 'var(--cy-suggestion)', background: 'var(--cy-suggestion-bg)' }">建议</n-tag>
                  <StatusBadge :status="t.status" />
                </div>
              </div>
              <div style="font-size:13px;margin-top:4px;line-height:1.4;overflow-wrap:anywhere">{{ t.title }}</div>
              <div style="display:flex;align-items:center;gap:6px;margin-top:4px">
                <n-tag size="tiny" :bordered="false" :style="{ color: sevColor(t.severity), background: sevBg(t.severity) }">
                  {{ t.severity }}
                </n-tag>
                <n-button v-if="t.status === 'passed' || t.status === 'approved'" size="tiny" tertiary @click.stop="openAiReview(t)">AI 审查</n-button>
              </div>
            </div>
          </div>
        </div>

        <!-- 详情 -->
        <div class="cy-card" style="flex:1;display:flex;flex-direction:column;min-width:0">
          <div style="flex-shrink:0;display:flex;align-items:center;justify-content:space-between;gap:10px;padding:12px 14px;border-bottom:1px solid var(--cy-border)">
            <div v-if="selected" style="display:flex;align-items:center;gap:10px;min-width:0">
              <span class="font-display" style="font-size:15px">{{ selected.title }}</span>
              <StatusBadge :status="selected.status" />
              <span style="color:var(--cy-muted);font-size:12px">重试 {{ selected.retries }}</span>
            </div>
            <span v-else style="color:var(--cy-muted);font-size:13px">任务详情</span>
            <n-space>
              <n-button v-if="selected && attempts.length" size="tiny" type="warning" secondary @click="showAttempts = true">
                查看失败记录 ({{ attempts.length }})
              </n-button>
              <n-button size="tiny" :disabled="!selected" @click="refreshCurrentTask">刷新</n-button>
            </n-space>
          </div>

          <div style="flex:1;overflow:auto;padding:14px;min-width:0">
            <template v-if="selected">
              <div style="color:var(--cy-muted);font-size:12px;margin-bottom:10px">
                涉及文件：{{ selected.files?.join(', ') || '—' }}
                <template v-if="selected.commits?.length"> · 提交：<span class="glow-green">{{ selected.commits.join(', ') }}</span></template>
                <template v-if="selected.archive_commit"> · 归档提交：<span class="glow-cyan">{{ selected.archive_commit }}</span></template>
              </div>
              <n-alert v-if="selected.status === 'failed' || selected.status === 'archive_blocked'" type="error" style="margin-bottom:10px">
                <span style="white-space:pre-wrap">{{ selectedError }}</span>
              </n-alert>

              <div class="cy-title" style="margin-bottom:6px">问题描述</div>
              <div style="font-size:13px;line-height:1.6;white-space:pre-wrap">{{ selected.description }}</div>

              <template v-if="selected.review">
                <div class="cy-title" style="margin:14px 0 6px">对抗性审查结论
                  <span :style="{ color: selected.review.verdict === 'pass' ? 'var(--cy-green)' : 'var(--cy-red)' }">
                    [{{ selected.review.verdict }}]
                  </span>
                </div>
                <div style="font-size:13px">{{ selected.review.summary }}</div>
                <div v-if="selected.review.findings?.length" style="margin-top:6px">
                  <div v-for="(f, i) in selected.review.findings" :key="i" style="font-size:12px;padding:6px 8px;margin-top:4px;background:var(--cy-findings-bg);border-left:2px solid var(--cy-red);border-radius:2px">
                    <span :style="{ color: sevColor(f.severity) }">[{{ f.severity }}]</span>
                    {{ f.file }}:{{ f.line }} — {{ f.desc }}
                  </div>
                </div>
              </template>

              <div class="cy-title" style="margin:14px 0 6px">改动 Diff</div>
              <DiffView :diff="patch" />

              <div class="cy-title" style="margin:14px 0 6px">人工备注与建议</div>
              <div style="display:flex;flex-direction:column;gap:8px">
                <n-input v-model:value="noteText" type="textarea" :rows="2" placeholder="人工备注（提供人工意见，不影响处理）" />
                <n-input v-model:value="suggestionText" type="textarea" :rows="2" placeholder="人工建议（特别标记，后续可能据此重审/重修）" />
                <div style="text-align:right">
                  <n-button size="tiny" type="primary" :loading="savingAnnotation" @click="saveAnnotation">保存备注/建议</n-button>
                </div>
              </div>
            </template>
            <n-empty v-else description="选择左侧任务查看详情" style="margin-top:60px" />
          </div>
        </div>

        <!-- 实时日志（全高右栏，始终可见；可向右收起成窄边） -->
        <div v-if="!logCollapsed" class="cy-card" style="width:400px;flex-shrink:0;display:flex;flex-direction:column;min-width:0">
          <div class="cy-title" style="padding:10px 12px 8px;flex-shrink:0;display:flex;align-items:center;justify-content:space-between">
            <span>实时日志</span>
            <n-button size="tiny" quaternary title="向右收起" @click="logCollapsed = true">»</n-button>
          </div>
          <div style="flex:1;min-height:0;padding:0 8px 8px">
            <TerminalPanel :lines="logLines" />
          </div>
        </div>
        <div v-else class="cy-card" style="flex-shrink:0;display:flex;flex-direction:column;align-items:center;padding:10px 4px;min-width:0">
          <n-button size="tiny" quaternary title="展开日志" @click="logCollapsed = false">«</n-button>
        </div>
      </div>
    </n-spin>

    <!-- 失败修复/审查记录弹窗：每次尝试的 diff 与审查结论成对展示 -->
    <n-modal v-model:show="showAttempts" preset="card" title="失败修复 / 审查记录" style="width:min(960px, 92vw)">
      <div style="max-height:72vh;overflow:auto;padding-right:4px">
        <n-empty v-if="!attempts.length" description="无失败记录" style="margin:20px 0" />
        <div v-for="(a, i) in attempts" :key="i"
          style="margin-bottom:20px;border:1px solid var(--cy-border);border-radius:4px;padding:12px">
          <div class="cy-title" style="margin-bottom:8px">第 {{ a.attempt }} 次尝试</div>
          <n-alert v-if="a.error" type="error" style="margin-bottom:8px">
            <span style="white-space:pre-wrap">{{ a.error }}</span>
          </n-alert>
          <template v-if="a.review?.verdict || a.review?.summary">
            <div style="display:flex;align-items:center;gap:8px;margin-bottom:6px">
              <span style="font-size:12px;color:var(--cy-muted)">审查结论</span>
              <n-tag size="tiny" :bordered="false" :style="{ color: a.review?.verdict === 'pass' ? 'var(--cy-green)' : 'var(--cy-red)' }">
                {{ a.review?.verdict || '—' }}
              </n-tag>
            </div>
            <div style="font-size:13px;line-height:1.6">{{ a.review?.summary }}</div>
            <div v-if="a.review?.findings?.length" style="margin-top:6px">
              <div v-for="(f, j) in a.review.findings" :key="j"
                style="font-size:12px;padding:6px 8px;margin-top:4px;background:var(--cy-findings-bg);border-left:2px solid var(--cy-red);border-radius:2px">
                <span :style="{ color: sevColor(f.severity) }">[{{ f.severity }}]</span>
                {{ f.file }}:{{ f.line }} — {{ f.desc }}
              </div>
            </div>
          </template>
          <div class="cy-title" style="margin:12px 0 6px">本次修复 Diff</div>
          <DiffView :diff="a.diff" />
        </div>
      </div>
    </n-modal>

    <!-- 人工发起的 AI 审查弹窗 -->
    <AiReviewModal v-model:show="showAiReview" :job-id="id" :task="aiReviewTarget" />

    <!-- 任务详情弹窗：基本信息（分支、仓库、报告来源等） -->
    <n-modal v-model:show="showJobDetail" preset="card" title="任务详情" style="max-width:640px">
      <div style="font-size:13px;line-height:2.1">
        <div>名称：{{ job?.name || '—' }}</div>
        <div>仓库：{{ job?.repo_path || '—' }}</div>
        <div>基础分支：{{ job?.base_branch || '—' }}</div>
        <div>修复分支：<span v-if="job?.fix_branch" class="glow-cyan">@{{ job.fix_branch }}</span><span v-else>—</span></div>
        <div v-if="job?.archive_branch">归档分支：<span class="glow-cyan">@{{ job.archive_branch }}</span></div>
        <div v-if="job?.report_source?.type === 'text'">
          <div style="margin-bottom:4px">报告内容（粘贴文本）：</div>
          <div style="max-height:220px;overflow:auto;white-space:pre-wrap;word-break:break-word;font-size:12px;line-height:1.6;background:var(--cy-hover-bg);border:1px solid var(--cy-border);border-radius:4px;padding:8px 10px">{{ job.report_source.value }}</div>
        </div>
        <div v-else>报告来源：{{ job?.report_source?.value || '—' }}</div>
      </div>
      <template #footer>
        <n-space justify="end">
          <n-button @click="showJobDetail = false">关闭</n-button>
        </n-space>
      </template>
    </n-modal>

    <!-- 约束查看/更换弹窗 -->
    <n-modal v-model:show="showConstraint" preset="card" title="任务约束" style="max-width:640px">
      <div style="font-size:12px;color:var(--cy-muted);margin-bottom:12px">
        约束随模板实时读取；任务进行中更换后在下一个任务开始生效。
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
          placeholder="如：不引入第三方依赖 / 保持 Go 1.20 兼容 / 遵循某代码规范..." />
      </template>
      <template #footer>
        <n-space justify="end">
          <n-button @click="showConstraint = false">取消</n-button>
          <n-button type="primary" :loading="savingConstraint" @click="saveConstraint">保存</n-button>
        </n-space>
      </template>
    </n-modal>

    <n-modal v-model:show="showRedecompose" preset="card" title="打回重做" style="max-width:640px">
      <div style="font-size:12px;color:var(--cy-muted);margin-bottom:12px">
        将清空当前任务列表并重新拆解报告。可在下方说明打回原因，AI 会据此改进本次拆解与复查。
      </div>
      <n-input v-model:value="redecomposeReason" type="textarea" :rows="4"
        placeholder="例如：把 A 问题误拆成了两个任务；XX 是误报不应进入列表；漏掉了报告里的 Y 问题…" />
      <template #footer>
        <n-space justify="end">
          <n-button @click="showRedecompose = false">取消</n-button>
          <n-button type="warning" :loading="redecomposing" @click="confirmRedecompose">确认重做</n-button>
        </n-space>
      </template>
    </n-modal>
  </div>
</template>
