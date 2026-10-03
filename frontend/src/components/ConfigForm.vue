<template>
  <div class="card">
    <h2>电站参数与分时电价</h2>

    <div class="row2">
      <div>
        <label>计划日</label>
        <input v-model="form.plan_day" type="date" />
      </div>
      <div>
        <label>网格划分数（离散精度）</label>
        <input v-model.number="form.station.grid_points" type="number" min="2" />
      </div>
    </div>

    <div class="row2">
      <div>
        <label>额定能量 (MWh)</label>
        <input v-model.number="form.station.rated_energy_mwh" type="number" step="any"
               :class="{'field-error': bad('rated_energy_mwh')}" />
      </div>
      <div>
        <label>偏差阈值 (MWh)</label>
        <input v-model.number="form.station.deviation_threshold_mwh" type="number" step="any"
               :class="{'field-error': bad('deviation_threshold_mwh')}" />
      </div>
    </div>

    <div class="row2">
      <div>
        <label>最大充电功率 (MW)</label>
        <input v-model.number="form.station.max_charge_mw" type="number" step="any"
               :class="{'field-error': bad('max_charge_mw')}" />
      </div>
      <div>
        <label>最大放电功率 (MW)</label>
        <input v-model.number="form.station.max_discharge_mw" type="number" step="any"
               :class="{'field-error': bad('max_discharge_mw')}" />
      </div>
    </div>

    <div class="row2">
      <div>
        <label>SOC 下限</label>
        <input v-model.number="form.station.soc_min" type="number" step="any"
               :class="{'field-error': bad('soc_min')}" />
      </div>
      <div>
        <label>SOC 上限</label>
        <input v-model.number="form.station.soc_max" type="number" step="any"
               :class="{'field-error': bad('soc_max')}" />
      </div>
    </div>

    <div class="row2">
      <div>
        <label>充电效率 (0,1]</label>
        <input v-model.number="form.station.charge_efficiency" type="number" step="any"
               :class="{'field-error': bad('charge_efficiency')}" />
      </div>
      <div>
        <label>放电效率 (0,1]</label>
        <input v-model.number="form.station.discharge_efficiency" type="number" step="any"
               :class="{'field-error': bad('discharge_efficiency')}" />
      </div>
    </div>

    <div class="row2">
      <div>
        <label>折损成本 (元/MWh 吞吐)</label>
        <input v-model.number="form.station.degradation_cost_per_mwh" type="number" step="any"
               :class="{'field-error': bad('degradation_cost_per_mwh')}" />
      </div>
      <div>
        <label>日末 SOC 下限</label>
        <input v-model.number="form.station.soc_end_min" type="number" step="any"
               :class="{'field-error': bad('soc_end_min')}" />
      </div>
    </div>

    <label>初始 SOC</label>
    <input v-model.number="form.station.soc_initial" type="number" step="any"
           :class="{'field-error': bad('soc_initial')}" />

    <label>96 个时段电价（元/MWh），逗号或换行分隔；也可只填峰平谷三段</label>
    <textarea v-model="priceText" rows="5"></textarea>
    <div class="row2">
      <button @click="fillPeakFlatValley">按峰平谷生成</button>
      <button class="secondary" @click="parsePrices" :disabled="!prices.length">已解析 {{ prices.length }} 点</button>
    </div>
    <div v-if="priceError" class="msg err">{{ priceError }}</div>

    <button @click="submit" :disabled="busy" style="width:100%">生成日前计划</button>
    <div v-if="message" class="msg" :class="message.kind">{{ message.text }}</div>
  </div>
</template>

<script setup>
import { reactive, ref } from 'vue'

const emit = defineEmits(['saved'])

const today = new Date(Date.now() - new Date().getTimezoneOffset() * 60000)
  .toISOString().slice(0, 10)

const form = reactive({
  plan_day: today,
  station: {
    rated_energy_mwh: 4,
    max_charge_mw: 2,
    max_discharge_mw: 2,
    soc_min: 0.05,
    soc_max: 0.95,
    charge_efficiency: 0.92,
    discharge_efficiency: 0.92,
    degradation_cost_per_mwh: 5,
    soc_end_min: 0.1,
    soc_initial: 0.1,
    deviation_threshold_mwh: 0.1,
    grid_points: 800
  }
})

const priceText = ref('')
const prices = ref([])
const priceError = ref('')
const busy = ref(false)
const message = ref(null)
const badFields = ref([])

function bad(name) {
  return badFields.value.some((f) => f === name || f.startsWith(name))
}

function parsePriceText() {
  return priceText.value.split(/[\s,，;；]+/).filter(Boolean).map(Number)
}

function parsePrices() {
  priceError.value = ''
  const vals = parsePriceText()
  if (vals.some((v) => !Number.isFinite(v))) {
    priceError.value = '电价包含非数字'
    prices.value = []
    return
  }
  if (vals.length !== 96) {
    priceError.value = `需要恰好 96 个价格，当前 ${vals.length} 个`
    prices.value = []
    return
  }
  prices.value = vals
}

function fillPeakFlatValley() {
  // 常见工业园区分时：00:00-08:00 谷、08:00-11:00 平、11:00-17:00 峰、
  // 17:00-21:00 尖峰、21:00-24:00 平。
  const seg = [
    [32, 350],   // 0-8h 谷
    [12, 700],   // 8-11 平
    [24, 1150],  // 11-17 峰
    [16, 1350],  // 17-21 尖峰
    [12, 700]    // 21-24 平
  ]
  const out = []
  for (const [n, p] of seg) for (let i = 0; i < n; i++) out.push(p)
  priceText.value = out.join(', ')
  parsePrices()
}

async function submit() {
  parsePrices()
  if (priceError.value) return
  busy.value = true
  message.value = null
  badFields.value = []
  try {
    const resp = await fetch('/api/config', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ station: form.station, prices: prices.value, plan_day: form.plan_day })
    }).then((r) => r.json().then((d) => ({ ok: r.ok, d })))
    if (!resp.ok) {
      badFields.value = resp.d.fields || []
      message.value = { kind: 'err', text: resp.d.error + (badFields.value.length ? `\n字段：${badFields.value.join(', ')}` : '') }
      return
    }
    const profit = resp.d.plan.profit_tail_yuan?.toFixed(2)
    message.value = { kind: 'ok', text: `日前计划已生成（v${resp.d.plan.version}），优化段净收益约 ${profit} 元` }
    emit('saved', form.plan_day)
  } catch (e) {
    message.value = { kind: 'err', text: e.message }
  } finally {
    busy.value = false
  }
}

function hydrate(cfg) {
  Object.assign(form.station, cfg.station)
  form.plan_day = cfg.plan_day
  priceText.value = (cfg.prices || []).join(', ')
  prices.value = cfg.prices || []
}

defineExpose({ hydrate })
</script>
