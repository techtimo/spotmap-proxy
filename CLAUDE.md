# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What This Is

Spotmap-Proxy is a Go service that bridges GPS tracking devices to Spotmap WordPress installations. It receives position data from two sources — **Zoleo** satellite communicators (via HTTP webhook) and **OGN/FLARM** glider transponders (via APRS TCP stream) — and forwards coordinates to registered WordPress endpoints. GPS points are never persisted; they flow through by design. One proxy instance serves multiple WordPress sites.

## Commands

```bash
# Build
go build -ldflags="-s -w" -o spotmap-proxy .

# Run locally (requires env vars)
go run .

# Run with Docker
docker compose up -d

# Health check
curl http://localhost:8080/health
```

There are no automated tests (`*_test.go` files do not exist). Testing is done manually against a running instance.

## Architecture

```
main.go                          # Wires everything together; starts HTTP + metrics servers + OGN + AIS clients
internal/db/db.go                # SQLite (WAL mode); zoleo_routes, ogn_routes, ais_routes tables
internal/forwarder/forwarder.go  # HTTP POST to WordPress ingest endpoints (async, fire-and-forget)
internal/ogn/client.go           # APRS TCP client; connects to aprs.glidernet.org:14580, parses positions
internal/ais/client.go           # AIS WebSocket client; connects to stream.aisstream.io, forwards vessel positions
internal/zoleo/receiver.go       # HTTP webhook handler with optional Basic Auth
internal/provision/routes.go     # Self-service route registration API (WordPress plugin calls this)
internal/admin/routes.go         # Admin route management
internal/metrics/metrics.go      # Prometheus metrics + stale-aircraft cleanup loop
static/index.html                # Self-hosted API docs
```

### Data flows

- **Zoleo**: `POST /zoleo` → DB lookup by account ID → `forwarder.Post()` → WordPress
- **OGN**: APRS TCP line → regex parse → DB lookup by device address → `forwarder.Post()` → WordPress + metrics
- **AIS**: WebSocket message → JSON parse → DB lookup by MMSI → `forwarder.Post()` → WordPress + metrics
- **Provisioning**: WordPress plugin `POST /provision/routes/{zoleo|ogn|ais}` with PROVISION_KEY → upsert in DB → reconnect if filter changed
- **Admin**: `GET|DELETE /admin/routes/...` with ADMIN_KEY → direct DB queries

### Key constraints

- A device can only route to **one WordPress site** — registering to a second returns HTTP 409.
- OGN reconnects to APRS immediately whenever routes are added or removed (to update the device filter string).
- Forwarding failures are silent and the GPS point is dropped — no retry queue.
- Routes can be self-deleted only if the caller supplies the matching `ingest_key`; admin endpoints bypass this.
- Provision endpoint has per-IP rate limiting: max 2 requests per minute.

## Configuration (environment variables)

| Variable | Default | Required | Purpose |
|---|---|---|---|
| `ADMIN_KEY` | — | Yes | Bearer token for `/admin/*` endpoints |
| `PROVISION_KEY` | — | Yes | Bearer token for `/provision/*` endpoints |
| `PORT` | `8080` | No | HTTP server port |
| `METRICS_PORT` | `9877` | No | Prometheus metrics port |
| `DATABASE_PATH` | `/data/spotmap-proxy.sqlite` | No | SQLite file |
| `MAX_ROUTES` | `100` | No | Total route capacity |
| `OGN_STALE_SECONDS` | `300` | No | Seconds before aircraft metrics expire |
| `ZOLEO_BASIC_AUTH_USER/PASS` | — | No | Optional Basic Auth on `/zoleo` |
| `AIS_API_KEY` | — | No | AISstream API key; AIS disabled if absent |
| `ZOLEO_LOG_PATH` | `/data/zoleo-payloads.log` | No | Raw Zoleo payload log for debugging |

## Deployment

- Deployed on **Fly.io** (Frankfurt, `spotmap-proxy.fly.dev`), 256MB RAM, shared CPU, HTTPS enforced.
- Docker: multi-stage build (`golang:1.22-alpine` → `alpine:3.20`), port 8080, `/data` volume for SQLite.
- No CI/CD pipeline — deploys are manual via `fly deploy`.

## Metrics & Observability

Prometheus metrics on port 9877: route counts, per-aircraft lat/lon/alt/last-seen. `grafana-dashboard.json` contains a ready-to-import Grafana dashboard with geomap and table panels.
