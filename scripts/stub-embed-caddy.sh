#!/usr/bin/env bash
# cmd/embed* use go:embed for build artifacts not checked in; create stubs for lint/vet.
set -euo pipefail

for dir in cmd/embed cmd/embed-app; do
	for file in caddy python-standalone.tar.gz; do
		if [[ ! -f "$dir/$file" ]]; then
			touch "$dir/$file"
		fi
	done
	if [[ ! -x "$dir/caddy" ]]; then
		chmod +x "$dir/caddy"
	fi
done

if [[ ! -f cmd/embed-app/app.zip ]]; then
	touch cmd/embed-app/app.zip
fi
