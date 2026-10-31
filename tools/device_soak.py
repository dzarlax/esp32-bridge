#!/usr/bin/env python3
"""Bounded, read-only observer for one HomeDash display."""

from __future__ import annotations

import argparse
import hashlib
import http.client
import json
import math
import os
import re
import signal
import stat
import sys
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Callable

MAX_TIMEOUT = 10.0
RENEW_AFTER = 6 * 60 * 60
SAFE_VERSION = re.compile(r"^v?\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.-]+)?$")
DEVICE_ID = re.compile(r"^[A-Fa-f0-9]{32}$")
PHASES = {"unknown", "error", "reference", "preview", "restored"}
MAX_DURATION = 7 * 24 * 60 * 60


class SafeFailure(Exception):
    def __init__(self, code: str):
        super().__init__(code)
        self.code = code


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


class UrllibTransport:
    """Only issues the exact GET settings and POST login requests."""

    def __init__(self) -> None:
        self.opener = urllib.request.build_opener(NoRedirect)

    def request(self, method: str, url: str, headers: dict[str, str], body: bytes | None,
                timeout: float) -> tuple[int, dict[str, str], bytes]:
        if not ((method == "POST" and url.endswith("/api/admin/v1/login")) or
                (method == "GET" and "/api/admin/v1/devices/" in url and url.endswith("/settings"))):
            raise SafeFailure("request_boundary")
        req = urllib.request.Request(url, data=body, headers=headers, method=method)
        try:
            with self.opener.open(req, timeout=min(MAX_TIMEOUT, max(0.001, timeout))) as response:
                return response.status, dict(response.headers.items()), response.read(1024 * 1024 + 1)
        except urllib.error.HTTPError as exc:
            return exc.code, dict(exc.headers.items()), exc.read(1024 * 1024 + 1)
        except (urllib.error.URLError, TimeoutError, OSError, http.client.HTTPException):
            raise SafeFailure("transport_unavailable") from None


def _valid_url(value: str, *, allow_loopback_http: bool) -> urllib.parse.SplitResult:
    try:
        p = urllib.parse.urlsplit(value)
        port = p.port
    except ValueError:
        raise SafeFailure("invalid_configuration") from None
    if (p.scheme == "https" and p.hostname and not p.username and not p.password and
            p.path in {"", "/"} and not p.query and not p.fragment):
        return p
    if (allow_loopback_http and p.scheme == "http" and p.hostname in {"127.0.0.1", "::1", "localhost"}
            and not p.username and not p.password and p.path in {"", "/"} and not p.query and not p.fragment):
        return p
    raise SafeFailure("invalid_configuration")


def _safe_int(obj: dict[str, Any], key: str, minimum: int = 0, maximum: int = 2**53) -> int | None:
    val = obj.get(key)
    return val if isinstance(val, int) and not isinstance(val, bool) and minimum <= val <= maximum else None


def _parse_timestamp(value: Any) -> datetime | None:
    if not isinstance(value, str):
        return None
    try:
        parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
        return parsed.astimezone(timezone.utc) if parsed.tzinfo else None
    except ValueError:
        return None


