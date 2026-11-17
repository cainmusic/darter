<script setup lang="ts">
import { computed, ref, h, type VNode } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  NConfigProvider, NMessageProvider, NDialogProvider, NLayout, NLayoutSider,
  NLayoutContent, NMenu, NPopover, NSelect, darkTheme,
  type GlobalThemeOverrides, type MenuOption,
} from 'naive-ui'
import { currentTheme, applyTheme, THEME_IDS, type ThemeId } from './theme'

const route = useRoute()
const router = useRouter()

interface Palette {
  primary: string
  success: string
  warning: string
  error: string
  info: string
  body: string
  card: string
  input: string
  popover: string
  modal: string
  border: string
  text1: string
  text2: string
  text3: string
  isDark: boolean
}

// 与 styles/cyber.css 中各主题变量保持一致（naive-ui 组件配色）
const palettes: Record<ThemeId, Palette> = {
  cyber: {
    primary: '#00f0ff', success: '#00ff9c', warning: '#ffb000', error: '#ff2d55', info: '#00f0ff',
    body: '#05070d', card: '#0a0f1a', input: 'rgba(255, 255, 255, 0.1)', popover: '#0d1420', modal: '#0a0f1a', border: '#1a2333',
    text1: '#c8d6e5', text2: '#8b98ac', text3: '#5c6a80', isDark: true,
  },
  light: {
    primary: '#2563eb', success: '#16a34a', warning: '#d97706', error: '#dc2626', info: '#2563eb',
    body: '#f5f6f8', card: '#ffffff', input: '#ffffff', popover: '#ffffff', modal: '#ffffff', border: '#e2e6ec',
    text1: '#1f2937', text2: '#4b5563', text3: '#9ca3af', isDark: false,
  },
  dark: {
    primary: '#58a6ff', success: '#3fb950', warning: '#d29922', error: '#f85149', info: '#58a6ff',
    body: '#0d1117', card: '#161b22', input: 'rgba(255, 255, 255, 0.1)', popover: '#1c2128', modal: '#161b22', border: '#30363d',
    text1: '#c9d1d9', text2: '#8b949e', text3: '#6e7681', isDark: true,
  },
  sepia: {
    primary: '#9c6b3f', success: '#6b8f3b', warning: '#b8860b', error: '#c0392b', info: '#9c6b3f',
    body: '#f4ecd8', card: '#faf3e3', input: '#faf3e3', popover: '#faf3e3', modal: '#faf3e3', border: '#e0d5bd',
    text1: '#3b3226', text2: '#6b5d44', text3: '#9c8d70', isDark: false,
  },
  dracula: {
    primary: '#bd93f9', success: '#50fa7b', warning: '#f1fa8c', error: '#ff5555', info: '#bd93f9',
    body: '#282a36', card: '#21222c', input: 'rgba(255, 255, 255, 0.1)', popover: '#1d1e26', modal: '#21222c', border: '#44475a',
    text1: '#f8f8f2', text2: '#b7b7c6', text3: '#6272a4', isDark: true,
  },
  'tokyo-night': {
    primary: '#7aa2f7', success: '#9ece6a', warning: '#e0af68', error: '#f7768e', info: '#7aa2f7',
    body: '#1a1b26', card: '#16161e', input: 'rgba(255, 255, 255, 0.1)', popover: '#1f2335', modal: '#16161e', border: '#3b4261',
    text1: '#c0caf5', text2: '#9aa5ce', text3: '#565f89', isDark: true,
  },
  synthwave: {
    primary: '#ff2d95', success: '#ffb000', warning: '#ffd166', error: '#ff3860', info: '#ff2d95',
    body: '#2b0f3a', card: '#1f0a2e', input: 'rgba(255, 255, 255, 0.1)', popover: '#240d33', modal: '#1f0a2e', border: '#4a1d6b',
    text1: '#f5e6ff', text2: '#c7b8d6', text3: '#8a7fa8', isDark: true,
  },
  matrix: {
    primary: '#00ff41', success: '#7dffb0', warning: '#c6ff00', error: '#ff003c', info: '#00ff41',
    body: '#020a02', card: '#051005', input: 'rgba(255, 255, 255, 0.1)', popover: '#0a1508', modal: '#051005', border: '#0f3d1f',
    text1: '#c8ffd4', text2: '#9fe8b0', text3: '#3a7d44', isDark: true,
  },
  nord: {
    primary: '#88c0d0', success: '#a3be8c', warning: '#ebcb8b', error: '#bf616a', info: '#88c0d0',
    body: '#2e3440', card: '#3b4252', input: 'rgba(255, 255, 255, 0.1)', popover: '#434c5e', modal: '#3b4252', border: '#4c566a',
    text1: '#eceff4', text2: '#c5ccd9', text3: '#616e88', isDark: true,
  },
  gruvbox: {
    primary: '#fabd2f', success: '#b8bb26', warning: '#fe8019', error: '#fb4934', info: '#fabd2f',
    body: '#282828', card: '#3c3836', input: 'rgba(255, 255, 255, 0.1)', popover: '#504945', modal: '#3c3836', border: '#665c54',
    text1: '#ebdbb2', text2: '#b8a99a', text3: '#928374', isDark: true,
  },
  'catppuccin-latte': {
    primary: '#1e66f5', success: '#40a02b', warning: '#df8e1d', error: '#d20f39', info: '#1e66f5',
    body: '#eff1f5', card: '#ffffff', input: '#ffffff', popover: '#e6e9ef', modal: '#ffffff', border: '#dce0e8',
    text1: '#4c4f69', text2: '#6c6f85', text3: '#8c8fa1', isDark: false,
  },
  'rose-pine-dawn': {
    primary: '#b4637a', success: '#56949f', warning: '#ea9d34', error: '#d7827e', info: '#b4637a',
    body: '#faf4ed', card: '#fffaf3', input: '#fffaf3', popover: '#f2e9e1', modal: '#fffaf3', border: '#dfdad9',
    text1: '#575279', text2: '#797593', text3: '#9893a5', isDark: false,
  },
  'everforest-light': {
    primary: '#3a94c5', success: '#8da101', warning: '#dfa000', error: '#f85552', info: '#3a94c5',
    body: '#fdf6e3', card: '#fffbef', input: '#fffbef', popover: '#f2efdf', modal: '#fffbef', border: '#e0dcc7',
    text1: '#5c6a72', text2: '#829181', text3: '#a3a891', isDark: false,
  },
}

