# HomeDash settings contract v3: independent display languages

User-approved: one Bridge serves multiple displays; upstream connections are shared,
but each display has independent settings, selections, photo album, status and commands.
Password-only and optional Cloudflare Access login are both supported. Local work only;
no commit, push, deployment or flash is authorized yet. This extends the deployed v2 contract with explicit locale support.

## Identity and trust

- `bridge_id`: durable 32 lowercase hex, generated in private server state.
- `device_id`: durable 32 lowercase hex, generated once in device NVS; survives reboot,
  preferences reset, network changes and Bridge migration. A deliberate full identity
  reset is outside the settings-reset action. `boot_id` is ephemeral and separate.
- Existing NVS API key identifies trusted displays belonging to the same owner.
  This is not a distributed read-only credential or a multi-owner isolation model.
  Device IDs prevent accidental state/command mixing; an API-key compromise permits
  device impersonation. Browser admin sessions have independent auth and CSRF.
- Managed device requests use X-API-Key and X-Device-ID on ALL native API requests.
  Body device_id on sync must equal the header. Unknown IDs register only through
  authenticated sync; default cap 8 devices. Reject overflow without changing state.
  Never fall back an unknown managed ID to another display's data.
- Device reads without X-Device-ID continue using the existing default config/feed; X-API-Key remains required.
  When admin management is disabled, keep legacy API behavior (ignore optional ID).

## Admin session (unchanged)

GET /api/admin/v1/session -> {authenticated,password_enabled,cloudflare_enabled,csrf_token?,identity?:{method,email}}.
POST /api/admin/v1/login {password}; POST /api/admin/v1/logout.
GET /api/admin/v1/auth/cloudflare initiates configured Access login.
Cookie credentials, exact Origin and X-CSRF-Token on unsafe methods. No device key
in browser JS, URL, QR, logs or diagnostics. Neither auth method configured means
closed panel. A Cloudflare session continues requiring a valid, unexpired Access application JWT
from the assertion header or CF_Authorization cookie on every request. Access may
protect only the login callback with domain-scoped cookies to keep password login
independent. A validated callback creates a session and redirects to /settings/.

## Shared connections

GET /api/admin/v1/connections ->
{revision,connections:{immich:{base_url},health:{base_url},tasks:{},news:{base_url},home:{base_url},calendar:{base_url},transport:{base_url}},secrets_present:{health_api_key,todoist_token,ha_token,calendar_platform_token,immich_api_key}}.

PATCH same path, If-Match:"<shared revision>", body
{connections:<complete connections object>,secrets?:<write-only map>,confirm_disconnect?:true}.
Empty secret string preserves; null explicitly removes. No secret values on reads.
Changing connections is explicitly marked as affecting all displays in the UI.
Removing a connection/credential used by enabled profiles requires confirm_disconnect;
then disable the affected selections/default source, clear/rebuild relevant caches
and advance affected desired revisions. Do not leave old pictures downloadable by
new requests after disconnect; already admitted transfers may finish.
Server config and mounted secret files are not overwritten. UI overrides are private.

## Devices and their settings

GET /api/admin/v1/devices -> {devices:[{id,name,connected,last_seen,applied_revision,desired_revision,firmware,ip,ssid}],max_devices:8}.
Only actual registrations are listed. No fake device when the list is empty; show
setup instructions. New trusted sync auto-registers from baseline defaults and uses
a distinguishable default name until renamed. Persist profiles across restarts.

GET /api/admin/v1/devices/{id}/settings ->
{
 device_id,revision,settings:{
  screen:{locale,name,city,timezone,clock_format,idle_action,night_enabled,night_start,night_end,auto_update_check,auto_install},
  photos:{enabled,source,base_url,album_id,idle_seconds,interval_seconds,order,layout,crop_limit_percent},
  services:{weather:{enabled,latitude,longitude,timezone},calendar:{enabled,base_url,ids,timezone},transport:{enabled,base_url,ids},health:{enabled,base_url},tasks:{enabled},news:{enabled,base_url,limit,since_hours},home:{enabled,base_url,sensors,lights,climates}},
  network:{bridge_url}
 },
 secrets_present:<shared presence flags>,
 device:{connected,last_seen,applied_revision,applied_locale?,firmware,boot_id,ip,ssid,uptime_seconds,time_synced,capabilities,error,command,model?,width?,height?,ota_progress?},
 statuses:{service:{state,last_success?,error?}},
 capabilities:{brightness:false,google_photos:false,locales:["ru","en","sr-Latn"],timezones:["Europe/Belgrade","UTC"]}
}.

PATCH same scoped settings path, If-Match:"<device desired revision>", body
{settings:<complete values>,network_credentials?:{wifi_ssid?,wifi_password?,bridge_key?}}.
Connection URLs included in the projection are read-only shared fields and must
match current connections. Reject per-device secrets or edits to shared URLs;
use the shared endpoint. All other fields above are per-display. Saving A never
changes B's revision/settings, status, command or photo collection. Conflicts 409,
invalid input 422, persistence error 503. Offline/unsupported network changes 409.

