#!/usr/bin/env python3
"""Accept one released archive on the machine it is for (NEW_247A §6).

Run by `.github/workflows/release.yml` on a NATIVE runner per operating-system
family, against the exact archive the release will attach, and runnable by
hand on any machine with Python 3:

  build/release-acceptance.py --archive auto-pigeon-companion-1.150-linux-amd64.zip \\
      --platform linux-amd64 --version 1.150 --aue-version 1.207 \\
      --fixture build/release-fixtures/release-acceptance.apmap --report report.json

It refuses to run a platform the machine is not: a native claim made on
another machine's hardware is not one. Every step runs the unpacked Companion
as a child process with a HOME of its own and with neither the developer
override nor a schema directory in its environment, so what passes is the
release as a user receives it.

  1. unpack the archive
  2. `companion version` is the release version
  3. `extractor status` says verified; `extractor version` runs the bundled one
  4. the extractor's bytes are the digest the bundle manifest lists
  5. `extractor convert` turns the fixture APMap into a .map through the real
     subprocess path
  6. a TAMPERED extractor is refused before it runs
  7. a MISSING extractor is named by its path, and nothing fetches one
  8. INTERACTIVE CLOSE (NEW_247B): the unpacked Companion in application mode
     holds one page's lease; closing that page stops the process after its
     grace period, with the causal line, no listener left and no token file
"""

import argparse
import base64
import json
import os
import platform as host
import re
import shutil
import socket
import subprocess
import sys
import tempfile
import threading
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
# No __pycache__ beside the scripts: an untracked file in the checkout makes the
# next `go build` stamp vcs.modified=true, which the archive check refuses.
sys.dont_write_bytecode = True
import releaselib  # noqa: E402

REFUSED_TAMPERED = "not the extractor this release shipped"
REFUSED_MISSING = "no extractor was shipped beside this Companion"
SCRUBBED = ("AUCOM_AUE_BINARY", "AUC_AUE_BINARY", "APMAP_SCHEMA_DIR", "AULIBS_DIR")


def this_machine():
    system = {"Linux": "linux", "Darwin": "darwin", "Windows": "windows"}.get(host.system(), host.system())
    machine = host.machine().lower()
    arch = {"x86_64": "amd64", "amd64": "amd64", "aarch64": "arm64", "arm64": "arm64"}.get(machine, machine)
    return f"{system}-{arch}"


class Run:
    def __init__(self, work):
        self.home = os.path.join(work, "home")
        os.makedirs(self.home, exist_ok=True)
        self.results = []

    def env(self):
        env = {key: value for key, value in os.environ.items() if key not in SCRUBBED}
        for key in ("HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "XDG_CONFIG_HOME", "XDG_DATA_HOME",
                    "XDG_CACHE_HOME", "XDG_STATE_HOME"):
            env[key] = self.home
        return env

    def companion(self, companion, *args):
        done = subprocess.run([companion, *args], capture_output=True, text=True, env=self.env(), timeout=120)
        return done.returncode, done.stdout, done.stderr

    def check(self, step, ok, detail):
        self.results.append({"step": step, "result": "pass" if ok else "fail", "detail": detail})
        print(f"{'PASS' if ok else 'FAIL'}  {step}: {detail}")
        return ok


def files_named(root, prefix):
    found = []
    for directory, _, names in os.walk(root):
        found += [os.path.join(directory, name) for name in names if name.startswith(prefix)]
    return sorted(found)


