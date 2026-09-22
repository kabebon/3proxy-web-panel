#!/bin/sh
set -e

CONFIG="/etc/3proxy/3proxy.cfg"
PID="/var/run/3proxy.pid"

# Handle SIGHUP — reload config
_reload() {
    echo "[entrypoint] Received SIGHUP, reloading 3proxy..."
    if [ -f "$PID" ]; then
        kill -HUP "$(cat $PID)" 2>/dev/null || true
    fi
}

trap '_reload' HUP

echo "[entrypoint] Starting 3proxy..."
exec 3proxy "$CONFIG" &
PROXY_PID=$!
echo $PROXY_PID > "$PID"

# Wait for the background process
wait $PROXY_PID
