<template>
  <div>
    <div class="card">
      <div class="row">
        <label class="field">计划日
          <input type="date" v-model="date" @change="load" />
        </label>
        <button class="ghost" @click="load">刷新</button>
      </div>
    </div>

    <div class="card">
      <h2>计划版本（{{ date }}）</h2>
      <div v-if="!versions.length" style="color: var(--muted)">该日期暂无计划版本。</div>
      <table v-else>
        <thead>
          <tr>
            <th>版本</th><th>触发原因</th><th>期望净收益（元）</th>
            <th>日起始 SoC</th><th>依据遥测时刻</th><th>生成时间</th><th></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="v in versions" :key="v.version">
            <td>v{{ v.version }}</td>
            <td>{{ v.trigger_reason }}</td>
            <td>{{ v.expected_revenue.toFixed(2) }}</td>
            <td>{{ v.initial_soc_mwh.toFixed(2) }} MWh</td>
            <td>{{ v.based_on_ts ? fmtTime(v.based_on_ts) : '—' }}</td>
            <td>{{ fmtTime(v.created_at) }}</td>
            <td><router-link :to="`/plan?date=${date}&version=${v.version}`">查看</router-link></td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

<script setup>
import { onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import { api, todayStr } from '../api'

const route = useRoute()
const date = ref(route.query.date || todayStr())
const versions = ref([])

function fmtTime(s) {
  return new Date(s).toLocaleString('zh-CN', { hour12: false })
}

async function load() {
  try {
    versions.value = await api.listVersions(date.value)
  } catch {
    versions.value = []
  }
}

onMounted(load)
</script>
