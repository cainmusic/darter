import { createRouter, createWebHistory } from 'vue-router'
import HomeView from './views/HomeView.vue'
import JobsView from './views/JobsView.vue'
import JobView from './views/JobView.vue'
import ReviewView from './views/ReviewView.vue'
import SettingsView from './views/SettingsView.vue'
import ConstraintTemplatesView from './views/ConstraintTemplatesView.vue'
import PromptTemplatesView from './views/PromptTemplatesView.vue'
import MigrationJobsView from './views/MigrationJobsView.vue'
import MigrationJobView from './views/MigrationJobView.vue'

export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', component: HomeView },
    { path: '/jobs', component: JobsView },
    { path: '/jobs/:id', component: JobView },
    { path: '/jobs/:id/review', component: ReviewView },
    { path: '/constraint-templates', component: ConstraintTemplatesView },
    { path: '/prompts', component: PromptTemplatesView },
    { path: '/migrations', component: MigrationJobsView },
    { path: '/migrations/:id', component: MigrationJobView },
    { path: '/settings', component: SettingsView },
  ],
})
