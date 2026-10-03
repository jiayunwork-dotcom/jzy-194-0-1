async function req(method, url, body) {
  const res = await fetch(url, {
    method,
    headers: body !== undefined ? { 'Content-Type': 'application/json' } : undefined,
    body: body !== undefined ? JSON.stringify(body) : undefined
  })
  const data = await res.json().catch(() => ({}))
  if (!res.ok) {
    const err = new Error(data.error || (data.errors && data.errors.map(e => `${e.field}: ${e.message}`).join('；')) || `请求失败 ${res.status}`)
    err.status = res.status
    err.fieldErrors = data.errors || []
    throw err
  }
  return data
}

export const api = {
  getStation: () => req('GET', '/api/station'),
  putStation: (p) => req('PUT', '/api/station', p),
  getPrices: (date) => req('GET', `/api/prices/${date}`),
  putPrices: (date, prices) => req('PUT', `/api/prices/${date}`, { prices }),
  optimize: (date, initialSoc) => req('POST', `/api/plans/${date}/optimize`, { initial_soc: initialSoc }),
  getPlan: (date, version) => req('GET', version ? `/api/plans/${date}/versions/${version}` : `/api/plans/${date}`),
  listVersions: (date) => req('GET', `/api/plans/${date}/versions`),
  submitTelemetry: (date, t) => req('POST', `/api/plans/${date}/telemetry`, t),
  listTelemetry: (date) => req('GET', `/api/plans/${date}/telemetry`)
}

// 时段工具：96 个 15 分钟时段
export function slotLabel(i) {
  const h = String(Math.floor(i / 4)).padStart(2, '0')
  const m = String((i % 4) * 15).padStart(2, '0')
  return `${h}:${m}`
}

export function todayStr() {
  // 计划日时区固定 +08:00（与后端 PLAN_TZ 默认一致）
  const now = new Date(Date.now() + 8 * 3600 * 1000)
  return now.toISOString().slice(0, 10)
}

export function dateStrOffset(days) {
  const d = new Date(Date.now() + 8 * 3600 * 1000 + days * 86400 * 1000)
  return d.toISOString().slice(0, 10)
}
