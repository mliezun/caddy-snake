#!/usr/bin/env bash
set -euo pipefail

# Audit Python dependency files for known CVEs using pip-audit.
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

if ! python3 -m pip_audit --version >/dev/null 2>&1; then
  python3 -m pip install pip-audit
fi

status=0
while IFS= read -r req; do
  echo "==> pip-audit: $req"
  if ! python3 -m pip_audit -r "$req" --progress-spinner=off; then
    status=1
  fi
done < <(find . -name 'requirements*.txt' -not -path './.git/*' | sort)

if [ -f cmd/cli/pyproject.toml ]; then
  echo "==> pip-audit: cmd/cli"
  # Positional project_path audits pyproject.toml in that directory.
  if ! python3 -m pip_audit cmd/cli --progress-spinner=off; then
    status=1
  fi
fi

exit "$status"
