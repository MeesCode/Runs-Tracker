import {
  Chart,
  LineController, LineElement, PointElement,
  BarController, BarElement,
  LinearScale, CategoryScale, Filler, Tooltip, Legend,
} from 'chart.js'

Chart.register(
  LineController, LineElement, PointElement,
  BarController, BarElement,
  LinearScale, CategoryScale, Filler, Tooltip, Legend,
)

const ORANGE = '#ff6b35'
const ORANGE2 = '#ff8f4f'
const GRID = 'rgba(255,255,255,0.05)'
const TICK = 'rgba(255,255,255,0.7)'

Chart.defaults.color = TICK
Chart.defaults.font.family = 'system-ui, -apple-system, Segoe UI, Roboto, sans-serif'

function baseScales(xTitle, yTitle, opts = {}) {
  return {
    x: {
      title: { display: !!xTitle, text: xTitle, color: TICK },
      grid: { color: GRID },
      ticks: { maxRotation: 0, autoSkip: true, maxTicksLimit: 8 },
    },
    y: {
      title: { display: !!yTitle, text: yTitle, color: TICK },
      grid: { color: GRID },
      reverse: !!opts.reverseY,
      ...(opts.y || {}),
    },
  }
}

const common = {
  responsive: true,
  maintainAspectRatio: false,
  plugins: { legend: { display: false } },
}

export function lineChart(canvas, { labels, data, xTitle, yTitle, fill = true, reverseY = false, paceTooltip = false }) {
  return new Chart(canvas, {
    type: 'line',
    data: {
      labels,
      datasets: [{
        data,
        borderColor: ORANGE,
        backgroundColor: fill ? 'rgba(255,107,53,0.15)' : 'transparent',
        borderWidth: 2,
        pointRadius: 0,
        pointHoverRadius: 4,
        pointHoverBackgroundColor: ORANGE2,
        tension: 0.35,
        fill,
      }],
    },
    options: {
      ...common,
      scales: baseScales(xTitle, yTitle, { reverseY }),
      plugins: {
        ...common.plugins,
        tooltip: paceTooltip ? { callbacks: { label: (c) => paceLabel(c.parsed.y) } } : {},
      },
    },
  })
}

export function barChart(canvas, { labels, data, xTitle, yTitle }) {
  return new Chart(canvas, {
    type: 'bar',
    data: {
      labels,
      datasets: [{
        data,
        backgroundColor: 'rgba(255,107,53,0.65)',
        hoverBackgroundColor: ORANGE,
        borderRadius: 4,
      }],
    },
    options: { ...common, scales: baseScales(xTitle, yTitle) },
  })
}

function paceLabel(secPerKm) {
  if (!secPerKm || secPerKm <= 0) return '—'
  const m = Math.floor(secPerKm / 60)
  const s = Math.round(secPerKm % 60)
  return `${m}:${String(s).padStart(2, '0')} /km`
}