One atomic versioned private state file stores shared connections/secrets, baseline
values, bridge ID, all profiles and command/network state. One monotonic sequence
allocates per-device desired revisions; shared changes assign a new revision to
every affected profile atomically. Device-only updates leave other revisions alone.
Prepare all affected runtimes before persistence; failed save aborts candidates and
keeps the old live state. Persist is atomic temp+sync+rename. Directory 0700, file0600.
Unpublished v1 migration must retain prior defaults, connection overrides and Bridge
identity; never send its unaddressed pending commands to an arbitrary new display.

Known values: screen timezone Europe/Belgrade|UTC, clock_format24h|12h,
idle_action photos|off, source none|immich (fixture only existing baseline),
order shuffle|sequential, layout adaptive|contain|cover, crop limit0..40,
idle30..3600s, interval15..3600s. auto_update_check defaults true; auto_install false.
Photo base_url is the shared Immich connection; album/order/layout/timing/enabled
are per-device. Google Photos remains explicitly unavailable.

## Scoped actions, diagnostics and firmware

POST /api/admin/v1/devices/{id}/actions {action:"photo_start|restart|ota_check|ota_install|reset",confirm?:true}
 ->202 {command:{id,action,state,created_at,expires_at,error?}}.
Restart/install/reset require confirm. Only one unfinished command per device;
offline/unsupported409. Commands and acknowledgments are device-addressed.
GET .../{id}/diagnostics -> redacted attachment; no network credentials, source URLs,
private content or raw identifiers beyond the selected device metadata needed.
GET .../{id}/updates -> {current_version,available_version,update_available,device_connected,history?}.
Unscoped old admin settings/actions/update routes must reject missing device ID,
not silently target the first connected device.

## Native device sync and photo/data routing

POST /api/device/v1/sync, X-API-Key, X-Device-ID, bounded JSON:
{device_id,bridge_id?,firmware,boot_id,applied_revision,applied_locale?,ip,ssid,uptime_seconds,time_synced,
 capabilities:["preferences","photo_start","restart","ota_check","ota_install","reset","network","locale"],
 error?,command_result?:{id,state:"accepted|succeeded|failed",error?},available_version?,ota_progress?,model?,width?,height?}.

Response {device_id,bridge_id,revision,preferences:{screen,photos,visible:{weather,calendar,transport,health,tasks,news,home},posix_timezone,settings_url,network?:{wifi_ssid?,wifi_password?,bridge_url?,bridge_key?}},command:null|{id,action,expires_at}}.
Settings URL may select the current device via non-secret query device_id.
Sync every10s; no heartbeat45s ->offline. Wrong/absent Bridge ID means bootstrap:
ignore that claimed revision/result, return this display's desired state and Bridge
ID. Firmware accepts a lower revision only when authenticated Bridge ID changes.
Do not conflate another display's current revision with this device's revision.

GET /api/device/v1/probe with device headers ->{bridge_id,version:1}; no registration,
heartbeat, revision or command acknowledgment side effects. Used for candidate
network/Bridge verification. Probe does not assume the candidate has this profile.

All normal native API requests (dashboard/calendar/HA/photo manifest/frame/OTA)
include device ID. Their router selects the profile-specific prepared runtime.
An unregistered, syntactically valid device ID may use only GET /api/ota/check
and GET /api/ota/firmware through the legacy shared firmware handler. Both still
require the native API key in X-API-Key (never a query-string key for bootstrap).
These reads never create a profile or apply settings;
dashboard, calendar, home actions and photos remain unavailable until sync.
Malformed identities remain rejected. Registered devices retain their runtime.
Photo frames advertised to A must not be borrowed from B's collection. Requests without X-Device-ID
keep the legacy default; X-API-Key is still required. Raw RGB565 payload protocol stays unchanged.
Photo preparation is bounded globally; cap default managed devices8 and use16
cached frames per managed profile, with one decoding/render job at a time. Shared
upstream connection/data caches should reuse identical source configurations.

## Device transactions (retain previous fixes)

Validate all prefs before a network effect; candidate probe carries no applied
revision. Stage a recoverable journal; credentials and prefs must both be durable
before reporting applied. Failed/ambiguous rollback blocks ordinary sync and fails
closed. Old-Bridge final acknowledgment after commit is best-effort and bounded;
it must not deadlock handoff when old network is unreachable. Stable device ID
continues across the handoff; Bridge ID scopes the new revision.

Persist command ID before effect (at most once), accept matching terminal recovery
from a new boot before marking an unfinished command interrupted, never resend an
accepted old command. reset clears local settings/Wi-Fi/Bridge but not stable
identity, replay markers, other NVS namespaces or any server/provider data.
Recovery AP is physically enabled, WPA2-protected, expires after10minutes. HTTP is
restricted to AP local destination/peer, exact Host/Origin, application/json and
per-window CSRF; GET form must not be exposed through the normal station interface.

## Acceptance

