<script setup lang="ts">
import { ref, watch, onBeforeUnmount } from 'vue'
import { NModal, NButton, NInput, NEmpty, NTag, NAlert, useMessage } from 'naive-ui'
import { listAiReviews, aiReviewTask, listAiActivity, type AiReview, type Task } from '../api'

const props = defineProps<{ jobId: string; task: Task | null }>()
const show = defineModel<boolean>('show', { default: false })

const prompt = ref('')
const records = ref<AiReview[]>([])
const loading = ref(false)
const submitting = ref(false)
const polling = ref(false) // 后台 AI 审查进行中
const message = useMessage()

let pollTimer: ReturnType<typeof setInterval> | null = null
let pendingCount = 0

function fmtTs(input: string): string {
  const d = new Date(input)
  if (isNaN(d.getTime())) return input
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`
}

function sevColor(s: string) {
  return ({ high: 'var(--cy-red)', medium: 'var(--cy-amber)', low: 'var(--cy-cyan)' } as Record<string, string>)[s] ?? 'var(--cy-muted)'
}

async function load() {
  if (!props.task) return
  loading.value = true
  try { records.value = await listAiReviews(props.jobId, props.task.id) } catch (e: any) { message.error(e.message) } finally { loading.value = false }
}

function stopPolling() {
  if (pollTimer) { clearInterval(pollTimer); pollTimer = null }
  polling.value = false
}

function startPolling() {
  stopPolling()
  polling.value = true
  pollTimer = setInterval(async () => {
    if (!props.task) { stopPolling(); return }
    try {
      const rs = await listAiReviews(props.jobId, props.task.id)
      if (rs.length > pendingCount) {
        records.value = rs
        stopPolling()
        message.success('AI 审查完成')
      }
    } catch { /* 暂时失败忽略，下轮重试 */ }
  }, 3000)
}

// 后台是否仍有针对当前任务的 AI 审查在跑。数据来自后端子进程记录（ai-activity），
// 而非本地内存，因此页面重开 / 弹窗重开也能正确反映真实状态。
async function isRunning(): Promise<boolean> {
  const t = props.task
  if (!t) return false
  try {
    const acts = await listAiActivity(props.jobId)
    return acts.some((a) => a.task_id === t.id && a.phase === 'AI 审查（人工发起）')
  } catch { return false }
}

watch(show, async (v) => {
  if (v && props.task) {
    prompt.value = ''
    await load()
    // 重开弹窗时若后端仍有在跑的审查，恢复「进行中」状态并继续轮询直至完成
    if (await isRunning()) {
      pendingCount = records.value.length
      startPolling()
    }
  } else {
    stopPolling()
  }
})

onBeforeUnmount(() => stopPolling())

async function submit() {
  if (!props.task || polling.value) return
  submitting.value = true
  try {
    await aiReviewTask(props.jobId, props.task.id, prompt.value.trim())
    pendingCount = records.value.length
    prompt.value = ''
    message.success('已提交 AI 审查，进行中…')
    startPolling()
  } catch (e: any) { message.error(e.message) } finally { submitting.value = false }
}
</script>

<template>
  <n-modal v-model:show="show" preset="card" :title="`AI 审查 — ${task?.title ?? ''}`" style="width:min(880px, 92vw)">
    <template v-if="task">
      <div style="font-size:12px;color:var(--cy-muted);margin-bottom:12px">
        对「{{ task.title }}」的修复内容发起一次 AI 审查。可补充审查方向或疑问（可选）；每次审查都会记录，不限次数。
      </div>
      <n-input v-model:value="prompt" type="textarea" :rows="3" :disabled="polling"
        placeholder="（可选）补充审查方向/疑问，例如：重点检查并发安全；这段改动会不会影响 XX 模块的调用方…" />
      <div style="margin-top:12px;text-align:right">
        <n-button type="primary" size="small" :loading="submitting" :disabled="polling" @click="submit">发起 AI 审查</n-button>
      </div>

      <n-alert v-if="polling" type="info" style="margin-top:12px">AI 审查进行中，稍候自动刷新…</n-alert>

      <div class="cy-title" style="margin:18px 0 8px">审查记录（{{ records.length }}）</div>
      <div style="max-height:52vh;overflow:auto;padding-right:4px">
        <n-empty v-if="!records.length && !polling" description="尚无审查记录" style="margin:20px 0" />
        <div v-for="r in [...records].reverse()" :key="r.id"
          style="margin-bottom:14px;border:1px solid var(--cy-border);border-radius:4px;padding:10px 12px">
          <div style="display:flex;align-items:center;gap:8px;margin-bottom:6px">
            <span style="font-size:12px;color:var(--cy-muted)">{{ fmtTs(r.created_at) }}</span>
            <n-tag size="tiny" :bordered="false"
              :style="{ color: r.review?.verdict === 'pass' ? 'var(--cy-green)' : 'var(--cy-red)' }">
              {{ r.review?.verdict || '—' }}
            </n-tag>
          </div>
          <div v-if="r.prompt"
            style="font-size:12px;color:var(--cy-suggestion);background:var(--cy-suggestion-bg);padding:6px 8px;border-radius:3px;margin-bottom:6px;white-space:pre-wrap">
            审查提示词：{{ r.prompt }}
          </div>
          <div v-if="r.error" style="font-size:12px;color:var(--cy-red);margin-bottom:6px;white-space:pre-wrap">{{ r.error }}</div>
          <div style="font-size:13px;line-height:1.6">{{ r.review?.summary }}</div>
          <div v-if="r.review?.findings?.length" style="margin-top:6px">
            <div v-for="(f, i) in r.review.findings" :key="i"
              style="font-size:12px;padding:6px 8px;margin-top:4px;background:var(--cy-findings-bg);border-left:2px solid var(--cy-red);border-radius:2px">
              <span :style="{ color: sevColor(f.severity) }">[{{ f.severity }}]</span>
              {{ f.file }}:{{ f.line }} — {{ f.desc }}
            </div>
          </div>
        </div>
      </div>
    </template>
  </n-modal>
</template>
