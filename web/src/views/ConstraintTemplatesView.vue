<script setup lang="ts">
import { onMounted, ref, computed } from 'vue'
import { useRouter } from 'vue-router'
import {
  NButton, NCard, NEmpty, NInput, NSpace, useMessage, useDialog,
} from 'naive-ui'
import {
  listConstraintTemplates, getConstraintTemplate, copyConstraintTemplate,
  createConstraintTemplate, updateConstraintTemplate, deleteConstraintTemplate,
  concatTemplateContent,
  type ConstraintTemplate, type ConstraintModule, type TemplateUser,
} from '../api'
import StatusBadge from '../components/StatusBadge.vue'
import { GAP } from '../layout'
import PageHeader from '../components/PageHeader.vue'

const message = useMessage()
const dialog = useDialog()
const router = useRouter()

const templates = ref<ConstraintTemplate[]>([])
const loading = ref(false)
const selectedId = ref<string | null>(null)
const isNew = ref(false)
const editName = ref('')
const editModules = ref<ConstraintModule[]>([])
const saving = ref(false)
const usedBy = ref<TemplateUser[]>([])
// 每段折叠状态（key 为模块 ID）；不存在的 key 视为展开
const collapsed = ref<Record<string, boolean>>({})
// 编辑会话标识：切换模板/新建时自增，作为分段列表的 key，
// 强制重建 autosize textarea，避免各模板间残留的输入框高度互相串。
const editorKey = ref(0)

const current = computed(() => templates.value.find((t) => t.id === selectedId.value) ?? null)

function genModuleId() {
  return `m_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 8)}`
}

function addModule() {
  editModules.value.push({ id: genModuleId(), name: '', content: '' })
}

function removeModule(i: number) {
  editModules.value.splice(i, 1)
}

function moveModule(i: number, dir: -1 | 1) {
  const j = i + dir
  if (j < 0 || j >= editModules.value.length) return
  const arr = editModules.value
  const tmp = arr[i]
  arr[i] = arr[j]
  arr[j] = tmp
}

function toggleCollapse(id: string) {
  collapsed.value[id] = !collapsed.value[id]
}

async function load(selectFirst = true) {
  loading.value = true
  try {
    templates.value = await listConstraintTemplates()
    if (selectFirst) {
      if (selectedId.value && templates.value.some((t) => t.id === selectedId.value)) {
        // 保持当前选中
      } else if (templates.value.length) {
        select(templates.value[0].id)
      } else {
        select(null)
      }
    }
  } catch (e: any) { message.error(e.message) } finally { loading.value = false }
}

async function select(id: string | null) {
  selectedId.value = id
  isNew.value = false
  const t = templates.value.find((x) => x.id === id)
  editName.value = t?.name ?? ''
  editModules.value = (t?.modules ?? []).map((m) => ({ ...m }))
  collapsed.value = {}
  editorKey.value++
  usedBy.value = []
  if (id) {
    try {
      const d = await getConstraintTemplate(id)
      usedBy.value = d.used_by
    } catch { /* ignore */ }
  }
}

function newTemplate() {
  selectedId.value = null
  isNew.value = true
  editName.value = ''
  editModules.value = [{ id: genModuleId(), name: '', content: '' }]
  collapsed.value = {}
  editorKey.value++
}

async function copyTemplate() {
  if (!selectedId.value) return
  try {
    const nt = await copyConstraintTemplate(selectedId.value)
    await load(false)
    await select(nt.id)
    message.success('已复制为新模板，可继续编辑')
  } catch (e: any) { message.error(e.message) }
}

function save() {
  // 编辑已有模板且被未完结任务使用 → 修改前提示（改动会对它们后续修复生效）
  if (!isNew.value && selectedId.value && usedBy.value.length) {
    dialog.warning({
      title: '修改约束模板',
      content: `有 ${usedBy.value.length} 个未完结任务正在使用该模板，改动将对它们后续的修复生效，确定继续吗？`,
      positiveText: '继续修改',
      negativeText: '取消',
      onPositiveClick: doSave,
    })
    return
  }
  doSave()
}

