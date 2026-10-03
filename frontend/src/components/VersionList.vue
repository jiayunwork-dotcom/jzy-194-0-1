<template>
  <div class="card">
    <h2>计划版本</h2>
    <button @click="$emit('reopt')" style="width:100%">按当前时刻手工滚动重优化</button>
    <div v-if="!versions.length" class="muted" style="margin-top:8px">暂无计划版本</div>
    <div v-for="v in versions" :key="v.version"
         class="version-item"
         :class="{ active: selected === v.version }"
         @click="$emit('select', v.version)">
      <span class="vno">v{{ v.version }}</span>
      <span v-if="v.version === current" class="tag">当前</span>
      <div class="muted" style="margin-top:3px">
        起于第 {{ v.start_period + 1 }} 时段｜收益 {{ v.profit_tail_yuan?.toFixed(1) }} 元
      </div>
      <div style="margin-top:3px">{{ v.trigger_reason }}</div>
      <div class="muted">{{ formatTime(v.created_at) }}</div>
    </div>
  </div>
</template>

<script setup>
defineProps({ versions: Array, current: Number, selected: Number })
defineEmits(['select', 'reopt'])

function formatTime(s) {
  if (!s) return ''
  return new Date(s).toLocaleString('zh-CN', { hour12: false })
}
</script>
