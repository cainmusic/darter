<script setup lang="ts">
import { onMounted, onBeforeUnmount, ref, watch } from 'vue'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'
import { currentTheme, terminalThemes } from '../theme'

const props = defineProps<{ lines: string[] }>()
const el = ref<HTMLElement>()
let term: Terminal | null = null
let fit: FitAddon | null = null
let written = 0

let ro: ResizeObserver | null = null

onMounted(() => {
  term = new Terminal({
    convertEol: true,
    cursorBlink: true,
    fontFamily: "'JetBrains Mono', monospace",
    fontSize: 12,
    theme: terminalThemes[currentTheme.value],
  })
  fit = new FitAddon()
  term.loadAddon(fit)
  term.open(el.value!)
  fit.fit()
  for (let i = 0; i < props.lines.length; i++) term.writeln(props.lines[i])
  written = props.lines.length
  // 监听容器尺寸变化（侧边栏收起/展开、窗口缩放等）自动重排，避免终端内容超框
  ro = new ResizeObserver(() => fit?.fit())
  ro.observe(el.value!)
})

watch(() => props.lines.length, () => {
  if (!term) return
  for (let i = written; i < props.lines.length; i++) term.writeln(props.lines[i])
  written = props.lines.length
})

watch(currentTheme, (t) => {
  if (!term) return
  term.options.theme = terminalThemes[t]
  term.refresh(0, term.rows - 1)
})

onBeforeUnmount(() => {
  ro?.disconnect()
  term?.dispose()
})
</script>

<template>
  <div class="terminal-wrap">
    <div ref="el" style="height: 100%"></div>
  </div>
</template>