async function doSave() {
  const name = editName.value.trim()
  if (!name) { message.warning('请输入模板名称'); return }
  // 只提交有内容的段；空段在保存时丢弃
  const mods = editModules.value.filter((m) => m.content.trim() !== '')
  if (!mods.length) { message.warning('请输入至少一段模板内容'); return }
  saving.value = true
  try {
    let t: ConstraintTemplate
    if (isNew.value) {
      t = await createConstraintTemplate(name, mods)
      templates.value.push(t)
    } else if (selectedId.value) {
      t = await updateConstraintTemplate(selectedId.value, name, mods)
      const i = templates.value.findIndex((x) => x.id === t.id)
      if (i >= 0) templates.value[i] = t
    } else {
      message.warning('请先选择一个模板或点击新建'); return
    }
    selectedId.value = t.id
    isNew.value = false
    editName.value = t.name
    editModules.value = (t.modules ?? []).map((m) => ({ ...m }))
    collapsed.value = {}
    message.success('已保存')
  } catch (e: any) { message.error(e.message) } finally { saving.value = false }
}

function del(t: ConstraintTemplate) {
  dialog.warning({
    title: '删除模板',
    content: `确定删除模板「${t.name}」吗？`,
    positiveText: '删除',
    negativeText: '取消',
    onPositiveClick: async () => {
      try {
        await deleteConstraintTemplate(t.id)
        templates.value = templates.value.filter((x) => x.id !== t.id)
        if (selectedId.value === t.id) {
          select(templates.value.length ? templates.value[0].id : null)
          if (!templates.value.length) newTemplate()
        }
        message.success('已删除')
      } catch (e: any) { message.error(e.message) }
    },
  })
}

onMounted(() => load())
</script>

