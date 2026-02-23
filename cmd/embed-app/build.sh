#!/usr/bin/env bash
# Build a standalone binary that embeds Caddy, caddy-snake, Python, and your app.
#
# Usage:
#   ./build.sh APP_ZIP PYTHON_VERSION [OPTIONS]
#
# Examples:
#   ./build.sh /path/to/app.zip 3.13
#   ./build.sh ./deployment.zip 3.12 --app main:app --server-type wsgi --output myapp
#
# Options:
#   --app MODULE:VAR      App entry point (default: main:app)
#   --server-type TYPE    wsgi or asgi (default: wsgi)
#   --output NAME         Output binary name (default: app or basename of zip)
#   --arch ARCH           python-build-standalone architecture (default: auto-detect)
#   --list-arch           List available architectures and exit

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
EMBED_DIR="$(dirname "$SCRIPT_DIR")/embed"
ROOT_DIR="$(dirname "$(dirname "$SCRIPT_DIR")")"

usage() {
    echo "Usage: $0 APP_ZIP PYTHON_VERSION [OPTIONS]"
    echo ""
    echo "  APP_ZIP        Path to your app .zip (Lambda-style, see docs)"
    echo "  PYTHON_VERSION Python version: 3.10, 3.11, 3.12, 3.13, 3.13-nogil, 3.14"
    echo ""
    echo "Options:"
    echo "  --app MODULE:VAR      App entry point (default: main:app)"
    echo "  --server-type TYPE    wsgi or asgi (default: wsgi)"
    echo "  --output NAME         Output binary name"
    echo "  --arch ARCH           python-build-standalone architecture"
    echo "  --list-arch           List available architectures"
    exit 1
}

# Defaults
APP_ENTRY="main:app"
SERVER_TYPE="wsgi"
OUTPUT_NAME=""
ARCH=""
PY_VERSION=""
APP_ZIP_PATH=""

# Parse arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --app)
            APP_ENTRY="$2"
            shift 2
            ;;
        --server-type)
            SERVER_TYPE="$2"
            shift 2
            ;;
        --output)
            OUTPUT_NAME="$2"
            shift 2
            ;;
        --arch)
            ARCH="$2"
            shift 2
            ;;
        --list-arch)
            python3 "$EMBED_DIR/pybs.py" latest --list-options
            exit 0
            ;;
        -h|--help)
            usage
            ;;
        *)
            if [[ -z "$APP_ZIP_PATH" ]]; then
                APP_ZIP_PATH="$1"
            elif [[ -z "$PY_VERSION" ]]; then
                PY_VERSION="$1"
            else
                echo "Unexpected argument: $1"
                usage
            fi
            shift
            ;;
    esac
done

if [[ -z "$APP_ZIP_PATH" ]] || [[ -z "$PY_VERSION" ]]; then
    echo "Error: APP_ZIP and PYTHON_VERSION are required"
    usage
fi

if [[ ! -f "$APP_ZIP_PATH" ]]; then
    echo "Error: App zip not found: $APP_ZIP_PATH"
    exit 1
fi

# Resolve absolute path for app zip
APP_ZIP_PATH="$(cd "$(dirname "$APP_ZIP_PATH")" && pwd)/$(basename "$APP_ZIP_PATH")"

# Output name: explicit, or from zip basename, or "app"
if [[ -z "$OUTPUT_NAME" ]]; then
    OUTPUT_NAME="$(basename "$APP_ZIP_PATH" .zip)"
    [[ "$OUTPUT_NAME" == "" ]] && OUTPUT_NAME="app"
fi

# Architecture: auto-detect from system if not specified
if [[ -z "$ARCH" ]]; then
    OS=$(uname -s | tr '[:upper:]' '[:lower:]')
    [[ "$OS" == "darwin" ]] && OS="mac"
    MACHINE=$(uname -m)
    case "$OS-$MACHINE" in
        linux-x86_64)   ARCH="x86_64_v2-unknown-linux-gnu" ;;
        linux-aarch64)  ARCH="aarch64-unknown-linux-gnu" ;;
        linux-arm64)    ARCH="aarch64-unknown-linux-gnu" ;;
        mac-x86_64)     ARCH="x86_64-apple-darwin" ;;
        mac-arm64)      ARCH="aarch64-apple-darwin" ;;
        mac-aarch64)    ARCH="aarch64-apple-darwin" ;;
        *)
            echo "Error: Unknown platform $OS-$MACHINE. Use --arch to specify."
            echo "Run with --list-arch for available architectures."
            exit 1
            ;;
    esac
fi

echo "Building embedded app binary..."
echo "  App zip:      $APP_ZIP_PATH"
echo "  Python:       $PY_VERSION"
echo "  Architecture: $ARCH"
echo "  Entry point:  $APP_ENTRY ($SERVER_TYPE)"
echo "  Output:       $OUTPUT_NAME"
echo ""

cd "$SCRIPT_DIR"

# 1. Copy user's app zip (skip if same file)
DEST_APP_ZIP="$SCRIPT_DIR/app.zip"
if [[ "$APP_ZIP_PATH" != "$DEST_APP_ZIP" ]]; then
    cp "$APP_ZIP_PATH" app.zip
fi

# 2. Retrieve Python standalone
if [[ "$PY_VERSION" == "3.13-nogil" ]]; then
    echo "Fetching Python 3.13 freethreaded (nogil)..."
    python3 "$EMBED_DIR/pybs.py" latest --python-version 3.13 --architecture "$ARCH" \
        --build-config freethreaded+pgo+lto --content-type full --dest .
    tar -xf cpython-*.tar.zst
    mv python/install .
    rm -rf python
    mv install python
    tar -czf python-standalone.tar.gz python
    rm -rf python cpython-*
else
    echo "Fetching Python $PY_VERSION..."
    python3 "$EMBED_DIR/pybs.py" latest --python-version "$PY_VERSION" --architecture "$ARCH" \
        --build-config pgo+lto --content-type install_only_stripped --dest .
    mv cpython-*.tar.gz python-standalone.tar.gz
fi

# 3. Build Caddy with caddy-snake
echo "Building Caddy with caddy-snake..."
CGO_ENABLED=0 xcaddy build --with github.com/mliezun/caddy-snake="$ROOT_DIR"

# 4. Build the embed-app binary
echo "Building embed-app binary..."
go build -ldflags "-X main.appEntry=$APP_ENTRY -X main.serverType=$SERVER_TYPE" -o "$OUTPUT_NAME" .

# Cleanup (keep app.zip for potential next build)
rm -f python-standalone.tar.gz caddy cpython-*

echo ""
echo "Done. Run your app with:"
echo "  ./$OUTPUT_NAME"
echo ""
echo "Or with options (e.g. --listen :8080 --domain example.com):"
echo "  ./$OUTPUT_NAME --listen :8080"