At least two concurrent isolated devices with different names/albums/layouts and
separate revisions. Save A, issue A command, restart storage, and verify B is
unchanged and never receives A's command/frame. Test shared connection transaction
for both with conflict/failure preservation and explicit disconnect confirmation.
Browser selector must show real profiles, dirty-switch handling and common-vs-device
scope. Firmware ID stable after reboot/reset/handoff; every API includes its ID.
No production mutation, publication or firmware flashing as part of local acceptance.

## Localization and persistent compatibility

The independent web language is `ru|en|sr-Latn`, stored in the browser under
`homedash.ui.locale`. Missing or invalid storage defaults to Russian; storage
failure permits session-only switching. The web language never changes a device.
`settings.screen.locale` selects the renderer language for exactly one display,
defaulting to `ru`. User content, IDs, protocol codes and service data are not
translated. Formatting uses ru-RU/en-GB/sr-Latn-RS while timezone and clock-format
settings remain independent.

Changing the device language requires its `locale` capability. A PATCH from an
older client omitting locale preserves the current value. New firmware negotiates reporting from a validated response containing
`preferences.screen.locale`. Its first heartbeat omits the extension so older
strict Bridges can accept it. Reporting is scoped to the Bridge URL; HTTP400
clears negotiation so a rolled-back Bridge receives a compatible next heartbeat.
`applied_locale` is nonempty only when a matching locale sidecar is durable. Admin views
project applied_revision as zero if language does not match; internal revision
checks for network handoff retain their original semantics. Firmware without
`locale` is considered Russian-only, including after downgrade. Native data and
OTA remain usable even if the desired language is unsupported by that firmware.

Private disk schema v3 migrates v1/v2 by filling missing locales with `ru` in the
baseline and every profile. The entire candidate is validated before an atomic
write; all other state and IDs are preserved. A v2 binary cannot read v3 state.
Before an authorized manual server rollback, export the **current** state using:

```sh
go run ./cmd/settings-export-v2 /private/settings.json /private/settings-v2.json
```

The image also includes `/app/settings-export-v2` with the same two arguments.
The output must not exist; it is created mode0600 and contains credentials. The
input is never overwritten. Export preserves integer revisions exactly and
removes only the new locale fields. Switching files/images is a separate
authorized deployment action; do not restore a stale state over newer edits.

Firmware retains the frozen prefs v2 and network trial v1 binary layouts. A
separate versioned NVS locale record is scoped by Bridge ID and revision; a
missing/corrupt/mismatched record safely renders Russian but is not acknowledged.
The matching network rollback locale uses a separate sidecar as well. A failed
prefs commit blocks further sync acknowledgment until a successful save/recovery
or reboot reconciles runtime and durable state. No credential or identity
namespace is erased by locale migration.

## Home Assistant action feedback

`POST /api/ha/action` retains the legacy `ok` and optional `lights`/`climate`
fields. A successful service call adds `action_result.state = "accepted"` and,
when available, a fresh direct `observation` for the requested entity. Acceptance
means the service call succeeded; only a matching available observation confirms
Home Assistant's state. It does not prove physical actuation. Errors use safe
codes; a transport failure after dispatch has `state = "unknown"` because the
service may have executed. The server does not repeat a command. The request has
an eight-second overall upstream budget.

`GET /api/ha/state?entity_id=<id>` uses the same API key and device routing,
validates the exact entity against that device's selected lights or climates, and
reads Home Assistant directly with a two-second budget. It returns
`{"observation":{"entity_id":"...","kind":"light|climate","available":true,"state":"...","target_temperature":22}}`;
the optional target applies to climate only. Unavailable, unknown and invalid
states produce `available:false`; a malformed response or mismatched entity
returns a safe error. Neither legacy snapshots nor unavailable states are proof
of an action. Older Bridges may return 404 for this read-only endpoint.

## Header-only native authentication and terminal OTA

Every native handler, including device sync/probe and OTA bootstrap, requires one
matching nonempty `X-API-Key`. Query-only authentication is rejected with 401.
With a valid header, any `key` query parameter returns 400
`query_key_not_supported`; malformed queries return 400 `invalid_query`.
Administrative session/Cloudflare/CSRF authentication and public health checks
are unchanged. Non-secret `device_id` URL selection remains supported.

`ota_target` is recorded only after image write. Awaiting reboot is `rebooting`;
a terminal command clears the target atomically with its result. Success has no
active stage. Failed/unknown history may retain its last known stage but cannot
supply live progress when there is no matching pending OTA command.

## Cached calendar diagnostics

Per-device calendar status uses the saved calendar selection and adds source
`last_success_at: string|null`, `next_sync_at: string|null` (RFC3339 UTC),
`coverage_complete: boolean|null`, optional `error_code`, and `reasons: string[]`.
Missing metadata means unknown, preserving older upstream compatibility. Coverage
is known only with a successful bounded cached projection. Safe errors are
transient, rate_limited, auth, permission, unsupported, protocol. No event content
or provider error text is included. These GETs do not trigger provider sync.
Explicitly bad coverage/errors and stale/non-ready/missing selected sources
prevent a complete result. Partial photo cards use only eligible selected fresh
sources; no eligible event clears the old card.
