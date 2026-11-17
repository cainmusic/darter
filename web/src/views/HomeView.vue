<script setup lang="ts">
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import { NCard, NGrid, NGridItem } from 'naive-ui'
import { modules, type ModuleDef } from '../modules'
import { GAP } from '../layout'
import PageHeader from '../components/PageHeader.vue'

const router = useRouter()

const ready = computed(() => modules.filter((m) => m.status === 'ready'))
const planned = computed(() => modules.filter((m) => m.status === 'planned'))

function go(m: ModuleDef) {
  if (m.status === 'ready') router.push(m.route)
}
</script>

<template>
  <div style="padding: 8px">
    <PageHeader title="首页" subtitle="选择要用的工具模块" />

    <n-grid :cols="2" :x-gap="GAP" :y-gap="GAP" responsive="screen">
      <n-grid-item v-for="m in ready" :key="m.id">
        <n-card class="cy-card" hoverable style="cursor:pointer;height:100%" @click="go(m)">
          <div style="display:flex;align-items:flex-start;gap:12px">
            <div style="flex-shrink:0;color:var(--cy-cyan)"><component :is="m.icon" /></div>
            <div style="min-width:0">
              <div class="font-display" style="font-size:15px">{{ m.name }}</div>
              <div style="font-size:10px;color:var(--cy-muted);letter-spacing:0.15em;margin-top:2px">{{ m.tag }}</div>
            </div>
          </div>
          <div style="margin-top:12px;color:var(--cy-muted);font-size:12px;line-height:1.7">{{ m.description }}</div>
        </n-card>
      </n-grid-item>
    </n-grid>

    <template v-if="planned.length">
      <div class="cy-title" style="font-size:13px;color:var(--cy-muted);margin:16px 0 8px">开发中</div>
      <n-grid :cols="2" :x-gap="GAP" :y-gap="GAP" responsive="screen">
        <n-grid-item v-for="m in planned" :key="m.id">
          <n-card class="cy-card" style="opacity:0.55;cursor:not-allowed;height:100%">
            <div style="display:flex;align-items:flex-start;gap:12px">
              <div style="flex-shrink:0;color:var(--cy-muted)"><component :is="m.icon" /></div>
              <div style="min-width:0">
                <div class="font-display" style="font-size:15px">{{ m.name }}</div>
                <div style="font-size:10px;color:var(--cy-muted);letter-spacing:0.15em;margin-top:2px">COMING SOON</div>
              </div>
            </div>
            <div style="margin-top:12px;color:var(--cy-muted);font-size:12px;line-height:1.7">{{ m.description }}</div>
          </n-card>
        </n-grid-item>
      </n-grid>
    </template>
  </div>
</template>
