#!/usr/bin/env python3
"""Browser-startup regressions, independent of the host's installed browser.

The native release gate still drives real Chrome/Edge and real AUCOM pages.
These tests cover startup delays, transient profile reads and truthful errors.
"""

import builtins
import importlib.util
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest import mock

sys.dont_write_bytecode = True
spec = importlib.util.spec_from_file_location(
    "release_acceptance", Path(__file__).with_name("release-acceptance.py"))
acceptance = importlib.util.module_from_spec(spec)
spec.loader.exec_module(acceptance)


class Clock:
    def __init__(self):
        self.now = 0

    def monotonic(self):
        return self.now

    def sleep(self, seconds):
        self.now += seconds


class DelayedBrowser:
    """The profile file arrives with simulated time, avoiding a minute's wait."""

    def __init__(self, clock, profile, ready_at):
        self.clock, self.profile, self.ready_at = clock, profile, ready_at
        self.returncode = None
        self.terminated = False

    def poll(self):
        if self.returncode is None and self.clock.now >= self.ready_at:
            Path(self.profile, "DevToolsActivePort").write_text("9222\n/browser/test\n", encoding="utf-8")
        return self.returncode

    def terminate(self):
        self.terminated = True
        self.returncode = -15

    def wait(self, timeout=None):
        return self.returncode


class BrowserStartupTests(unittest.TestCase):
    def delayed(self, work, ready_at):
        clock = Clock()
        process = DelayedBrowser(clock, os.path.join(work, "browser-profile"), ready_at)
        return clock, process

    def test_cold_start_after_old_thirty_second_limit(self):
        with tempfile.TemporaryDirectory() as work:
            clock, process = self.delayed(work, ready_at=60)
            with mock.patch.object(acceptance.time, "monotonic", clock.monotonic), \
                 mock.patch.object(acceptance.time, "sleep", clock.sleep), \
                 mock.patch.object(acceptance.subprocess, "Popen", return_value=process):
                browser = acceptance.Browser("chrome", work)
                self.assertEqual(browser.port, 9222)
                self.assertFalse(process.terminated)
                browser.close()
            self.assertTrue(process.terminated)
            self.assertTrue(browser.stderr.closed)
            self.assertFalse(os.path.exists(browser.profile))

    def test_timeout_reports_pre_cleanup_state_and_reaps_browser(self):
        with tempfile.TemporaryDirectory() as work:
            clock, process = self.delayed(work, ready_at=float("inf"))
            with mock.patch.object(acceptance.time, "monotonic", clock.monotonic), \
                 mock.patch.object(acceptance.time, "sleep", clock.sleep), \
                 mock.patch.object(acceptance.subprocess, "Popen", return_value=process):
                with self.assertRaises(RuntimeError) as raised:
                    acceptance.Browser("chrome", work, startup_timeout=1)
            message = str(raised.exception)
            self.assertIn("process still running", message)
            self.assertIn("after ", message)
            self.assertNotIn("exit -15", message)
            self.assertTrue(process.terminated)
            self.assertFalse(os.path.exists(process.profile))

    def test_profile_rename_permission_error_is_retried(self):
        with tempfile.TemporaryDirectory() as work:
            clock, process = self.delayed(work, ready_at=0)
            real_open = builtins.open
            refused = []

            def profile_open(path, *args, **kwargs):
                if os.fspath(path).endswith("DevToolsActivePort") and not refused:
                    refused.append(True)
                    raise PermissionError("profile file is being renamed")
                return real_open(path, *args, **kwargs)

            with mock.patch.object(acceptance.time, "monotonic", clock.monotonic), \
                 mock.patch.object(acceptance.time, "sleep", clock.sleep), \
                 mock.patch.object(acceptance.subprocess, "Popen", return_value=process), \
                 mock.patch("builtins.open", side_effect=profile_open):
                browser = acceptance.Browser("chrome", work)
                self.assertEqual(browser.port, 9222)
                browser.close()
            self.assertEqual(refused, [True])

    def test_browser_exit_preserves_real_code_and_bounded_stderr(self):
        real_popen = subprocess.Popen
        # More than a pipe buffer: this would stall a browser whose stderr is
        # captured in a PIPE without being drained while startup is awaited.
        source = "import sys; sys.stderr.write('x' * 200000 + '\\nstartup failed\\n'); sys.exit(23)"
        captured = []

        def launch(args, **kwargs):
            captured.append(kwargs["stderr"])
            return real_popen([sys.executable, "-c", source], **kwargs)

        with tempfile.TemporaryDirectory() as work, \
             mock.patch.object(acceptance.subprocess, "Popen", side_effect=launch):
            with self.assertRaises(RuntimeError) as raised:
                acceptance.Browser("chrome", work, startup_timeout=5)
            message = str(raised.exception)
            self.assertIn("exit 23", message)
            self.assertIn("startup failed", message)
            self.assertLess(len(message), 4500)
            self.assertTrue(captured[0].closed)
            self.assertFalse(os.path.exists(os.path.join(work, "browser-profile")))

    def test_failed_spawn_cleans_profile_and_stderr(self):
        captured = []

        def fail(args, **kwargs):
            captured.append(kwargs["stderr"])
            raise FileNotFoundError("browser executable missing")

        with tempfile.TemporaryDirectory() as work, \
             mock.patch.object(acceptance.subprocess, "Popen", side_effect=fail):
            with self.assertRaises(FileNotFoundError):
                acceptance.Browser("missing-browser", work)
            self.assertTrue(captured[0].closed)
            self.assertFalse(os.path.exists(os.path.join(work, "browser-profile")))


if __name__ == "__main__":
    unittest.main()
