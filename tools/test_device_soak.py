import json
import os
import stat
import tempfile
import threading
import unittest
from datetime import datetime, timezone
from pathlib import Path

from device_soak import Observer, SafeFailure, _open_private_file, project_response


def envelope(**device_overrides):
    device = {"connected": True, "firmware": "1.2.3", "boot_id": "boot-secret", "uptime_seconds": 120,
              "applied_revision": 7, "backlight_percent": 44,
              "last_seen": "2027-01-15T08:00:00+00:00",
              "backlight_report": {"phase": "reference", "output_known": True, "last_command": 44,
                                   "sample_uptime_ms": 119000},
              "ssid": "SSID_SENTINEL", "ip": "IP_SENTINEL", "error": "ERR_SENTINEL"}
    device.update(device_overrides)
    return {"revision": 7, "settings": {"screen": {"night_backlight_percent": 9, "photo": "PHOTO_SENTINEL"}},
            "device": device, "tokens": "TOKEN_SENTINEL", "calendar": "EVENT_SENTINEL"}


class FakeClock:
    def __init__(self):
        self.now = 0.0
        self.wall_extra = 0.0
    def mono(self):
        return self.now
    def sleep(self, duration):
        self.now += duration
    def wall(self):
        return datetime.fromtimestamp(1_800_000_000 + self.now + self.wall_extra, timezone.utc)


class FakeTransport:
    def __init__(self, responses=None, wallclock=None, freeze_heartbeat=False, hang_event=None):
        self.responses = list(responses or [])
        self.calls = []
        self.wallclock = wallclock or (lambda: datetime.fromtimestamp(1_800_000_000, timezone.utc))
        self.freeze_heartbeat = freeze_heartbeat
        self.hang_event = hang_event
        self.entered = threading.Event()
    def request(self, method, url, headers, body, timeout):
        self.calls.append((method, url, headers, body, timeout))
        if self.hang_event is not None:
            self.entered.set()
            self.hang_event.wait()
        if self.responses:
            result = self.responses.pop(0)
            if isinstance(result, Exception):
                raise result
            return result
        if method == "POST":
            return 200, {"Set-Cookie": "bridge_admin_session=COOKIE_SENTINEL; Secure; HttpOnly"}, json.dumps({"authenticated": True, "csrf_token": "CSRF_SENTINEL"}).encode()
        data = envelope()
        if not self.freeze_heartbeat:
            data["device"]["last_seen"] = self.wallclock().isoformat()
        return 200, {}, json.dumps(data).encode()


def observer(clock=None, transport=None, **kw):
    clock = clock or FakeClock()
    transport = transport or FakeTransport(wallclock=clock.wall)
    return Observer(base_url="http://127.0.0.1:9000", admin_origin="https://bridge.example.test",
                    device_id="a" * 32, password="PASSWORD_SENTINEL", duration=kw.pop("duration", 120),
                    interval=kw.pop("interval", 60), transport=transport, monotonic=clock.mono,
                    wallclock=clock.wall, sleeper=clock.sleep, **kw)


