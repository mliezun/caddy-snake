#!/usr/bin/env bash
set -euo pipefail

# Audit Python dependency files for known CVEs using pip-audit.
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

if ! command -v pip-audit >/dev/null 2>&1; then
  pip install pip-audit
fi

status=0
while IFS= read -r req; do
  echo "==> pip-audit: $req"
  if ! pip-audit -r "$req" --progress-spinner=off; then
    status=1
  fi
done < <(find . -name 'requirements*.txt' -not -path './.git/*' | sort)

if [ -f cmd/cli/pyproject.toml ]; then
  echo "==> pip-audit: cmd/cli/pyproject.toml"
  if ! pip-audit --project-path cmd/cli --progress-spinner=off; then
    status=1
  fi
fi

exit "$status"
