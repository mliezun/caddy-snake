#!/usr/bin/env bash
set -euo pipefail

# Audit Python dependency files for known CVEs using pip-audit.
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

if ! command -v pip-audit >/dev/null 2>&1; then
  pip install pip-audit
fi

status=0
for req in requirements-dev.txt; do
  echo "==> pip-audit: $req"
  if ! pip-audit -r "$req" --progress-spinner=off; then
    status=1
  fi
done

exit "$status"
