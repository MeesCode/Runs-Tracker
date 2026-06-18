// Thin API client for the Go backend.

async function getJSON(url) {
  const res = await fetch(url)
  if (!res.ok) {
    let msg = `request failed (${res.status})`
    try {
      const body = await res.json()
      if (body.error) msg = body.error
    } catch (_) { /* ignore */ }
    throw new Error(msg)
  }
  return res.json()
}

export function fetchRuns({ page = 1, perPage = 20, sort = 'date', order = 'desc', search = '' } = {}) {
  const params = new URLSearchParams({ page, per_page: perPage, sort, order })
  if (search) params.set('search', search)
  return getJSON(`/api/runs?${params.toString()}`)
}

export function fetchRun(id) {
  return getJSON(`/api/runs/${id}`)
}

export function fetchStats() {
  return getJSON('/api/stats')
}

export function gpxUrl(id) {
  return `/api/runs/${id}/gpx`
}

// Trigger a Google Health pull on the backend. Resolves to {imported, merged, skipped}.
export async function syncHealth() {
  const res = await fetch('/api/sync/health', { method: 'POST' })
  const body = await res.json().catch(() => ({}))
  if (!res.ok) throw new Error(body.error || `sync failed (${res.status})`)
  return body
}
