# Runs Tracker

A self-hosted running-activity tracker. Go (`net/http`) backend, SQLite storage,
embedded Vite + vanilla-JS frontend with Leaflet maps and Chart.js graphs.

## Quick start (Docker)

```bash
docker compose build
docker compose up -d
# open http://localhost:8651
```

The Strava export in `data/strava-export.json` is embedded in the binary and
seeded into the database on first start (`INSERT OR IGNORE`, idempotent).

## Stack

- **Backend:** Go, standard library `net/http` + `mattn/go-sqlite3` (CGO).
- **Frontend:** Vite, vanilla JS/HTML/CSS, Leaflet, Chart.js. Built to `dist/`
  and embedded into the Go binary via `//go:embed`.
- **Database:** SQLite (single `runs` table). Path via `DB_PATH` (default `/data/runs.db`).
- **Port:** `8651` (override with `PORT`).

## API

| Method | Path | Description |
| ------ | ---- | ----------- |
| GET | `/api/runs` | Paginated list. Params: `page`, `per_page`, `sort` (date/distance/pace/elevation), `order` (asc/desc), `search`. |
| GET | `/api/runs/:id` | Full run detail incl. splits, elevation/cadence series, best efforts. |
| GET | `/api/runs/:id/gpx` | GPX track built from the route polyline. |
| GET | `/api/stats` | Aggregated statistics + progression + calendar heatmap data. |
| GET | `/api/health` | Health check. |

## Development

```bash
# Terminal 1 — backend (needs Go + CGO)
go run .

# Terminal 2 — frontend dev server (proxies /api to :8651)
cd frontend && npm install && npm run dev
```

## A note on derived metrics

The Strava *summary* export contains routes (polylines), aggregate pace,
cadence and elevation bounds, but **no per-point streams or per-km splits and
no heart-rate data**. The backend therefore derives km splits, the pace /
cadence / elevation charts and 1k/5k/10k best efforts deterministically from
each run's geometry and aggregates. These are flagged **estimated** in the UI.
Heart-rate charts only appear for runs that actually carry HR data.
