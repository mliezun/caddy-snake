#!/bin/bash

set -euo pipefail

SIZE=$1

HOST="[2001:bc8:1210:6e9e:dc00:ff:fe7a:770f]"
DURATION=20s
THREADS=2
CONNECTIONS=20
RESULTS_FILE="results.txt"


function run_benchmark() {
    SERVER_NAME="$1"
    PORT="$2"
    URL="http://$HOST:$PORT/pastes"
    LOG_FILE="${SERVER_NAME}_log.txt"
    echo "Running wrk benchmark on $SERVER_NAME..." | tee -a "$RESULTS_FILE"
    wrk -t$THREADS -c$CONNECTIONS -d$DURATION -s "wrk_post_$SIZE.lua" "$URL" | tee -a "$RESULTS_FILE"
}

# Clean previous results
rm -f "$RESULTS_FILE"

run_benchmark "caddy" 9080
run_benchmark "uvicorn" 9081
run_benchmark "hypercorn" 9082
run_benchmark "granian" 9083

echo "=== Benchmark Summary ===" | tee -a "$RESULTS_FILE"
grep -E 'Running wrk benchmark|Requests/sec|Latency' "$RESULTS_FILE" | tee -a "$RESULTS_FILE"
