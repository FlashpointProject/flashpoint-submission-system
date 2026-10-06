#!/usr/bin/env bash
# Disposable production-sized MariaDB replay. Never sources the developer .env.
set -euo pipefail
repo="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$repo"
action="${1:-}"
base="$repo/snapshot-results"
mkdir -p "$base"
case "$action" in
  restore)
    dump="${2:?Usage: run.sh restore sanitized.sql.zst}"
    [[ -f "$dump" && "$dump" == *.sanitized.sql.zst ]] || { echo 'Expected a sanitized .sql.zst dump.' >&2; exit 2; }
    run="$(mktemp -d "$base/mariadb-search-XXXXXXXX")"
    project="fpfss-search-$(basename "$run" | tr '[:upper:]' '[:lower:]')"
    printf '%s\n' "$project" > "$run/project"
    ;;
  benchmark|down)
    run="${2:?Usage: run.sh benchmark|down /absolute/run/directory}"
    [[ "$run" == "$base"/mariadb-search-* && -f "$run/project" ]] || { echo 'Invalid snapshot directory.' >&2; exit 2; }
    project="$(cat "$run/project")"
    [[ "$project" == fpfss-search-mariadb-search-* && "$project" != *[!a-z0-9-]* ]] || exit 2
    ;;
  *) echo 'Usage: run.sh restore DUMP | benchmark RUN [LABEL] [GO_FLAGS...] | down RUN' >&2; exit 2;;
esac
compose=(docker compose --env-file "$repo/integration_tests/testenv.env" -f "$repo/scripts/mariadb-search/compose.yml" -p "$project")
case "$action" in
  restore)
    echo "Snapshot artifacts: $run"
    trap 'status=$?; printf "%s\n" "$status" > "$run/restore-exit-code.txt"; if [[ $status != 0 ]]; then "${compose[@]}" logs --no-color > "$run/containers.log" 2>&1 || true; "${compose[@]}" down --volumes > "$run/cleanup.log" 2>&1 || true; fi' EXIT
    shasum -a 256 "$dump" > "$run/dump.sha256"
    "${compose[@]}" up -d --wait --wait-timeout 180 > "$run/startup.log" 2>&1
    zstd -q -dc -- "$dump" | "${compose[@]}" exec -T mariadb mariadb -uroot -psnapshot-root fpfss > "$run/restore.log" 2>&1
    auth="$("${compose[@]}" exec -T mariadb mariadb -N -uroot -psnapshot-root fpfss -e 'SELECT (SELECT COUNT(*) FROM session), (SELECT COUNT(*) FROM oauth_client)')"
    [[ "$auth" == $'0\t0' ]] || { echo 'Authentication tables must be empty.' >&2; exit 1; }
    "${compose[@]}" exec -T mariadb mariadb -N -uroot -psnapshot-root fpfss -e 'SELECT (SELECT COUNT(*) FROM submission), (SELECT COUNT(*) FROM submission_cache), (SELECT COUNT(*) FROM masterdb_game)' > "$run/row-counts.tsv"
    touch "$run/restored"
    ;;
  benchmark)
    [[ -f "$run/restored" ]] || { echo 'Restore has not completed.' >&2; exit 1; }
    label="${3:-benchmark}"
    [[ "$label" != *[!a-zA-Z0-9_-]* && -n "$label" ]] || exit 2
    out="$(mktemp -d "$run/$label-XXXXXXXX")"
    shift $(( $# >= 3 ? 3 : 2 ))
    trap 'status=$?; printf "%s\n" "$status" > "$out/exit-code.txt"' EXIT
    git rev-parse HEAD > "$out/revision.txt"
    git diff -- database cmd/snapshot-search-baseline > "$out/working-tree.patch"
    shasum -a 256 go.mod go.sum activityevents/*.go config/*.go constants/*.go database/*.go types/*.go utils/*.go cmd/snapshot-search-baseline/*.go scripts/mariadb-search/Dockerfile > "$out/source.sha256"
    docker build -f scripts/mariadb-search/Dockerfile -t "$project-benchmark" . > "$out/build.log" 2>&1
    docker image inspect "$project-benchmark" mariadb:11.1.5 > "$out/images.json"
    echo "Benchmark artifacts: $out"
    docker run --rm --network "${project}_default" -e 'SNAPSHOT_SEARCH_DSN=root:snapshot-root@tcp(mariadb:3306)/fpfss' -v "$out:/results" "$project-benchmark" -out /results -variants original "$@" > "$out/progress.log" 2>&1
    ;;
  down)
    "${compose[@]}" down --volumes --remove-orphans
    if docker image inspect "$project-benchmark" >/dev/null 2>&1; then docker image rm "$project-benchmark"; fi
    ;;
esac
