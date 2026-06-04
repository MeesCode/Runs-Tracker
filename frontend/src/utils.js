// Formatting helpers shared across views.

export function fmtKm(km) {
  return `${(km ?? 0).toFixed(2)} km`
}

export function fmtDistanceShort(km) {
  return `${(km ?? 0).toFixed(2)}`
}

// seconds -> "h:mm:ss" or "m:ss"
export function fmtDuration(seconds) {
  seconds = Math.round(seconds || 0)
  const h = Math.floor(seconds / 3600)
  const m = Math.floor((seconds % 3600) / 60)
  const s = seconds % 60
  if (h > 0) return `${h}:${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}`
  return `${m}:${String(s).padStart(2, '0')}`
}

// seconds-per-km -> "m:ss /km"
export function fmtPace(secPerKm) {
  if (!secPerKm || secPerKm <= 0) return '—'
  const m = Math.floor(secPerKm / 60)
  const s = Math.round(secPerKm % 60)
  return `${m}:${String(s).padStart(2, '0')}`
}

const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec']
const DAYS = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat']

function parse(ts) {
  if (!ts) return null
  // Treat the local timestamp as wall-clock; strip trailing Z to avoid TZ shift.
  const clean = ts.replace('Z', '')
  const d = new Date(clean)
  return isNaN(d.getTime()) ? null : d
}

export function fmtDate(ts) {
  const d = parse(ts)
  if (!d) return ts || ''
  return `${DAYS[d.getDay()]} ${d.getDate()} ${MONTHS[d.getMonth()]} ${d.getFullYear()}`
}

export function fmtTime(ts) {
  const d = parse(ts)
  if (!d) return ''
  return `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`
}

export function fmtDateShort(ts) {
  const d = parse(ts)
  if (!d) return ts || ''
  return `${MONTHS[d.getMonth()]} ${d.getDate()}`
}

// Strip emoji / pictographic symbols from user-provided text, keeping the
// actual words. Collapses the whitespace they leave behind.
export function stripEmoji(str) {
  return String(str ?? '')
    .replace(/[\u{1F000}-\u{1FAFF}\u{2600}-\u{27BF}\u{2B00}-\u{2BFF}\u{FE00}-\u{FE0F}\u{1F1E6}-\u{1F1FF}\u{2190}-\u{21FF}\u{2300}-\u{23FF}]/gu, '')
    .replace(/\s{2,}/g, ' ')
    .trim()
}

// Escape user-provided strings for safe innerHTML use.
export function esc(str) {
  return String(str ?? '').replace(/[&<>"']/g, (c) => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
  }[c]))
}