def interactive_close(run, companion, grace_seconds=2):
    """Start the Companion in application mode, hold one lease the way a page
    does (a WebSocket carrying this run's token as a subprotocol), close it,
    and measure what the process does. Returns (ok, detail)."""
    child = subprocess.Popen([companion, "serve", "--interactive", f"--close-grace={grace_seconds}s",
                              "--startup-window=60s"], stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                             text=True, env=run.env())
    lines = []

    def read():
        for line in child.stdout:
            lines.append(line)

    reader = threading.Thread(target=read, daemon=True)
    reader.start()
    try:
        deadline = time.monotonic() + 60
        address = None
        while time.monotonic() < deadline and address is None and child.poll() is None:
            for line in list(lines):
                found = re.search(r"is running at http://([0-9.]+:[0-9]+)/", line)
                if found:
                    address = found.group(1)
            time.sleep(0.05)
        if address is None:
            return False, f"no address printed: {lines!r}"
        tokens = files_named(run.home, "api-token")
        if not tokens:
            return False, "no api-token file under the run's home"
        token = open(tokens[0], encoding="utf-8").read().strip()
        host_, port = address.split(":")
        page = socket.create_connection((host_, int(port)), timeout=10)
        key = base64.b64encode(os.urandom(16)).decode()
        page.sendall((f"GET /api/lifecycle/lease HTTP/1.1\r\nHost: {address}\r\nOrigin: http://{address}\r\n"
                      "Upgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Version: 13\r\n"
                      f"Sec-WebSocket-Key: {key}\r\n"
                      f"Sec-WebSocket-Protocol: aucom.lease.v1, aucom.token.{token}\r\n\r\n").encode())
        answer = b""
        while b"hello" not in answer:
            chunk = page.recv(4096)
            if not chunk:
                break
            answer += chunk
        if not answer.startswith(b"HTTP/1.1 101") or b'"hello"' not in answer:
            return False, f"lease refused: {answer[:200]!r}"
        if child.poll() is not None:
            return False, f"exited while its page was open (exit {child.returncode})"
        closed = time.monotonic()
        page.close()
        try:
            code = child.wait(timeout=grace_seconds + 30)
        except subprocess.TimeoutExpired:
            return False, f"still running {grace_seconds + 30}s after its only page closed"
        waited = time.monotonic() - closed
        reader.join(timeout=5)
        said = "".join(lines)
        listening = True
        try:
            socket.create_connection((host_, int(port)), timeout=2).close()
        except OSError:
            listening = False
        token_left = bool(files_named(run.home, "api-token"))
        ok = (code == 0 and waited >= grace_seconds and "its last browser window was closed" in said
              and not listening and not token_left)
        return ok, (f"exit {code} {waited:.1f}s after the close (grace {grace_seconds}s); "
                    f"listener {'STILL OPEN' if listening else 'closed'}; token file "
                    f"{'LEFT' if token_left else 'removed'}; said {said.strip().splitlines()[-1:]!r}")
    finally:
        if child.poll() is None:
            child.kill()
            child.wait()


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--archive", required=True)
    parser.add_argument("--platform", required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--aue-version", required=True)
    parser.add_argument("--fixture", required=True)
    parser.add_argument("--report", default=None)
    parser.add_argument("--work", default=None)
    args = parser.parse_args()

    machine = this_machine()
    if machine != args.platform:
        print(f"error: this machine is {machine} and the archive is {args.platform}; a native acceptance runs on "
              "the hardware it is for, never on another", file=sys.stderr)
        return 2

    work = args.work or tempfile.mkdtemp(prefix="aucom-acceptance-")
    run = Run(work)
    top = f"auto-pigeon-companion-{args.version}-{args.platform}"

    # 1. The exact archive, unpacked.
    unpacked = os.path.join(work, "unpacked")
    releaselib.extract_zip(args.archive, unpacked)
    bundle = os.path.join(unpacked, top)
    run.check("unpack", os.path.isdir(bundle), f"{os.path.basename(args.archive)} -> {top}/")
    where = releaselib.layout(bundle, args.platform)
    companion = os.path.join(bundle, where["companion"])
    extractor = os.path.join(bundle, where["extractor"])
    manifest = json.load(open(os.path.join(bundle, where["manifest"]), encoding="utf-8"))

    # 2. The version is the release's.
    code, out, err = run.companion(companion, "version")
    first = out.splitlines()[0].strip() if out else ""
    run.check("companion version", code == 0 and first == args.version, f"exit {code}, printed {first!r}")

    # 3. The bundled extractor, with no override and no schema directory.
    code, out, err = run.companion(companion, "extractor", "status")
    run.check("extractor status", code == 0 and "checked against its bundle manifest" in out, out.strip())
    code, out, err = run.companion(companion, "extractor", "version")
    run.check("extractor version", code == 0 and args.aue_version in out and not err.strip(),
              f"exit {code}, stdout {out.strip()!r}, stderr {err.strip()!r}")

    # 4. Its bytes are the ones the manifest lists.
    listed = {member["path"]: member["sha256"] for member in manifest["members"]}
    digest = releaselib.digest_of(extractor)[1]
    run.check("extractor digest", listed.get(where["extractor"]) == digest,
              f"{digest} (manifest lists {listed.get(where['extractor'])})")

    # 5. A real conversion, through the build's own subprocess path.
    fixtures = os.path.join(work, "fixture")
    os.makedirs(fixtures, exist_ok=True)
    source = os.path.join(fixtures, "release-acceptance.apmap")
    shutil.copyfile(args.fixture, source)
    code, out, err = run.companion(companion, "extractor", "convert", source)
    converted = os.path.join(fixtures, "converted-release-acceptance", "release-acceptance.map")
    written = open(converted, encoding="utf-8").read() if os.path.isfile(converted) else ""
    run.check("convert", code == 0 and '"classname" "worldspawn"' in written and "verified" in out,
              f"exit {code}, {len(written)} bytes of .map, {out.strip()!r} {err.strip()!r}")

    # 6. Tampered: one byte appended. The bytes would still run and still
    # speak the protocol, so a refusal here is the digest check, before any
    # handshake. Off Windows, a second substitute would leave a file behind if
    # anything executed it.
    tampered = os.path.join(work, "tampered")
    shutil.copytree(bundle, tampered, symlinks=True)
    tampered_extractor = os.path.join(tampered, where["extractor"])
    tampered_companion = os.path.join(tampered, where["companion"])
    with open(tampered_extractor, "ab") as handle:
        handle.write(b"\0")
    code, out, err = run.companion(tampered_companion, "extractor", "status")
    run.check("tampered: status", REFUSED_TAMPERED in out, out.strip())
    code, out, err = run.companion(tampered_companion, "extractor", "version")
    run.check("tampered: version refused", code != 0 and REFUSED_TAMPERED in err, f"exit {code}, {err.strip()!r}")
    tampered_fixture = os.path.join(tampered, "t.apmap")
    shutil.copyfile(args.fixture, tampered_fixture)
    code, out, err = run.companion(tampered_companion, "extractor", "convert", tampered_fixture)
    run.check("tampered: convert refused", code != 0 and REFUSED_TAMPERED in err
              and not os.path.exists(os.path.join(tampered, "converted-t")), f"exit {code}, {err.strip()!r}")
    if not args.platform.startswith("windows-"):
        sentinel = os.path.join(work, "the-substitute-ran")
        with open(tampered_extractor, "w", encoding="utf-8") as handle:
            handle.write(f"#!/bin/sh\ntouch '{sentinel}'\necho '{{\"protocol\":\"1.0\"}}'\n")
        os.chmod(tampered_extractor, 0o755)
        code, out, err = run.companion(tampered_companion, "extractor", "version")
        run.check("substitute: never executed", code != 0 and not os.path.exists(sentinel),
                  f"exit {code}, sentinel {'PRESENT' if os.path.exists(sentinel) else 'absent'}")

    # 7. Missing: the path is named, and nothing arrives in its place.
    removed = os.path.join(work, "removed")
    shutil.copytree(bundle, removed, symlinks=True)
    os.remove(os.path.join(removed, where["extractor"]))
    removed_companion = os.path.join(removed, where["companion"])
    code, out, err = run.companion(removed_companion, "extractor", "status")
    # The path is printed as the Companion resolved its own executable (on
    # macOS /var is /private/var), so the check is the sentence and the
    # directory the bundle was copied to, not a byte-exact path.
    run.check("missing: status names the path", REFUSED_MISSING in out and "looked for" in out
              and os.path.basename(removed) in out and os.path.basename(where["extractor"]) in out, out.strip())
    code, out, err = run.companion(removed_companion, "extractor", "version")
    run.check("missing: version refused", code != 0 and "no extractor" in err.lower(), f"exit {code}, {err.strip()!r}")
    appeared = files_named(removed, "auto-pigeon-extractor") + files_named(run.home, "auto-pigeon-extractor")
    run.check("missing: nothing fetched one", not appeared, f"extractor files after the run: {appeared}")

    # 8. The application lifecycle, on the release as a user receives it.
    ok, detail = interactive_close(run, companion)
    run.check("interactive close", ok, detail)

    failed = [result for result in run.results if result["result"] != "pass"]
    report = {
        "schema": "aucom.release-acceptance/1.0",
        "archive": os.path.basename(args.archive),
        "archive_sha256": releaselib.digest_of(args.archive)[1],
        "platform": args.platform,
        "machine": machine,
        "version": args.version,
        "aue_version": args.aue_version,
        "verdict": "pass" if not failed else "fail",
        "steps": run.results,
    }
    if args.report:
        with open(args.report, "w", encoding="utf-8") as handle:
            json.dump(report, handle, indent=2)
            handle.write("\n")
    print(f"{report['verdict'].upper()}: {len(run.results) - len(failed)}/{len(run.results)} steps")
    return 0 if not failed else 1


if __name__ == "__main__":
    sys.exit(main())
