<template>
  <div>
    <div class="card">
      <div class="row">
        <label class="field">计划日
          <input type="date" v-model="date" @change="loadAll" />
        </label>
        <label class="field">计划版本
          <select v-model="selectedVersion" @change="loadPlan">
            <option :value="null">当前版本</option>
            <option v-for="v in versions" :key="v.version" :value="v.version">
              v{{ v.version }} — {{ v.trigger_reason }}
            </option>
          </select>
        </label>
        <button class="ghost" @click="loadAll">刷新</button>
      </div>
      <div v-if="error" class="msg err">{{ error }}</div>
      <div v-if="!plan && !error" class="msg err">该日期暂无计划，请先到「参数与电价」录入数据并生成日前计划。</div>
    </div>

    <div class="card" v-if="plan">
      <div class="stat-grid">
        <div class="stat"><div class="v">v{{ plan.version }}</div><div class="k">版本</div></div>
        <div class="stat"><div class="v">{{ plan.expected_revenue.toFixed(1) }} 元</div><div class="k">期望净收益（优化区间）</div></div>
        <div class="stat"><div class="v">{{ plan.initial_soc_mwh.toFixed(2) }} MWh</div><div class="k">日起始 SoC</div></div>
        <div class="stat"><div class="v">{{ fmtTime(plan.created_at) }}</div><div class="k">生成时间</div></div>
      </div>
      <h3>触发原因</h3>
      <div>{{ plan.trigger_reason }}</div>
    </div>

    <div class="card" v-if="plan">
      <h2>计划功率与 SoC（叠加实际遥测）</h2>
      <PlanChart :plan="plan" :telemetry="telemetry" />
    </div>

    <div class="card" v-if="plan">
      <h2>上报遥测（演示）</h2>
      <div class="row">
        <label class="field">遥测编号
          <input v-model="teleForm.id" />
        </label>
        <label class="field">采集时刻
          <input type="datetime-local" v-model="teleForm.tsLocal" step="60" />
        </label>
        <label class="field">实际 SoC (MWh)
          <input type="number" step="any" v-model.number="teleForm.soc_mwh" />
        </label>
        <label class="field">实际功率 (MW)
          <input type="number" step="any" v-model.number="teleForm.power_mw" />
        </label>
        <button @click="submitTelemetry" :disabled="submitting">提交</button>
      </div>
      <div v-if="teleMsg" class="msg" :class="teleOk ? 'ok' : 'err'">{{ teleMsg }}</div>
    </div>

    <div class="card" v-if="telemetry.length">
      <h2>遥测记录（按采集时刻归位）</h2>
      <table>
        <thead><tr><th>编号</th><th>采集时刻</th><th>时段</th><th>SoC (MWh)</th><th>功率 (MW)</th></tr></thead>
        <tbody>
          <tr v-for="t in telemetry" :key="t.id">
            <td>{{ t.id }}</td>
            <td>{{ fmtTime(t.ts) }}</td>
            <td>{{ t.slot }}</td>
            <td>{{ t.soc_mwh.toFixed(3) }}</td>
            <td>{{ t.power_mw.toFixed(3) }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

<script setup>
import { onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api, todayStr } from '../api'
import PlanChart from '../components/PlanChart.vue'

const route = useRoute()
const router = useRouter()

const date = ref(route.query.date || todayStr())
const versions = ref([])
const selectedVersion = ref(route.query.version ? Number(route.query.version) : null)
const plan = ref(null)
const telemetry = ref([])
const error = ref('')

const teleForm = ref({ id: '', tsLocal: '', soc_mwh: 0, power_mw: 0 })
const teleMsg = ref('')
const teleOk = ref(true)
const submitting = ref(false)

function fmtTime(s) {
  if (!s) return '—'
  return new Date(s).toLocaleString('zh-CN', { hour12: false })
}

async function loadAll() {
  error.value = ''
  plan.value = null
  try {
    versions.value = await api.listVersions(date.value)
  } catch { versions.value = [] }
  await loadPlan()
  await loadTelemetry()
}

async function loadPlan() {
  error.value = ''
  try {
    plan.value = await api.getPlan(date.value, selectedVersion.value)
  } catch (e) {
    plan.value = null
    if (e.status !== 404) error.value = e.message
  }
}

async function loadTelemetry() {
  try {
    telemetry.value = await api.listTelemetry(date.value)
  } catch { telemetry.value = [] }
}

function newTeleId() {
  return `tm-${Date.now()}-${Math.floor(Math.random() * 1000)}`
}

async function submitTelemetry() {
  submitting.value = true
  teleMsg.value = ''
  try {
    // datetime-local 无时区，按 +08:00（计划日时区）解释
    const ts = new Date(teleForm.value.tsLocal + ':00+08:00')
    const res = await api.submitTelemetry(date.value, {
      id: teleForm.value.id,
      ts: ts.toISOString(),
      soc_mwh: teleForm.value.soc_mwh,
      power_mw: teleForm.value.power_mw
    })
    teleOk.value = true
    if (res.duplicate) {
      teleMsg.value = '重复上报，已忽略（幂等去重）'
    } else if (res.replanned) {
      teleMsg.value = `偏差 ${res.deviation_mwh.toFixed(3)} MWh 超阈值，已生成新版本 v${res.version}`
    } else {
      teleMsg.value = `已接收：${res.reason}`
    }
    teleForm.value.id = newTeleId()
    selectedVersion.value = null
    await loadAll()
  } catch (e) {
    teleOk.value = false
    teleMsg.value = e.message
  } finally {
    submitting.value = false
  }
}

watch(date, (d) => {
  teleForm.value.tsLocal = `${d}T12:00`
})

onMounted(async () => {
  teleForm.value.id = newTeleId()
  teleForm.value.tsLocal = `${date.value}T12:00`
  await loadAll()
  if (route.query.version) selectedVersion.value = Number(route.query.version)
})
</script>
