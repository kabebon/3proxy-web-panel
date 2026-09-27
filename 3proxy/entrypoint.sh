#!/bin/sh
set -e

CONFIG="/etc/3proxy/3proxy.cfg"
PID="/var/run/3proxy.pid"

# 3proxy does not live-reload config: SIGHUP makes it exit expecting a supervisor
# restart. So the entrypoint IS the supervisor — restart 3proxy in-place, the
# container (and its ports) never goes down. 3proxy is stateless (config is the
# only state), so SIGKILL + short sleep gives the fastest possible convergence.
_reload() {
    echo "[entrypoint] SIGHUP received, restarting 3proxy to pick up new config..."
    if [ -f "$PID" ]; then
        kill -9 "$(cat $PID)" 2>/dev/null || true
    fi
}

trap '_reload' HUP

echo "[entrypoint] Starting 3proxy supervisor..."
while true; do
    3proxy "$CONFIG" &
    PROXY_PID=$!
    echo $PROXY_PID > "$PID"
    wait "$PROXY_PID" || true
    echo "[entrypoint] 3proxy exited, restarting in 0.3s..."
    rm -f "$PID"
    sleep 0.3
done
