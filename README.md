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
│    /api/calendar ────→ Calendar Platform events │
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
| `/api/calendar?date=YYYY-MM-DD` | GET | On-demand events for a date (Calendar Platform when configured; otherwise Home Assistant) |
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
| `HA_CLIMATES` | | Comma-separated climate entity IDs allowed on the display |
| `HA_CACHE_TTL` | `120` | Cache TTL in seconds |
| `CALENDAR_PLATFORM_BASE_URL` | | Calendar Platform public base URL; enable together with token and IDs |
| `CALENDAR_PLATFORM_TOKEN` | | Calendar Platform <code>READ_ONLY_TOKEN</code> used only for cached events |
| `CALENDAR_PLATFORM_IDS` | | Comma-separated canonical calendar IDs, in display colour order |
| `CALENDAR_TIME_ZONE` | `Europe/Belgrade` | IANA timezone used to select a calendar day and display its times |
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
- [Health Dashboard](https://github.com/Dzarlax-AI/health_dashboard) — Apple Health data ingestion, readiness scoring, web UI and MCP server
- [Evening News](https://github.com/Dzarlax-AI/evening-news) — AI-powered news aggregator with summaries and categorization
- [City Dashboard](https://github.com/dzarlax/city-dashboard) — Belgrade/Novi Sad/Niš public transit: schedules, routes, and real-time arrivals where available

## License

MIT

### Photo frame (Stage A)

The photo frame API is disabled by default. Set `PHOTO_FRAME_ENABLED=true` and
`PHOTO_FRAME_SOURCE=fixture` to test synthetic images. `none` reports the source
as unavailable; it does not read local image files. Optional settings are
`PHOTO_FRAME_IDLE_SECONDS` (30–3600, default 300),
`PHOTO_FRAME_INTERVAL_SECONDS` (15–3600, default 60),
`PHOTO_FRAME_MAX_STALE_SECONDS` (60–3600, default 900), and
`PHOTO_FRAME_CACHE_FRAMES` (16–32, default 32). JPEG, PNG and WebP source images are
bounded to 20 MiB and 24 megapixels; unknown embedded color profiles are rejected. The exact standard libvips
sRGB and Display P3 JPEG profiles are recognized; P3 pixels are converted to
sRGB before RGB565 quantization. The fixture is for local integration testing only.

The authenticated `/api/photo-frame/v1/manifest` describes prepared frames;
`/api/photo-frame/v1/frames/{id}` returns immutable 1024×600 RGB565 little-endian
pixels. After EXIF orientation, photos use a centered cover crop when it removes
at most 10% of their area. Otherwise, the complete sharp photo is centered over
a blurred, dimmed cover copy of itself. Each frame contains one photo, without
stretching. Background blur runs at 128×75 before smooth enlargement; foreground
colors and sharpness are preserved.
Rendering runs in a separate background worker, not in request handlers.


### Immich album source

Set `PHOTO_FRAME_SOURCE=immich` with `PHOTO_FRAME_ENABLED=true` and configure:

```env
IMMICH_BASE_URL=https://photos.example.com
IMMICH_ALBUM_ID=your-album-uuid
IMMICH_API_KEY_FILE=/run/secrets/immich-api-key
```

Mount the key as a read-only private regular file (mode 0400 or 0600, readable
by the Bridge process). Do not put the key in Git, the display, command-line
arguments or logs. Only the selected album is queried. This is an application
restriction: a user's Immich API key may still have access to other albums.
Use a dedicated key with only the read permissions needed for the album,
metadata search and preview access. Verify these permissions on the installed
Immich version before deployment; do not grant write/delete or original-download
permissions for the photo frame.

The adapter targets Immich 3.3.5: it verifies the album, searches its images
using `filter.albumIds.any` and cursor pagination, and fetches edited previews
when available. It rejects redirects, missing configuration, unexpected schemas,
cyclic pagination and albums exceeding 5000 entries. It does not fall back to
the whole library or original media. Preview bodies are limited to 20 MiB and
24 megapixels; JSON pages are limited to 4 MiB. Only the pinned libvips sRGB/Display P3 JPEG profiles are supported; all other
embedded profiles remain rejected. Test real preview formats before switching from the fixture.

Removing an asset or changing its revision revokes its old cached frame ID
after the next successful source refresh. Authorization denial clears the
cache immediately for new requests. In-flight transfers finish from their
borrowed bytes. A transient source failure permits existing cached frames only
within `PHOTO_FRAME_MAX_STALE_SECONDS` of the last successful listing; requests
do not extend that period. A frame already displayed may remain until the
display receives the next manifest or reaches its stale deadline.

Keep `PHOTO_FRAME_SOURCE=fixture` until album access, preview rendering, service
health and the physical display have been accepted. Rollback uses the previous
pinned image/configuration and fixture source; no Immich media is modified.
