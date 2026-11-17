import { h, type Component, type VNode } from 'vue'

// 模块清单 —— Darter 是「任务工具集合」，每个可独立使用的工具是一个模块。
// 首页由这份清单数据驱动渲染；后续新增工具，往 modules 里加一条即可，
// 无需改动首页 / 侧边栏 / 路由之外的任何地方。
export type ModuleStatus = 'ready' | 'planned'

export interface ModuleDef {
  id: string
  name: string        // 中文名（首页卡片标题）
  tag: string         // 英文小标签（首页卡片副标签）
  description: string // 一句话说明这个工具是干嘛的
  route: string       // 跳转路由（status 为 ready 时点击生效）
  status: ModuleStatus
  icon: Component     // 24×24 stroke 图标（跟随 currentColor）
}

// 统一 24×24 stroke 图标外壳，与侧边栏菜单图标风格一致。
function svgIcon(children: VNode[]): Component {
  return () =>
    h('svg', {
      viewBox: '0 0 24 24', width: 28, height: 28,
      fill: 'none', stroke: 'currentColor', 'stroke-width': 2,
      'stroke-linecap': 'round', 'stroke-linejoin': 'round',
      style: 'display:block',
    }, children)
}

export const modules: ModuleDef[] = [
  {
    id: 'review-fixer',
    name: '审查修复',
    tag: 'AI REVIEW FIXER',
    description:
      '输入一份 review 报告 + 代码库 + 基础分支，自动拆解修复任务、串行修复、对抗性审查、逐任务提交，交人类审批后归档交付。',
    route: '/jobs',
    status: 'ready',
    icon: svgIcon([
      h('polyline', { points: '16 18 22 12 16 6' }),
      h('polyline', { points: '8 6 2 12 8 18' }),
    ]),
  },
  {
    id: 'migration',
    name: '代码迁移',
    tag: 'CODE MIGRATION',
    description:
      '对比两个分支的 commit 列表，逐个或按范围 cherry-pick 到目标分支，冲突可人工或 AI 解决并留档。',
    route: '/migrations',
    status: 'ready',
    icon: svgIcon([
      h('line', { x1: 6, y1: 3, x2: 6, y2: 15 }),
      h('circle', { cx: 18, cy: 6, r: 3 }),
      h('circle', { cx: 6, cy: 18, r: 3 }),
      h('path', { d: 'M18 9a9 9 0 0 1-9 9' }),
    ]),
  },
]