def project_response(payload: Any, observed_at: str, heartbeat_max_age: float = 120) -> dict[str, Any]:
    """Project only explicitly permitted fields; reject malformed essentials."""
    if not isinstance(payload, dict) or not isinstance(payload.get("device"), dict):
        raise SafeFailure("invalid_response")
    device = payload["device"]
    revision = _safe_int(payload, "revision", 1)
    applied = _safe_int(device, "applied_revision", 0)
    connected = device.get("connected")
    uptime = _safe_int(device, "uptime_seconds", 1)
    if revision is None or applied is None or uptime is None or not isinstance(connected, bool):
        raise SafeFailure("invalid_response")
    firmware = device.get("firmware")
    boot_id = device.get("boot_id")
    if not isinstance(firmware, str) or not SAFE_VERSION.fullmatch(firmware):
        raise SafeFailure("invalid_response")
    if not isinstance(boot_id, str) or not boot_id or len(boot_id) > 512:
        raise SafeFailure("invalid_response")
    observed_dt, last_seen = _parse_timestamp(observed_at), _parse_timestamp(device.get("last_seen"))
    if observed_dt is None or last_seen is None:
        raise SafeFailure("invalid_response")
    heartbeat_age = (observed_dt - last_seen).total_seconds()
    if heartbeat_age < -5:
        raise SafeFailure("invalid_response")
    if connected and heartbeat_age > heartbeat_max_age:
        raise SafeFailure("stale_snapshot")
    out: dict[str, Any] = {
        "observed_at": observed_at,
        "device_last_seen": last_seen.isoformat(),
        "state": "ok" if connected else "device_offline",
        "firmware": firmware,
        "boot_id_sha256": hashlib.sha256(boot_id.encode()).hexdigest(),
        "connected": connected,
        "uptime_seconds": uptime,
        "revision": revision,
        "applied_revision": applied,
    }
    report = device.get("backlight_report")
    if isinstance(report, dict):
        phase, known, cmd = report.get("phase"), report.get("output_known"), report.get("last_command")
        sample_ms = _safe_int(report, "sample_uptime_ms")
        cmd_valid = cmd is None or (isinstance(cmd, int) and not isinstance(cmd, bool) and 0 <= cmd <= 97)
        fresh = sample_ms is not None and -2000 <= (uptime * 1000 - sample_ms) <= 120_000
        if isinstance(phase, str) and phase in PHASES and isinstance(known, bool) and cmd_valid and fresh:
            out["backlight_report"] = {"phase": phase, "output_known": known, "last_command": cmd}
            brightness = device.get("backlight_percent")
            if known is True and isinstance(brightness, int) and not isinstance(brightness, bool) and 0 <= brightness <= 100:
                out["reported_brightness_percent"] = brightness
    return out


