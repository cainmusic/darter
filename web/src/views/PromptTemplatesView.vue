<script setup lang="ts">
import { onMounted, ref, computed } from 'vue'
import { NCard, NTag, NEmpty, NSelect, useMessage } from 'naive-ui'
import { listPrompts, listScenes, type PromptItem, type Scene } from '../api'
import PageHeader from '../components/PageHeader.vue'
import { GAP } from '../layout'

const message = useMessage()

const prompts = ref<PromptItem[]>([])
const scenes = ref<Scene[]>([])
const selectedKey = ref<string | null>(null)

const current = computed(() => prompts.value.find((p) => p.key === selectedKey.value) ?? null)

const filterFn = ref<string>('')

const sceneName = (key: string) => scenes.value.find((s) => s.key === key)?.name ?? key

const filterOptions = computed(() => [
  { label: '全部场景', value: '' },
  ...scenes.value.map((s) => ({ label: `${s.name}（${s.key}）`, value: s.key })),
])

const filtered = computed(() => {
  if (!filterFn.value) return prompts.value
  return prompts.value.filter((p) => p.scene === filterFn.value)
})

async function load() {
  try {
    scenes.value = await listScenes()
    prompts.value = await listPrompts()
    if (!selectedKey.value || !prompts.value.some((p) => p.key === selectedKey.value)) {
      selectedKey.value = prompts.value[0]?.key ?? null
    }
  } catch (e: any) { message.error(e.message) }
}

function phText(ph: string) { return '{{' + ph + '}}' }

onMounted(() => load())
</script>

<template>
  <div style="height:100%;display:flex;flex-direction:column;padding:8px">
    <PageHeader title="提示词库" subtitle="内置模板固定，仅展示；修改请走开发流程" />

    <div :style="`display:flex;gap:${GAP}px;align-items:stretch;flex:1;min-height:0`">
      <!-- 左侧列表 -->
      <div style="width:300px;flex:none;min-height:0">
        <n-card class="cy-card" :bordered="false" style="height:100%" :content-style="{ height: '100%', overflowY: 'auto' }">
          <n-select v-model:value="filterFn" :options="filterOptions" size="small" clearable placeholder="按场景筛选" style="margin-bottom:8px" />
          <n-empty v-if="!filtered.length" description="暂无提示词模板" style="margin:40px 0" />
          <div v-for="p in filtered" :key="p.key"
            @click="selectedKey = p.key"
            :style="{
              display:'flex',alignItems:'flex-start',justifyContent:'space-between',gap:8,
              padding:'10px 12px',marginBottom:8,cursor:'pointer',borderRadius:4,
              border: selectedKey === p.key ? '1px solid var(--cy-cyan)' : '1px solid transparent',
              background: selectedKey === p.key ? 'var(--cy-selected-bg)' : 'var(--cy-hover-bg)',
            }">
            <div style="min-width:0;flex:1">
              <div style="font-size:13px;font-weight:600;overflow:hidden;text-overflow:ellipsis;white-space:nowrap">{{ sceneName(p.scene) }} · {{ p.name }}</div>
            </div>
          </div>
        </n-card>
      </div>

      <!-- 右侧详情（只读） -->
      <div style="flex:1;min-width:0;min-height:0;display:flex;flex-direction:column">
        <n-card class="cy-card" :bordered="false" style="flex:1;min-height:0;display:flex;flex-direction:column"
          :content-style="{ flex: '1', minHeight: '0', display: 'flex', flexDirection: 'column', overflow: 'hidden' }">
          <n-empty v-if="!current" description="选择左侧提示词模板查看" style="margin:60px 0" />
          <div v-else style="flex:1;min-height:0;display:flex;flex-direction:column">
            <!-- 顶部：名称 + 所属模块 + 说明 + 占位符（固定） -->
            <div style="flex-shrink:0">
              <div style="display:flex;align-items:center;gap:8px;margin-bottom:8px">
                <span style="font-size:15px;font-weight:600">{{ sceneName(current.scene) }} · {{ current.name }}</span>
              </div>
              <div style="font-size:12px;color:var(--cy-muted);margin-bottom:8px">{{ current.description }}</div>
              <div v-if="current.placeholders?.length" style="font-size:12px;color:var(--cy-muted);margin-bottom:12px">
                可用占位符：
                <n-tag v-for="ph in current.placeholders" :key="ph" size="tiny" :bordered="false" style="margin-right:6px;font-family:var(--font-mono)">{{ phText(ph) }}</n-tag>
              </div>
            </div>

            <!-- 中部：提示词内容（只读，滚动） -->
            <div style="flex:1;min-height:0;display:flex;flex-direction:column">
              <div style="font-size:12px;color:var(--cy-muted);margin-bottom:6px;flex-shrink:0">提示词内容</div>
              <div style="flex:1;min-height:0;overflow-y:auto;white-space:pre-wrap;word-break:break-word;font-size:12px;line-height:1.7;background:var(--cy-hover-bg);border:1px solid var(--cy-border);border-radius:4px;padding:10px 12px;font-family:var(--font-mono)">{{ current.content }}</div>
            </div>
          </div>
        </n-card>
      </div>
    </div>
  </div>
</template>
