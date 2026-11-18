<script setup lang="ts">
import { NInput } from 'naive-ui'
import type { ConstraintVariable } from '../api'

const props = defineProps<{ variables: ConstraintVariable[] }>()
// values 为 key → value 的取值（父组件通过 v-model:values 绑定）。
const values = defineModel<Record<string, string>>('values', { default: () => ({}) })

function ph(key: string) { return '{{' + key + '}}' }
function setVar(key: string, value: string) {
  values.value = { ...(values.value ?? {}), [key]: value }
}
</script>

<template>
  <div v-if="props.variables?.length" style="margin-top:12px">
    <div style="font-size:12px;color:var(--cy-muted);margin-bottom:6px">注入变量配置</div>
    <div v-for="v in props.variables" :key="v.key" style="margin-bottom:10px">
      <div style="font-size:13px;margin-bottom:4px">
        {{ v.name }}
        <code style="font-family:var(--font-mono);font-size:12px;color:var(--cy-cyan)">{{ ph(v.key) }}</code>
      </div>
      <div v-if="v.description" style="font-size:12px;color:var(--cy-muted);margin-bottom:4px">{{ v.description }}</div>
      <n-input :value="(values ?? {})[v.key] ?? ''" @update:value="setVar(v.key, $event)" :placeholder="v.description || v.name" />
    </div>
  </div>
</template>
