#!/usr/bin/env bash
set -euo pipefail

# Audit docs npm dependencies. Fails on high/critical findings except the
# advisories listed below, which have no fixed release to move to; each says
# why it is acceptable meanwhile.
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root/docs"

allowlisted=(
  GHSA-w3rx-r6r6-pgpr # image-size ICNS infinite loop; no release after 2.0.2
  GHSA-5p2g-fcmc-qvqq # image-size JXL/HEIF infinite loop; same archive
  # picomatch: slow on crafted glob patterns (ReDoS). Every release is affected
  # (<= 4.0.7, the newest on 2026-11-17), so there is nothing to pin. It is only
  # used while building the docs, on patterns from our own config; the published
  # site is static files. Accepted by the maintainer on 2026-11-17. Remove this
  # entry once a fixed picomatch is released.
  GHSA-fc5q-39xc-vjpf
)

npm ci

tmp="$(mktemp)"
err="$(mktemp)"
trap 'rm -f "$tmp" "$err"' EXIT

# npm audit exits 1 when findings exist; other non-zero codes are tool failures.
set +e
npm audit --json >"$tmp" 2>"$err"
status=$?
set -e

python3 - "$tmp" "$err" "$status" "${allowlisted[@]}" <<'PY'
import json
import sys

path, err_path, status_s, *allow_ids = sys.argv[1:]
status = int(status_s)
allow = set(allow_ids)
raw = open(path, encoding="utf-8").read()
stderr = open(err_path, encoding="utf-8").read()

try:
    data = json.loads(raw) if raw.strip() else None
except json.JSONDecodeError as exc:
    print(f"npm audit did not return JSON: {exc}", file=sys.stderr)
    if raw.strip():
        print(raw[:2000], file=sys.stderr)
    if stderr.strip():
        print(stderr[:2000], file=sys.stderr)
    sys.exit(1)

if not isinstance(data, dict):
    print("npm audit returned empty output", file=sys.stderr)
    if stderr.strip():
        print(stderr[:2000], file=sys.stderr)
    sys.exit(1)

if "error" in data:
    err = data["error"]
    print(f"npm audit failed: {err}", file=sys.stderr)
    sys.exit(1)

if "vulnerabilities" not in data:
    print("npm audit JSON missing 'vulnerabilities'", file=sys.stderr)
    sys.exit(1)

# 0 = clean, 1 = findings. Anything else is a failed audit request.
if status not in (0, 1):
    print(f"npm audit exited {status}", file=sys.stderr)
    if stderr.strip():
        print(stderr[:2000], file=sys.stderr)
    sys.exit(status)

blocking = []
ignored = []
seen = set()
for vuln in data["vulnerabilities"].values():
    for via in vuln.get("via", []):
        if not isinstance(via, dict):
            continue
        url = via.get("url") or ""
        ghsa = url.rsplit("/", 1)[-1] if url else ""
        title = via.get("title") or via.get("name") or ghsa
        severity = (via.get("severity") or "").lower()
        if severity not in ("high", "critical"):
            continue
        key = (ghsa, title)
        if key in seen:
            continue
        seen.add(key)
        if ghsa in allow:
            ignored.append(f"{ghsa}: {title}")
            continue
        blocking.append(f"{ghsa}: {title} ({severity})")

if ignored:
    print("Ignored unfixable advisories:")
    for line in ignored:
        print(f"  - {line}")
if blocking:
    print("High/critical npm audit findings:")
    for line in blocking:
        print(f"  - {line}")
    sys.exit(1)
print("No blocking high/critical npm audit findings.")
PY
