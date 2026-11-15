#!/bin/sh
# Isolated local test only: no production access, published ports or real keys.
set -eu
umask 077
repo=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
image='traefik:v3.7.16@sha256:c8a121e4bb1347e8fd52657fab12f2cb70b68f8a018895e2068f92c9e519b9c2'
case "$(uname -s)" in
  Darwin) temporary_root=/private/tmp ;;
  *) temporary_root=${TMPDIR:-/tmp} ;;
esac
work=$(mktemp -d "$temporary_root/voice-system-qa.XXXXXX")
suffix="$$-$(openssl rand -hex 4)"
network="voice-system-qa-$suffix"
backend="voice-system-qa-backend-$suffix"
proxy="voice-system-qa-proxy-$suffix"
compose_created=false
project="voice-system-qa-$suffix"
runtime_user="$(id -u):$(id -g)"
main_compose=${VOICE_MAIN_COMPOSE:-}
old_image=${VOICE_ROLLBACK_IMAGE:-}
network_created=false
backend_created=false
proxy_created=false
cleanup() {
  if [ "$compose_created" = true ]; then docker compose -p "$project" -f "$work/main.compose.json" down >/dev/null 2>&1 || true; fi
  if [ "$backend_created" = true ]; then docker rm -f "$backend" >/dev/null 2>&1 || true; fi
  if [ "$proxy_created" = true ]; then docker rm -f "$proxy" >/dev/null 2>&1 || true; fi
  if [ "$network_created" = true ]; then docker network rm "$network" >/dev/null 2>&1 || true; fi
  rm -rf "$work"
}
trap cleanup EXIT HUP INT TERM
# Avoid implicit downloads and do not delete or retag any pre-existing image.
docker image inspect "$image" >/dev/null
if [ -n "$main_compose" ]; then
  : "${VOICE_ROLLBACK_IMAGE:?Set the verified immutable rollback image for this local test}"
  docker image inspect "$old_image" >/dev/null
