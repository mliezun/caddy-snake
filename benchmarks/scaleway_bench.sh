#!/usr/bin/env bash
# Create a Scaleway POP2-2C-8G (linux/amd64) instance, run the Docker benchmark harness,
# copy results back to this machine, then delete the instance and release the IP.
#
# Requirements: scw (Scaleway CLI), jq, tar, ssh, nc (netcat).
#
# SSH uses plain `ssh root@$IP` (not `scw instance server ssh`) because routed/elastic IPs
# are exposed via `public_ips[]` while older Scaleway CLI builds only read `public_ip` and
# may never connect. Upload your public key in the Scaleway console (IAM → SSH keys).
#
# Optional: BENCH_SSH_IDENTITY=/path/to/id_ed25519 if your agent does not offer the right key.
#
# Usage (from repository root):
#   ./benchmarks/scaleway_bench.sh
#
# Environment:
#   SCW_ZONE              default fr-par-1
#   BENCH_SERVER_TYPE     default POP2-2C-8G
#   BENCH_SERVER_NAME     optional fixed name (default caddy-snake-bench-<epoch>)
#   BENCH_GIT_URL         clone URL when BENCH_RSYNC_LOCAL=0 (default upstream GitHub)
#   BENCH_GIT_REF         branch or tag for shallow clone when BENCH_RSYNC_LOCAL=0 (default main)
#   BENCH_RSYNC_LOCAL=1   tar-stream current repo instead of git clone (bench uncommitted work)
#   BENCH_SSH_IDENTITY    optional path to private key matching a Scaleway IAM SSH key
#   KEEP_SERVER=1         if set, skip deletion (for debugging; you pay until you delete)
#
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

ZONE="${SCW_ZONE:-fr-par-1}"
SERVER_TYPE="${BENCH_SERVER_TYPE:-POP2-2C-8G}"
NAME="${BENCH_SERVER_NAME:-caddy-snake-bench-$(date +%s)}"
BENCH_GIT_URL="${BENCH_GIT_URL:-https://github.com/mliezun/caddy-snake.git}"
BENCH_GIT_REF="${BENCH_GIT_REF:-main}"
RSYNC_LOCAL="${BENCH_RSYNC_LOCAL:-0}"
KEEP="${KEEP_SERVER:-0}"

need() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "Missing required command: $1" >&2
    exit 1
  }
}

need scw
need jq
need tar
need ssh
need nc

if ! scw config dump >/dev/null 2>&1; then
  echo "Scaleway CLI is not configured. Run: scw init" >&2
  exit 1
fi

SERVER_ID=""
KNOWN_HOSTS="$(mktemp)"

cleanup() {
  rm -f "${KNOWN_HOSTS}" 2>/dev/null || true
  if [[ "$KEEP" == "1" ]] || [[ -z "$SERVER_ID" ]]; then
    return 0
  fi
  echo ""
  echo ">>> Deleting instance $SERVER_ID (zone=$ZONE) and attached IP..."
  scw instance server delete "$SERVER_ID" zone="$ZONE" with-volumes=all with-ip=true force-shutdown=true -w || true
}

trap cleanup EXIT

SSH_CMD=()

build_ssh_cmd() {
  SSH_CMD=(
    ssh
    -o "StrictHostKeyChecking=accept-new"
    -o "ConnectTimeout=15"
    -o "ServerAliveInterval=30"
    -o "UserKnownHostsFile=${KNOWN_HOSTS}"
  )
  if [[ -n "${BENCH_SSH_IDENTITY:-}" ]]; then
    SSH_CMD+=(-i "$BENCH_SSH_IDENTITY" -o IdentitiesOnly=yes)
  fi
}

REMOTE_DIR="/root/caddy-snake"
REMOTE_PREP='
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive
for _ in $(seq 1 24); do
  apt-get update -qq && break
  sleep 10
done
apt-get install -y -qq ca-certificates curl git docker.io
systemctl enable --now docker || true
docker --version
git --version
'

SSH_PROBE_OPTS=(
  -o BatchMode=yes
  -o ConnectTimeout=10
  -o StrictHostKeyChecking=accept-new
  -o UserKnownHostsFile="${KNOWN_HOSTS}"
  -o IdentitiesOnly=yes
)

try_discover_identity() {
  if [[ -n "${BENCH_SSH_IDENTITY:-}" ]]; then
    [[ -f "$BENCH_SSH_IDENTITY" ]] || {
      echo "BENCH_SSH_IDENTITY is not a file: $BENCH_SSH_IDENTITY" >&2
      return 1
    }
    ssh "${SSH_PROBE_OPTS[@]}" -i "$BENCH_SSH_IDENTITY" "root@$IP" /bin/true 2>/dev/null
    return $?
  fi
  local cand
  for cand in "$HOME/.ssh/id_ed25519" "$HOME/.ssh/id_ed25519_scw" "$HOME/.ssh/id_ecdsa" "$HOME/.ssh/id_rsa"; do
    [[ -f "$cand" ]] || continue
    if ssh "${SSH_PROBE_OPTS[@]}" -i "$cand" "root@$IP" /bin/true 2>/dev/null; then
      BENCH_SSH_IDENTITY=$cand
      echo "    Using SSH identity: $cand"
      return 0
    fi
  done
  return 1
}

