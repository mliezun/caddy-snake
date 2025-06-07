#!/bin/bash

set -euo pipefail

HOST=localhost
DURATION=20s
THREADS=2
CONNECTIONS=20
RESULTS_FILE="results.txt"


function run_benchmark() {
    SERVER_CMD="$1"
    SERVER_NAME="$2"
    PORT="$3"
    URL="http://$HOST:$PORT/pastes"
    LOG_FILE="${SERVER_NAME}_log.txt"
    echo "=== Starting $SERVER_NAME ===" | tee -a "$RESULTS_FILE"
    # Start server in background
    eval "$SERVER_CMD" > "$LOG_FILE" 2>&1 &
    # Wait for server to be ready
    sleep 10
    for i in {1..30}; do
        if curl -s "$URL" -o /dev/null; then
            break
        fi
        sleep 1
    done
    echo "Running wrk benchmark on $SERVER_NAME..." | tee -a "$RESULTS_FILE"
    wrk -t$THREADS -c$CONNECTIONS -d$DURATION -s "wrk_post.lua" "$URL" | tee -a "$RESULTS_FILE"
    # Find and kill the server process by command line
    ps aux | grep "$SERVER_CMD" | grep -v grep | awk '{print $2}' | xargs -r kill
    echo "=== Finished $SERVER_NAME ===" | tee -a "$RESULTS_FILE"
}

# Clean previous results
rm -f "$RESULTS_FILE"

run_benchmark "caddy run --config Caddyfile" "caddy" 9080
run_benchmark "uvicorn main:app --host 0.0.0.0 --port 9081" "uvicorn" 9081
run_benchmark "hypercorn main:app --bind 0.0.0.0:9082" "hypercorn" 9082
run_benchmark "granian --interface asgi --host 0.0.0.0 --port 9083 main:app" "granian" 9083

echo "=== Benchmark Summary ===" | tee -a "$RESULTS_FILE"
grep -E 'Running wrk benchmark|Requests/sec|Latency' "$RESULTS_FILE" | tee -a "$RESULTS_FILE"

# Print best performer
BEST=$(grep 'Requests/sec' "$RESULTS_FILE" | awk '{print $2" "$3}' | sort -nr | head -1)
BEST_SERVER=$(grep -B2 "$BEST" "$RESULTS_FILE" | head -1 | awk '{print $3}')
echo -e "Best performer: $BEST_SERVER with $BEST requests/sec" | tee -a "$RESULTS_FILE"
