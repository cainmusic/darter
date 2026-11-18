<script setup lang="ts">
import { onMounted, ref, computed } from 'vue'
import { useRouter } from 'vue-router'
import {
  NButton, NCard, NEmpty, NInput, NSpace, NSelect, NTag, NModal, useMessage, useDialog,
} from 'naive-ui'
import {
  listConstraintTemplates, getConstraintTemplate, copyConstraintTemplate,
  createConstraintTemplate, updateConstraintTemplate, deleteConstraintTemplate,
  listConstraintInjectables, SCENE_LABELS,
  type ConstraintTemplate, type ConstraintModule, type ConstraintVariable, type TemplateUser,
  type InjectableCatalog, type InjectableField,
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

const editScene = ref('common')
const editVariables = ref<ConstraintVariable[]>([])
const injectableCatalog = ref<InjectableCatalog | null>(null)
const injectableError = ref('')
const showInjectables = ref(false)

const sceneOptions = [
  { label: '通用', value: 'common' },
  { label: '审查修复', value: 'fixer' },
  { label: '代码迁移', value: 'migration' },
]
// 只展示「确有可注入字段」的场景，跳过通用等空段。
const injectableScenes = computed(() => {
  const scenes = injectableCatalog.value?.scenes ?? []
  const fields = injectableCatalog.value?.fields ?? []
  return scenes.filter((s) => fields.some((f) => f.scene === s.key))
})
const fieldsByScene = computed(() => {
  const map = new Map<string, InjectableField[]>()
  for (const f of injectableCatalog.value?.fields ?? []) {
    const arr = map.get(f.scene) ?? []
    arr.push(f)
    map.set(f.scene, arr)
  }
  return map
})

function sceneLabel(s?: string) { return SCENE_LABELS[s ?? 'common'] ?? s ?? '通用' }
async function openInjectables() {
  showInjectables.value = true
  injectableError.value = ''
  if (!injectableCatalog.value) {
    try { injectableCatalog.value = await listConstraintInjectables() } catch (e: any) {
      injectableError.value = e?.message || '加载失败，请确认服务已重启到最新版本'
    }
  }
}
async function copyPlaceholder(p: string) {
  try { await navigator.clipboard.writeText(p); message.success('已复制 ' + p) } catch { message.warning('复制失败，请手动复制') }
}

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

function addVariable() {
  editVariables.value.push({ name: '', key: '', description: '' })
}

function removeVariable(i: number) {
  editVariables.value.splice(i, 1)
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
  editScene.value = t?.scene ?? 'common'
  editModules.value = (t?.modules ?? []).map((m) => ({ ...m }))
  editVariables.value = (t?.variables ?? []).map((v) => ({ ...v }))
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
  editScene.value = 'common'
  editModules.value = [{ id: genModuleId(), name: '', content: '' }]
  editVariables.value = []
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
      t = await createConstraintTemplate(name, mods, editScene.value, editVariables.value)
      templates.value.push(t)
    } else if (selectedId.value) {
      t = await updateConstraintTemplate(selectedId.value, name, mods, editScene.value, editVariables.value)
      const i = templates.value.findIndex((x) => x.id === t.id)
      if (i >= 0) templates.value[i] = t
    } else {
      message.warning('请先选择一个模板或点击新建'); return
    }
    selectedId.value = t.id
    isNew.value = false
    editName.value = t.name
    editModules.value = (t.modules ?? []).map((m) => ({ ...m }))
    editVariables.value = (t.variables ?? []).map((v) => ({ ...v }))
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
      <n-space>
        <n-button @click="openInjectables">可注入字段</n-button>
        <n-button type="primary" @click="newTemplate">+ 新建模板</n-button>
      </n-space>
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
              <n-tag size="tiny" :bordered="false" style="margin-top:4px">{{ sceneLabel(t.scene) }}</n-tag>
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
              <div style="margin-bottom:12px">
                <div style="font-size:12px;color:var(--cy-muted);margin-bottom:6px">所属场景</div>
                <n-select v-model:value="editScene" :options="sceneOptions" style="max-width:260px" />
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

              <div style="margin-top:20px">
                <div style="font-size:12px;color:var(--cy-muted);margin-bottom:6px">
                  注入变量（可选，建任务时按此渲染配置列表；约束文本里用双花括号包裹 key 引用）
                </div>
                <div v-for="(v, i) in editVariables" :key="i"
                  style="border:1px solid var(--cy-border);border-radius:4px;padding:10px;margin-bottom:10px;background:var(--cy-hover-bg)">
                  <div style="display:flex;align-items:center;justify-content:space-between;margin-bottom:8px">
                    <span style="font-size:12px;color:var(--cy-muted)">变量 {{ i + 1 }}</span>
                    <n-button size="tiny" text type="error" @click="removeVariable(i)" title="删除此变量">✕</n-button>
                  </div>
                  <div style="display:flex;gap:8px;margin-bottom:8px">
                    <n-input v-model:value="v.name" placeholder="字段名（如：参考分支）" size="small" style="flex:1" />
                    <n-input v-model:value="v.key" placeholder="注入 key（如：refs）" size="small" style="flex:1" />
                  </div>
                  <n-input v-model:value="v.description" placeholder="说明文字（配置时提示，可选）" size="small" />
                </div>
                <n-button size="small" dashed block @click="addVariable">+ 添加变量</n-button>
              </div>
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

    <!-- 可注入字段表 -->
    <n-modal v-model:show="showInjectables" preset="card" title="可注入字段" style="max-width:820px">
      <div style="font-size:12px;color:var(--cy-muted);margin-bottom:12px">
        写约束文本时可插入以下占位符，运行时会被替换成对应任务的真实信息。点击占位符复制。
      </div>
      <div v-if="injectableError" style="font-size:12px;color:#d03050;margin-bottom:12px">
        加载失败：{{ injectableError }}
      </div>
      <div v-else-if="!injectableCatalog" style="font-size:12px;color:var(--cy-muted);padding:20px 0;text-align:center">加载中…</div>
      <div v-else style="max-height:60vh;overflow:auto;padding-right:4px">
        <div v-for="sc in injectableScenes" :key="sc.key" style="margin-bottom:16px">
          <div style="font-size:13px;font-weight:600;margin-bottom:8px">{{ sc.name }}</div>
          <table style="width:100%;border-collapse:collapse;font-size:12px">
            <thead>
              <tr>
                <th style="text-align:left;padding:6px 8px;border-bottom:1px solid var(--cy-border);color:var(--cy-muted);font-weight:600;width:240px">占位符</th>
                <th style="text-align:left;padding:6px 8px;border-bottom:1px solid var(--cy-border);color:var(--cy-muted);font-weight:600">说明</th>
                <th style="text-align:left;padding:6px 8px;border-bottom:1px solid var(--cy-border);color:var(--cy-muted);font-weight:600;width:140px">有值时机</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="f in (fieldsByScene.get(sc.key) ?? [])" :key="f.placeholder">
                <td style="padding:6px 8px;vertical-align:top;border-bottom:1px solid var(--cy-border)">
                  <code @click="copyPlaceholder(f.placeholder)" title="点击复制"
                    style="font-family:var(--font-mono);font-size:12px;color:var(--cy-cyan);cursor:pointer;word-break:break-all">{{ f.placeholder }}</code>
                </td>
                <td style="padding:6px 8px;vertical-align:top;border-bottom:1px solid var(--cy-border)">
                  <div>{{ f.name }}</div>
                  <div style="color:var(--cy-muted);margin-top:2px">{{ f.description }}</div>
                </td>
                <td style="padding:6px 8px;vertical-align:top;border-bottom:1px solid var(--cy-border);color:var(--cy-amber)">{{ f.availability }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
      <template #footer>
        <n-space justify="end">
          <n-button @click="showInjectables = false">关闭</n-button>
        </n-space>
      </template>
    </n-modal>
  </div>
</template>
