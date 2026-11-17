<script setup lang="ts">
import { computed } from 'vue'

const props = defineProps<{ diff: string }>()
const lines = computed(() => (props.diff || '').split('\n'))

function cls(line: string) {
  if (line.startsWith('+') && !line.startsWith('+++')) return 'diff-add'
  if (line.startsWith('-') && !line.startsWith('---')) return 'diff-del'
  if (/^(@@|diff |index |--- |\+\+\+ |new file|deleted file)/.test(line)) return 'diff-meta'
  return ''
}
</script>

<template>
  <div class="code">
    <div v-if="!diff" class="diff-meta">（无 diff）</div>
    <div v-for="(l, i) in lines" :key="i" :class="cls(l)">{{ l || ' ' }}</div>
  </div>
</template>
