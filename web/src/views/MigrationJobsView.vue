<script setup lang="ts">
import { onMounted, ref, computed } from 'vue'
import { useRouter } from 'vue-router'
import {
  NButton, NCard, NEmpty, NGrid, NGridItem, NModal, NForm, NFormItem,
  NInput, NSelect, NAlert, NSpace, NSpin, useMessage,
} from 'naive-ui'
import {
  listMigrations, createMigration, listConstraintTemplates, concatTemplateContent,
  type MigJob, type CreateMigrationInput, type ConstraintTemplate,
} from '../api'
import StatusBadge from '../components/StatusBadge.vue'
import { GAP } from '../layout'
import PageHeader from '../components/PageHeader.vue'

const router = useRouter()
const message = useMessage()
const jobs = ref<MigJob[]>([])
const loading = ref(false)
const showCreate = ref(false)
const creating = ref(false)

const templates = ref<ConstraintTemplate[]>([])
const selectedTemplate = ref<string | null>(null)

const form = ref<CreateMigrationInput>({
  name: '',
  repo_path: '',
  source_branch: '',
  target_base: '',
  target_branch: '',
  constraints: '',
  constraint_template_id: '',
})

const query = ref('')
// 是否归档过滤：代码迁移的「归档」即「已完成」（mig_completed）
const archiveFilter = ref<'all' | 'archived' | 'unarchived'>('unarchived')

const archiveFilterOptions = [
  { label: '全部', value: 'all' },
  { label: '已归档', value: 'archived' },
  { label: '未归档', value: 'unarchived' },
]

const isArchived = (status: string) => status === 'mig_completed'

const filteredJobs = computed(() => {
  const q = query.value.trim().toLowerCase()
  return jobs.value.filter((j) => {
    const arch = isArchived(j.status)
    if (archiveFilter.value === 'archived' && !arch) return false
    if (archiveFilter.value === 'unarchived' && arch) return false
    if (!q) return true
    const hay = [j.name, j.repo_path, j.migration?.source_branch, j.migration?.target_branch, j.id]
      .filter((s): s is string => !!s)
      .join('\n')
      .toLowerCase()
    return hay.includes(q)
  })
})

const templateOptions = computed(() => templates.value.map((t) => ({ label: t.name, value: t.id })))
const selectedTemplateContent = computed(() => {
  const t = templates.value.find((t) => t.id === selectedTemplate.value)
  return t ? concatTemplateContent(t) : ''
})

async function refresh() {
  loading.value = true
  try { jobs.value = await listMigrations() } catch (e: any) { message.error(e.message) } finally { loading.value = false }
}

async function loadTemplates() {
  try { templates.value = await listConstraintTemplates() } catch { /* ignore */ }
}

function onTemplatePick(id: string | null) {
  selectedTemplate.value = id
  form.value.constraint_template_id = id ?? ''
  // 用模板时清空内联文本，避免「引用模板 + 内联文本」并存造成歧义
  if (id) form.value.constraints = ''
}

async function submit() {
  creating.value = true
  try {
    const j = await createMigration(form.value)
    message.success('迁移任务已创建')
    showCreate.value = false
    router.push('/migrations/' + j.id)
  } catch (e: any) { message.error(e.message) } finally { creating.value = false }
}

function fmt(t: string) { return new Date(t).toLocaleString() }
function go(id: string) { router.push('/migrations/' + id) }

onMounted(() => { refresh(); loadTemplates() })
</script>

<template>
  <div style="padding: 8px">
    <PageHeader title="代码迁移">
      <n-button type="primary" @click="showCreate = true">+ 新建迁移任务</n-button>
    </PageHeader>

    <div style="display:flex;align-items:center;gap:10px;margin-bottom:10px;flex-wrap:wrap">
      <n-input v-model:value="query" placeholder="搜索名称 / 仓库 / 分支 / ID" clearable style="max-width:340px" />
      <n-select v-model:value="archiveFilter" :options="archiveFilterOptions" placeholder="是否归档" style="width:130px" />
      <span style="color:var(--cy-muted);font-size:12px;margin-left:auto">共 {{ jobs.length }} 个，显示 {{ filteredJobs.length }} 个</span>
    </div>

    <n-spin :show="loading">
      <n-empty v-if="!loading && !filteredJobs.length"
        description="暂无迁移任务，点击右上角新建"
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
              <div style="white-space:nowrap;overflow:hidden;text-overflow:ellipsis">源分支 <span class="glow-cyan">{{ j.migration?.source_branch }}</span></div>
              <div style="white-space:nowrap;overflow:hidden;text-overflow:ellipsis">目标分支 <span class="glow-green">{{ j.migration?.target_branch }}</span></div>
              <div style="white-space:nowrap;overflow:hidden;text-overflow:ellipsis">创建：{{ fmt(j.created_at) }}</div>
            </div>
          </n-card>
        </n-grid-item>
      </n-grid>
    </n-spin>

    <n-modal v-model:show="showCreate" preset="card" title="新建迁移任务" style="max-width:600px">
      <n-form label-placement="top">
        <n-form-item label="名称（可选）">
          <n-input v-model:value="form.name" placeholder="如 game-logic-migration" />
        </n-form-item>
        <n-form-item label="项目目录 *">
          <n-input v-model:value="form.repo_path" placeholder="/path/to/repo（需是 git 仓库）" />
        </n-form-item>
        <n-form-item label="源分支 *（要迁移的 commit 所在分支）">
          <n-input v-model:value="form.source_branch" placeholder="如 feature/old-version" />
        </n-form-item>
        <n-form-item label="目标基底 *（目标分支从其切出）">
          <n-input v-model:value="form.target_base" placeholder="如 master / develop" />
        </n-form-item>
        <n-form-item label="目标分支 *（从目标基底切出，接收 cherry-pick）">
          <n-input v-model:value="form.target_branch" placeholder="如 migrate/new-version" />
        </n-form-item>
        <n-form-item label="约束模板（可选，AI 解决冲突时引用）">
          <n-select v-model:value="selectedTemplate" :options="templateOptions" placeholder="选择一个已保存的模板，或留空用下方自定义文本" clearable @update:value="onTemplatePick" />
        </n-form-item>
        <n-form-item label="项目约束（可选，AI 解决冲突时带给 AI）">
          <div style="width:100%">
            <n-alert v-if="selectedTemplate" type="info" :bordered="false" style="margin-bottom:6px">
              <span style="white-space:pre-wrap">已引用模板，内容（只读）：{{ selectedTemplateContent }}</span>
            </n-alert>
            <n-input v-else v-model:value="form.constraints" type="textarea" :rows="3"
              placeholder="如：不引入第三方依赖 / 保持 Go 1.20 兼容 / 遵循某代码规范 / 不修改公共 API..." />
          </div>
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