function buildOverrides(p: Palette): GlobalThemeOverrides {
  return {
    common: {
      primaryColor: p.primary,
      primaryColorHover: p.primary,
      primaryColorPressed: p.primary,
      primaryColorSuppl: p.primary,
      successColor: p.success,
      warningColor: p.warning,
      errorColor: p.error,
      infoColor: p.info,
      bodyColor: p.body,
      cardColor: p.card,
      inputColor: p.input,
      popoverColor: p.popover,
      modalColor: p.modal,
      borderColor: p.border,
      dividerColor: p.border,
      textColorBase: p.text1,
      textColor1: p.text1,
      textColor2: p.text2,
      textColor3: p.text3,
      fontFamily: "'JetBrains Mono','PingFang SC',monospace",
      fontFamilyMono: "'JetBrains Mono',monospace",
      borderRadius: '4px',
    },
  }
}

const naiveTheme = computed(() => (palettes[currentTheme.value].isDark ? darkTheme : null))
const naiveOverrides = computed(() => buildOverrides(palettes[currentTheme.value]))

const siderCollapsed = ref(false)

// 菜单图标：内联 SVG（stroke 风格，跟随 currentColor），项目未引入图标库
function strokeIcon(children: VNode[]) {
  return () =>
    h('svg', {
      viewBox: '0 0 24 24', width: 20, height: 20,
      fill: 'none', stroke: 'currentColor', 'stroke-width': 2,
      'stroke-linecap': 'round', 'stroke-linejoin': 'round',
      style: 'display:block',
    }, children)
}

