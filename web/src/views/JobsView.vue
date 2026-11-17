<script setup lang="ts">
import { onMounted, ref, computed } from 'vue'
import { useRouter } from 'vue-router'
import {
  NButton, NCard, NEmpty, NGrid, NGridItem, NModal, NForm, NFormItem,
  NInput, NInputNumber, NRadioGroup, NRadio, NSelect, NSpace, NSpin, useMessage,
} from 'naive-ui'
import { listJobs, createJob, type Job, type CreateJobInput } from '../api'
import StatusBadge from '../components/StatusBadge.vue'
import ConstraintPicker from '../components/ConstraintPicker.vue'
import { GAP } from '../layout'
import PageHeader from '../components/PageHeader.vue'

const router = useRouter()
const message = useMessage()
const jobs = ref<Job[]>([])
const loading = ref(false)
const showCreate = ref(false)
const creating = ref(false)

const form = ref({
  name: '',
  repo_path: '',
  base_branch: '',
  report_type: 'text' as 'text' | 'file',
  report_value: '',
  constraints: '',
  constraint_template_id: '',
  max_retries: 3,
})

async function refresh() {
  loading.value = true
  try { jobs.value = await listJobs() } catch (e: any) { message.error(e.message) } finally { loading.value = false }
}

// —— 列表筛选 / 查询 ——（只区分「是否归档」，不按具体状态过滤）
const query = ref('')
const archiveFilter = ref<'all' | 'archived' | 'unarchived'>('unarchived')

const archiveFilterOptions = [
  { label: '全部', value: 'all' },
  { label: '已归档', value: 'archived' },
  { label: '未归档', value: 'unarchived' },
]

// 归档有两种：completed（归档流程迁移 commit 后确认完成）、archived（人工手动归档冻结）。
const isArchived = (status: string) => status === 'completed' || status === 'archived'

const filteredJobs = computed(() => {
  const q = query.value.trim().toLowerCase()
  return jobs.value.filter((j) => {
    const arch = isArchived(j.status)
    if (archiveFilter.value === 'archived' && !arch) return false
    if (archiveFilter.value === 'unarchived' && arch) return false
    if (!q) return true
    const hay = [j.name, j.repo_path, j.base_branch, j.fix_branch, j.id]
      .filter((s): s is string => !!s)
      .join('\n')
      .toLowerCase()
    return hay.includes(q)
  })
})

async function submit() {
  creating.value = true
  try {
    const body: CreateJobInput = {
      name: form.value.name,
      repo_path: form.value.repo_path,
      base_branch: form.value.base_branch,
      report_source: { type: form.value.report_type, value: form.value.report_value },
      constraints: form.value.constraints,
      constraint_template_id: form.value.constraint_template_id || undefined,
      max_retries: form.value.max_retries,
    }
    const j = await createJob(body)
    message.success('任务已创建')
    showCreate.value = false
    router.push('/jobs/' + j.id)
  } catch (e: any) { message.error(e.message) } finally { creating.value = false }
}

function fmt(t: string) { return new Date(t).toLocaleString() }
function go(id: string) { router.push('/jobs/' + id) }

onMounted(refresh)
</script>

<template>
  <div style="padding: 8px">
    <PageHeader title="审查修复">
      <n-button type="primary" @click="showCreate = true">+ 新建修复任务</n-button>
    </PageHeader>

    <div style="display:flex;align-items:center;gap:10px;margin-bottom:10px;flex-wrap:wrap">
      <n-input v-model:value="query" placeholder="搜索名称 / 仓库 / 分支 / ID" clearable style="max-width:340px" />
      <n-select v-model:value="archiveFilter" :options="archiveFilterOptions" placeholder="是否归档" style="width:130px" />
      <span style="color:var(--cy-muted);font-size:12px;margin-left:auto">共 {{ jobs.length }} 个，显示 {{ filteredJobs.length }} 个</span>
    </div>

    <n-spin :show="loading">
      <n-empty v-if="!loading && !filteredJobs.length"
        :description="jobs.length ? '没有符合筛选条件的任务' : '暂无任务，点击右上角新建'"
        style="margin-top:80px" />
      <n-grid :cols="2" :x-gap="GAP" :y-gap="GAP" responsive="screen">
        <n-grid-item v-for="j in filteredJobs" :key="j.id">
          <n-card class="cy-card" hoverable @click="go(j.id)" style="cursor:pointer;height:100%">
            <div style="display:flex;align-items:center;justify-content:space-between;gap:8px">
              <span class="font-display" style="font-size:15px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis">{{ j.name }}</span>
              <StatusBadge :status="j.status" />
            </div>
            <div style="margin-top:10px;color:var(--cy-muted);font-size:12px;display:flex;flex-direction:column;gap:4px">
              <div style="white-space:nowrap;overflow:hidden;text-overflow:ellipsis">仓库：{{ j.repo_path }}</div>
              <div style="white-space:nowrap;overflow:hidden;text-overflow:ellipsis">分支：{{ j.base_branch }}<template v-if="j.fix_branch"> → <span class="glow-cyan">{{ j.fix_branch }}</span></template></div>
              <div style="white-space:nowrap;overflow:hidden;text-overflow:ellipsis">创建：{{ fmt(j.created_at) }}</div>
            </div>
          </n-card>
        </n-grid-item>
      </n-grid>
    </n-spin>

    <n-modal v-model:show="showCreate" preset="card" title="新建修复任务" style="max-width:640px">
      <n-form label-placement="top">
        <n-form-item label="名称（可选）">
          <n-input v-model:value="form.name" placeholder="如 backend-cr-fix" />
        </n-form-item>
        <n-form-item label="代码库路径 *">
          <n-input v-model:value="form.repo_path" placeholder="/path/to/repo" />
        </n-form-item>
        <n-form-item label="基础分支 *">
          <n-input v-model:value="form.base_branch" placeholder="如 master / dev / main（必填）" />
        </n-form-item>
        <n-form-item label="报告来源">
          <n-radio-group v-model:value="form.report_type">
            <n-radio value="text">粘贴文本</n-radio>
            <n-radio value="file">服务器文件路径</n-radio>
          </n-radio-group>
        </n-form-item>
        <n-form-item :label="form.report_type === 'text' ? 'Review 报告内容 *' : '报告文件绝对路径 *'">
          <n-input v-if="form.report_type === 'file'" v-model:value="form.report_value" placeholder="/path/to/report.md" />
          <n-input v-else v-model:value="form.report_value" type="textarea" :rows="8" placeholder="粘贴 review 报告..." />
        </n-form-item>
        <ConstraintPicker
          v-model:template-id="form.constraint_template_id"
          v-model:constraints="form.constraints"
          template-label="约束模板（可选，选中后引用该模板，约束随模板更新）"
          constraints-label="项目约束（可选，修复时带给 AI）" />
        <n-form-item label="最大重试次数">
          <n-input-number v-model:value="form.max_retries" :min="1" :max="10" />
        </n-form-item>
      </n-form>
      <template #footer>
        <n-space justify="end">
          <n-button @click="showCreate = false">取消</n-button>
          <n-button type="primary" :loading="creating" @click="submit">创建</n-button>
        </n-space>
      </template>
    </n-modal>
  </div>
</template>
