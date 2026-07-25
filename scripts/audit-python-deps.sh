#!/usr/bin/env bash
set -euo pipefail

# Audit Python dependency files for known CVEs using pip-audit.
# Dev dependencies gate by default; PIP_AUDIT_STRICT=1 also gates app fixtures.
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

if ! python3 -m pip_audit --version >/dev/null 2>&1; then
  python3 -m pip install pip-audit
fi

shopt -s globstar nullglob
requirements=(**/requirements*.txt)
if ((${#requirements[@]} == 0)); then
  echo "No requirements files found" >&2
  exit 1
fi

status=0
for req in "${requirements[@]}"; do
  echo "==> pip-audit: $req"
  if ! python3 -m pip_audit -r "$req" --progress-spinner=off; then
    if [[ "$req" == "requirements-dev.txt" || "${PIP_AUDIT_STRICT:-0}" == "1" ]]; then
      status=1
    else
      echo "::warning title=pip-audit::$req contains known vulnerabilities; set PIP_AUDIT_STRICT=1 to fail"
    fi
  fi
done
exit "$status"
