import { fetchStats } from './api.js'
import { lineChart, barChart } from './charts.js'
import { fmtDateShort, esc } from './utils.js'

let charts = []
let heatScope = 'year' // 'month' | 'year' | 'all'

function destroyCharts() {
  charts.forEach((c) => { try { c.destroy() } catch (_) {} })
  charts = []
}

export async function renderStatsView(app) {
  destroyCharts()
  app.innerHTML = `<section class="view"><div class="loading">Loading stats…</div></section>`

  let st
  try {
    st = await fetchStats()
  } catch (e) {
    app.innerHTML = `<section class="view"><div class="loading error">Failed: ${esc(e.message)}</div></section>`
    return
  }

  app.innerHTML = `
    <section class="view stats">
      <div class="hero">
        ${hero('Total runs', st.total_runs)}
        ${hero('Total distance', `${st.total_distance_km.toFixed(1)}`, 'km')}
        ${hero('Total time', `${st.total_duration_hours.toFixed(1)}`, 'hrs')}
      </div>

      <div class="statgrid">
        ${mini('Avg pace', st.avg_pace_min_per_km, '/km')}
        ${mini('Avg distance', st.avg_distance_km.toFixed(2), 'km')}
        ${mini('Avg cadence', st.avg_cadence.toFixed(0), 'spm')}
        ${mini('Total elevation', st.total_elevation_gain.toFixed(0), 'm')}
        ${mini('Longest run', st.longest_run_km.toFixed(2), 'km')}
        ${mini('Fastest pace', st.fastest_pace_min_per_km, '/km')}
        ${mini('This month', st.runs_this_month, 'runs')}
        ${mini('This year', st.runs_this_year, 'runs')}
      </div>

      <div class="card section">
        <h3>Best efforts${st.has_estimated_splits ? ' <span class="est">est.</span>' : ''}</h3>
        <div class="efforts">
          ${effort('1k', st.best_efforts['1k'])}
          ${effort('5k', st.best_efforts['5k'])}
          ${effort('10k', st.best_efforts['10k'])}
        </div>
      </div>

      <div class="card section">
        <h3>Pace progression</h3>
        <div class="chart-box"><canvas id="s-pace"></canvas></div>
      </div>

      <div class="card section">
        <h3>Distance per run</h3>
        <div class="chart-box"><canvas id="s-dist"></canvas></div>
      </div>

      <div class="card section">
        <h3>Elevation per run</h3>
        <div class="chart-box"><canvas id="s-elev"></canvas></div>
      </div>

      <div class="card section">
        <div class="heat-head">
          <h3>Run frequency</h3>
          <div class="heat-toggle">
            <button data-scope="month" class="${heatScope === 'month' ? 'active' : ''}">Month</button>
            <button data-scope="year" class="${heatScope === 'year' ? 'active' : ''}">Year</button>
            <button data-scope="all" class="${heatScope === 'all' ? 'active' : ''}">All</button>
          </div>
        </div>
        <div id="heatmap" class="heatmap-wrap"></div>
      </div>
    </section>`

  buildCharts(app, st)
  buildHeatmap(app, st.heatmap || [])

  app.querySelectorAll('.heat-toggle button').forEach((b) => {
    b.addEventListener('click', () => {
      heatScope = b.dataset.scope
      app.querySelectorAll('.heat-toggle button').forEach((x) => x.classList.toggle('active', x === b))
      buildHeatmap(app, st.heatmap || [])
    })
  })
}

function hero(label, value, sub = '') {
  return `<div class="hero-card">
    <div class="hero-value">${value}${sub ? `<span class="hero-sub"> ${sub}</span>` : ''}</div>
    <div class="hero-label">${label}</div>
  </div>`
}

function mini(label, value, sub = '') {
  return `<div class="mini-card">
    <div class="mini-value">${value}<span class="mini-sub">${sub ? ' ' + sub : ''}</span></div>
    <div class="mini-label">${label}</div>
  </div>`
}

function effort(k, v) {
  return `<div class="effort"><span class="effort-k">${k}</span><span class="effort-v">${v || '—'}</span></div>`
}

function buildCharts(app, st) {
  const prog = st.progression || []
  const labels = prog.map((p) => fmtDateShort(p.date))

  const paceC = app.querySelector('#s-pace')
  if (paceC) charts.push(lineChart(paceC, {
    labels, data: prog.map((p) => p.pace_sec_per_km),
    xTitle: '', yTitle: 'Pace (min/km)', reverseY: true, paceTooltip: true, fill: false,
  }))

  const distC = app.querySelector('#s-dist')
  if (distC) charts.push(barChart(distC, {
    labels, data: prog.map((p) => p.distance_km), xTitle: '', yTitle: 'Distance (km)',
  }))

  const elevC = app.querySelector('#s-elev')
  if (elevC) charts.push(barChart(elevC, {
    labels, data: prog.map((p) => p.elevation), xTitle: '', yTitle: 'Elevation (m)',
  }))
}

// GitHub-style contribution grid built from per-day counts.
function buildHeatmap(app, days) {
  const wrap = app.querySelector('#heatmap')
  if (!wrap) return
  const counts = {}
  let maxKm = 0
  days.forEach((d) => { counts[d.date] = d; if (d.km > maxKm) maxKm = d.km })

  const today = new Date()
  let start
  if (heatScope === 'month') {
    start = new Date(today.getFullYear(), today.getMonth(), 1)
  } else if (heatScope === 'year') {
    start = new Date(today.getFullYear(), 0, 1)
  } else {
    // earliest run date, fallback to one year back
    const dates = days.map((d) => d.date).sort()
    start = dates.length ? new Date(dates[0]) : new Date(today.getFullYear() - 1, today.getMonth(), 1)
  }

  // Align start to the Sunday on/just before it for clean week columns.
  const gridStart = new Date(start)
  gridStart.setDate(gridStart.getDate() - gridStart.getDay())

  const weeks = []
  let cur = new Date(gridStart)
  while (cur <= today) {
    const week = []
    for (let i = 0; i < 7; i++) {
      const iso = cur.toISOString().slice(0, 10)
      const entry = counts[iso]
      week.push({ iso, inRange: cur >= start && cur <= today, km: entry ? entry.km : 0, count: entry ? entry.count : 0 })
      cur.setDate(cur.getDate() + 1)
    }
    weeks.push(week)
  }

  const cells = weeks.map((week) => `
    <div class="heat-col">
      ${week.map((d) => {
        const level = d.count === 0 ? 0 : levelFor(d.km, maxKm)
        const title = `${d.iso}: ${d.count} run${d.count === 1 ? '' : 's'}${d.km ? `, ${d.km.toFixed(1)} km` : ''}`
        const dim = d.inRange ? '' : ' out'
        return `<div class="heat-cell l${level}${dim}" title="${title}"></div>`
      }).join('')}
    </div>`).join('')

  wrap.innerHTML = `
    <div class="heat-grid">${cells}</div>
    <div class="heat-legend">Less
      <span class="heat-cell l0"></span><span class="heat-cell l1"></span>
      <span class="heat-cell l2"></span><span class="heat-cell l3"></span>
      <span class="heat-cell l4"></span> More
    </div>`
}

function levelFor(km, maxKm) {
  if (maxKm <= 0) return 1
  const r = km / maxKm
  if (r > 0.75) return 4
  if (r > 0.5) return 3
  if (r > 0.25) return 2
  return 1
}