<template>
  <div style="height:100%;display:flex;flex-direction:column;padding:8px">
    <PageHeader title="约束模板">
      <n-button type="primary" @click="newTemplate">+ 新建模板</n-button>
    </PageHeader>

    <div :style="`display:flex;gap:${GAP}px;align-items:stretch;flex:1;min-height:0`">
      <!-- 左侧列表 -->
      <div style="width:280px;flex:none;min-height:0">
        <n-card class="cy-card" :bordered="false" style="height:100%" :content-style="{ height: '100%', overflowY: 'auto' }">
          <n-empty v-if="!templates.length" description="还没有约束模板，点击右上角新建" style="margin:40px 0" />
          <div v-for="t in templates" :key="t.id"
            @click="select(t.id)"
            :style="{
              display:'flex',alignItems:'flex-start',justifyContent:'space-between',gap:8,
              padding:'10px 12px',marginBottom:8,cursor:'pointer',borderRadius:4,
              border: selectedId === t.id ? '1px solid var(--cy-cyan)' : '1px solid transparent',
              background: selectedId === t.id ? 'var(--cy-selected-bg)' : 'var(--cy-hover-bg)',
            }">
            <div style="min-width:0;flex:1">
              <div style="font-size:13px;font-weight:600">{{ t.name }}</div>
              <div style="font-size:12px;color:var(--cy-muted);white-space:nowrap;overflow:hidden;text-overflow:ellipsis;margin-top:4px">
                {{ concatTemplateContent(t) }}
              </div>
            </div>
            <n-button size="tiny" type="error" text @click.stop="del(t)">删除</n-button>
          </div>
        </n-card>
      </div>

      <!-- 右侧编辑区 -->
      <div style="flex:1;min-width:0;min-height:0;display:flex;flex-direction:column">
        <n-card class="cy-card" :bordered="false" style="flex:1;min-height:0;display:flex;flex-direction:column"
          :content-style="{ flex: '1', minHeight: '0', display: 'flex', flexDirection: 'column', overflow: 'hidden' }">
          <n-empty v-if="!current && !isNew" description="选择左侧模板查看/编辑，或点击右上角新建" style="margin:60px 0" />
          <div v-else style="flex:1;min-height:0;display:flex;flex-direction:column">
            <!-- 顶部：标题 + 名称（固定） -->
            <div style="flex-shrink:0">
              <div style="display:flex;align-items:center;justify-content:space-between;margin-bottom:14px">
                <div style="font-size:13px;color:var(--cy-muted)">
                  {{ isNew ? '新建模板' : `编辑模板 · ${current?.id ?? ''}` }}
                </div>
                <n-button v-if="!isNew" size="tiny" @click="copyTemplate">复制为新模板</n-button>
              </div>
              <div style="margin-bottom:12px">
                <div style="font-size:12px;color:var(--cy-muted);margin-bottom:6px">模板名称</div>
                <n-input v-model:value="editName" placeholder="如：Go 项目通用约束" />
              </div>
            </div>

            <!-- 中部：分段列表（滚动） -->
            <div :key="editorKey" style="flex:1;min-height:0;overflow-y:auto;padding-right:4px">
              <div style="font-size:12px;color:var(--cy-muted);margin-bottom:6px">
                模板分段（按顺序拼接成最终约束文本）
              </div>
              <div v-for="(m, i) in editModules" :key="m.id"
                style="border:1px solid var(--cy-border);border-radius:4px;padding:10px;margin-bottom:10px;background:var(--cy-hover-bg)">
                <div style="display:flex;align-items:center;gap:8px">
                  <n-button size="small" text @click="toggleCollapse(m.id)"
                    :title="collapsed[m.id] ? '展开此段' : '收起此段'" style="flex:none;font-size:15px;line-height:1">
                    {{ collapsed[m.id] ? '▸' : '▾' }}
                  </n-button>
                  <span style="font-size:12px;color:var(--cy-muted);flex:none">段 {{ i + 1 }}</span>
                  <n-input v-model:value="m.name" placeholder="段名（可选，如：项目背景）" size="small" style="flex:1" />
                  <n-button size="tiny" text :disabled="i === 0" @click="moveModule(i, -1)" title="上移">↑</n-button>
                  <n-button size="tiny" text :disabled="i === editModules.length - 1" @click="moveModule(i, 1)" title="下移">↓</n-button>
                  <n-button size="tiny" text type="error" @click="removeModule(i)" title="删除此段">✕</n-button>
                </div>
                <n-input v-if="!collapsed[m.id]" v-model:value="m.content" type="textarea"
                  :autosize="{ minRows: 3 }"
                  style="margin-top:8px"
                  placeholder="该段的约束详情，如：不引入第三方依赖 / 保持 Go 1.20 兼容..." />
                <div v-else
                  style="margin-top:8px;font-size:12px;color:var(--cy-muted);white-space:nowrap;overflow:hidden;text-overflow:ellipsis">
                  {{ m.content.trim() || '（此段为空）' }}
                </div>
              </div>
              <n-button size="small" dashed block @click="addModule">+ 添加一段</n-button>
            </div>

            <!-- 底部：使用中的任务 + 保存（固定） -->
            <div style="flex-shrink:0;margin-top:12px;padding-top:12px;border-top:1px solid var(--cy-border)">
              <div v-if="!isNew" style="margin-bottom:12px">
                <div style="font-size:12px;color:var(--cy-muted);margin-bottom:6px">
                  使用中的任务（未完结）
                </div>
                <n-empty v-if="!usedBy.length" description="当前没有未完结任务使用该模板" size="small" style="margin:8px 0" />
                <div v-for="u in usedBy" :key="u.id"
                  @click="router.push('/jobs/' + u.id)"
                  style="display:flex;align-items:center;justify-content:space-between;padding:6px 10px;margin-bottom:6px;border:1px solid var(--cy-border);border-radius:4px;cursor:pointer;background:var(--cy-hover-bg)">
                  <span style="font-size:13px">{{ u.name }}</span>
                  <StatusBadge :status="u.status" />
                </div>
              </div>
              <n-space justify="end">
                <n-button type="primary" :loading="saving" @click="save">保存</n-button>
              </n-space>
            </div>
          </div>
        </n-card>
      </div>
    </div>
  </div>
</template>
