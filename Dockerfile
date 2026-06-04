# syntax=docker/dockerfile:1

# ---------- Stage 1: build the Vite frontend ----------
FROM node:22-bookworm AS frontend
WORKDIR /app/frontend
COPY frontend/package.json ./
RUN npm install
COPY frontend/ ./
RUN npm run build

# ---------- Stage 2: build the Go backend ----------
FROM golang:1.23-bookworm AS backend
ENV CGO_ENABLED=1 \
    GOFLAGS=-mod=mod
WORKDIR /src
# Fetch dependencies first for better layer caching.
COPY go.mod ./
RUN go mod download || true
# Copy Go sources and embedded seed data.
COPY *.go ./
COPY data/ ./data/
# The frontend build must exist before `go build` so //go:embed can include it.
COPY --from=frontend /app/frontend/dist ./frontend/dist
RUN go build -ldflags="-s -w" -o /out/runs-tracker .

# ---------- Stage 3: minimal runtime ----------
FROM debian:bookworm-slim AS runtime
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates tzdata \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --system --uid 10001 --no-create-home appuser \
    && mkdir -p /data \
    && chown appuser:appuser /data
COPY --from=backend /out/runs-tracker /usr/local/bin/runs-tracker
USER appuser
ENV PORT=8651 \
    DB_PATH=/data/runs.db
EXPOSE 8651
VOLUME ["/data"]
ENTRYPOINT ["/usr/local/bin/runs-tracker"]