fi
arch=$(docker version --format '{{.Server.Arch}}')
case "$arch" in amd64|arm64) ;; *) echo 'Unsupported Docker architecture' >&2; exit 1;; esac
mkdir -m 700 "$work/keys" "$work/data" "$work/data/pair" "$work/data/voice-pairing" "$work/dynamic" "$work/results"
openssl req -x509 -newkey rsa:2048 -nodes -keyout "$work/backend-ca.key" -out "$work/backend-ca.crt" -days 1 -subj '/CN=Synthetic backend CA' >/dev/null 2>&1
openssl req -newkey rsa:2048 -nodes -keyout "$work/keys/backend.key" -out "$work/backend.csr" -subj '/CN=homedash-bridge.internal' >/dev/null 2>&1
printf 'subjectAltName=DNS:homedash-bridge.internal\nextendedKeyUsage=serverAuth\n' > "$work/backend.ext"
openssl x509 -req -in "$work/backend.csr" -CA "$work/backend-ca.crt" -CAkey "$work/backend-ca.key" -CAcreateserial -out "$work/keys/backend.crt" -days 1 -extfile "$work/backend.ext" >/dev/null 2>&1
openssl req -x509 -newkey rsa:2048 -nodes -keyout "$work/frontend-ca.key" -out "$work/frontend-ca.crt" -days 1 -subj '/CN=Synthetic frontend CA' >/dev/null 2>&1
openssl req -newkey rsa:2048 -nodes -keyout "$work/frontend.key" -out "$work/frontend.csr" -subj '/CN=voice-system-qa-proxy' >/dev/null 2>&1
printf 'subjectAltName=DNS:voice-system-qa-proxy\nextendedKeyUsage=serverAuth\n' > "$work/frontend.ext"
openssl x509 -req -in "$work/frontend.csr" -CA "$work/frontend-ca.crt" -CAkey "$work/frontend-ca.key" -CAcreateserial -out "$work/frontend.crt" -days 1 -extfile "$work/frontend.ext" >/dev/null 2>&1
openssl ecparam -name prime256v1 -genkey -noout -out "$work/keys/signing.pem"
openssl rand -out "$work/keys/master.key" 32
openssl rand -out "$work/keys/integrity.key" 32
chmod 600 "$work"/keys/* "$work/frontend.key"
cd "$repo"
printf 'synthetic-test-password-only\n' | go run ./cmd/admin-password -out "$work/keys/admin.hash"
GOOS=linux GOARCH="$arch" CGO_ENABLED=0 go build -o "$work/bridge" ./cmd/server
GOOS=linux GOARCH="$arch" CGO_ENABLED=0 go test -c -o "$work/test" ./internal/voicepairsetup
cp deploy/voice-pairing/traefik-dynamic.yml "$work/dynamic/transport.yml"
cp "$work/backend-ca.crt" "$work/dynamic/voice-pairing-backend-ca.crt"
cp "$work/frontend-ca.crt" "$work/dynamic/wrong-ca.crt"
cat > "$work/dynamic/routes.yml" <<'YAML'
tls:
  certificates:
    - certFile: /fixture/frontend.crt
      keyFile: /fixture/frontend.key
http:
  routers:
    bridge: {entryPoints: [websecure], rule: 'Host(`voice-system-qa-proxy`)', service: bridge, tls: {}}
    badname: {entryPoints: [websecure], rule: 'Host(`voice-system-qa-proxy`) && PathPrefix(`/qa-badname`)', service: badname, tls: {}}
    badca: {entryPoints: [websecure], rule: 'Host(`voice-system-qa-proxy`) && PathPrefix(`/qa-badca`)', service: badca, tls: {}}
  services:
    bridge:
      loadBalancer:
        serversTransport: homedash-voice-backend@file
        servers: [{url: 'https://voice-system-qa-bridge:8090'}]
    badname:
      loadBalancer:
        serversTransport: wrongname@file
        servers: [{url: 'https://voice-system-qa-bridge:8090'}]
    badca:
      loadBalancer:
        serversTransport: wrongca@file
        servers: [{url: 'https://voice-system-qa-bridge:8090'}]
  serversTransports:
    wrongname:
      serverName: invalid.example
      rootCAs: [/safety-dynamic/voice-pairing-backend-ca.crt]
      insecureSkipVerify: false
    wrongca:
      serverName: homedash-bridge.internal
      rootCAs: [/safety-dynamic/wrong-ca.crt]
      insecureSkipVerify: false
YAML
docker network create --internal --label voice-system-qa=temporary "$network" >/dev/null
network_created=true
if [ -n "$main_compose" ]; then
  : > "$work/empty.env"
  VOICE_BRIDGE_IMAGE="$image" docker compose --env-file "$work/empty.env" -f "$main_compose" config --no-env-resolution --format json > "$work/input.json"
  python3 deploy/voice-pairing/render-main-fixture.py "$work/input.json" "$work" "$network" "$image" "$old_image"
  compose_created=true
  docker compose -p "$project" -f "$work/main.compose.json" up -d --no-deps esp32-bridge >/dev/null
else
docker run -d --pull=never --user "$runtime_user" --name "$backend" --label voice-system-qa=temporary --network "$network" --network-alias voice-system-qa-bridge \
  --mount type=bind,src="$work/bridge",dst=/fixture/bridge,readonly --mount type=bind,src="$work/keys",dst=/fixture/keys,readonly --mount type=bind,src="$work/data",dst=/fixture/data \
  -e LISTEN_ADDR=:8090 -e API_KEY=synthetic-device-api-key-only -e ADMIN_ENABLED=true -e ADMIN_BASE_URL=https://voice-system-qa-proxy:8443 -e ADMIN_STATE_DIR=/fixture/data/settings -e ADMIN_PASSWORD_HASH_FILE=/fixture/keys/admin.hash \
  -e VOICE_PAIRING_ENABLED=true -e VOICE_PAIRING_HTTPS_ORIGIN=https://voice-system-qa-proxy:8443 -e VOICE_PAIRING_STATE_DIR=/fixture/data/pair -e VOICE_PAIRING_MASTER_FILE=/fixture/keys/master.key -e VOICE_PAIRING_MASTER_ID=synthetic-master -e VOICE_PAIRING_INTEGRITY_FILE=/fixture/keys/integrity.key -e VOICE_PAIRING_INTEGRITY_ID=synthetic-integrity -e VOICE_PAIRING_SIGNING_FILE=/fixture/keys/signing.pem -e VOICE_PAIRING_TLS_CERT_FILE=/fixture/keys/backend.crt -e VOICE_PAIRING_TLS_KEY_FILE=/fixture/keys/backend.key -e VOICE_PUSH_PUBLISHER_TOKEN=synthetic-publisher-token-only-12345 \
  --entrypoint /fixture/bridge "$image" >/dev/null
backend_created=true
fi
docker run -d --pull=never --user "$runtime_user" --name "$proxy" --label voice-system-qa=temporary --network "$network" --network-alias voice-system-qa-proxy \
  --mount type=bind,src="$work/dynamic",dst=/safety-dynamic,readonly --mount type=bind,src="$work/frontend.crt",dst=/fixture/frontend.crt,readonly --mount type=bind,src="$work/frontend.key",dst=/fixture/frontend.key,readonly \
  "$image" --entrypoints.websecure.address=:8443 --providers.file.directory=/safety-dynamic --providers.file.watch=false --log.level=ERROR >/dev/null
proxy_created=true
run_test() {
  mode=$1
  case "$mode" in system) test_name=TestTraefikBridgeSystem;; *) test_name=TestTraefikBridgeRollback;; esac
  docker run --rm --pull=never --user "$runtime_user" --label voice-system-qa=temporary --network "$network" --mount type=bind,src="$work/test",dst=/fixture/test,readonly --mount type=bind,src="$work/frontend-ca.crt",dst=/fixture/ca.crt,readonly --mount type=bind,src="$work/results",dst=/fixture/results \
    -e VOICE_PROXY_TEST_ORIGIN=https://voice-system-qa-proxy:8443 -e VOICE_PROXY_TEST_CA=/fixture/ca.crt -e VOICE_PROXY_TEST_MODE="$mode" -e VOICE_PROXY_TEST_MIGRATION="$migration" -e VOICE_PROXY_TEST_TOKEN_FILE=/fixture/results/token \
    --entrypoint /fixture/test "$image" -test.run "^$test_name$" -test.v -test.count=1 -test.timeout=45s
}
switch_bridge() {
  phase=$1
  docker compose -p "$project" -f "$work/$phase.compose.json" up -d --no-deps esp32-bridge >/dev/null
  python3 - "$work" "$phase" <<'PYROUTE'
import pathlib,sys
work=pathlib.Path(sys.argv[1]); text=(work/'routes-tls.yml').read_text()
if sys.argv[2]=='rollback': text=text.replace('https://voice-system-qa-bridge:8090','http://voice-system-qa-bridge:8090')
(work/'dynamic/routes.yml').write_text(text)
PYROUTE
  docker restart "$proxy" >/dev/null
}
migration=false
if [ -n "$main_compose" ]; then
  migration=true
  cp "$work/dynamic/routes.yml" "$work/routes-tls.yml"
  run_test preack
  switch_bridge rollback
  run_test rollback
  switch_bridge main
fi
run_test system
if [ -n "$main_compose" ]; then
  pair_hash=$(shasum -a 256 "$work/data/voice-pairing/voicepair.json")
  switch_bridge rollback
  run_test rollback
  test "$pair_hash" = "$(shasum -a 256 "$work/data/voice-pairing/voicepair.json")"
  switch_bridge main
  run_test restored
  test "$pair_hash" = "$(shasum -a 256 "$work/data/voice-pairing/voicepair.json")"
  printf '%s\n' 'PASS rollback before/after ACK and return: pairing state preserved, revoked/legacy access not restored'
fi
