# ESP32 Bridge

A Go microservice that aggregates data from multiple smart home and productivity sources into a single API for the [HomeDash](https://github.com/dzarlax/homedash) ESP32 touchscreen display.

## Why

The ESP32 has limited memory and TLS connections. Instead of making 7+ HTTPS calls to different APIs, it makes one call to Bridge. Bridge handles caching, error recovery, text sanitization, and OTA firmware delivery.

## Architecture

```
┌─────────────────────────────────────────────────┐
│                ESP32 Bridge                     │
│                                                 │
│  Fetchers (parallel, cached):                   │
│    Health Dashboard ──→ steps, sleep, HR, HRV   │
│    Todoist API ───────→ tasks                   │
│    Evening News ──────→ news headlines          │
│    Home Assistant ────→ sensors + lights         │
│    Open-Meteo ────────→ weather + forecast       │
│    Transport API ─────→ bus arrivals             │
│                                                 │
│  On-demand endpoints:                           │
│    /api/calendar ────→ HA calendar events       │
│    /api/ha/action ───→ light toggle (+ fresh state) │
│    /api/ota/check ───→ firmware version check    │
│    /api/ota/firmware → streams firmware binary   │
└───────────────────┬─────────────────────────────┘
                    │
                    ▼
              HomeDash ESP32
```

## API Endpoints

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/api/dashboard` | GET | All cached data: health, tasks, news, sensors, lights, weather, transport |
| `/api/calendar?date=YYYY-MM-DD` | GET | On-demand HA calendar events for a date |
| `/api/ha/action` | POST | Light toggle/on/off — returns fresh light state |
| `/api/ota/check?v=X.Y.Z` | GET | Compares version against latest GitHub Release |
| `/api/ota/firmware` | GET | Proxies firmware binary from GitHub Release |
| `/health` | GET | Server health check with uptime |

All endpoints except `/health` require authentication via `X-API-Key` header or `?key=` query parameter.

## Configuration

All configuration is via environment variables:

### Required

| Variable | Description |
|----------|-------------|
| `API_KEY` | Authentication key for all API endpoints |

### Data Sources (all optional — only enabled if configured)

| Variable | Default | Description |
|----------|---------|-------------|
| `HEALTH_BASE_URL` | | Health Dashboard URL (e.g. `http://health-receiver:8080`) |
| `HEALTH_API_KEY` | | Health Dashboard API key |
| `HEALTH_CACHE_TTL` | `300` | Cache TTL in seconds |
| `TODOIST_TOKEN` | | Todoist API bearer token |
| `TODOIST_CACHE_TTL` | `60` | Cache TTL in seconds |
| `NEWS_BASE_URL` | | Evening News API URL |
| `NEWS_LIMIT` | `5` | Max news items |
| `NEWS_SINCE_HOURS` | `24` | News recency window |
| `NEWS_CACHE_TTL` | `900` | Cache TTL in seconds |
| `HA_BASE_URL` | | Home Assistant URL (e.g. `https://ha.example.com`) |
| `HA_TOKEN` | | Home Assistant long-lived access token |
| `HA_SENSORS` | | Comma-separated sensor entity IDs |
| `HA_LIGHTS` | | Comma-separated light entity IDs |
| `HA_CACHE_TTL` | `120` | Cache TTL in seconds |
| `WEATHER_LAT` | `44.82` | Latitude for Open-Meteo |
| `WEATHER_LON` | `20.46` | Longitude for Open-Meteo |
| `WEATHER_TZ` | `Europe/Belgrade` | Timezone for Open-Meteo |
| `WEATHER_CACHE_TTL` | `1800` | Cache TTL in seconds |
| `TRANSPORT_BASE_URL` | | Transport API URL |
| `TRANSPORT_STOPS` | | Comma-separated stop IDs |
| `TRANSPORT_CACHE_TTL` | `30` | Cache TTL in seconds |

### OTA Firmware Updates

| Variable | Description |
|----------|-------------|
| `OTA_GITHUB_REPO` | GitHub repo for firmware releases (e.g. `dzarlax/homedash`) |
| `OTA_GITHUB_TOKEN` | GitHub token (optional, for private repos) |
| `OTA_MIGRATE_BRIDGE_URL` | New Bridge URL for device migration (optional) |

Bridge auto-detects the latest GitHub Release and serves `firmware.bin` to the ESP32. No manual version/URL management needed.

### Server

| Variable | Default | Description |
|----------|---------|-------------|
| `LISTEN_ADDR` | `:8090` | Listen address |
| `FETCH_TIMEOUT` | `5` | Per-fetcher timeout in seconds |

## Deployment

### Docker Compose

```yaml
services:
  esp32-bridge:
    image: ghcr.io/dzarlax/esp32-bridge:latest
    restart: unless-stopped
    env_file: .env
    networks:
      - traefik
      - infra
```

The image is built and pushed automatically on every commit to `main`.

### Text Sanitization

All text from external APIs is sanitized before sending to the display. Characters outside the supported font ranges (Basic Latin, Latin-1 Supplement, Cyrillic) are replaced with `?` to prevent missing-glyph squares on the ESP32 screen.

## Development

```bash
go build ./...          # Build
go run cmd/server/main.go  # Run locally
```

## Related

- [HomeDash](https://github.com/dzarlax/homedash) — ESP32 display firmware that consumes this API
- [Health Dashboard](https://github.com/dzarlax/health_dashboard) — Apple Watch + RingConn health metrics aggregator
- [Evening News](https://github.com/dzarlax/rss-summarizer) — AI-powered news aggregator with summaries and categorization
- [City Dashboard](https://github.com/dzarlax/city-dashboard) — Belgrade/Novi Sad/Niš public transit: schedules, routes, and real-time arrivals where available

## License

MIT
