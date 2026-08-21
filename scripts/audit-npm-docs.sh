#!/usr/bin/env bash
set -euo pipefail

# Audit docs npm dependencies. Fails on high/critical findings except
# image-size GHSAs: the package is archived and 2.0.3 was never published.
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root/docs"

allowlisted=(
  GHSA-w3rx-r6r6-pgpr # image-size ICNS infinite loop; no release after 2.0.2
  GHSA-5p2g-fcmc-qvqq # image-size JXL/HEIF infinite loop; same archive
)

npm ci

tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT

# npm audit exits 1 when findings exist; we inspect JSON ourselves.
npm audit --json >"$tmp" || true

python3 - "$tmp" "${allowlisted[@]}" <<'PY'
import json
import sys

path = sys.argv[1]
allow = set(sys.argv[2:])
data = json.loads(open(path, encoding="utf-8").read())
blocking = []
ignored = []
seen = set()
for vuln in data.get("vulnerabilities", {}).values():
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
