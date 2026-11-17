<script setup lang="ts">
// 统一的页面标题区：标题（18px）+ 可选副标题（标题右侧一行小字）+ 右侧操作插槽。
// 各页面统一走这里，避免标题区结构/间距手写不一致。
defineProps<{
  title: string
  subtitle?: string
}>()
</script>

<template>
  <div style="display:flex;align-items:center;justify-content:space-between;margin-bottom:8px;min-height:34px;flex-shrink:0">
    <div style="display:flex;align-items:baseline;gap:8px;min-width:0">
      <span class="cy-title" style="font-size:18px;line-height:1.4;white-space:nowrap">{{ title }}</span>
      <span v-if="subtitle" style="color:var(--cy-muted);font-size:12px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis">{{ subtitle }}</span>
    </div>
    <div v-if="$slots.default" style="flex-shrink:0;display:flex;align-items:center;gap:8px">
      <slot />
    </div>
  </div>
</template>
