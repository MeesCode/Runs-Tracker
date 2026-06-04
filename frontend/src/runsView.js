import { fetchRuns } from './api.js'
import { renderRoute } from './map.js'
import { openPanel } from './detail.js'
import { fmtPace, fmtDuration, fmtDateShort, esc, stripEmoji } from './utils.js'

const state = {
  page: 1,
  perPage: 12,
  sort: 'date',
  order: 'desc',
  search: '',
}

let thumbMaps = []
let searchTimer = null

function destroyThumbs() {
  thumbMaps.forEach((m) => { try { m.remove() } catch (_) {} })
  thumbMaps = []
}

export async function renderRunsView(app) {
  destroyThumbs()
  app.innerHTML = `
    <section class="view">
      <div class="controls">
        <input id="search" class="search" type="search" placeholder="Search runs by name…" value="${esc(state.search)}" />
        <div class="control-group">
          <label>Sort
            <select id="sort">
              <option value="date">Date</option>
              <option value="distance">Distance</option>
              <option value="pace">Pace</option>
              <option value="elevation">Elevation</option>
            </select>
          </label>
          <button id="order" class="order-btn" title="Toggle order"></button>
        </div>
      </div>
      <div id="grid" class="grid"></div>
      <div id="pager" class="pager"></div>
    </section>`

  const searchEl = app.querySelector('#search')
  const sortEl = app.querySelector('#sort')
  const orderEl = app.querySelector('#order')
  sortEl.value = state.sort
  updateOrderBtn(orderEl)

  searchEl.addEventListener('input', (e) => {
    clearTimeout(searchTimer)
    state.search = e.target.value.trim()
    state.page = 1
    searchTimer = setTimeout(() => load(app), 250)
  })
  sortEl.addEventListener('change', (e) => {
    state.sort = e.target.value
    state.page = 1
    load(app)
  })
  orderEl.addEventListener('click', () => {
    state.order = state.order === 'desc' ? 'asc' : 'desc'
    updateOrderBtn(orderEl)
    load(app)
  })

  await load(app)
}

function updateOrderBtn(btn) {
  btn.textContent = state.order === 'desc' ? '↓ Desc' : '↑ Asc'
}

async function load(app) {
  const grid = app.querySelector('#grid')
  const pager = app.querySelector('#pager')
  destroyThumbs()
  grid.innerHTML = `<div class="loading">Loading runs…</div>`

  let data
  try {
    data = await fetchRuns(state)
  } catch (e) {
    grid.innerHTML = `<div class="loading error">Failed to load: ${esc(e.message)}</div>`
    return
  }

  if (!data.runs.length) {
    grid.innerHTML = `<div class="loading">No runs found.</div>`
    pager.innerHTML = ''
    return
  }

  grid.innerHTML = data.runs.map(card).join('')

  grid.querySelectorAll('.run-card').forEach((el) => {
    el.addEventListener('click', () => openPanel(el.dataset.id))
  })

  // Lazy-render route thumbnails.
  data.runs.forEach((run) => {
    const el = grid.querySelector(`#thumb-${run.id}`)
    if (el) {
      const m = renderRoute(el, run.polyline, { interactive: false, markers: false })
      if (m) thumbMaps.push(m)
    }
  })

  renderPager(pager, data, app)
}

function card(run) {
  return `
  <article class="run-card" data-id="${run.id}" tabindex="0">
    <div id="thumb-${run.id}" class="thumb"></div>
    <div class="card-body">
      <div class="card-date">${fmtDateShort(run.start_date_local)}</div>
      <h3 class="card-name">${esc(stripEmoji(run.name)) || 'Run'}</h3>
      <div class="card-stats">
        <div class="cs"><span class="cs-v">${run.distance_km.toFixed(2)}</span><span class="cs-l">km</span></div>
        <div class="cs"><span class="cs-v">${fmtPace(run.pace_sec_per_km)}</span><span class="cs-l">/km</span></div>
        <div class="cs"><span class="cs-v">${fmtDuration(run.moving_time_seconds)}</span><span class="cs-l">time</span></div>
        <div class="cs"><span class="cs-v">${run.total_elevation_gain.toFixed(0)}</span><span class="cs-l">m elev</span></div>
      </div>
    </div>
  </article>`
}

function renderPager(pager, data, app) {
  const { page, total_pages } = data
  if (total_pages <= 1) { pager.innerHTML = ''; return }
  const prevDisabled = page <= 1 ? 'disabled' : ''
  const nextDisabled = page >= total_pages ? 'disabled' : ''
  pager.innerHTML = `
    <button class="pg" id="prev" ${prevDisabled}>← Prev</button>
    <span class="pg-info">Page ${page} of ${total_pages} · ${data.total} runs</span>
    <button class="pg" id="next" ${nextDisabled}>Next →</button>`
  const prev = pager.querySelector('#prev')
  const next = pager.querySelector('#next')
  if (prev) prev.addEventListener('click', () => { if (state.page > 1) { state.page--; load(app) } })
  if (next) next.addEventListener('click', () => { if (state.page < total_pages) { state.page++; load(app) } })
}
