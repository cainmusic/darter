import { ref } from 'vue'

// 主题切换：维护 <html data-theme> 与 localStorage。
// CSS 变量定义在 styles/cyber.css（:root 为默认赛博主题，其余为 html[data-theme=...]），
// naive-ui 组件配色定义在 App.vue 的 palettes，终端配色见下方 terminalThemes。
export type ThemeId =
  | 'cyber' | 'light' | 'dark' | 'sepia'
  | 'dracula' | 'tokyo-night' | 'synthwave' | 'matrix' | 'nord' | 'gruvbox'
  | 'catppuccin-latte' | 'rose-pine-dawn' | 'everforest-light'

export const THEME_IDS: ThemeId[] = [
  'cyber', 'matrix', 'dark', 'synthwave', 'tokyo-night', 'dracula', 'nord', 'gruvbox',
  'sepia', 'rose-pine-dawn', 'everforest-light', 'catppuccin-latte', 'light',
]

const STORAGE_KEY = 'darter-theme'

function readStored(): ThemeId {
  try {
    const v = localStorage.getItem(STORAGE_KEY)
    if (v && (THEME_IDS as string[]).includes(v)) return v as ThemeId
  } catch { /* ignore */ }
  return 'cyber'
}

// 当前主题的响应式源（App.vue / TerminalPanel 等共用）
export const currentTheme = ref<ThemeId>(readStored())

export function loadThemeId(): ThemeId {
  return readStored()
}

export function applyTheme(id: ThemeId) {
  currentTheme.value = id
  try {
    if (id === 'cyber') document.documentElement.removeAttribute('data-theme')
    else document.documentElement.setAttribute('data-theme', id)
    localStorage.setItem(STORAGE_KEY, id)
  } catch { /* ignore */ }
  applyFavicon(id)
}

// favicon 跟随主题：favicon 是独立 SVG 文档，读不到页面的 CSS 变量，
// 只能按主题切换 <link rel="icon"> 的 href（每个主题配色一个 favicon 文件）。
const FAVICON: Record<ThemeId, string> = {
  cyber: '/favicon.svg', // 复用默认 cyber 配色文件
  light: '/favicon-light.svg',
  dark: '/favicon-dark.svg',
  sepia: '/favicon-sepia.svg',
  dracula: '/favicon-dracula.svg',
  'tokyo-night': '/favicon-tokyo-night.svg',
  synthwave: '/favicon-synthwave.svg',
  matrix: '/favicon-matrix.svg',
  nord: '/favicon-nord.svg',
  gruvbox: '/favicon-gruvbox.svg',
  'catppuccin-latte': '/favicon-catppuccin-latte.svg',
  'rose-pine-dawn': '/favicon-rose-pine-dawn.svg',
  'everforest-light': '/favicon-everforest-light.svg',
}

function applyFavicon(id: ThemeId) {
  try {
    const link = document.querySelector<HTMLLinkElement>('link[rel="icon"]')
    if (link) link.href = FAVICON[id]
  } catch { /* ignore */ }
}

// xterm 终端配色（跟随主题，与 cyber.css 的 --cy-* 保持一致）
export interface TerminalTheme {
  background: string
  foreground: string
  cursor: string
  selectionBackground: string
  green: string
  cyan: string
  yellow: string
  red: string
}

export const terminalThemes: Record<ThemeId, TerminalTheme> = {
  cyber: { background: '#04060a', foreground: '#c8d6e5', cursor: '#00f0ff', selectionBackground: '#1a2333', green: '#00ff9c', cyan: '#00f0ff', yellow: '#ffb000', red: '#ff2d55' },
  light: { background: '#ffffff', foreground: '#1f2937', cursor: '#2563eb', selectionBackground: '#e2e6ec', green: '#16a34a', cyan: '#2563eb', yellow: '#d97706', red: '#dc2626' },
  dark: { background: '#0d1117', foreground: '#c9d1d9', cursor: '#58a6ff', selectionBackground: '#30363d', green: '#3fb950', cyan: '#58a6ff', yellow: '#d29922', red: '#f85149' },
  sepia: { background: '#faf3e3', foreground: '#3b3226', cursor: '#9c6b3f', selectionBackground: '#e0d5bd', green: '#6b8f3b', cyan: '#9c6b3f', yellow: '#b8860b', red: '#c0392b' },
  dracula: { background: '#21222c', foreground: '#f8f8f2', cursor: '#bd93f9', selectionBackground: '#44475a', green: '#50fa7b', cyan: '#bd93f9', yellow: '#f1fa8c', red: '#ff5555' },
  'tokyo-night': { background: '#1a1b26', foreground: '#c0caf5', cursor: '#7aa2f7', selectionBackground: '#3b4261', green: '#9ece6a', cyan: '#7aa2f7', yellow: '#e0af68', red: '#f7768e' },
  synthwave: { background: '#1f0a2e', foreground: '#f5e6ff', cursor: '#ff2d95', selectionBackground: '#4a1d6b', green: '#ffb000', cyan: '#ff2d95', yellow: '#ffd166', red: '#ff3860' },
  matrix: { background: '#020a02', foreground: '#c8ffd4', cursor: '#00ff41', selectionBackground: '#0f3d1f', green: '#7dffb0', cyan: '#00ff41', yellow: '#c6ff00', red: '#ff003c' },
  nord: { background: '#2e3440', foreground: '#eceff4', cursor: '#88c0d0', selectionBackground: '#4c566a', green: '#a3be8c', cyan: '#88c0d0', yellow: '#ebcb8b', red: '#bf616a' },
  gruvbox: { background: '#1d2021', foreground: '#ebdbb2', cursor: '#fabd2f', selectionBackground: '#665c54', green: '#b8bb26', cyan: '#fabd2f', yellow: '#fe8019', red: '#fb4934' },
  'catppuccin-latte': { background: '#ffffff', foreground: '#4c4f69', cursor: '#1e66f5', selectionBackground: '#dce0e8', green: '#40a02b', cyan: '#1e66f5', yellow: '#df8e1d', red: '#d20f39' },
  'rose-pine-dawn': { background: '#faf4ed', foreground: '#575279', cursor: '#b4637a', selectionBackground: '#f2e9e1', green: '#56949f', cyan: '#b4637a', yellow: '#ea9d34', red: '#d7827e' },
  'everforest-light': { background: '#fdf6e3', foreground: '#5c6a72', cursor: '#3a94c5', selectionBackground: '#f2efdf', green: '#8da101', cyan: '#3a94c5', yellow: '#dfa000', red: '#f85552' },
}
