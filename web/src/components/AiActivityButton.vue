<script setup lang="ts">
import { ref, onBeforeUnmount } from 'vue'
import { NButton, NModal, NEmpty, NTag } from 'naive-ui'
import { listAiActivity, type AiActivity } from '../api'

const props = defineProps<{ jobId: string }>()

const show = ref(false)
const activity = ref<AiActivity[]>([])
let timer: ReturnType<typeof setInterval> | null = null

function fmtTs(input: string): string {
  const d = new Date(input)
  if (isNaN(d.getTime())) return input
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`
}
function fmtElapsed(s: number) {
  const h = Math.floor(s / 3600)
  const m = Math.floor((s % 3600) / 60)
  const sec = s % 60
  const p = (n: number) => String(n).padStart(2, '0')
  return h > 0 ? `${h}:${p(m)}:${p(sec)}` : `${m}:${p(sec)}`
}

async function poll() {
  try { activity.value = await listAiActivity(props.jobId) } catch { /* ignore */ }
}
function open() {
  show.value = true
  poll()
  if (!timer) timer = setInterval(poll, 3000)
}
function close() {
  show.value = false
  if (timer) { clearInterval(timer); timer = null }
}
onBeforeUnmount(() => { if (timer) { clearInterval(timer); timer = null } })
</script>

<template>
  <n-button size="small" secondary @click="open">AI 任务</n-button>
  <n-modal v-model:show="show" preset="card" title="后台 AI 任务" style="width:min(720px, 92vw)" @update:show="(v: boolean) => { if (!v) close() }">
    <div style="max-height:70vh;overflow:auto;padding-right:4px">
      <n-empty v-if="!activity.length" description="当前没有运行中的 AI 任务" style="margin:24px 0" />
      <div v-for="(a, i) in activity" :key="i"
        style="margin-bottom:12px;border:1px solid var(--cy-border);border-radius:4px;padding:10px 12px">
        <div style="display:flex;align-items:center;justify-content:space-between">
          <div style="display:flex;align-items:center;gap:8px">
            <span class="glow-cyan font-display" style="font-size:14px">{{ a.phase || 'AI 调用' }}</span>
            <n-tag v-if="a.task_id" size="tiny" :bordered="false">{{ a.task_id }}</n-tag>
          </div>
          <span class="glow-green" style="font-size:12px">运行中 · {{ fmtElapsed(a.elapsed_seconds) }}</span>
        </div>
        <div v-if="a.task_title" style="font-size:12px;color:var(--cy-muted);margin-top:4px;overflow-wrap:anywhere">{{ a.task_title }}</div>
        <div style="font-size:11px;color:var(--cy-muted);margin-top:6px">
          PID {{ a.pid }} · {{ a.cmd }}<span v-if="a.started_at"> · 启动于 {{ fmtTs(a.started_at) }}</span>
        </div>
      </div>
    </div>
  </n-modal>
</template>
