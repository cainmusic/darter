<script setup lang="ts">
import { onMounted, ref, computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { NButton, NSpace, NEmpty, NTag, NInput, useMessage, useDialog } from 'naive-ui'
import { getJob, getTask, approveTask, rejectTask, unapproveTask, markReviewComplete, type Job, type Task } from '../api'
import StatusBadge from '../components/StatusBadge.vue'
import DiffView from '../components/DiffView.vue'
import AiReviewModal from '../components/AiReviewModal.vue'
import AiActivityButton from '../components/AiActivityButton.vue'
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
const note = ref('')

// 人工发起的 AI 审查弹窗
const aiReviewTarget = ref<Task | null>(null)
const showAiReview = ref(false)
function openAiReview(t: Task) {
  aiReviewTarget.value = t
  showAiReview.value = true
}

async function refresh() {
  const d = await getJob(id)
  job.value = d.job
  tasks.value = d.tasks
  if (selected.value) {
    const f = d.tasks.find((t) => t.id === selected.value!.id)
    if (f) selected.value = f
  }
  if (!selected.value) {
    const first = d.tasks.find((t) => t.status === 'passed') ?? d.tasks.find(reviewable)
    if (first) select(first)
  }
}

async function select(t: Task) {
  selected.value = t
  patch.value = ''
  note.value = ''
  try { const r = await getTask(id, t.id); patch.value = r.patch } catch { /* ignore */ }
}

async function approve(t: Task) {
  try { await approveTask(id, t.id, note.value); message.success('已批准'); await refresh() } catch (e: any) { message.error(e.message) }
}
async function reject(t: Task) {
  try { await rejectTask(id, t.id, note.value); message.warning('已驳回，任务判为失败'); await refresh() } catch (e: any) { message.error(e.message) }
}
function unapprove(t: Task) {
  dialog.warning({
    title: '取消审批',
    content: `确定将「${t.title}」取消审批、回退为待审状态吗？`,
    positiveText: '取消审批',
    negativeText: '返回',
    onPositiveClick: async () => {
      try { await unapproveTask(id, t.id); message.success('已取消审批'); await refresh() } catch (e: any) { message.error(e.message) }
    },
  })
}

// 待审/已审批任务都进入审批列表；已审批任务可在此取消审批回退为通过。
const reviewable = (t: Task) => t.status === 'passed' || t.status === 'approved'

// 审查完成门禁：归档前必须显式标记「审查完成」，避免在人类审查过程中误触归档。
const reviewDone = computed(() => job.value?.status === 'review_done')
const reviewing = computed(() => job.value?.status === 'awaiting_review')
// completed 表示归档完成（终态）：按钮保留展示但置灰，表示已完成不可再撤销。
const completed = computed(() => job.value?.status === 'completed')

function doReviewComplete() {
  const left = tasks.value.filter((t) => t.status === 'passed').length
  const undoing = reviewDone.value
  dialog.warning({
    title: undoing ? '撤销审查完成' : '标记审查完成',
    content: undoing
      ? '确定撤销审查完成并重新进入人类审查吗？'
      : left > 0
        ? `还有 ${left} 个任务尚未给出审查结论，确定标记审查完成吗？`
        : '确定标记审查完成吗？标记后即可开始归档。',
    positiveText: undoing ? '撤销' : '标记完成',
    negativeText: '取消',
    onPositiveClick: async () => {
      try {
        await markReviewComplete(id, !undoing)
        message.success(undoing ? '已撤销，重新进入人类审查' : '已标记审查完成，可开始归档')
        await refresh()
      } catch (e: any) { message.error(e.message) }
    },
  })
}

function sevColor(s: string) {
  return ({ high: 'var(--cy-red)', medium: 'var(--cy-amber)', low: 'var(--cy-cyan)' } as Record<string, string>)[s] ?? 'var(--cy-muted)'
}
function sevBg(s: string) {
  return ({ high: 'var(--cy-red-bg)', medium: 'var(--cy-amber-bg)', low: 'var(--cy-cyan-bg)' } as Record<string, string>)[s] ?? 'var(--cy-muted-bg)'
}

onMounted(() => refresh().catch((e) => message.error(e.message)))
</script>

<template>
  <div style="height:100%;display:flex;flex-direction:column;padding:8px">
    <div style="display:flex;align-items:center;justify-content:space-between;margin-bottom:12px">
      <div style="display:flex;align-items:center;gap:12px">
        <span class="cy-title" style="font-size:16px">人类审查</span>
        <span class="font-display" style="font-size:14px">{{ job?.name }}</span>
        <StatusBadge v-if="job" :status="job.status" />
      </div>
      <n-space>
        <AiActivityButton :job-id="id" />
        <n-button v-if="reviewing || reviewDone || completed" size="small" :type="reviewing ? 'success' : 'default'" :disabled="completed" @click="doReviewComplete">
          {{ reviewing ? '标记审查完成' : '撤销审查完成' }}
        </n-button>
        <n-button size="small" @click="router.push(`/jobs/${id}`)">返回工作台</n-button>
      </n-space>
    </div>

    <div :style="`flex:1;display:flex;gap:${GAP}px;min-height:0`">
      <div class="cy-card" style="width:280px;overflow:auto;padding:8px">
        <div class="cy-title" style="padding:4px 6px 8px">审批任务</div>
        <n-empty v-if="!tasks.filter(reviewable).length" description="无待审/已审批任务" style="margin-top:40px" />
        <div v-for="t in tasks.filter(reviewable)" :key="t.id" @click="select(t)"
          :style="{
            padding: '8px 10px', marginBottom: 6, borderRadius: 4, cursor: 'pointer',
            border: selected?.id === t.id ? '1px solid var(--cy-cyan)' : '1px solid transparent',
            background: selected?.id === t.id ? 'var(--cy-selected-bg)' : 'var(--cy-hover-bg)',
          }">
          <div style="display:flex;justify-content:space-between;align-items:center">
            <div style="display:flex;align-items:center;gap:6px">
              <span style="font-size:12px;color:var(--cy-muted)">{{ t.id }}</span>
            </div>
            <div style="display:flex;align-items:center;gap:4px">
              <n-tag v-if="t.archived" size="tiny" :bordered="false" :style="{ color: 'var(--cy-green)', background: 'var(--cy-green-bg)' }">已归档</n-tag>
              <n-tag v-if="t.suggestion" size="tiny" :bordered="false" :style="{ color: 'var(--cy-suggestion)', background: 'var(--cy-suggestion-bg)' }">建议</n-tag>
              <StatusBadge :status="t.status" />
            </div>
          </div>
          <div style="font-size:13px;margin-top:4px;line-height:1.4;overflow-wrap:anywhere">{{ t.title }}</div>
          <div style="display:flex;align-items:center;gap:6px;margin-top:4px">
            <n-tag size="tiny" :bordered="false" :style="{ color: sevColor(t.severity), background: sevBg(t.severity) }">{{ t.severity }}</n-tag>
            <n-button size="tiny" tertiary @click.stop="openAiReview(t)">AI 审查</n-button>
          </div>
        </div>
      </div>

      <div class="cy-card" style="flex:1;display:flex;flex-direction:column;min-width:0;min-height:0">
        <div style="flex:1;overflow:auto;padding:14px;min-height:0">
          <template v-if="selected">
            <div style="display:flex;align-items:center;gap:10px;margin-bottom:8px">
              <span class="font-display" style="font-size:15px">{{ selected.title }}</span>
              <StatusBadge :status="selected.status" />
              <span v-if="selected.commits?.length" class="glow-green" style="font-size:12px">{{ selected.commits.join(', ') }}</span>
            </div>
            <div style="color:var(--cy-muted);font-size:12px;margin-bottom:10px">{{ selected.description }}</div>

            <template v-if="selected.review">
              <div class="cy-title" style="margin-bottom:6px">审查结论 [{{ selected.review.verdict }}]</div>
              <div style="font-size:13px">{{ selected.review.summary }}</div>
              <div v-for="(f, i) in selected.review.findings || []" :key="i" style="font-size:12px;padding:6px 8px;margin-top:4px;background:var(--cy-findings-bg);border-left:2px solid var(--cy-red);border-radius:2px">
                <span :style="{ color: sevColor(f.severity) }">[{{ f.severity }}]</span> {{ f.file }}:{{ f.line }} — {{ f.desc }}
              </div>
            </template>

            <div class="cy-title" style="margin:14px 0 6px">Diff</div>
            <DiffView :diff="patch" />
          </template>
          <n-empty v-else description="选择左侧任务进行审查" style="margin-top:60px" />
        </div>

        <!-- 审批操作固定区：不随内容滚动，长 Diff 时也能直接操作 -->
        <div v-if="selected" style="flex-shrink:0;padding:12px 14px;border-top:1px solid var(--cy-border)">
          <template v-if="selected.status === 'passed'">
            <div class="cy-title" style="margin-bottom:6px">审批意见</div>
            <n-input v-model:value="note" type="textarea" :rows="2" placeholder="可选备注" />
            <n-space style="margin-top:12px">
              <n-button type="success" @click="approve(selected)">批准</n-button>
              <n-button type="error" @click="reject(selected)">驳回</n-button>
            </n-space>
          </template>
          <template v-else-if="selected.status === 'approved'">
            <div class="cy-title" style="margin-bottom:6px">已审批</div>
            <div v-if="selected.human?.note" style="font-size:13px;color:var(--cy-muted);margin-bottom:6px">审批意见：{{ selected.human.note }}</div>
            <n-space style="margin-top:12px">
              <n-button v-if="!selected.archived && !selected.archive_commit" type="warning" @click="unapprove(selected)">取消审批</n-button>
              <n-tag v-else size="small" :bordered="false" type="info">已归档，不可回退</n-tag>
            </n-space>
          </template>
        </div>
      </div>
    </div>

    <!-- 人工发起的 AI 审查弹窗 -->
    <AiReviewModal v-model:show="showAiReview" :job-id="id" :task="aiReviewTarget" />
  </div>
</template>
