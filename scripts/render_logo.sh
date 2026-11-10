#!/usr/bin/env bash
# Regenerate the logo images and the docs favicon from docs/static/img/logo.svg.
# Needs rsvg-convert (librsvg) to draw the images and ImageMagick (`magick`) to
# pack the favicon. Run from the repository root.
set -euo pipefail

img=docs/static/img
svg="$img/logo.svg"

render() { # size, output
  rsvg-convert -w "$1" -h "$1" "$svg" -o "$2"
}

render 512 "$img/caddysnake-512x512.png"
render 1024 "$img/caddysnake.png"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
for size in 16 32 48; do render "$size" "$tmp/$size.png"; done
magick "$tmp/16.png" "$tmp/32.png" "$tmp/48.png" "$img/favicon.ico"

cp "$svg" "$img/favicon.svg"
echo "wrote the logo images and favicon in $img"
