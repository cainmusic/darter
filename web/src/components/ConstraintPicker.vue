<script setup lang="ts">
import { onMounted, ref, computed } from 'vue'
import { NSelect, NInput, NAlert, NButton, NModal, NForm, NFormItem, useMessage } from 'naive-ui'
import { listConstraintTemplates, createConstraintTemplate, concatTemplateContent, type ConstraintTemplate } from '../api'

const props = defineProps<{
  templateLabel?: string
  constraintsLabel?: string
  constraintsPlaceholder?: string
}>()

// 选中的约束模板 id（'' = 未选，用内联文本）；内联约束文本（未选模板时使用）。
const templateId = defineModel<string>('templateId', { default: '' })
const constraints = defineModel<string>('constraints', { default: '' })

const message = useMessage()
const templates = ref<ConstraintTemplate[]>([])
const showSaveTemplate = ref(false)
const templateName = ref('')
const savingTemplate = ref(false)

const templateOptions = computed(() => templates.value.map((t) => ({ label: t.name, value: t.id })))
const selectedTemplateContent = computed(() => {
  const t = templates.value.find((t) => t.id === templateId.value)
  return t ? concatTemplateContent(t) : ''
})

async function loadTemplates() {
  try { templates.value = await listConstraintTemplates() } catch { /* ignore */ }
}

function onTemplatePick(id: string | null) {
  templateId.value = id ?? ''
  // 用模板时清空内联文本，避免「引用模板 + 内联文本」并存造成歧义
  if (id) constraints.value = ''
}

function openSaveTemplate() {
  if (!constraints.value.trim()) { message.warning('请先填写项目约束内容'); return }
  templateName.value = ''
  showSaveTemplate.value = true
}

async function confirmSaveTemplate() {
  const name = templateName.value.trim()
  if (!name) { message.warning('请输入模板名称'); return }
  savingTemplate.value = true
  try {
    const t = await createConstraintTemplate(name, [{ id: 'm0', name: '内容', content: constraints.value }])
    templates.value.push(t)
    templateId.value = t.id
    constraints.value = ''
    message.success('已保存为模板')
    showSaveTemplate.value = false
  } catch (e: any) { message.error(e.message) } finally { savingTemplate.value = false }
}

onMounted(loadTemplates)
</script>

<template>
  <n-form-item :label="props.templateLabel ?? '约束模板（可选）'">
    <n-select
      :value="templateId || undefined"
      :options="templateOptions"
      placeholder="选择一个已保存的模板，或留空使用下方自定义文本"
      clearable
      @update:value="onTemplatePick" />
  </n-form-item>
  <n-form-item :label="props.constraintsLabel ?? '项目约束（可选）'">
    <div style="width:100%">
      <n-alert v-if="templateId" type="info" :bordered="false" style="margin-bottom:6px">
        <span style="white-space:pre-wrap">已引用模板，内容（只读）：{{ selectedTemplateContent }}</span>
      </n-alert>
      <n-input v-else v-model:value="constraints" type="textarea" :rows="3"
        :placeholder="props.constraintsPlaceholder ?? '如：不引入第三方依赖 / 保持兼容 / 遵循某代码规范 / 不修改公共 API...'" />
      <div v-if="!templateId" style="margin-top:6px;text-align:right">
        <n-button size="tiny" @click="openSaveTemplate">将当前约束保存为模板</n-button>
      </div>
    </div>
  </n-form-item>

  <n-modal v-model:show="showSaveTemplate" preset="card" title="保存为约束模板" style="max-width:420px">
    <n-form label-placement="top">
      <n-form-item label="模板名称 *">
        <n-input v-model:value="templateName" placeholder="如：Go 项目通用约束" />
      </n-form-item>
    </n-form>
    <template #footer>
      <n-space justify="end">
        <n-button @click="showSaveTemplate = false">取消</n-button>
        <n-button type="primary" :loading="savingTemplate" @click="confirmSaveTemplate">保存</n-button>
      </n-space>
    </template>
  </n-modal>
</template>