echo "=============================================="
echo " Scaleway benchmark (linux/amd64)"
echo "   zone:    $ZONE"
echo "   type:    $SERVER_TYPE"
echo "   name:    $NAME"
echo "=============================================="

echo ">>> Creating server..."
SERVER_JSON=$(scw instance server create \
  name="$NAME" \
  type="$SERVER_TYPE" \
  image=ubuntu_jammy \
  ip=ipv4 \
  zone="$ZONE" \
  -w -o json)

SERVER_ID=$(echo "$SERVER_JSON" | jq -r '.id // .server.id // empty')
if [[ -z "$SERVER_ID" || "$SERVER_ID" == "null" ]]; then
  echo "Could not read server id from create response:" >&2
  echo "$SERVER_JSON" | jq . >&2
  exit 1
fi
echo "    Server ID: $SERVER_ID"

echo ">>> Waiting for public IPv4..."
IP=""
for _ in $(seq 1 60); do
  IP=$(scw instance server get "$SERVER_ID" zone="$ZONE" -o json \
    | jq -r '(.public_ips[0].address // .public_ip.address // empty)')
  if [[ -n "$IP" && "$IP" != "null" ]]; then
    break
  fi
  sleep 2
done
if [[ -z "$IP" ]]; then
  echo "Could not determine public IP for $SERVER_ID" >&2
  exit 1
fi
echo "    Public IP: $IP"

echo ">>> Waiting for SSH and installing Docker + git (cloud-init)..."
BOOTSTRAP_OK=0
for attempt in $(seq 1 48); do
  if ! nc -z -w 2 "$IP" 22 2>/dev/null; then
    echo "    Port 22 not open yet (attempt $attempt/48)..."
    sleep 5
    continue
  fi
  if try_discover_identity; then
    build_ssh_cmd
    if echo "$REMOTE_PREP" | "${SSH_CMD[@]}" "root@$IP" bash -s; then
      BOOTSTRAP_OK=1
      break
    fi
    echo "    Bootstrap command failed (attempt $attempt/48)..."
  else
    echo "    No SSH key accepted yet (attempt $attempt/48)..."
  fi
  sleep 5
done
if [[ "$BOOTSTRAP_OK" != "1" ]]; then
  echo "Could not SSH and bootstrap the host." >&2
  echo "Upload a matching public key in Scaleway (IAM → SSH keys), run ssh-add, or set BENCH_SSH_IDENTITY." >&2
  exit 1
fi

echo ">>> Copying repository to $IP:$REMOTE_DIR ..."
"${SSH_CMD[@]}" "root@$IP" "rm -rf $REMOTE_DIR && mkdir -p $REMOTE_DIR"

if [[ "$RSYNC_LOCAL" == "1" ]]; then
  (
    cd "$ROOT" || exit 1
    tar -czf - \
      --exclude='./.git' \
      --exclude='./.venv-doc-test' \
      --exclude='./benchmarks/venv' \
      --exclude='./.cursor' \
      .
  ) | "${SSH_CMD[@]}" "root@$IP" "tar xzf - -C $REMOTE_DIR"
else
  BR="$BENCH_GIT_REF" GU="$BENCH_GIT_URL" RD="$REMOTE_DIR"
  "${SSH_CMD[@]}" "root@$IP" \
    "git clone --depth 1 --single-branch --branch $(printf '%q' "$BR") $(printf '%q' "$GU") $(printf '%q' "$RD")"
fi

echo ">>> Running Docker benchmark on remote (this can take 30–45 minutes)..."
REMOTE_BENCH='
set -euo pipefail
cd /root/caddy-snake
docker build -t caddy-snake-bench -f benchmarks/Dockerfile .
docker run --rm -v /root/caddy-snake/benchmarks:/workspace/benchmarks caddy-snake-bench
echo -n "Host arch: "; uname -m
docker run --rm --entrypoint /bin/sh caddy-snake-bench -c "echo -n Container arch: ; uname -m"
'

echo "$REMOTE_BENCH" | "${SSH_CMD[@]}" "root@$IP" bash -s

echo ">>> Copying results and charts to local benchmarks/ ..."
mkdir -p "$ROOT/benchmarks"
for f in results.json benchmark_chart.svg benchmark_chart.png; do
  "${SSH_CMD[@]}" "root@$IP" "cat $REMOTE_DIR/benchmarks/$f" >"$ROOT/benchmarks/$f"
done

echo ""
echo ">>> Done. Results:"
cat "$ROOT/benchmarks/results.json" | jq .
echo ""
echo "    benchmarks/results.json"
echo "    benchmarks/benchmark_chart.svg"
echo "    benchmarks/benchmark_chart.png"
echo ""
echo "Copy docs chart with:"
echo "    cp benchmarks/benchmark_chart.svg docs/static/img/benchmark_chart.svg"
echo ""

if [[ "$KEEP" == "1" ]]; then
  echo "KEEP_SERVER=1: instance $SERVER_ID left running at $IP — delete it when finished." >&2
  trap - EXIT
fi