const menuOptions: MenuOption[] = [
  {
    label: '首页', key: 'home',
    icon: strokeIcon([
      h('rect', { x: 3, y: 3, width: 7, height: 7 }),
      h('rect', { x: 14, y: 3, width: 7, height: 7 }),
      h('rect', { x: 14, y: 14, width: 7, height: 7 }),
      h('rect', { x: 3, y: 14, width: 7, height: 7 }),
    ]),
  },
  {
    label: '审查修复', key: 'jobs',
    icon: strokeIcon([
      h('line', { x1: 8, y1: 6, x2: 21, y2: 6 }),
      h('line', { x1: 8, y1: 12, x2: 21, y2: 12 }),
      h('line', { x1: 8, y1: 18, x2: 21, y2: 18 }),
      h('line', { x1: 3, y1: 6, x2: 3.01, y2: 6 }),
      h('line', { x1: 3, y1: 12, x2: 3.01, y2: 12 }),
      h('line', { x1: 3, y1: 18, x2: 3.01, y2: 18 }),
    ]),
  },
  {
    label: '代码迁移', key: 'migrations',
    icon: strokeIcon([
      h('line', { x1: 6, y1: 3, x2: 6, y2: 15 }),
      h('circle', { cx: 18, cy: 6, r: 3 }),
      h('circle', { cx: 6, cy: 18, r: 3 }),
      h('path', { d: 'M18 9a9 9 0 0 1-9 9' }),
    ]),
  },
  {
    label: '约束模板', key: 'constraint-templates',
    icon: strokeIcon([
      h('path', { d: 'M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z' }),
      h('polyline', { points: '14 2 14 8 20 8' }),
      h('line', { x1: 16, y1: 13, x2: 8, y2: 13 }),
      h('line', { x1: 16, y1: 17, x2: 8, y2: 17 }),
    ]),
  },
  {
    label: '提示词库', key: 'prompts',
    icon: strokeIcon([
      h('path', { d: 'M4 4h16v12H8l-4 4z' }),
      h('line', { x1: 8, y1: 8, x2: 16, y2: 8 }),
      h('line', { x1: 8, y1: 12, x2: 13, y2: 12 }),
    ]),
  },
  {
    label: '设置', key: 'settings',
    icon: strokeIcon([
      h('circle', { cx: 12, cy: 12, r: 3 }),
      h('path', { d: 'M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1 0 2.83 2 2 0 0 1-2.83 0l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-2 2 2 2 0 0 1-2-2v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83 0 2 2 0 0 1 0-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1-2-2 2 2 0 0 1 2-2h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 0-2.83 2 2 0 0 1 2.83 0l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 2-2 2 2 0 0 1 2 2v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 0 2 2 0 0 1 0 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 2 2 2 2 0 0 1-2 2h-.09a1.65 1.65 0 0 0-1.51 1z' }),
    ]),
  },
]

const activeKey = computed(() => {
  if (route.path === '/') return 'home'
  if (route.path.startsWith('/migrations')) return 'migrations'
  if (route.path.startsWith('/settings')) return 'settings'
  if (route.path.startsWith('/constraint-templates')) return 'constraint-templates'
  if (route.path.startsWith('/prompts')) return 'prompts'
  return 'jobs'
})

function onMenu(key: string) {
  router.push(key === 'home' ? '/' : '/' + key)
}

// 点击侧边栏底部 logo：单击循环切换配色，长按弹出选配色框。
function cycleTheme() {
  const idx = THEME_IDS.indexOf(currentTheme.value)
  const next = THEME_IDS[(idx + 1) % THEME_IDS.length]
  applyTheme(next)
}

