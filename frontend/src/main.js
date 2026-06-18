import './style.css'
import { renderRunsView } from './runsView.js'
import { renderStatsView } from './statsView.js'
import { closePanel } from './detail.js'
import { syncHealth } from './api.js'

const app = document.getElementById('app')

let toastTimer = null
function toast(msg, kind = '') {
  const el = document.getElementById('toast')
  if (!el) return
  el.textContent = msg
  el.className = `toast show ${kind}`
  clearTimeout(toastTimer)
  toastTimer = setTimeout(() => { el.className = 'toast' }, 4000)
}

async function route() {
  const path = window.location.pathname
  highlightNav(path)
  closePanel()
  if (path === '/stats') {
    await renderStatsView(app)
  } else {
    await renderRunsView(app)
  }
}

function highlightNav(path) {
  document.querySelectorAll('.nav-link').forEach((a) => {
    const active = a.getAttribute('data-route') === path ||
      (path !== '/stats' && a.getAttribute('data-route') === '/')
    a.classList.toggle('active', active)
  })
}

// Intercept internal nav links for client-side routing.
document.addEventListener('click', (e) => {
  const link = e.target.closest('a[data-route]')
  if (link) {
    e.preventDefault()
    const to = link.getAttribute('data-route')
    if (to !== window.location.pathname) {
      history.pushState({}, '', to)
      route()
    }
  }
})

window.addEventListener('popstate', route)

// Manual "Sync" button: pull latest from Google Health, then refresh the view.
const syncBtn = document.getElementById('sync-btn')
if (syncBtn) {
  syncBtn.addEventListener('click', async () => {
    if (syncBtn.disabled) return
    syncBtn.disabled = true
    syncBtn.classList.add('syncing')
    toast('Syncing with Google Health…')
    try {
      const r = await syncHealth()
      const added = r.imported || 0
      toast(added ? `Synced — ${added} new run${added === 1 ? '' : 's'}` : 'Up to date', 'ok')
      await route() // refresh list/stats
    } catch (e) {
      toast(`Sync failed: ${e.message}`, 'err')
    } finally {
      syncBtn.disabled = false
      syncBtn.classList.remove('syncing')
    }
  })
}

// Close panel via overlay click or Escape.
document.getElementById('overlay').addEventListener('click', closePanel)
document.addEventListener('keydown', (e) => {
  if (e.key === 'Escape') closePanel()
})

route()
