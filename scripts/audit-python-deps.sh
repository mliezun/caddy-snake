#!/usr/bin/env bash
set -euo pipefail

# Audit Python dependency files for known CVEs using pip-audit.
# Each file is independent, so they are audited at the same time (four at
# once) and the reports are printed in order afterwards.
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

if ! python3 -m pip_audit --version >/dev/null 2>&1; then
  python3 -m pip install pip-audit
fi

out="$(mktemp -d)"
trap 'rm -rf "$out"' EXIT

audit_one() { # index, target
  local log="$out/$1.log"
  echo "==> pip-audit: $2" > "$log"
  if [ "$2" = "cmd/cli" ]; then
    # Positional project_path audits pyproject.toml in that directory.
    python3 -m pip_audit cmd/cli --progress-spinner=off >> "$log" 2>&1 || touch "$out/$1.failed"
  else
    python3 -m pip_audit -r "$2" --progress-spinner=off >> "$log" 2>&1 || touch "$out/$1.failed"
  fi
}

targets=()
while IFS= read -r req; do
  targets+=("$req")
done < <(find . -name 'requirements*.txt' -not -path './.git/*' -not -path '*/node_modules/*' -not -path '*/venv/*' -not -path '*/.venv/*' | sort)
if [ -f cmd/cli/pyproject.toml ]; then
  targets+=("cmd/cli")
fi

running=0
for i in "${!targets[@]}"; do
  audit_one "$i" "${targets[$i]}" &
  running=$((running + 1))
  if [ "$running" -ge 4 ]; then
    wait -n 2>/dev/null || wait
    running=$((running - 1))
  fi
done
wait

status=0
for i in "${!targets[@]}"; do
  cat "$out/$i.log"
  if [ -e "$out/$i.failed" ]; then
    status=1
  fi
done
exit "$status"
