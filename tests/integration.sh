#!/usr/bin/env bash
set -euo pipefail

# ---------------------------------------------------------------------------
# integration_test.sh – run integration tests locally inside Docker
#
# Usage:
#   ./integration_test.sh <tool-name> <python-version>
#
# Examples:
#   ./integration_test.sh fastapi 3.13
#   ./integration_test.sh django 3.12
#   ./integration_test.sh simple 3.13-nogil
#
# Valid tool names:
#   django, django_channels, flask, fastapi, simple_autoreload, simple_async, socketio, dynamic
#
# Valid python versions:
#   3.10, 3.11, 3.12, 3.13, 3.13-nogil, 3.14
# ---------------------------------------------------------------------------

VALID_TOOLS=("django" "django_channels" "flask" "fastapi" "simple_autoreload" "simple_async" "socketio" "dynamic")
VALID_PYVERSIONS=("3.10" "3.11" "3.12" "3.13" "3.13-nogil" "3.14")

usage() {
  echo "Usage: $0 <tool-name> <python-version>"
  echo ""
  echo "  tool-name:       one of: ${VALID_TOOLS[*]}"
  echo "  python-version:  one of: ${VALID_PYVERSIONS[*]}"
  exit 1
}

if [[ $# -lt 2 ]]; then
  usage
fi

TOOL_NAME="$1"
PYTHON_VERSION="$2"

# Validate tool name
match=0
for t in "${VALID_TOOLS[@]}"; do
  [[ "$t" == "$TOOL_NAME" ]] && match=1 && break
done
if [[ $match -eq 0 ]]; then
  echo "Error: invalid tool-name '$TOOL_NAME'"
  echo "Valid options: ${VALID_TOOLS[*]}"
  exit 1
fi

# Validate python version
match=0
for v in "${VALID_PYVERSIONS[@]}"; do
  [[ "$v" == "$PYTHON_VERSION" ]] && match=1 && break
done
if [[ $match -eq 0 ]]; then
  echo "Error: invalid python-version '$PYTHON_VERSION'"
  echo "Valid options: ${VALID_PYVERSIONS[*]}"
  exit 1
fi

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
CONTAINER_NAME="caddy-snake-integration-${TOOL_NAME}-${PYTHON_VERSION//\./-}"

echo "============================================"
echo " Integration Test"
echo "   tool:    $TOOL_NAME"
echo "   python:  $PYTHON_VERSION"
echo "   arch:    linux/amd64"
echo "============================================"
echo ""

# Build the inner script that will run inside the container
IS_NOGIL=0
[[ "$PYTHON_VERSION" == "3.13-nogil" ]] && IS_NOGIL=1

# For nogil the actual python package version is still 3.13
PY_PKG_VERSION="$PYTHON_VERSION"

read -r -d '' INNER_SCRIPT <<'INNEREOF' || true
#!/usr/bin/env bash
set -euo pipefail

TOOL_NAME="__TOOL_NAME__"
PYTHON_VERSION="__PYTHON_VERSION__"
IS_NOGIL="__IS_NOGIL__"
PY_PKG_VERSION="__PY_PKG_VERSION__"

export GOEXPERIMENT=cgocheck2
export DEBIAN_FRONTEND=noninteractive

echo ">>> Installing base packages..."
apt-get update -yyqq
apt-get install -yyqq software-properties-common valgrind time curl gcc build-essential ca-certificates git pkg-config

# Install Go 1.26
echo ">>> Installing Go 1.26..."
curl -fsSL "https://go.dev/dl/go1.26.0.linux-amd64.tar.gz" -o /tmp/go.tar.gz
tar -C /usr/local -xzf /tmp/go.tar.gz
export PATH="/usr/local/go/bin:$HOME/go/bin:$PATH"
go version

# Install xcaddy
echo ">>> Installing xcaddy..."
go install github.com/caddyserver/xcaddy/cmd/xcaddy@latest

# Install Python
echo ">>> Installing Python ${PY_PKG_VERSION}..."
add-apt-repository -y ppa:deadsnakes/ppa
apt-get update -yyqq

cd "/workspace/tests/${TOOL_NAME}"

PKGCONFIG_DIR=$(find /usr/lib -type d -name pkgconfig -path "*linux-gnu*" | head -1)
echo ">>> pkgconfig dir: ${PKGCONFIG_DIR}"

if [[ "$IS_NOGIL" == "1" ]]; then
  apt-get install -yyqq python${PY_PKG_VERSION} python3.13-dev
  echo ">>> Available python .pc files:"
  ls -la "${PKGCONFIG_DIR}"/python* || true
  PC_FILE=$(find "${PKGCONFIG_DIR}" -name "python-${PY_PKG_VERSION}*embed.pc" -o -name "python-3.13*embed.pc" | head -1)
  if [[ -n "$PC_FILE" ]]; then
    cp "$PC_FILE" "${PKGCONFIG_DIR}/python3-embed.pc"
    echo ">>> Copied $PC_FILE -> ${PKGCONFIG_DIR}/python3-embed.pc"
  else
    echo ">>> WARNING: No embed .pc file found for ${PY_PKG_VERSION}"
  fi
  curl -fsSL https://bootstrap.pypa.io/get-pip.py -o get-pip.py
  python${PY_PKG_VERSION} -m venv --without-pip venv
  source venv/bin/activate
  python get-pip.py
  pip install -r requirements.txt
else
  apt-get install -yyqq python${PY_PKG_VERSION}-dev python${PY_PKG_VERSION}-venv
  echo ">>> Available python .pc files:"
  ls -la "${PKGCONFIG_DIR}"/python* || true
  PC_FILE=$(find "${PKGCONFIG_DIR}" -name "python-${PY_PKG_VERSION}*embed.pc" | head -1)
  if [[ -n "$PC_FILE" ]]; then
    cp "$PC_FILE" "${PKGCONFIG_DIR}/python3-embed.pc"
    echo ">>> Copied $PC_FILE -> ${PKGCONFIG_DIR}/python3-embed.pc"
  else
    echo ">>> WARNING: No embed .pc file found for ${PY_PKG_VERSION}"
  fi
  rm -rf venv
  python${PY_PKG_VERSION} -m venv venv
  source venv/bin/activate
  pip install -r requirements.txt
fi

# Build caddy with caddy-snake plugin
echo ">>> Building caddy with caddy-snake..."
CGO_ENABLED=1 xcaddy build --with github.com/mliezun/caddy-snake=/workspace

# Run integration tests
echo ">>> Starting caddy..."
./caddy run --config Caddyfile > caddy.log 2>&1 &
CADDY_PID=$!

echo ">>> Waiting for caddy to be ready..."
timeout 60 bash -c 'while ! grep -q "finished cleaning storage units" caddy.log; do sleep 1; done'
echo ">>> Caddy is ready (PID=${CADDY_PID})"

echo ">>> Running tests..."
source venv/bin/activate

if [[ "$IS_NOGIL" == "1" ]]; then
  python main_test.py || true
else
  python main_test.py
fi

echo ""
echo ">>> Tests completed!"

# Clean up caddy
kill "$CADDY_PID" 2>/dev/null || true
INNEREOF

# Substitute placeholders
INNER_SCRIPT="${INNER_SCRIPT//__TOOL_NAME__/$TOOL_NAME}"
INNER_SCRIPT="${INNER_SCRIPT//__PYTHON_VERSION__/$PYTHON_VERSION}"
INNER_SCRIPT="${INNER_SCRIPT//__IS_NOGIL__/$IS_NOGIL}"
INNER_SCRIPT="${INNER_SCRIPT//__PY_PKG_VERSION__/$PY_PKG_VERSION}"

# Remove any previous container with the same name
docker rm -f "$CONTAINER_NAME" 2>/dev/null || true

echo ">>> Launching Docker container (linux/amd64, ubuntu:22.04)..."
echo ""

docker run \
  --rm \
  --name "$CONTAINER_NAME" \
  --platform linux/amd64 \
  -v "${REPO_ROOT}:/workspace:cached" \
  -w /workspace \
  ubuntu:22.04 \
  bash -c "$INNER_SCRIPT"

echo ""
echo "============================================"
echo " Done!"
echo "============================================"