class DeviceSoakTests(unittest.TestCase):
    def test_deadline_bounded_and_pass_does_not_claim_physical_acceptance(self):
        clock = FakeClock()
        run = observer(clock).run()
        self.assertEqual(run["outcome"], "observation_passed")
        self.assertEqual(run["duration_seconds"], 120)
        self.assertEqual(run["samples"], 2)
        self.assertTrue(all(v == "pending" for v in run["acceptance_checks"].values()))

    def test_more_than_two_missing_observations_fails(self):
        login = (200, {"Set-Cookie": "bridge_admin_session=x; Secure"}, b'{"authenticated":true,"csrf_token":"x"}')
        transport = FakeTransport([login] + [(503, {}, b"PRIVATE BODY")] * 4)
        run = observer(transport=transport, duration=240).run()
        self.assertEqual(run["outcome"], "inconclusive")
        self.assertEqual(run["failure_code"], "observation_gaps")
        self.assertNotIn("PRIVATE", json.dumps(run))

    def test_transport_timeout_stays_safe_and_is_a_gap(self):
        transport = FakeTransport([SafeFailure("transport_unavailable")])
        run = observer(transport=transport, duration=60).run()
        self.assertEqual(run["outcome"], "inconclusive")
        self.assertEqual(run["consecutive_missing"], 1)

    def test_unauthorized_get_has_exactly_one_login_refresh_and_retry(self):
        transport = FakeTransport([
            (200, {"Set-Cookie": "bridge_admin_session=one; Secure"}, b'{"authenticated":true,"csrf_token":"c1"}'),
            (401, {}, b"secret"),
            (200, {"Set-Cookie": "bridge_admin_session=two; Secure"}, b'{"authenticated":true,"csrf_token":"c2"}'),
            (200, {}, json.dumps(envelope()).encode()),
        ])
        o = observer(transport=transport)
        row = o._observe()
        self.assertEqual(row["state"], "ok")
        self.assertEqual([c[0] for c in transport.calls], ["POST", "GET", "POST", "GET"])
        self.assertEqual(transport.calls[-1][2]["Cookie"], "bridge_admin_session=two")
        self.assertEqual(transport.calls[0][2]["Host"], "bridge.example.test")
        self.assertEqual(transport.calls[0][2]["Origin"], "https://bridge.example.test")
        self.assertEqual(transport.calls[0][2]["X-Forwarded-Proto"], "https")
        self.assertTrue(all(c[4] <= 10 for c in transport.calls))

    def test_failed_login_is_not_retried_and_raw_error_is_hidden(self):
        transport = FakeTransport([(403, {}, b"PASSWORD_SENTINEL rejected")])
        run = observer(transport=transport, duration=60).run()
        self.assertEqual(run["outcome"], "failed")
        self.assertEqual(len(transport.calls), 1)
        self.assertNotIn("PASSWORD_SENTINEL", json.dumps(run))

    def test_boot_firmware_and_revision_changes_fail_without_resetting_baseline(self):
        for changes in ({"boot_id": "new-boot"}, {"firmware": "1.2.4"}, {"revision": 8, "applied_revision": 8}):
            with self.subTest(changes=changes):
                transport = FakeTransport()
                o = observer(transport=transport)
                o._observe()
                changed = envelope(**{k: v for k, v in changes.items() if k != "revision"})
                if "revision" in changes:
                    changed["revision"] = changes["revision"]
                transport.responses = [(200, {}, json.dumps(changed).encode())]
                second = o._observe()
                self.assertEqual(second["failure"], "baseline_changed")
                self.assertEqual(o.baseline, ("1.2.3", project_response(envelope(), "2027-01-15T08:00:00+00:00")["boot_id_sha256"], 7))

    def test_persistently_unapplied_startup_revision_does_not_pass(self):
        login = (200, {"Set-Cookie": "bridge_admin_session=x; Secure"}, b'{"authenticated":true,"csrf_token":"x"}')
        pending = envelope(applied_revision=6)
        transport = FakeTransport([login] + [(200, {}, json.dumps(pending).encode())] * 3)
        run = observer(transport=transport, duration=180).run()
        self.assertEqual(run["outcome"], "failed")
        self.assertEqual(run["failure_code"], "revision_not_applied")

    def test_wallclock_pause_gap_cannot_pass(self):
        clock = FakeClock()
        transport = FakeTransport()
        # Model the process waking after its final scheduled poll.
        def wall_only_suspend(_):
            clock.now += 60
            clock.wall_extra += 121
        clock.sleep = wall_only_suspend
        o = observer(clock, transport, duration=60)
        run = o.run()
        self.assertEqual(run["outcome"], "inconclusive")
        self.assertEqual(run["failure_code"], "clock_or_sleep_gap")

    def test_ninety_second_jitter_is_not_more_than_two_missed_samples(self):
        clock = FakeClock()
        transport = FakeTransport(wallclock=clock.wall)
        o = observer(clock, transport, duration=180)
        o._observe()
        clock.now += 90
        clock.wall_extra += 90
        row = o._observe()
        self.assertNotEqual(row.get("failure"), "observation_gaps")

    def test_invalid_schema_is_counted_as_gap(self):
        login = (200, {"Set-Cookie": "bridge_admin_session=x; Secure"}, b'{"authenticated":true,"csrf_token":"x"}')
        transport = FakeTransport([login, (200, {}, b'{"settings":[]}')])
        row = observer(transport=transport)._observe()
        self.assertEqual(row["state"], "invalid_response")

    def test_strict_projection_redacts_unwanted_fields_and_hashes_boot_id(self):
        row = project_response(envelope(), "2027-01-15T08:00:00+00:00")
        serialized = json.dumps(row)
        for secret in ("boot-secret", "SSID_SENTINEL", "IP_SENTINEL", "ERR_SENTINEL", "PHOTO_SENTINEL",
                       "TOKEN_SENTINEL", "EVENT_SENTINEL", "night_backlight_percent"):
            self.assertNotIn(secret, serialized)
        self.assertEqual(len(row["boot_id_sha256"]), 64)
        self.assertEqual(row["reported_brightness_percent"], 44)

    def test_brightness_unknown_and_stale_report_are_omitted(self):
        data = envelope()
        data["device"]["backlight_report"]["sample_uptime_ms"] = 900_000
        data["device"]["backlight_report"]["output_known"] = False
        row = project_response(data, "2027-01-15T08:00:00+00:00")
        self.assertNotIn("backlight_report", row)
        self.assertNotIn("reported_brightness_percent", row)
        data["device"].pop("backlight_percent")
        self.assertNotIn("reported_brightness_percent", project_response(data, "2027-01-15T08:00:00+00:00"))

    def test_http_non_loopback_is_rejected(self):
        with self.assertRaises(SafeFailure):
            Observer(base_url="http://bridge.example.test", admin_origin="https://bridge.example.test",
                     device_id="a" * 32, password="x")

    def test_actual_report_schema_preserves_null_unknown_and_numeric_pwm(self):
        data = envelope()
        report = data["device"]["backlight_report"]
        report.update(phase="preview", output_known=None, last_command=None)
        row = project_response(data, "2027-01-15T08:00:00+00:00")
        self.assertNotIn("backlight_report", row)  # output_known is required to be a bool
        self.assertNotIn("reported_brightness_percent", row)
        report["output_known"] = False
        self.assertEqual(project_response(data, "2027-01-15T08:00:00+00:00")["backlight_report"],
                         {"phase": "preview", "output_known": False, "last_command": None})
        report.update(output_known=True, last_command=97)
        self.assertEqual(project_response(data, "2027-01-15T08:00:00+00:00")["backlight_report"]["last_command"], 97)

    def test_offline_with_old_heartbeat_is_device_failure(self):
        login = (200, {"Set-Cookie": "bridge_admin_session=x; Secure"}, b'{"authenticated":true,"csrf_token":"x"}')
        offline = envelope(connected=False, last_seen="2027-01-15T07:00:00+00:00")
        transport = FakeTransport([login, (200, {}, json.dumps(offline).encode())])
        run = observer(transport=transport, duration=60).run()
        self.assertEqual(run["outcome"], "failed")
        self.assertEqual(run["failure_code"], "device_offline")
        self.assertEqual(observer(transport=FakeTransport([login, (200, {}, json.dumps(offline).encode())]))._observe()["state"], "device_offline")

    def test_connected_with_old_heartbeat_is_uncertain_not_device_offline(self):
        login = (200, {"Set-Cookie": "bridge_admin_session=x; Secure"}, b'{"authenticated":true,"csrf_token":"x"}')
        stale = envelope(last_seen="2027-01-15T07:00:00+00:00")
        transport = FakeTransport([login, (200, {}, json.dumps(stale).encode())])
        o = observer(transport=transport, duration=60)
        row = o._observe()
        self.assertEqual(row["state"], "invalid_response")
        self.assertNotEqual(row["state"], "device_offline")
        run = observer(transport=FakeTransport([login, (200, {}, json.dumps(stale).encode())]), duration=60).run()
        self.assertEqual(run["outcome"], "inconclusive")

    def test_semver_whitelist_rejects_arbitrary_string(self):
        data = envelope(firmware="SECRET_SENTINEL")
        with self.assertRaises(SafeFailure):
            project_response(data, "2027-01-15T08:00:00+00:00")

    def test_repeated_stale_backend_snapshot_cannot_pass(self):
        clock = FakeClock()
        transport = FakeTransport(wallclock=clock.wall, freeze_heartbeat=True)
        run = observer(clock, transport, duration=360).run()
        self.assertNotEqual(run["outcome"], "observation_passed")
        self.assertIn(run["failure_code"], {"observation_gaps", "stale_snapshot"})

    def test_duration_and_interval_must_be_finite_and_bounded(self):
        for duration, interval in ((float("nan"), 60), (float("inf"), 60), (604801, 60), (60, float("inf")), (60, 0.5)):
            with self.subTest(duration=duration, interval=interval):
                with self.assertRaises(SafeFailure):
                    Observer(base_url="http://127.0.0.1:9000", admin_origin="https://bridge.example.test",
                             device_id="a" * 32, password="x", duration=duration, interval=interval)

    def test_destination_origin_must_match_direct_https_bridge(self):
        with self.assertRaises(SafeFailure):
            Observer(base_url="https://bridge.example.test", admin_origin="https://other.example.test",
                     device_id="a" * 32, password="x")
        with self.assertRaises(SafeFailure):
            Observer(base_url="http://127.0.0.1:9000/?secret=1", admin_origin="https://bridge.example.test",
                     device_id="a" * 32, password="x")

    def test_hanging_transport_is_bounded_and_does_not_spawn_more_requests(self):
        hang = threading.Event()
        transport = FakeTransport(hang_event=hang)
        def immediate_timeout(event, timeout):
            transport.entered.wait(0.1)
            return False
        run = observer(transport=transport, duration=120, call_waiter=immediate_timeout).run()
        hang.set()
        self.assertEqual(run["outcome"], "inconclusive")
        self.assertEqual(run["failure_code"], "request_timeout")
        self.assertEqual(len(transport.calls), 1)

    def test_journal_is_private_and_sample_sink_flushes_each_sanitized_sample(self):
        with tempfile.TemporaryDirectory() as temp:
            path = Path(temp) / "samples.jsonl"
            with _open_private_file(path) as stream:
                o = observer(sample_sink=lambda item: (stream.write(json.dumps(item) + "\n"), stream.flush()))
                o._observe()
                self.assertEqual(len(path.read_text().splitlines()), 1)
            self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o600)
            serialized = path.read_text()
            self.assertNotIn("PASSWORD_SENTINEL", serialized)
            self.assertNotIn("COOKIE_SENTINEL", serialized)


if __name__ == "__main__":
    unittest.main()
