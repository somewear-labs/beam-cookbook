#!/bin/sh
set -eu

script_directory=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
compose_file="$script_directory/compose.yaml"
environment_file="$script_directory/.env"
output_file=$(mktemp "${TMPDIR:-/tmp}/grid-file-upload-test.XXXXXX")
trap 'rm -f "$output_file"' EXIT INT TERM

target_account_id=$(
    docker compose \
        --env-file "$environment_file" \
        -f "$compose_file" \
        exec -T grid-client sh -lc '
            exec grid-remote-shell nmap \
                --beam-url http://127.0.0.1:9091 \
                --webhook-port 8080 \
                --timeout 3s
        ' \
    | awk '$1 == "grid-target" { print $2; exit }'
)
case "$target_account_id" in
    ''|*[!0-9]*)
        echo "Could not discover the grid-target account." >&2
        exit 1
        ;;
esac
printf "Discovered grid-target account %s.\n" "$target_account_id"

docker compose \
    --env-file "$environment_file" \
    -f "$compose_file" \
    exec -T -e "GRID_TEST_TARGET_ACCOUNT_ID=$target_account_id" grid-client sh -lc '
        set -eu

        local_file=/tmp/grid-hello-world.sh
        printf "%s\n" \
            "#!/bin/sh" \
            "printf '\''GRID_FILE_UPLOAD_HELLO\\n'\''" \
            > "$local_file"
        chmod 700 "$local_file"

        printf "%s\n" \
            "/put $local_file" \
            "sh /tmp/grid-remote-shell-uploads/*/grid-hello-world.sh" \
            "exit" \
        | exec grid-remote-shell shell \
            --target-user "$GRID_TEST_TARGET_ACCOUNT_ID" \
            --beam-url http://127.0.0.1:9091 \
            --webhook-port 8080 \
            --timeout 30s
    ' | tee "$output_file"

grep -F "Uploaded /tmp/grid-hello-world.sh ->" "$output_file" >/dev/null
grep -F "GRID_FILE_UPLOAD_HELLO" "$output_file" >/dev/null
printf "Grid file upload hello-world test passed.\n"
