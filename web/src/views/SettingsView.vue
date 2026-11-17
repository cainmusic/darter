<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { NButton, NCard, NForm, NFormItem, NInput, NInputNumber, NSelect, NSpace, NAlert, useMessage, type SelectOption } from 'naive-ui'
import { getSettings, saveSettings, type Settings } from '../api'
import { currentTheme, applyTheme, type ThemeId } from '../theme'
import PageHeader from '../components/PageHeader.vue'

const message = useMessage()
const form = ref<Settings>({ claude_cmd: 'claude', claude_model: '', default_retries: 3 })
const saving = ref(false)

const themeOptions: SelectOption[] = [
  { label: '赛博 Cyber', value: 'cyber' },
  { label: '黑客帝国 Matrix', value: 'matrix' },
  { label: '暗色 Dark', value: 'dark' },
  { label: '合成波 Synthwave', value: 'synthwave' },
  { label: '午夜东京 Tokyo Night', value: 'tokyo-night' },
  { label: '德古拉 Dracula', value: 'dracula' },
  { label: '北欧 Nord', value: 'nord' },
  { label: '复古 Gruvbox', value: 'gruvbox' },
  { label: '护眼 Sepia', value: 'sepia' },
  { label: '玫瑰黎明 Rosé Pine Dawn', value: 'rose-pine-dawn' },
  { label: '常青森林 Everforest Light', value: 'everforest-light' },
  { label: '拿铁 Catppuccin Latte', value: 'catppuccin-latte' },
  { label: '亮色 Light', value: 'light' },
]

function onThemeChange(v: unknown) {
  if (typeof v === 'string') applyTheme(v as ThemeId)
}

async function load() {
  try { form.value = await getSettings() } catch (e: any) { message.error(e.message) }
}
async function save() {
  saving.value = true
  try { await saveSettings(form.value); message.success('已保存') } catch (e: any) { message.error(e.message) } finally { saving.value = false }
}

onMounted(load)
</script>

<template>
  <div style="padding: 8px">
    <PageHeader title="全局设置" />

    <div style="max-width: 640px">
      <n-card class="cy-card" style="margin-bottom:8px">
      <div class="cy-title" style="font-size:13px;margin-bottom:10px">配色主题</div>
      <n-select :value="currentTheme" :options="themeOptions" style="max-width:240px" @update:value="onThemeChange" />
    </n-card>

    <n-card class="cy-card">
      <n-alert type="info" :bordered="false" style="margin-bottom:12px">
        <div style="font-size:12px;line-height:1.6">
          以下配置会在<strong>创建修复任务时</strong>固化到该任务里（claude 命令 → claude_cmd、claude model → claude_model、默认重试次数 → max_retries）。
          之后修改这些设置<strong>只对新建的任务生效</strong>，不影响已存在的任务；如需改动已有任务，请手动编辑其 job.json。
        </div>
      </n-alert>
      <n-form label-placement="left" label-width="130">
        <n-form-item label="claude 命令">
          <n-input v-model:value="form.claude_cmd" placeholder="claude" />
        </n-form-item>
        <n-form-item label="claude model">
          <n-input v-model:value="form.claude_model" placeholder="留空用默认（如 opus/sonnet）" />
        </n-form-item>
        <n-form-item label="默认重试次数">
          <n-input-number v-model:value="form.default_retries" :min="1" :max="10" />
        </n-form-item>
      </n-form>
      <n-space justify="end" style="margin-top:8px">
        <n-button type="primary" :loading="saving" @click="save">保存</n-button>
      </n-space>
      </n-card>
    </div>
  </div>
</template>