const showThemePicker = ref(false)
let pressTimer: number | undefined
let longPressTriggered = false

const themeOptions = [
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

function onLogoPressStart() {
  longPressTriggered = false
  pressTimer = window.setTimeout(() => {
    longPressTriggered = true
    showThemePicker.value = true
  }, 500)
}

function onLogoPressEnd() {
  if (pressTimer !== undefined) {
    clearTimeout(pressTimer)
    pressTimer = undefined
  }
}

function onLogoClick() {
  if (longPressTriggered) {
    longPressTriggered = false
    return
  }
  if (showThemePicker.value) {
    showThemePicker.value = false
    return
  }
  cycleTheme()
}

function onThemePick(v: unknown) {
  if (typeof v === 'string') applyTheme(v as ThemeId)
  showThemePicker.value = false
}
</script>

<template>
  <n-config-provider :theme="naiveTheme" :theme-overrides="naiveOverrides">
    <n-message-provider>
      <n-dialog-provider>
        <n-layout has-sider style="height: 100vh">
          <n-layout-sider bordered width="200" :collapsed-width="64" collapse-mode="width"
            v-model:collapsed="siderCollapsed" show-trigger="bar"
            :trigger-style="{ right: '-15.5px', zIndex: 2, top: 'calc(33.333% - 36px)' }"
            :collapsed-trigger-style="{ right: '-15.5px', zIndex: 2, top: 'calc(33.333% - 36px)' }"
            :content-style="siderCollapsed ? 'padding: 14px 0' : 'padding: 14px 8px'">
            <div style="display:flex;flex-direction:column;height:100%;min-height:0">
              <div class="logo font-display" style="font-size:22px;padding:0 8px 16px;text-align:center">
                <template v-if="!siderCollapsed">
                  <span class="glow-cyan">DART</span><span class="glow-green">ER</span>
                  <div style="font-size:10px;color:var(--cy-muted);letter-spacing:0.2em;margin-top:2px">GET THINGS DONE</div>
                </template>
                <template v-else>
                  <span class="glow-cyan">D</span>
                </template>
              </div>
              <n-menu :value="activeKey" :options="menuOptions" :collapsed="siderCollapsed"
                :collapsed-width="64" :collapsed-icon-size="20" style="flex:1;min-height:0" @update:value="onMenu" />
              <n-popover trigger="manual" :show="showThemePicker" placement="top" :show-arrow="true" @clickoutside="showThemePicker = false">
                <template #trigger>
                  <div style="flex-shrink:0;display:flex;justify-content:center;padding:12px 0 6px;border-top:1px solid var(--cy-border);cursor:pointer" title="单击切换配色 / 长按选择配色"
                    @mousedown="onLogoPressStart" @mouseup="onLogoPressEnd" @mouseleave="onLogoPressEnd" @click="onLogoClick">
                    <svg viewBox="0 0 24 24" width="28" height="28" fill="none" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                      <path d="M8 7 A4 4 0 0 1 16 7" style="stroke:var(--cy-cyan)"/>
                      <path d="M8 7 H16" style="stroke:var(--cy-cyan)"/>
                      <path d="M12 7 V20" style="stroke:var(--cy-cyan)"/>
                      <path d="M6 11 L18 17" style="stroke:var(--cy-green)"/>
                      <path d="M18 11 L6 17" style="stroke:var(--cy-green)"/>
                    </svg>
                  </div>
                </template>
                <div style="width:240px">
                  <n-select :value="currentTheme" :options="themeOptions" @update:value="onThemePick" />
                </div>
              </n-popover>
            </div>
          </n-layout-sider>
          <n-layout-content style="height: 100vh; overflow: auto">
            <router-view />
          </n-layout-content>
        </n-layout>
      </n-dialog-provider>
    </n-message-provider>
  </n-config-provider>
</template>
