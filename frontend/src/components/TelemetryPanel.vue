<template>
  <div class="card">
    <h2>遥测录入与上报记录</h2>
    <div class="row2">
      <div>
        <label>遥测编号（重复只算一次）</label>
        <input v-model="form.id" placeholder="如 TM-20261004-0001" />
      </div>
      <div>
        <label>时刻</label>
        <input v-model="form.timestamp" type="datetime-local" step="60" />
      </div>
    </div>
    <div class="row2">
      <div>
        <label>实际 SOC</label>
        <input v-model.number="form.soc" type="number" min="0" max="1" step="any" />
      </div>
      <div></div>
    </div>
    <div class="row2">
      <div>
        <label>实际充电功率 (MW)</label>
        <input v-model.number="form.charge_mw" type="number" min="0" step="any" />
      </div>
      <div>
        <label>实际放电功率 (MW)</label>
        <input v-model.number="form.discharge_mw" type="number" min="0" step="any" />
      </div>
    </div>
    <button @click="submit" style="width:100%">上报遥测</button>
    <div v-if="message" class="msg" :class="message.kind">{{ message.text }}</div>

    <div class="table-wrap" style="margin-top:10px">
      <table>
        <thead>
          <tr><th>编号</th><th>时段</th><th>SOC</th><th>充 MW</th><th>放 MW</th><th>接收时间</th></tr>
        </thead>
        <tbody>
          <tr v-for="t in telemetry" :key="t.id">
            <td>{{ t.id }}</td>
            <td>{{ t.period_index + 1 }}</td>
            <td>{{ t.soc.toFixed(3) }}</td>
            <td>{{ t.charge_mw.toFixed(2) }}</td>
            <td>{{ t.discharge_mw.toFixed(2) }}</td>
            <td>{{ formatTime(t.received_at || t.timestamp) }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

<script setup>
import { reactive, ref } from 'vue'

const props = defineProps({ planDay: String })
const emit = defineEmits('ingested')

const nowLocal = () => {
  const d = new Date()
  d.setSeconds(0, 0)
  const pad = (n) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
}

const form = reactive({
  id: '',
  timestamp: nowLocal(),
  soc: 0.5,
  charge_mw: 0,
  discharge_mw: 0
})
const message = ref(null)

async function submit() {
  message.value = null
  try {
    const res = await fetch('/api/telemetry', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        id: form.id,
        timestamp: new Date(form.timestamp).toISOString(),
        soc: Number(form.soc),
        charge_mw: Number(form.charge_mw) || 0,
        discharge_mw: Number(form.discharge_mw) || 0
      })
    })
    const data = await res.json()
    if (!res.ok) {
      message.value = { kind: 'err', text: `${data.error}${data.fields?.length ? '\n字段：' + data.fields.join(', ') : ''}` }
      return
    }
    if (data.duplicate) {
      message.value = { kind: 'warn', text: '遥测编号重复，只算一次，未触发新处理' }
    } else if (data.warning) {
      message.value = { kind: 'warn', text: data.warning.message }
    } else if (data.reoptimized) {
      message.value = {
        kind: 'ok',
        text: `偏差 ${data.deviation_mwh?.toFixed(3)} MWh，已生成新版本 v${data.new_version.version}：${data.new_version.trigger_reason}`
      }
    } else {
      message.value = { kind: 'ok', text: `遥测已收录，偏差 ${data.deviation_mwh?.toFixed(3)} MWh 未超阈值` }
    }
    emit('ingested')
  } catch (e) {
    message.value = { kind: 'err', text: e.message }
  }
}

function formatTime(s) {
  return new Date(s).toLocaleString('zh-CN', { hour12: false })
}
</script>
