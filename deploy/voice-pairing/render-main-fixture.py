"""Render a disposable single-file Compose from the reviewed main Compose JSON.
No production env files, credentials, mounts, networks or exposed ports are used.
"""
import copy
import json
import os
import pathlib
import sys
from urllib.parse import urlsplit

source, work, network, image, old_image = sys.argv[1:]
work = pathlib.Path(work)
original = json.load(open(source))["services"]["esp32-bridge"]
pair = {k: v for k, v in original["environment"].items() if k.startswith("VOICE_PAIRING_")}
assert len(pair) == 11 and pair["VOICE_PAIRING_ENABLED"] == "true"
assert pair["VOICE_PAIRING_LEGACY_MIGRATION"] == "true"
origin = urlsplit(pair["VOICE_PAIRING_HTTPS_ORIGIN"])
admin = urlsplit(original["environment"]["ADMIN_BASE_URL"])
assert origin.scheme == admin.scheme == "https" and origin.hostname
assert origin.hostname == admin.hostname and origin.port == (admin.port or 443)
assert not origin.username and not origin.password and not origin.path and not origin.query and not origin.fragment
labels = original["labels"]
assert labels["traefik.http.services.esp32-bridge.loadbalancer.server.scheme"] == "https"
assert labels["traefik.http.services.esp32-bridge.loadbalancer.serverstransport"] == "homedash-voice-backend@file"
assert any(v["target"] == "/run/voice-pairing" and v.get("read_only") and not v["bind"]["create_host_path"] for v in original["volumes"])
assert set(original["networks"]) == {"traefik", "infra"}
pair["VOICE_PAIRING_HTTPS_ORIGIN"] = "https://voice-system-qa-proxy:8443"
env = dict(pair, API_KEY="synthetic-device-api-key-only", LISTEN_ADDR=original["environment"]["LISTEN_ADDR"], ADMIN_ENABLED="true", ADMIN_BASE_URL="https://voice-system-qa-proxy:8443", ADMIN_STATE_DIR=original["environment"]["ADMIN_STATE_DIR"], ADMIN_PASSWORD_HASH_FILE=original["environment"]["ADMIN_PASSWORD_HASH_FILE"], VOICE_PUSH_PUBLISHER_TOKEN="synthetic-publisher-token-only-12345", VOICE_PUSH_SUBSCRIBER_TOKEN="synthetic-legacy-subscriber-only-12345", VOICE_PUSH_TARGET_ID="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
vols = [{"type":"bind","source":str(work/"keys"),"target":"/run/voice-pairing","read_only":True}, {"type":"bind","source":str(work/"keys/admin.hash"),"target":env["ADMIN_PASSWORD_HASH_FILE"],"read_only":True}, {"type":"bind","source":str(work/"data"),"target":"/data"}, {"type":"bind","source":str(work/"bridge"),"target":"/fixture/bridge","read_only":True}]
service = {"image":image,"user":f"{os.getuid()}:{os.getgid()}","pull_policy":"never","entrypoint":["/fixture/bridge"],"environment":env,"volumes":vols,"labels":{"traefik.enable":"false"},"networks":{"qa":{"aliases":["voice-system-qa-bridge"]}}}
def emit(name,s):
 (work/name).write_text(json.dumps({"services":{"esp32-bridge":s},"networks":{"qa":{"external":True,"name":network}}}))
emit("main.compose.json",service)
rollback=copy.deepcopy(service)
rollback.update(image=old_image,platform="linux/amd64",entrypoint=["/app/server"])
rollback["volumes"]=[v for v in rollback["volumes"] if v["target"] not in ("/run/voice-pairing","/fixture/bridge")]
rollback["environment"]={k:v for k,v in rollback["environment"].items() if not k.startswith("VOICE_PAIRING_")}
for k in ("VOICE_PUSH_PUBLISHER_TOKEN","VOICE_PUSH_SUBSCRIBER_TOKEN","VOICE_PUSH_TARGET_ID"): rollback["environment"][k]=""
emit("rollback.compose.json",rollback)
print("PASS main Compose assertions; isolated main/rollback fixtures rendered")
