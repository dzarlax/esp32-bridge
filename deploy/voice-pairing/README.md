# Voice pairing deployment

This directory contains reusable configuration and isolated test tools. It contains no operator deployment inventory, real endpoint addresses, credentials, private keys or runtime state. Keep actual deployment files and release evidence outside the public source repository.

## Main Compose and backend TLS

Add pairing settings directly to the service's existing main Compose. Do not replace it with this repository's minimal development Compose and do not add another deployment override. Preserve existing integrations, authentication, data mounts, networks and router settings.

Enable `VOICE_PAIRING_ENABLED` and administration. Configure `VOICE_PAIRING_HTTPS_ORIGIN` to match the canonical HTTPS authority of `ADMIN_BASE_URL`, with an explicit port, for example `https://bridge.example.test:443`. The WSS authority must match. Use an accepted immutable CI image digest, never `latest` or an unverified tag. Configuration parsing does not verify image provenance.

Provide these separate private files through a read-only bind at `/run/voice-pairing`, with `bind.create_host_path: false`:

- `master.key` and `integrity.key`: independent random 32-byte keys. Set distinct, stable `VOICE_PAIRING_MASTER_ID` and `VOICE_PAIRING_INTEGRITY_ID`.
- `signing.pem`: a separate P-256 signing key.
- `backend.key` and `backend.crt`: a distinct TLS private key and its certificate chain.

Set the corresponding `VOICE_PAIRING_*_FILE` paths. Private files must be regular files, mode 0600, without symlinks or hard-link reuse. Match ownership to the runtime user. Persist `VOICE_PAIRING_STATE_DIR` under the service's data mount in an exclusive directory, mode 0700, on storage supporting locks, atomic rename and fsync. Preserve state and keys together. Keep secrets out of environment dumps, labels, logs, Git and shell history.

Issue the backend certificate with DNS SAN `homedash-bridge.internal`, matching `traefik-dynamic.yml`. Keep the CA signing key offline. Traefik receives only the public CA certificate, exposed at `/safety-dynamic/voice-pairing-backend-ca.crt` through an existing read-only file-provider mount. Adjust that mount path to the installation if necessary. Deploy only the supplied `ServersTransport` fragment and public CA through that provider. Do not change unrelated shared routes or disable certificate verification.

Add these service labels to the main Compose (substitute the existing service name if different):

```yaml
traefik.http.services.esp32-bridge.loadbalancer.server.scheme: https
traefik.http.services.esp32-bridge.loadbalancer.serverstransport: homedash-voice-backend@file
```

Switch the backend listener and labels together. Inventory internal HTTP clients and move them to a verified TLS path before switching. The public TLS terminator and Bridge are inside the secret-delivery trust boundary; protect every intermediary link. Do not log bodies, Authorization, cookies, CSRF tokens or pairing codes.

`VOICE_PAIRING_LEGACY_MIGRATION=true` explicitly allows a legacy subscriber/target until the first signed ACK. The durable managed marker then prevents automatic legacy fallback. With migration disabled, remove the legacy subscriber and target from the runtime configuration before enabling pairing. Keep publisher, subscriber and device API credentials distinct. Remove obsolete legacy material under the installation's operational procedure.

## Isolated tests and CI

Pre-pull the exact public Traefik digest specified by `test-traefik-system.sh`, then run that script from the repository. It builds test executables, uses synthetic keys and certificates, and runs disposable containers as the invoking UID/GID on an internal network without published ports. Containers, network and temporary files are removed on exit. It does not access a deployment or read its `.env`.

The test covers frontend/backend TLS, rejection of incorrect backend CA/SAN, real admin password login, CSRF, signed device pairing, WSS publish/delivery, revocation, signed clear and clear ACK. CI requires this path and Go race tests before the image build job. Hosted CI and the final image must pass their own release acceptance; a local executable test does not validate image packaging.

For optional main-Compose/rollback validation, supply `VOICE_MAIN_COMPOSE` as an absolute local path and `VOICE_ROLLBACK_IMAGE` as the previously accepted immutable image reference. Pre-pull both images. The renderer checks relevant main-Compose fields and creates disposable single-file fixture configurations with synthetic environment, mounts and an isolated network. These test inputs are not deployment overrides. The test exercises downgrade before/after ACK, ordinary admin/sync with voice disabled, return to the new binary, unchanged state hash, and rejection of legacy/revoked credentials after return. Production env files and mounts are excluded.

The fixture uses the file provider for routing. It does not validate the actual deployment's Docker-provider discovery, external authentication, internal clients or physical display. Perform those separately before accepting a rollout. A health endpoint alone does not establish pairing or delivery.

## Release and rollback

Publish images only from an authorized reviewed source revision after all CI gates pass. Record the source SHA, CI run and image digest; retrieve by digest and verify the OCI revision label and architecture. Test the actual image's embedded UI, login/CSRF, ordinary sync and signed pairing/WSS flow in isolation before putting its digest into the main Compose. PR builds do not publish; inspect workflow triggers before integration because a main push or dispatch can publish and update mutable tags.

Before rollout, inventory the exact main Compose, all effective proxy Compose inputs, current immutable image, mounts, data and key locations. Prepare an access-controlled consistent backup and verify the proxy provider, transport and certificate without exposing secrets. Never overwrite newer runtime state or `.env` during configuration delivery. Deployment and device firmware changes are separate operations.

There is no rollback-resistant counter or independent revocation ledger. Deleting state, restoring an older valid backup, restored settings or an old binary can revive prior authorization. Never expose an uncertain state with a publisher or any subscriber enabled.

1. For a code/config downgrade with current state intact, stop publishing and close/revoke voice connections where possible. Preserve the latest state and matching keys. Restore the prior image and its matching HTTP/TLS routing as one coordinated main-Compose edit.
2. Before starting an old binary, explicitly set `VOICE_PUSH_PUBLISHER_TOKEN`, `VOICE_PUSH_SUBSCRIBER_TOKEN` and `VOICE_PUSH_TARGET_ID` to empty strings in the main Compose's environment. This must override inherited env-file values. Prevent parallel old instances. Check that neither publishing nor subscribing works. Verify ordinary login/sync; voice stays off.
3. For corrupt, lost, stale or uncertain state, isolate voice and preserve evidence. Do not restore an old JSON file into a live service or assume re-pairing resets revocation epochs. Reconcile authoritative state or design and test recovery before enabling voice.
4. Return to the pairing image only with current verified state, matching keys and validated TLS. Check targeted WSS and device cleanup separately before declaring voice restored.

The disabled-mode guard in the new server cannot protect an older image or detect removal of every external marker. The procedure relies on withholding voice credentials and publisher access during downgrade.
