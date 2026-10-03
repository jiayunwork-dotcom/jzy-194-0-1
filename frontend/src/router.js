import { createRouter, createWebHistory } from 'vue-router'
import PlanView from './views/PlanView.vue'
import SettingsView from './views/SettingsView.vue'
import VersionsView from './views/VersionsView.vue'

export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', redirect: '/plan' },
    { path: '/plan', component: PlanView },
    { path: '/settings', component: SettingsView },
    { path: '/versions', component: VersionsView }
  ]
})
