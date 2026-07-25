#!/usr/bin/env bash
set -euo pipefail

# Audit Python dependency files for known CVEs using pip-audit.
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

if ! command -v pip-audit >/dev/null 2>&1; then
  pip install pip-audit
fi

shopt -s globstar nullglob
requirements=(**/requirements*.txt)
if ((${#requirements[@]} == 0)); then
  echo "No requirements files found" >&2
  exit 1
fi

for req in "${requirements[@]}"; do
  echo "==> pip-audit: $req"
  pip-audit -r "$req" --progress-spinner=off
done
