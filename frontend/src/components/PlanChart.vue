<template>
  <div class="card">
    <h2>计划功率与荷电状态（叠加实际遥测）</h2>
    <div v-if="!plan" class="muted">请先生成日前计划</div>
    <template v-else>
      <div class="muted" style="margin-bottom:8px">
        当前版本 v{{ plan.version }}
        <span v-if="plan.trigger_reason">—— {{ plan.trigger_reason }}</span>
        ｜优化从第 {{ plan.start_period + 1 }} 时段开始｜
        优化段净收益 {{ plan.profit_tail_yuan?.toFixed(2) }} 元｜
        离散误差上界 ≤ {{ plan.error_bound_yuan?.toFixed(2) }} 元
      </div>
      <div class="chart-wrap"><canvas ref="barCanvas"></canvas></div>
      <div class="chart-wrap" style="margin-top:18px"><canvas ref="socCanvas"></canvas></div>
    </template>
  </div>
</template>

<script setup>
import { ref, watch, onMounted, nextTick } from 'vue'
import Chart from 'chart.js/auto'

const props = defineProps({
  plan: Object,
  telemetry: Array
})

const barCanvas = ref(null)
const socCanvas = ref(null)
let barChart = null
let socChart = null

const labels = Array.from({ length: 96 }, (_, i) => {
  const h = String(Math.floor(i / 4)).padStart(2, '0')
  const m = String((i % 4) * 15).padStart(2, '0')
  return `${h}:${m}`
})

function render() {
  nextTick(() => {
    renderBar()
    renderSoc()
  })
}

function renderBar() {
  if (!barCanvas.value) return
  const data = props.plan
  if (barChart) barChart.destroy()
  barChart = new Chart(barCanvas.value, {
    type: 'bar',
    data: {
      labels,
      datasets: [
        {
          label: '计划充电 (MW)',
          data: data.charge_mw.map((v) => -v), // 向下绘制
          backgroundColor: 'rgba(43,127,212,0.75)',
          stack: 'p',
          order: 2
        },
        {
          label: '计划放电 (MW)',
          data: data.discharge_mw,
          backgroundColor: 'rgba(215,38,61,0.75)',
          stack: 'p',
          order: 2
        },
        {
          label: '实际充电 (MW)',
          data: actualSeries('charge_mw').map((v) => -v),
          type: 'line',
          borderColor: 'rgba(15,82,18,0.9)',
          borderWidth: 1.2,
          pointRadius: 0,
          spanGaps: true,
          order: 1
        },
        {
          label: '实际放电 (MW)',
          data: actualSeries('discharge_mw'),
          type: 'line',
          borderColor: 'rgba(120,20,30,0.9)',
          borderWidth: 1.2,
          pointRadius: 0,
          spanGaps: true,
          order: 1
        }
      ]
    },
    options: {
      responsive: true,
      maintainAspectRatio: false,
      animation: false,
      plugins: { legend: { labels: { boxWidth: 10, font: { size: 10 } } } },
      scales: {
        x: { stacked: true, ticks: { maxTicksLimit: 13, font: { size: 9 } } },
        y: { stacked: true, title: { display: true, text: 'MW（充↓ / 放↑）' } }
      }
    }
  })
}

function renderSoc() {
  if (!socCanvas.value) return
  const actualSoc = new Array(96).fill(null)
  for (const t of props.telemetry || []) {
    actualSoc[t.period_index] = t.soc
  }
  if (socChart) socChart.destroy()
  socChart = new Chart(socCanvas.value, {
    type: 'line',
    data: {
      labels,
      datasets: [
        {
          label: '计划 SOC',
          data: props.plan.soc,
          borderColor: 'rgba(43,127,212,1)',
          backgroundColor: 'rgba(43,127,212,0.08)',
          borderWidth: 2,
          pointRadius: 0,
          fill: true
        },
        {
          label: '实际 SOC',
          data: actualSoc,
          borderColor: 'rgba(215,38,61,1)',
          borderWidth: 1.6,
          pointRadius: 2.5,
          spanGaps: true
        }
      ]
    },
    options: {
      responsive: true,
      maintainAspectRatio: false,
      animation: false,
      plugins: { legend: { labels: { boxWidth: 10, font: { size: 10 } } } },
      scales: {
        x: { ticks: { maxTicksLimit: 13, font: { size: 9 } } },
        y: { min: 0, max: 1, title: { display: true, text: 'SOC' } }
      }
    }
  })
}

function actualSeries(field) {
  const arr = new Array(96).fill(null)
  for (const t of props.telemetry || []) {
    if (t[field] != null) arr[t.period_index] = t[field]
  }
  return arr
}

onMounted(render)
watch(() => [props.plan, props.telemetry], render, { deep: true })
</script>
