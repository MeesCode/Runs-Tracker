import L from 'leaflet'
import 'leaflet/dist/leaflet.css'

const ORANGE = '#ff6b35'
const TILE = 'https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png'
const ATTR = '&copy; OpenStreetMap contributors'

// Render a route polyline into a container element. `points` is [{lat,lng}].
// `interactive` controls zoom/drag (off for card thumbnails).
export function renderRoute(container, points, { interactive = true, markers = true } = {}) {
  if (!points || points.length === 0) {
    container.innerHTML = '<div class="map-empty">No route data</div>'
    return null
  }

  const map = L.map(container, {
    zoomControl: interactive,
    dragging: interactive,
    scrollWheelZoom: interactive,
    doubleClickZoom: interactive,
    boxZoom: interactive,
    keyboard: interactive,
    tap: interactive,
    attributionControl: interactive,
  })

  L.tileLayer(TILE, { attribution: ATTR, maxZoom: 19 }).addTo(map)

  const latlngs = points.map((p) => [p.lat, p.lng])
  const line = L.polyline(latlngs, { color: ORANGE, weight: 4, opacity: 0.9 }).addTo(map)

  if (markers && latlngs.length > 1) {
    L.circleMarker(latlngs[0], dot('#2ecc71')).addTo(map).bindTooltip('Start')
    L.circleMarker(latlngs[latlngs.length - 1], dot('#e74c3c')).addTo(map).bindTooltip('Finish')
  }

  map.fitBounds(line.getBounds(), { padding: [16, 16] })

  // Cards may not have final size when created; recompute shortly after.
  setTimeout(() => map.invalidateSize(), 60)
  return map
}

function dot(color) {
  return { radius: 6, color: '#fff', weight: 2, fillColor: color, fillOpacity: 1 }
}
