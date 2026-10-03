<template>
  <div ref="el" style="width: 100%; height: 460px"></div>
</template>

<script setup>
import { onMounted, onBeforeUnmount, ref, watch } from 'vue'
import * as echarts from 'echarts'
import { slotLabel } from '../api'

const props = defineProps({
  plan: { type: Object, default: null },       // { initial_soc_mwh, slots: [{slot, power_mw, soc_mwh}] }
  telemetry: { type: Array, default: () => [] } // [{ts, slot, soc_mwh, power_mw}]
})

const el = ref(null)
let chart = null

// 遥测 x 坐标：时段序号 + 时段内位置（时区偏移是 15 分钟的整数倍，故可直接取模）
function teleX(t) {
  const frac = ((new Date(t.ts).getTime() / 60000) % 15) / 15
  return t.slot + frac
}

function buildOption() {
  const labels = Array.from({ length: 96 }, (_, i) => slotLabel(i))
  const slots = props.plan ? props.plan.slots : []
  const powers = slots.map(s => s.power_mw)
  const socLine = props.plan
    ? [[0, props.plan.initial_soc_mwh], ...slots.map(s => [s.slot + 1, s.soc_mwh])]
    : []
  const teleSoc = props.telemetry.map(t => [teleX(t), t.soc_mwh])
  const telePower = props.telemetry.map(t => [teleX(t), t.power_mw])

  return {
    animation: false,
    legend: { data: ['计划功率', '计划SoC', '实际SoC(遥测)', '实际功率(遥测)'] },
    grid: { left: 60, right: 60, top: 40, bottom: 40 },
    tooltip: {
      trigger: 'item',
      formatter: (p) => {
        if (p.seriesIndex === 0) {
          const s = slots[p.dataIndex]
          const kind = s.power_mw > 0 ? '放电' : s.power_mw < 0 ? '充电' : '不动'
          return `时段 ${slotLabel(s.slot)}–${slotLabel(s.slot + 1)}<br/>${kind} ${Math.abs(s.power_mw).toFixed(3)} MW<br/>时段末 SoC ${s.soc_mwh.toFixed(3)} MWh`
        }
        if (p.seriesIndex === 1) return `计划 SoC：${p.value[1].toFixed(3)} MWh`
        if (p.seriesIndex === 2) return `实际 SoC：${p.value[1].toFixed(3)} MWh`
        return `实际功率：${p.value[1].toFixed(3)} MW`
      }
    },
    xAxis: [
      { type: 'category', data: labels, axisLabel: { interval: 7 } },
      { type: 'value', min: 0, max: 96, show: false }
    ],
    yAxis: [
      { type: 'value', name: '功率 MW', splitLine: { show: true } },
      { type: 'value', name: 'SoC MWh', splitLine: { show: false } }
    ],
    series: [
      {
        name: '计划功率',
        type: 'bar',
        data: powers,
        barWidth: '70%',
        itemStyle: {
          color: (p) => (p.value > 0 ? '#e8833a' : p.value < 0 ? '#2f6fed' : '#c4ccd6')
        }
      },
      {
        name: '计划SoC',
        type: 'line',
        xAxisIndex: 1,
        yAxisIndex: 1,
        data: socLine,
        showSymbol: false,
        lineStyle: { width: 2, color: '#2e9e5b' },
        itemStyle: { color: '#2e9e5b' }
      },
      {
        name: '实际SoC(遥测)',
        type: 'scatter',
        xAxisIndex: 1,
        yAxisIndex: 1,
        data: teleSoc,
        symbolSize: 9,
        itemStyle: { color: '#d64545' }
      },
      {
        name: '实际功率(遥测)',
        type: 'scatter',
        xAxisIndex: 1,
        yAxisIndex: 0,
        data: telePower,
        symbol: 'triangle',
        symbolSize: 8,
        itemStyle: { color: '#8e44ad' }
      }
    ]
  }
}

function render() {
  if (!chart) return
  chart.setOption(buildOption(), true)
}

onMounted(() => {
  chart = echarts.init(el.value)
  render()
  window.addEventListener('resize', resize)
})
onBeforeUnmount(() => {
  window.removeEventListener('resize', resize)
  chart && chart.dispose()
})
function resize() { chart && chart.resize() }

watch(() => [props.plan, props.telemetry], render, { deep: true })
</script>
