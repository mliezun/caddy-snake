#!/usr/bin/env bash
# cmd/embed* embed a built Caddy binary that is not checked in; create a stub for lint/vet.
set -euo pipefail

for dir in cmd/embed cmd/embed-app; do
	if [[ ! -f "$dir/caddy" ]]; then
		printf '#!/bin/sh\nexit 0\n' >"$dir/caddy"
		chmod +x "$dir/caddy"
	fi
done
