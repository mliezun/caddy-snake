#!/usr/bin/env bash
# Local test script for embed-app. Run after building with ./build.sh.
# Usage: ./test_embed.sh [binary]
set -e

BINARY="${1:-embed-test}"
PORT="${TEST_PORT:-19080}"

if [[ ! -x "$BINARY" ]]; then
    echo "Error: $BINARY not found or not executable. Build first: ./build.sh app.zip 3.13 --output $BINARY"
    exit 1
fi

echo "Starting $BINARY on 127.0.0.1:$PORT..."
./"$BINARY" --listen "127.0.0.1:$PORT" 2>&1 &
PID=$!
trap "kill $PID 2>/dev/null || true" EXIT

echo "Waiting for server and workers..."
for i in $(seq 1 45); do
    RESPONSE=$(curl -s "http://127.0.0.1:$PORT/")
    if echo "$RESPONSE" | grep -q "Replace this placeholder"; then
        echo "Response: $RESPONSE"
        echo "Test passed."
        exit 0
    fi
    sleep 1
done
echo "Timeout waiting for expected response. Last response: $RESPONSE"
exit 1
