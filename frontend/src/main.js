import './style.css'
import { renderRunsView } from './runsView.js'
import { renderStatsView } from './statsView.js'
import { closePanel } from './detail.js'

const app = document.getElementById('app')

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

// Close panel via overlay click or Escape.
document.getElementById('overlay').addEventListener('click', closePanel)
document.addEventListener('keydown', (e) => {
  if (e.key === 'Escape') closePanel()
})

route()
