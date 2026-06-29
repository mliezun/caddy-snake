#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

echo "=== pre-commit ==="
pre-commit run --all-files

echo "=== gofmt / go mod tidy ==="
unformatted="$(gofmt -l .)"
if [[ -n "$unformatted" ]]; then
  echo "gofmt needed on:"
  echo "$unformatted"
  exit 1
fi
go mod tidy
if ! git diff --exit-code go.mod go.sum >/dev/null 2>&1; then
  echo "go.mod or go.sum is not tidy"
  exit 1
fi

echo "=== golangci-lint ==="
golangci-lint run ./...

echo "=== go vet ==="
go vet ./...

echo "=== go tests ==="
go test -race ./...
go test -race -tags=caddytest -timeout 180s ./...

echo "=== ruff ==="
ruff check .
ruff format --check .

echo "=== ty ==="
if command -v ty >/dev/null 2>&1; then
  ty check
else
  uvx "ty==0.0.55" check
fi

echo "=== pytest ==="
pip install -q -r requirements-dev.txt
pytest caddysnake_test.py -v

echo "=== bandit ==="
bandit -r caddysnake.py cmd/cli/caddysnake cmd/cli/caddysnake_cli.py -ll -q

echo "=== gosec ==="
if command -v gosec >/dev/null 2>&1; then
  gosec -exclude-generated -severity medium -quiet ./...
fi

echo "=== govulncheck ==="
if command -v govulncheck >/dev/null 2>&1; then
  govulncheck ./...
fi

echo "=== gitleaks ==="
if command -v gitleaks >/dev/null 2>&1; then
  gitleaks detect --source . --redact --verbose
fi

echo "=== shellcheck ==="
find . -name '*.sh' -not -path './tests/*/venv/*' -print0 | xargs -0 shellcheck

echo "=== All QA checks passed ==="
