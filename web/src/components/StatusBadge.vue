<script setup lang="ts">
import { computed } from 'vue'
import { NTag } from 'naive-ui'

const props = defineProps<{ status: string }>()

// status → 语义色键，对应 cyber.css 的 --cy-<key> / --cy-<key>-bg 变量
const keyMap: Record<string, string> = {
  draft: 'muted',
  parsing: 'cyan',
  reviewing_tasks: 'cyan',
  ready: 'cyan',
  running: 'amber',
  paused: 'amber',
  cancelled: 'muted',
  awaiting_review: 'amber',
  review_done: 'green',
  completed: 'green',
  archived: 'green',
  failed: 'red',
  archiving: 'cyan',
  awaiting_archive_confirm: 'amber',
  archive_blocked: 'red',
  mig_draft: 'muted',
  mig_ready: 'cyan',
  mig_running: 'amber',
  mig_conflict: 'red',
  mig_completed: 'green',
  mig_cancelled: 'muted',
  mig_failed: 'red',
  pending: 'muted',
  fixing: 'amber',
  reviewing: 'cyan',
  passed: 'green',
  skipped: 'skip',
  deferred: 'deferred',
  approved: 'green',
  rejected: 'red',
}

const labelMap: Record<string, string> = {
  draft: '草稿',
  parsing: '解析中',
  reviewing_tasks: '复查中',
  ready: '待开始',
  running: '运行中',
  paused: '已暂停',
  cancelled: '已取消',
  awaiting_review: '待审查',
  review_done: '审查完成',
  completed: '已完成',
  archived: '已归档',
  failed: '失败',
  archiving: '归档中',
  awaiting_archive_confirm: '待确认归档',
  archive_blocked: '归档卡点',
  mig_draft: '草稿',
  mig_ready: '待迁移',
  mig_running: '迁移中',
  mig_conflict: '冲突',
  mig_completed: '已完成',
  mig_cancelled: '已取消',
  mig_failed: '失败',
  pending: '待处理',
  fixing: '修复中',
  reviewing: '审查中',
  passed: '通过',
  skipped: '无需修改',
  deferred: '已跳过',
  approved: '已审批',
  rejected: '已驳回',
}

const info = computed(() => ({
  label: labelMap[props.status] ?? props.status,
  key: keyMap[props.status] ?? 'muted',
}))
</script>

<template>
  <n-tag size="small" :bordered="false"
    :style="{
      background: `var(--cy-${info.key}-bg)`,
      color: `var(--cy-${info.key})`,
      boxShadow: 'var(--cy-tag-glow)',
    }">
    {{ info.label }}
  </n-tag>
</template>
