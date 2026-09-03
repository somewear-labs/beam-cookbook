#!/bin/sh
set -eu

role="${GRID_ROLE:?GRID_ROLE must be client or target}"
workspace_id="${WORKSPACE_ID:?WORKSPACE_ID is required}"
api_key_file="${BEAM_API_KEY_FILE:-/run/secrets/beam_api_key}"
beam_jar="${BEAM_JAR:-/opt/beam/beam.jar}"

case "$role" in
    client|target) ;;
    *)
        echo "GRID_ROLE must be client or target" >&2
        exit 64
        ;;
esac

if [ ! -r "$api_key_file" ]; then
    echo "Beam API key secret is not readable: $api_key_file" >&2
    exit 66
fi

if [ ! -r "$beam_jar" ]; then
    echo "Beam jar is not readable: $beam_jar" >&2
    exit 66
fi

if grep -q '^[[:space:]]*api-key=' "$api_key_file"; then
    SW_API_KEY="$(
        awk -F= '
            /^[[:space:]]*api-key=/ {
                sub(/^[^=]*=/, "")
                gsub(/^[[:space:]]+|[[:space:]]+$/, "")
                print
                exit
            }
        ' "$api_key_file"
    )"
else
    SW_API_KEY="$(tr -d '\r\n' < "$api_key_file")"
fi
if [ -z "$SW_API_KEY" ]; then
    echo "Beam API key secret is empty" >&2
    exit 65
fi
export SW_API_KEY

beam_pid=""
rpc_pid=""

shutdown() {
    if [ -n "$rpc_pid" ]; then
        kill "$rpc_pid" 2>/dev/null || true
    fi
    if [ -n "$beam_pid" ]; then
        kill "$beam_pid" 2>/dev/null || true
    fi
}
trap shutdown INT TERM EXIT

java "${JAVA_OPTS:--Xmx512m}" \
    -jar "$beam_jar" \
    --non-interactive \
    --log-level "${LOG_LEVEL:-info}" \
    --webhook-address http://127.0.0.1:8080 \
    daemon start &
beam_pid=$!

attempt=0
until curl -fsS http://127.0.0.1:9091/api/health >/dev/null 2>&1; do
    if ! kill -0 "$beam_pid" 2>/dev/null; then
        wait "$beam_pid"
        exit $?
    fi
    attempt=$((attempt + 1))
    if [ "$attempt" -ge 60 ]; then
        echo "Beam did not become healthy within 60 seconds" >&2
        exit 1
    fi
    sleep 1
done

activation_response=/tmp/workspace-activation-response
attempt=0
while :; do
    status="$(
        curl -sS \
            -o "$activation_response" \
            -w '%{http_code}' \
            -H 'Content-Type: application/json' \
            -d "{\"workspaceId\":\"$workspace_id\"}" \
            http://127.0.0.1:9091/api/workspace/activate \
        || true
    )"
    case "$status" in
        2??) break ;;
    esac

    if ! kill -0 "$beam_pid" 2>/dev/null; then
        wait "$beam_pid"
        exit $?
    fi

    attempt=$((attempt + 1))
    if [ "$attempt" -ge 60 ]; then
        echo "Workspace $workspace_id did not activate; last HTTP status=$status" >&2
        sed -n '1,20p' "$activation_response" >&2
        exit 1
    fi
    sleep 1
done

if [ "$role" = "target" ]; then
    grid-remote-shell server \
        --port 8080 \
        --beam-url http://127.0.0.1:9091 \
        --max-response "${MAX_RESPONSE_BYTES:-128}" &
    rpc_pid=$!
fi

echo "Grid harness $role ready; workspace=$workspace_id"

wait "$beam_pid"
