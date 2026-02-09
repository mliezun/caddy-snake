#!/usr/bin/env bash
set -euo pipefail

WORKSPACE="/workspace"
BENCH_DIR="${WORKSPACE}/benchmarks"
RESULTS_FILE="${BENCH_DIR}/results.json"

export PATH="/usr/local/go/bin:$HOME/go/bin:$PATH"
export DEBIAN_FRONTEND=noninteractive

echo "============================================"
echo " Caddy Snake Benchmarks"
echo "============================================"

# Install system packages
echo ">>> Installing dependencies..."
apt-get update -yyqq
apt-get install -yyqq software-properties-common curl gcc build-essential ca-certificates git pkg-config jq bc

# Install Python 3.13
add-apt-repository -y ppa:deadsnakes/ppa
apt-get update -yyqq
apt-get install -yyqq python3.13-dev python3.13-venv

# Set up python pkgconfig
PKGCONFIG_DIR=$(find /usr/lib -type d -name pkgconfig -path "*linux-gnu*" | head -1)
PC_FILE=$(find "${PKGCONFIG_DIR}" -name "python-3.13*embed.pc" | head -1)
if [[ -n "$PC_FILE" ]]; then
    cp "$PC_FILE" "${PKGCONFIG_DIR}/python3-embed.pc"
fi

# Install Go
echo ">>> Installing Go..."
ARCH=$(dpkg --print-architecture)
if [[ "$ARCH" == "arm64" ]]; then
    GO_ARCH="arm64"
else
    GO_ARCH="amd64"
fi
curl -fsSL "https://go.dev/dl/go1.25.0.linux-${GO_ARCH}.tar.gz" -o /tmp/go.tar.gz
tar -C /usr/local -xzf /tmp/go.tar.gz
go version

# Install xcaddy and hey
echo ">>> Installing xcaddy and hey..."
go install github.com/caddyserver/xcaddy/cmd/xcaddy@latest
go install github.com/rakyll/hey@latest

# Set up Python venv
echo ">>> Setting up Python environment..."
cd "$BENCH_DIR"
python3.13 -m venv venv
source venv/bin/activate
pip install -r requirements.txt

# Build caddy with caddy-snake
echo ">>> Building caddy with caddy-snake..."
CADDY="/usr/local/bin/caddy-snake-bench"
cd /tmp
CGO_ENABLED=1 xcaddy build --with github.com/mliezun/caddy-snake="$WORKSPACE" --output "$CADDY"
cd "$BENCH_DIR"
echo "    Caddy built at $CADDY"

HEY="$HOME/go/bin/hey"

# Initialize results
echo '{}' > "$RESULTS_FILE"

run_benchmark() {
    local name="$1"
    local setup_cmd="$2"
    local teardown_cmd="$3"

    echo ""
    echo ">>> Running benchmark: $name"
    echo "-------------------------------------------"

    eval "$setup_cmd"

    # Wait for server to be ready
    echo "    Waiting for server..."
    for i in $(seq 1 30); do
        if curl -s http://localhost:9080/hello > /dev/null 2>&1; then
            break
        fi
        sleep 1
    done

    # Warmup
    echo "    Warming up..."
    $HEY -c 10 -n 200 http://localhost:9080/hello > /dev/null 2>&1
    sleep 2

    # Run benchmark
    echo "    Running benchmark (100 concurrent, 10s)..."
    local hey_output_file="${BENCH_DIR}/hey_output_${name}.txt"
    $HEY -c 100 -z 10s http://localhost:9080/hello > "$hey_output_file" 2>&1

    # Parse results (hey uses %% in some output modes)
    local rps avg_latency p99_latency
    rps=$(grep "Requests/sec:" "$hey_output_file" | awk '{print $2}' || echo "0")
    avg_latency=$(grep "Average" "$hey_output_file" | head -1 | awk '{print $2}' || echo "0")
    p99_latency=$(grep -E "99%[%]? in" "$hey_output_file" | awk '{print $3}' || echo "0")
    : "${rps:=0}" "${avg_latency:=0}" "${p99_latency:=0}"

    # Handle NaN or empty values
    [[ "$rps" == "NaN" || -z "$rps" ]] && rps="0"
    [[ "$avg_latency" == "NaN" || -z "$avg_latency" ]] && avg_latency="0"
    [[ "$p99_latency" == "NaN" || -z "$p99_latency" ]] && p99_latency="0"

    echo "    Results: ${rps} req/s, avg ${avg_latency}s, p99 ${p99_latency}s"

    # Convert to ms
    local avg_ms p99_ms
    avg_ms=$(python3 -c "print(round(float('${avg_latency}') * 1000, 3))")
    p99_ms=$(python3 -c "print(round(float('${p99_latency}') * 1000, 3))")

    # Save to results file
    local tmp
    tmp=$(mktemp)
    jq --arg name "$name" \
       --argjson rps "$rps" \
       --argjson avg "$avg_ms" \
       --argjson p99 "$p99_ms" \
       '.[$name] = {"requests_per_sec": $rps, "avg_latency_ms": $avg, "p99_latency_ms": $p99}' \
       "$RESULTS_FILE" > "$tmp"
    mv "$tmp" "$RESULTS_FILE"

    # Teardown
    eval "$teardown_cmd"
    sleep 2
}

# Benchmark 1: Flask + Gunicorn + Caddy reverse proxy
run_benchmark "flask_gunicorn_caddy" \
    'cd $BENCH_DIR && source venv/bin/activate && gunicorn -w 1 --threads 4 -b 0.0.0.0:8000 app_flask:app > /dev/null 2>&1 &
     sleep 3
     $CADDY run --config Caddyfile.flask-reverseproxy > /dev/null 2>&1 &
     sleep 3' \
    'pkill -f "caddy-snake-bench" || true; pkill -f gunicorn || true; sleep 2'

# Benchmark 2: Flask + Caddy Snake
run_benchmark "flask_caddysnake" \
    'cd $BENCH_DIR && $CADDY run --config Caddyfile.flask-caddysnake > /tmp/caddy-flask.log 2>&1 &
     sleep 5' \
    'pkill -f "caddy-snake-bench" || true; sleep 2; cat /tmp/caddy-flask.log || true'

# Benchmark 3: FastAPI + Uvicorn + Caddy reverse proxy
run_benchmark "fastapi_uvicorn_caddy" \
    'cd $BENCH_DIR && source venv/bin/activate && uvicorn app_fastapi:app --host 0.0.0.0 --port 8000 --workers 1 > /dev/null 2>&1 &
     sleep 3
     $CADDY run --config Caddyfile.fastapi-reverseproxy > /dev/null 2>&1 &
     sleep 3' \
    'pkill -f "caddy-snake-bench" || true; pkill -f uvicorn || true; sleep 2'

# Benchmark 4: FastAPI + Caddy Snake
run_benchmark "fastapi_caddysnake" \
    'cd $BENCH_DIR && $CADDY run --config Caddyfile.fastapi-caddysnake > /tmp/caddy-fastapi.log 2>&1 &
     sleep 5' \
    'pkill -f "caddy-snake-bench" || true; sleep 2; cat /tmp/caddy-fastapi.log || true'

echo ""
echo "============================================"
echo " Results Summary"
echo "============================================"
echo ""
cat "$RESULTS_FILE" | jq .

echo ""
echo ">>> Generating chart..."
source venv/bin/activate
pip install matplotlib
python3 generate_chart.py

echo ""
echo ">>> Done! Results saved to $RESULTS_FILE"
echo ">>> Chart saved to $BENCH_DIR/benchmark_chart.png"
