<template>
  <header>储能电站日前计划与滚动修正系统</header>

  <div class="layout">
    <div>
      <ConfigForm ref="configForm" @saved="onSaved" />
      <VersionList
        :versions="versions"
        :current="currentVersion"
        :selected="selectedVersion"
        @select="selectVersion"
        @reopt="manualReopt"
      />
    </div>

    <div>
      <PlanChart :plan="plan" :telemetry="telemetry" />
      <TelemetryPanel :plan-day="planDay" @ingested="refreshAll" />
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import ConfigForm from './components/ConfigForm.vue'
import PlanChart from './components/PlanChart.vue'
import VersionList from './components/VersionList.vue'
import TelemetryPanel from './components/TelemetryPanel.vue'
import { api } from './api'

const configForm = ref(null)
const planDay = ref(new Date(Date.now() - new Date().getTimezoneOffset() * 60000)
  .toISOString().slice(0, 10))
const plan = ref(null)
const versions = ref([])
const telemetry = ref([])
const currentVersion = ref(0)
const selectedVersion = ref(0)

async function loadConfig() {
  try {
    const cfg = await api.getConfig()
    planDay.value = cfg.plan_day
    configForm.value?.hydrate(cfg)
  } catch { /* 尚未配置 */ }
}

async function refreshAll() {
  await Promise.all([refreshVersions(), refreshTelemetry()])
}

async function refreshVersions() {
  try {
    const data = await api.listPlans(planDay.value)
    versions.value = data.versions || []
    currentVersion.value = data.current_version
    const want = selectedVersion.value || data.current_version
    await loadPlan(want === 0 ? undefined : want)
  } catch {
    versions.value = []
    plan.value = null
    currentVersion.value = 0
    selectedVersion.value = 0
  }
}

async function refreshTelemetry() {
  try {
    const data = await api.listTelemetry(planDay.value)
    telemetry.value = data.telemetry || []
  } catch {
    telemetry.value = []
  }
}

async function loadPlan(version) {
  try {
    plan.value = version
      ? await api.getPlan(planDay.value, version)
      : await api.currentPlan(planDay.value)
    selectedVersion.value = plan.value?.version || 0
  } catch {
    plan.value = null
  }
}

async function selectVersion(v) {
  await loadPlan(v)
}

async function onSaved(day) {
  planDay.value = day
  selectedVersion.value = 0
  await refreshAll()
}

async function manualReopt() {
  try {
    await api.reoptimize(planDay.value, '运行员手工触发')
    selectedVersion.value = 0
    await refreshAll()
  } catch (e) {
    alert('重优化失败：' + e.message)
  }
}

onMounted(async () => {
  await loadConfig()
  await refreshAll()
})
</script>
