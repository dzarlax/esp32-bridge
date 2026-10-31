# Device acceptance: supervised restarts and 24-hour observation

This checklist is preparation, not evidence that hardware acceptance has passed.
Commit/merge, deployment, firmware publication, three restarts and the real run
require explicit release authorization. Obtain fresh device readiness before OTA.
Keep Google Photos outside this scope.

## Preconditions

- Record the deployed Bridge/Calendar image digests and installed firmware version.
- Confirm desired/applied settings revision, normal data loading and saved brightness.
- Keep the existing calibrated PWM endpoints and day/night values. Do not reset NVS,
  replace keys, alter partitions/bootloader or install via USB automatically.
- Verify the known clients use X-API-Key before removing query authentication.
  Source inspection alone cannot rule out unknown clients when access logs are off.
- Use isolated calendar fixtures for automated checks. Real diagnostics use cached
  read-only GET routes; do not create events, invite attendees or trigger sync.

## Three supervised restarts

Once the whole series is authorized, send one restart through the selected device's
Bridge panel. Wait for a fresh heartbeat, a changed boot ID, the expected firmware,
applied revision and successful data refresh before the next restart. Repeat three
times, recording each result. Check Summary and the photo frame visually after each.
Stop on failure; do not automatically retry OTA or switch to USB. UART capture, when
needed, stays private and only sanitized fault details enter the report.

## Bounded observer

The runner sends only POST login and GET selected-device settings. It cannot issue
a command, change settings or flash firmware. It renews the 8-hour session at six
hours, with at most one refresh/retry after an expired-session response. Credentials
are read from a private regular file, never passed as a literal command argument.

Run from the Bridge repository, using task-specific variables:

```sh
python3 tools/device_soak.py \
  --base-url "$SOAK_BASE_URL" \
  --admin-origin "$SOAK_ADMIN_ORIGIN" \
  --device-id "$SOAK_DEVICE_ID" \
  --password-file "$SOAK_PASSWORD_FILE" \
  --output-dir "$SOAK_OUTPUT_DIR" \
  --duration-seconds 86400 \
  --interval-seconds 60
```

`SOAK_ADMIN_ORIGIN` is the canonical HTTPS admin origin. For a temporary SSH tunnel,
`SOAK_BASE_URL` may be its loopback HTTP address. For direct HTTPS it must match the
admin origin host and port. URLs cannot carry credentials, paths, queries or
fragments. Password-file permissions must exclude group/world access.

The caller owns the temporary tunnel and Mac awake session. Keep the Mac powered,
awake and online for the run; a bounded `caffeinate` session may be used. Record
which processes belong to this run and stop them afterward. The runner does not
create or clean up SSH/caffeinate processes. Ctrl-C finalizes an interrupted report.

Each run creates a new private directory containing incremental `samples.jsonl`
and `summary.json`. Reports retain only whitelisted diagnostics: firmware,
revision, uptime, hashed boot identity and backlight acknowledgement. Raw device
identity, IP/SSID, credentials, photos, events and task content are excluded.
Do not commit private raw logs. Retain a compact sanitized acceptance report.

## Interpret the report

- `observation_passed` means the bounded heartbeat/revision/boot checks passed;
  physical day/night, visual checks and the restart series remain separately pending.
- A confirmed offline device differs from an observer transport outage or stale
  contradictory snapshot. Missing observation or Mac sleep makes the result
  inconclusive rather than proving a device fault or a successful 24-hour run.
- Unexpected reboot, firmware/revision change or genuine offline state needs review.
- Healthy heartbeats do not prove absence of flicker, all panics, memory leaks or
  touch faults. Those metrics are not measured by this runner.

## Manual acceptance and cleanup

Check the configured night transition and wake behavior, then daytime brightness;
keep the chosen schedule/dim/off settings unchanged. Inspect photo transitions,
clock/weather/event persistence, single-tap exit and hold controls. Exercise Summary,
calendar loading and Home action feedback. Record actual times and observed results.

Only close point four after three recorded restarts, a completed uninterrupted
24-hour observation and the visual/day-night checks. Close task-owned tunnels,
observer and awake helpers, remove disposable build copies/fixtures, and retain
source, sanitized evidence and required rollback material.
