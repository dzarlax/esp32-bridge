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

### Web settings panel

The optional `/settings/` panel controls the display, photo frame, data sources,
network, updates, and diagnostics. It is a React/TypeScript app compiled into the
same Bridge binary and Docker image. Enable it explicitly with `ADMIN_ENABLED=true`;
existing deployments keep the panel disabled until configured.

One Bridge supports up to eight registered displays. Connections and credentials
for Immich, Home Assistant and other upstream services are shared. Each display
has its own screen preferences, photo album and placement, source selections,
network settings, status and command queue. The device selector lists actual
registrations; a new display appears after its first authenticated settings sync.
Changing shared connections explicitly affects every display using that service.

Managed firmware generates a stable `device_id` in a separate NVS namespace and
sends `X-Device-ID` on all native API requests. The identity survives preferences
reset, reboot and network changes. Unknown identities must sync before reading
managed data. Headerless legacy reads keep the default environment configuration;
when administration is disabled, native API behavior remains compatible.

The panel supports either or both independent authentication methods:

- Standalone administrator password: mount a private verifier file and set
  `ADMIN_PASSWORD_HASH_FILE`. The browser receives an opaque session cookie,
  not the display's API key. Generate the verifier without putting a password
  in command arguments or shell history, for example:

  ```sh
  mkdir -p secrets
  chmod 700 secrets
  python3 -c 'import getpass,sys; sys.stdout.write(getpass.getpass("Admin password: "))' | go run ./cmd/admin-password -out secrets/admin-password-hash
  ```

  The output is a salted PBKDF2-SHA256 verifier (600,000 iterations), mode 0600.
  Mount it read-only at `/run/secrets/admin-password-hash` and set that path in
  `ADMIN_PASSWORD_HASH_FILE`. Keep `secrets/` outside Git. The image also includes
  `/app/admin-password` for deployments without a Go installation.
- Optional Cloudflare Access: set `ADMIN_CF_TEAM_DOMAIN` (the team's
  `https://…cloudflareaccess.com` URL), `ADMIN_CF_AUDIENCE`, and comma-separated
  `ADMIN_CF_EMAILS`. Bridge checks signature, issuer, audience, expiry and the
  email allowlist on the origin. A forged identity header is insufficient.
  Protect the panel and `/api/admin/v1/*` at the proxy as appropriate; keep native
  device endpoints independent. Password-only installations need no Cloudflare.

Set `ADMIN_BASE_URL` to the exact externally visible origin (HTTPS in production).
`http://127.0.0.1:<port>` and other loopback origins are supported for local tests.
Browser writes require a session, exact Origin, and a CSRF token; the device API
key does not grant administrator access. When enabled without a complete valid
authentication method, startup fails closed.

The Compose definition mounts `./data:/data`. `ADMIN_STATE_DIR` must be a private
mode-0700 directory on that persistent volume; state files are mode 0600. They
contain sensitive configuration and must stay out of Git, diagnostics, and
ordinary backups intended for sharing. Environment values and mounted secrets
provide initial defaults; UI overrides are stored separately with versioned,
atomic writes. The panel never overwrites the VPS `.env` or original secret files.
Back up the private state directory before changing deployment storage.

Server changes take effect after validation and persistence. Device changes show
as pending until managed firmware confirms the applied revision; disconnected
or legacy firmware is not reported as configured. The existing NVS Bridge API
key represents fully trusted displays belonging to one owner, including sync and existing Home
Assistant commands. This is not multi-owner isolation: a compromised shared key
can impersonate another display. Do not distribute it as a read-only token. No device keys,
provider tokens or Wi-Fi passwords are returned by administrative reads.

The panel configures one shared Immich endpoint and read-only key, with a separate
album UUID for each display. Managed frame caches are bounded to 16 frames per
display and image decoding uses one global slot; identical upstream fetches reuse
a cache keyed by their effective configuration. Google Photos
is explicitly unavailable until its adapter and production API access exist.
Brightness is unavailable until the hardware advertises support. Photo placement
supports adaptive crop with an adjustable threshold, full-photo blurred background,
and cover; rendering options and source changes invalidate the prepared frames.
The illustration in the panel is a sample preview, not a private photo download.

Build and check locally:

```sh
cd web
npm ci
npm run typecheck
npm test
npm run build
cd ..
go test ./...
go test -race ./internal/adminauth ./internal/settings ./internal/management ./internal/photoframe
go vet ./...
go build -tags webassets ./cmd/server
```

The CI and Docker builds produce the frontend before the embedded Go binary.
Without the `webassets` tag a developer build can serve `web/dist`; if the frontend
has not been built it returns 503 rather than a blank or placeholder panel.
The device protocol and safe network/command lifecycle are documented in
[settings-contract.md](docs/settings-contract.md). Firmware publication and a
real device update remain separate release actions.
