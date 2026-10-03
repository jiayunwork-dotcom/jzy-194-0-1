const base = '/api'

async function request(path, options = {}) {
  const res = await fetch(base + path, {
    headers: { 'Content-Type': 'application/json' },
    ...options
  })
  const text = await res.text()
  const data = text ? JSON.parse(text) : null
  if (!res.ok) {
    const err = new Error(data?.error || `HTTP ${res.status}`)
    err.fields = data?.fields || []
    err.status = res.status
    err.data = data
    throw err
  }
  return data
}

export const api = {
  getConfig: () => request('/config'),
  saveConfig: (payload) => request('/config', { method: 'POST', body: JSON.stringify(payload) }),
  currentPlan: (day) => request(`/plans/current?day=${encodeURIComponent(day)}`),
  getPlan: (day, version) => request(`/plans/${version}?day=${encodeURIComponent(day)}`),
  listPlans: (day) => request(`/plans?day=${encodeURIComponent(day)}`),
  reoptimize: (planDay, reason) =>
    request('/reoptimize', { method: 'POST', body: JSON.stringify({ plan_day: planDay, reason }) }),
  ingestTelemetry: (payload) =>
    request('/telemetry', { method: 'POST', body: JSON.stringify(payload) }),
  listTelemetry: (day) => request(`/telemetry?day=${encodeURIComponent(day)}`)
}
