<template>
  <div>
    <div class="card">
      <h2>电站参数</h2>
      <div class="row">
        <label class="field" v-for="f in stationFields" :key="f.key">
          {{ f.label }}（{{ f.unit }}）
          <input type="number" step="any" v-model.number="station[f.key]"
                 :class="{ bad: fieldErrs[f.key] }" />
          <span class="field-err" v-if="fieldErrs[f.key]">{{ fieldErrs[f.key] }}</span>
        </label>
      </div>
      <div class="row" style="margin-top: 12px">
        <button @click="saveStation" :disabled="saving">保存参数</button>
      </div>
      <div v-if="stationMsg" class="msg" :class="stationOk ? 'ok' : 'err'">{{ stationMsg }}</div>
    </div>

    <div class="card">
      <h2>分时电价（96 时段，元/MWh）</h2>
      <div class="row" style="margin-bottom: 12px">
        <label class="field">计划日
          <input type="date" v-model="priceDate" @change="loadPrices" />
        </label>
        <button class="ghost" @click="fillTypical">填入典型峰谷曲线</button>
        <button class="ghost" @click="clearPrices">清空</button>
        <button @click="savePrices" :disabled="saving">保存电价</button>
      </div>
      <div class="row" style="margin-bottom: 12px">
        <label class="field" style="flex:1">批量导入（96 个数值，逗号/空白分隔）
          <input v-model="csvText" placeholder="380, 360, 350, ..." style="width:100%" />
        </label>
        <button class="ghost" @click="importCsv">导入</button>
      </div>
      <div class="price-grid">
        <label v-for="i in 96" :key="i - 1">
          {{ slotLabel(i - 1) }}
          <input type="number" step="any" v-model.number="prices[i - 1]" />
        </label>
      </div>
      <div v-if="priceMsg" class="msg" :class="priceOk ? 'ok' : 'err'">{{ priceMsg }}</div>
    </div>

    <div class="card">
      <h2>生成日前计划</h2>
      <div class="row">
        <label class="field">计划日
          <input type="date" v-model="planDate" />
        </label>
        <label class="field">日起始荷电状态（0~1）
          <input type="number" step="any" min="0" max="1" v-model.number="initialSoc"
                 :class="{ bad: fieldErrs['initial_soc'] }" />
          <span class="field-err" v-if="fieldErrs['initial_soc']">{{ fieldErrs['initial_soc'] }}</span>
        </label>
        <button @click="runOptimize" :disabled="saving">生成计划</button>
      </div>
      <div v-if="optMsg" class="msg" :class="optOk ? 'ok' : 'err'">
        {{ optMsg }}
        <router-link v-if="optOk" :to="`/plan?date=${planDate}`">查看计划 →</router-link>
      </div>
    </div>
  </div>
</template>

<script setup>
import { onMounted, ref } from 'vue'
import { api, slotLabel, dateStrOffset } from '../api'

const stationFields = [
  { key: 'energy_mwh', label: '额定能量', unit: 'MWh' },
  { key: 'max_charge_mw', label: '最大充电功率', unit: 'MW' },
  { key: 'max_discharge_mw', label: '最大放电功率', unit: 'MW' },
  { key: 'soc_min', label: '荷电状态下限', unit: '0~1' },
  { key: 'soc_max', label: '荷电状态上限', unit: '0~1' },
  { key: 'charge_eff', label: '充电效率', unit: '0~1' },
  { key: 'discharge_eff', label: '放电效率', unit: '0~1' },
  { key: 'degradation_cost_per_mwh', label: '折损成本', unit: '元/MWh' },
  { key: 'end_soc_min', label: '日末荷电状态下限', unit: '0~1' },
  { key: 'deviation_threshold_mwh', label: '重排偏差阈值', unit: 'MWh' }
]

const station = ref({
  energy_mwh: 10, max_charge_mw: 5, max_discharge_mw: 5,
  soc_min: 0.1, soc_max: 0.9, charge_eff: 0.9, discharge_eff: 0.9,
  degradation_cost_per_mwh: 2, end_soc_min: 0.2, deviation_threshold_mwh: 0.5
})
const fieldErrs = ref({})
const saving = ref(false)
const stationMsg = ref('')
const stationOk = ref(true)

const priceDate = ref(dateStrOffset(1))
const planDate = ref(dateStrOffset(1))
const prices = ref(new Array(96).fill(0))
const csvText = ref('')
const priceMsg = ref('')
const priceOk = ref(true)

const initialSoc = ref(0.2)
const optMsg = ref('')
const optOk = ref(true)

function applyFieldErrors(err) {
  fieldErrs.value = {}
  for (const fe of err.fieldErrors || []) fieldErrs.value[fe.field] = fe.message
}

async function loadStation() {
  try {
    station.value = await api.getStation()
  } catch { /* 未录入则用默认值 */ }
}

async function saveStation() {
  saving.value = true
  stationMsg.value = ''
  try {
    await api.putStation(station.value)
    stationOk.value = true
    stationMsg.value = '已保存'
    fieldErrs.value = {}
  } catch (e) {
    stationOk.value = false
    stationMsg.value = e.message
    applyFieldErrors(e)
  } finally {
    saving.value = false
  }
}

async function loadPrices() {
  priceMsg.value = ''
  try {
    const d = await api.getPrices(priceDate.value)
    prices.value = d.prices
  } catch {
    prices.value = new Array(96).fill(0)
  }
}

async function savePrices() {
  saving.value = true
  priceMsg.value = ''
  try {
    await api.putPrices(priceDate.value, prices.value.map(Number))
    priceOk.value = true
    priceMsg.value = `已保存 ${priceDate.value} 的 96 时段电价`
  } catch (e) {
    priceOk.value = false
    priceMsg.value = e.message
  } finally {
    saving.value = false
  }
}

function fillTypical() {
  // 典型峰谷：00-07 谷，08-11 峰，12-13 平，14-17 峰，18-21 尖峰，22-23 平
  const v = new Array(96)
  for (let i = 0; i < 96; i++) {
    const h = i / 4
    if (h < 7) v[i] = 320
    else if (h < 12) v[i] = 780
    else if (h < 14) v[i] = 520
    else if (h < 18) v[i] = 760
    else if (h < 21) v[i] = 1150
    else v[i] = 500
  }
  prices.value = v
}

function clearPrices() {
  prices.value = new Array(96).fill(0)
}

function importCsv() {
  const nums = csvText.value.split(/[\s,，;；]+/).filter(Boolean).map(Number)
  if (nums.length !== 96 || nums.some(Number.isNaN)) {
    priceOk.value = false
    priceMsg.value = `需要恰好 96 个数值，实际解析出 ${nums.length} 个`
    return
  }
  prices.value = nums
  priceOk.value = true
  priceMsg.value = '已导入，确认后请保存'
}

async function runOptimize() {
  saving.value = true
  optMsg.value = ''
  fieldErrs.value = {}
  try {
    const plan = await api.optimize(planDate.value, initialSoc.value)
    optOk.value = true
    optMsg.value = `已生成 v${plan.version}，期望净收益 ${plan.expected_revenue.toFixed(2)} 元。`
  } catch (e) {
    optOk.value = false
    optMsg.value = e.message
    applyFieldErrors(e)
  } finally {
    saving.value = false
  }
}

onMounted(async () => {
  await loadStation()
  await loadPrices()
})
</script>
