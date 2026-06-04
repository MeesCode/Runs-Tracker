import { fetchRun, gpxUrl } from './api.js'
import { renderRoute } from './map.js'
import { lineChart } from './charts.js'
import { fmtKm, fmtPace, fmtDuration, fmtDate, fmtTime, esc, stripEmoji } from './utils.js'

let activeCharts = []
let activeMap = null

function destroyActive() {
  activeCharts.forEach((c) => { try { c.destroy() } catch (_) {} })
  activeCharts = []
  if (activeMap) { try { activeMap.remove() } catch (_) {} activeMap = null }
}

export function closePanel() {
  const panel = document.getElementById('panel')
  const overlay = document.getElementById('overlay')
  panel.classList.remove('open')
  overlay.classList.remove('show')
  document.body.classList.remove('detail-open')
  panel.setAttribute('aria-hidden', 'true')
  // Allow the 300ms slide-out before tearing charts/maps down.
  setTimeout(destroyActive, 320)
}

export async function openPanel(id) {
  const panel = document.getElementById('panel')
  const overlay = document.getElementById('overlay')
  destroyActive()
  panel.innerHTML = `<div class="panel-loading">Loading…</div>`
  panel.classList.add('open')
  overlay.classList.add('show')
  document.body.classList.add('detail-open')
  panel.setAttribute('aria-hidden', 'false')
  panel.scrollTop = 0

  let run
  try {
    run = await fetchRun(id)
  } catch (e) {
    panel.innerHTML = `<div class="panel-loading">Failed to load run: ${esc(e.message)}</div>`
    return
  }

  panel.innerHTML = renderPanel(run)

  panel.querySelector('.panel-close').addEventListener('click', closePanel)

  // Map
  const mapEl = panel.querySelector('#detail-map')
  if (mapEl) activeMap = renderRoute(mapEl, run.polyline, { interactive: true })

  // Charts
  buildCharts(panel, run)
}

function statBlock(label, value, sub = '') {
  return `<div class="bigstat">
    <div class="bigstat-value">${value}</div>
    <div class="bigstat-label">${label}</div>
    ${sub ? `<div class="bigstat-sub">${sub}</div>` : ''}
  </div>`
}

function renderPanel(run) {
  const splits = run.splits || []
  const be = run.best_efforts || {}
  const hasHR = run.has_heartrate && (run.hr_series || []).length > 0

  const splitRows = splits.map((s) => `
    <tr>
      <td>${s.split}</td>
      <td>${(s.distance_meters / 1000).toFixed(2)}</td>
      <td>${fmtDuration(s.elapsed_seconds)}</td>
      <td>${fmtPace(s.pace_sec_per_km)}/km</td>
      <td>${s.elevation_gain.toFixed(0)} m</td>
    </tr>`).join('')

  const beItems = [['1k', be['1k']], ['5k', be['5k']], ['10k', be['10k']]]
    .filter(([, v]) => v)
    .map(([k, v]) => `<div class="effort"><span class="effort-k">${k}</span><span class="effort-v">${v}</span></div>`)
    .join('')

  return `
  <button class="panel-close" aria-label="Close">✕</button>
  <div class="panel-inner">
    <div class="panel-header">
      <h2>${esc(stripEmoji(run.name)) || 'Run'}</h2>
      <div class="panel-meta">${fmtDate(run.start_date_local)} · ${fmtTime(run.start_date_local)}${run.device_name ? ' · ' + esc(run.device_name) : ''}</div>
    </div>

    <div class="bigstats">
      ${statBlock('Distance', run.distance_km.toFixed(2), 'km')}
      ${statBlock('Duration', fmtDuration(run.moving_time_seconds))}
      ${statBlock('Pace', fmtPace(run.pace_sec_per_km), '/km')}
      ${statBlock('Elevation', run.total_elevation_gain.toFixed(0), 'm')}
    </div>

    <div id="detail-map" class="detail-map"></div>

    ${beItems ? `<div class="card section">
      <h3>Best efforts <span class="est">est.</span></h3>
      <div class="efforts">${beItems}</div>
    </div>` : ''}

    <div class="card section">
      <h3>Pace <span class="est">estimated</span></h3>
      <div class="chart-box"><canvas id="ch-pace"></canvas></div>
    </div>

    <div class="card section">
      <h3>Elevation profile <span class="est">estimated</span></h3>
      <div class="chart-box"><canvas id="ch-elev"></canvas></div>
    </div>

    <div class="card section">
      <h3>Cadence <span class="est">estimated</span></h3>
      <div class="chart-box"><canvas id="ch-cad"></canvas></div>
    </div>

    ${hasHR ? `<div class="card section">
      <h3>Heart rate</h3>
      <div class="chart-box"><canvas id="ch-hr"></canvas></div>
    </div>` : ''}

    <div class="card section">
      <h3>Splits <span class="est">estimated</span></h3>
      <div class="table-wrap">
        <table class="splits">
          <thead><tr><th>Km</th><th>Dist</th><th>Time</th><th>Pace</th><th>Elev</th></tr></thead>
          <tbody>${splitRows || '<tr><td colspan="5">No splits</td></tr>'}</tbody>
        </table>
      </div>
    </div>

    <a class="btn-download" href="${gpxUrl(run.id)}" download>↓ Download GPX</a>
  </div>`
}

function buildCharts(panel, run) {
  const splits = run.splits || []

  const paceCanvas = panel.querySelector('#ch-pace')
  if (paceCanvas && splits.length) {
    activeCharts.push(lineChart(paceCanvas, {
      labels: splits.map((s) => `${s.split}`),
      data: splits.map((s) => s.pace_sec_per_km),
      xTitle: 'Km', yTitle: 'Pace (min/km)',
      reverseY: true, paceTooltip: true, fill: false,
    }))
  }

  const elev = run.elevation_profile || []
  const elevCanvas = panel.querySelector('#ch-elev')
  if (elevCanvas && elev.length) {
    activeCharts.push(lineChart(elevCanvas, {
      labels: elev.map((p) => p.distance_km.toFixed(1)),
      data: elev.map((p) => p.elevation),
      xTitle: 'Km', yTitle: 'Elevation (m)', fill: true,
    }))
  }

  const cad = run.cadence_series || []
  const cadCanvas = panel.querySelector('#ch-cad')
  if (cadCanvas && cad.length) {
    activeCharts.push(lineChart(cadCanvas, {
      labels: cad.map((p) => p.distance_km.toFixed(1)),
      data: cad.map((p) => p.value),
      xTitle: 'Km', yTitle: 'Cadence (spm)', fill: false,
    }))
  }

  const hr = run.hr_series || []
  const hrCanvas = panel.querySelector('#ch-hr')
  if (hrCanvas && hr.length) {
    activeCharts.push(lineChart(hrCanvas, {
      labels: hr.map((p) => p.distance_km.toFixed(1)),
      data: hr.map((p) => p.value),
      xTitle: 'Km', yTitle: 'HR (bpm)', fill: true,
    }))
  }
}