class Observer:
    def __init__(self, *, base_url: str, admin_origin: str, device_id: str, password: str,
                 duration: float = 86400, interval: float = 60, transport: Any | None = None,
                 monotonic: Callable[[], float] = time.monotonic,
                 wallclock: Callable[[], datetime] = lambda: datetime.now(timezone.utc),
                 sleeper: Callable[[float], None] = time.sleep,
                 call_waiter: Callable[[threading.Event, float], bool] | None = None,
                 sample_sink: Callable[[dict[str, Any]], None] | None = None):
        if (not math.isfinite(duration) or not math.isfinite(interval) or duration <= 0 or
                duration > MAX_DURATION or interval < 1 or interval > 3600 or interval > duration or
                not DEVICE_ID.fullmatch(device_id)):
            raise SafeFailure("invalid_configuration")
        base, origin = _valid_url(base_url.rstrip("/"), allow_loopback_http=True), _valid_url(admin_origin.rstrip("/"), allow_loopback_http=False)
        if origin.scheme != "https" or not origin.hostname or origin.path not in {"", "/"} or origin.query or origin.fragment:
            raise SafeFailure("invalid_configuration")
        if base.scheme != "http" and (base.hostname != origin.hostname or (base.port or 443) != (origin.port or 443)):
            raise SafeFailure("invalid_configuration")
        self.base_url, self.admin_origin, self.admin_host = base_url.rstrip("/"), admin_origin.rstrip("/"), origin.netloc
        self.device_id, self.password = device_id, password
        self.duration, self.interval, self.transport = duration, interval, transport or UrllibTransport()
        self.monotonic, self.wallclock, self.sleeper = monotonic, wallclock, sleeper
        self.call_waiter = call_waiter or (lambda event, timeout: event.wait(timeout))
        self.sample_sink = sample_sink
        self.cookie = self.csrf = None
        self.auth_stopped = False
        self.login_at = 0.0
        self.baseline: tuple[str, str, int] | None = None
        self.gaps = 0
        self.pending_revision_samples = 0
        self.failure: str | None = None
        self.samples: list[dict[str, Any]] = []
        self.started = monotonic()
        self.deadline = self.started + duration
        self.last_sample_mono = self.started
        self.last_sample_wall = wallclock().astimezone(timezone.utc)
        self.last_seen: datetime | None = None
        self.last_seen_advance_mono = self.started
        self.hung_call = False
        self.pending_acceptance = {"physical_restart": "pending", "night_mode_visual": "pending", "brightness_visual": "pending"}

    def _request(self, method: str, path: str, body: bytes | None = None, authenticated: bool = False):
        remaining = self.deadline - self.monotonic()
        if remaining <= 0:
            raise SafeFailure("deadline")
        headers = {"Host": self.admin_host, "Origin": self.admin_origin, "X-Forwarded-Proto": "https", "Accept": "application/json"}
        if body is not None:
            headers["Content-Type"] = "application/json"
        if authenticated:
            if not self.cookie:
                raise SafeFailure("auth_unavailable")
            headers["Cookie"] = self.cookie
        timeout = min(MAX_TIMEOUT, remaining)
        done = threading.Event()
        result: list[Any] = []

        def invoke() -> None:
            try:
                result.append((True, self.transport.request(method, self.base_url + path, headers, body, timeout)))
            except BaseException:
                result.append((False, None))
            finally:
                done.set()

        threading.Thread(target=invoke, name="device-soak-http", daemon=True).start()
        if not self.call_waiter(done, timeout):
            self.hung_call = True
            self.failure = "request_timeout"
            raise SafeFailure("transport_unavailable")
        if not result or not result[0][0]:
            raise SafeFailure("transport_unavailable")
        return result[0][1]

    def _login(self) -> None:
        if self.auth_stopped:
            raise SafeFailure("auth_unavailable")
        status, headers, body = self._request("POST", "/api/admin/v1/login", json.dumps({"password": self.password}).encode())
        if status != 200:
            self.cookie = self.csrf = None
            self.auth_stopped = True
            raise SafeFailure("auth_unavailable")
        try:
            data = json.loads(body)
            if not isinstance(data, dict):
                raise ValueError()
            set_cookie = next(v for k, v in headers.items() if k.lower() == "set-cookie")
            match = re.search(r"(?:^|,\s*)bridge_admin_session=([^;,\s]+)", set_cookie)
            secure = re.search(r"(?:^|;)\s*Secure(?:;|$)", set_cookie, re.IGNORECASE)
            csrf = data.get("csrf_token")
            if not data.get("authenticated") or not match or not secure or not isinstance(csrf, str) or not csrf:
                raise ValueError()
        except (ValueError, TypeError, AttributeError, StopIteration, json.JSONDecodeError):
            self.cookie = self.csrf = None
            self.auth_stopped = True
            raise SafeFailure("auth_unavailable") from None
        self.cookie, self.csrf, self.login_at = "bridge_admin_session=" + match.group(1), csrf, self.monotonic()

    def _settings(self) -> dict[str, Any]:
        if self.cookie is None or self.monotonic() - self.login_at >= RENEW_AFTER:
            self._login()
        path = f"/api/admin/v1/devices/{self.device_id}/settings"
        status, headers, body = self._request("GET", path, authenticated=True)
        if status == 401:
            self._login()  # Exactly one renewal and one retry.
            status, headers, body = self._request("GET", path, authenticated=True)
        if status == 401:
            self.cookie = None
            self.auth_stopped = True
            raise SafeFailure("auth_unavailable")
        if status < 200 or status >= 300 or len(body) > 1024 * 1024:
            raise SafeFailure("transport_unavailable")
        try:
            return project_response(json.loads(body), self.wallclock().astimezone(timezone.utc).isoformat(),
                                    heartbeat_max_age=max(120, 2 * self.interval))
        except (json.JSONDecodeError, UnicodeDecodeError):
            raise SafeFailure("invalid_response") from None

    def _observe(self) -> dict[str, Any]:
        now = self.monotonic()
        wall_now = self.wallclock().astimezone(timezone.utc)
        try:
            row = self._settings()
            self.gaps = 0
            if not row["connected"]:
                self.failure = "device_offline"
            elif row["applied_revision"] != row["revision"]:
                # A brief startup grace is allowed, but persistent mismatch is failure.
                self.pending_revision_samples += 1
                if self.baseline is not None or self.pending_revision_samples >= 3:
                    self.failure = "revision_not_applied"
            else:
                self.pending_revision_samples = 0
                identity = (row["firmware"], row["boot_id_sha256"], row["revision"])
                if self.baseline is None:
                    self.baseline = identity
                elif identity != self.baseline:
                    self.failure = "baseline_changed"
            if self.failure and row["state"] not in {"device_offline", "auth_unavailable", "transport_unavailable", "invalid_response"}:
                row["state"] = "failed"
            if self.failure:
                row["failure"] = self.failure
        except SafeFailure as exc:
            if exc.code == "stale_snapshot":
                state = "invalid_response"
            else:
                state = exc.code if exc.code in {"auth_unavailable", "transport_unavailable", "invalid_response"} else "transport_unavailable"
            row = {"observed_at": wall_now.isoformat(), "state": state}
            self.gaps += 1
            if self.hung_call:
                row["failure"] = "request_timeout"
            if state == "auth_unavailable":
                self.failure = "auth_unavailable"
            if self.gaps > 2 and self.failure is None:
                self.failure = "observation_gaps"
        gap_seconds = now - self.last_sample_mono
        wall_gap = (wall_now - self.last_sample_wall).total_seconds()
        row["sample_gap_seconds"] = round(max(0.0, gap_seconds), 3)
        if wall_gap < -5:
            row["state"], row["failure"] = "invalid_response", "clock_skew"
            self.failure = self.failure or "clock_skew"
        elif wall_gap - gap_seconds > max(5, self.interval * 0.1):
            row["state"], row["failure"] = "invalid_response", "clock_or_sleep_gap"
            self.failure = self.failure or "clock_or_sleep_gap"
        if "device_last_seen" in row:
            seen = _parse_timestamp(row["device_last_seen"])
            if seen is not None:
                if self.last_seen is not None and seen <= self.last_seen and now - self.last_seen_advance_mono > max(120, 2 * self.interval):
                    row["state"], row["failure"] = "transport_unavailable", "stale_snapshot"
                    self.failure = self.failure or "stale_snapshot"
                elif self.last_seen is None or seen > self.last_seen:
                    self.last_seen, self.last_seen_advance_mono = seen, now
        self.last_sample_mono = now
        self.last_sample_wall = wall_now
        missed = max(0, int(gap_seconds // self.interval) - 1)
        if missed > 2 and self.failure is None:
            row["state"] = "invalid_response"
            row["failure"] = "observation_gaps"
            self.failure = "observation_gaps"
        if self.failure and "failure" not in row:
            if row["state"] not in {"device_offline", "auth_unavailable", "transport_unavailable", "invalid_response"}:
                row["state"] = "failed"
            row["failure"] = self.failure
        self.samples.append(row)
        if self.sample_sink is not None:
            self.sample_sink(row)
        return row

    def run(self, stop: Callable[[], bool] = lambda: False) -> dict[str, Any]:
        interrupted = False
        while self.monotonic() < self.deadline and not stop():
            self._observe()
            if self.hung_call or self.failure == "auth_unavailable":
                break
            remaining = self.deadline - self.monotonic()
            if remaining <= 0 or stop():
                break
            self.sleeper(min(self.interval, remaining))
        if stop():
            interrupted = True
        if not interrupted and self.samples:
            mono_gap = self.monotonic() - self.last_sample_mono
            wall_gap = (self.wallclock().astimezone(timezone.utc) - self.last_sample_wall).total_seconds()
            if wall_gap < -5:
                self.failure = self.failure or "clock_skew"
            elif wall_gap - mono_gap > max(5, self.interval * 0.1):
                self.failure = self.failure or "clock_or_sleep_gap"
            elif max(0, int(mono_gap // self.interval) - 1) > 2:
                self.failure = self.failure or "observation_gaps"
        elapsed = max(0.0, min(self.duration, self.monotonic() - self.started))
        if interrupted:
            outcome = "interrupted"
        elif self.failure in {"observation_gaps", "stale_snapshot", "request_timeout", "clock_skew", "clock_or_sleep_gap"}:
            outcome = "inconclusive"
        elif self.failure or self.gaps > 2:
            outcome = "failed"
        elif elapsed + 0.01 < self.duration or self.baseline is None:
            outcome = "inconclusive"
        else:
            outcome = "observation_passed"
        return {"outcome": outcome, "duration_seconds": round(elapsed, 3), "samples": len(self.samples),
                "consecutive_missing": self.gaps, "baseline_established": self.baseline is not None,
                "failure_code": self.failure, "acceptance_checks": self.pending_acceptance}


def _password_from_file(path: Path) -> str:
    try:
        st = path.stat()
        if not stat.S_ISREG(st.st_mode) or st.st_mode & 0o077:
            raise SafeFailure("password_file_permissions")
        value = path.read_text(encoding="utf-8").rstrip("\r\n")
        if not value or "\x00" in value:
            raise SafeFailure("password_file_invalid")
        return value
    except SafeFailure:
        raise
    except (OSError, UnicodeError):
        raise SafeFailure("password_file_unavailable") from None


def _new_run_dir(root: Path) -> Path:
    root.mkdir(parents=True, exist_ok=True, mode=0o700)
    for _ in range(20):
        day_dir = root / datetime.now(timezone.utc).strftime("%Y%m%d")
        day_dir.mkdir(mode=0o700, exist_ok=True)
        candidate = day_dir / (datetime.now(timezone.utc).strftime("%H%M%S") + "-" + os.urandom(4).hex())
        try:
            candidate.mkdir(parents=True, mode=0o700)
            return candidate
        except FileExistsError:
            continue
    raise SafeFailure("output_unavailable")


def _open_private_file(path: Path):
    try:
        fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        return os.fdopen(fd, "w", encoding="utf-8")
    except OSError:
        raise SafeFailure("output_unavailable") from None


def _replace_summary(stream, summary: dict[str, Any]) -> None:
    stream.seek(0)
    stream.truncate()
    json.dump(summary, stream, indent=2)
    stream.write("\n")
    stream.flush()
    os.fsync(stream.fileno())


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base-url", required=True, help="Bridge URL; loopback HTTP requires an externally managed tunnel")
    parser.add_argument("--admin-origin", required=True, help="Canonical HTTPS Bridge origin")
    parser.add_argument("--device-id", required=True)
    parser.add_argument("--password-file", required=True, type=Path)
    parser.add_argument("--output-dir", required=True, type=Path)
    parser.add_argument("--duration-seconds", type=float, default=86400)
    parser.add_argument("--interval-seconds", type=float, default=60)
    args = parser.parse_args(argv)
    run_dir: Path | None = None
    journal = None
    summary_stream = None
    observer: Observer | None = None
    try:
        password = _password_from_file(args.password_file)
        observer = Observer(base_url=args.base_url, admin_origin=args.admin_origin, device_id=args.device_id,
                            password=password, duration=args.duration_seconds, interval=args.interval_seconds)
        run_dir = _new_run_dir(args.output_dir)
        journal = _open_private_file(run_dir / "samples.jsonl")
        summary_stream = _open_private_file(run_dir / "summary.json")
        _replace_summary(summary_stream, {"outcome": "incomplete", "incomplete": True, "samples": 0,
                                          "failure_code": None, "samples_file": "samples.jsonl"})
        def write_sample(item: dict[str, Any]) -> None:
            journal.write(json.dumps(item, separators=(",", ":")) + "\n")
            journal.flush()
            os.fsync(journal.fileno())

        observer.sample_sink = write_sample
        stop_state = {"stop": False}
        signal.signal(signal.SIGINT, lambda *_: stop_state.__setitem__("stop", True))
        summary = observer.run(lambda: stop_state["stop"])
        summary["incomplete"] = summary["outcome"] == "interrupted" or summary["duration_seconds"] < args.duration_seconds
        summary["run_id"] = run_dir.parent.name + "-" + run_dir.name
        summary["samples_file"] = "samples.jsonl"
        journal.flush()
        os.fsync(journal.fileno())
        journal.close()
        journal = None
        _replace_summary(summary_stream, summary)
        summary_stream.close()
        summary_stream = None
        print(json.dumps(summary, separators=(",", ":")))
        return 0 if summary["outcome"] == "observation_passed" else 1
    except SafeFailure as exc:
        if journal:
            journal.close()
        if summary_stream:
            try:
                _replace_summary(summary_stream, {"outcome": "failed", "failure_code": exc.code, "incomplete": True,
                                                  "samples": len(observer.samples) if observer else 0,
                                                  "samples_file": "samples.jsonl"})
            except OSError:
                pass
            summary_stream.close()
        print(json.dumps({"outcome": "failed", "failure_code": exc.code}), file=sys.stderr)
        return 2
    except Exception:
        if journal:
            journal.close()
        if summary_stream:
            try:
                _replace_summary(summary_stream, {"outcome": "failed", "failure_code": "runner_error", "incomplete": True,
                                                  "samples": len(observer.samples) if observer else 0,
                                                  "samples_file": "samples.jsonl"})
            except OSError:
                pass
            summary_stream.close()
        print(json.dumps({"outcome": "failed", "failure_code": "runner_error"}), file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
