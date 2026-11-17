import { createApp } from 'vue'
import { createPinia } from 'pinia'
import App from './App.vue'
import { router } from './router'
import { applyTheme, loadThemeId } from './theme'
import './styles/cyber.css'

// 本地字体（@fontsource 打包，无外部 CDN；中文回退到系统 PingFang SC）
import '@fontsource/orbitron/latin-500.css'
import '@fontsource/orbitron/latin-700.css'
import '@fontsource/orbitron/latin-900.css'
import '@fontsource/jetbrains-mono/latin-400.css'
import '@fontsource/jetbrains-mono/latin-500.css'
import '@fontsource/jetbrains-mono/latin-700.css'

// 在挂载前应用主题，避免首屏主题闪烁
applyTheme(loadThemeId())

const app = createApp(App)
app.use(createPinia())
app.use(router)
app.mount('#app')
