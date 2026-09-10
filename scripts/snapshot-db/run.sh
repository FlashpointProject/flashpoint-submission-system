#!/usr/bin/env bash
set -euo pipefail
repo="$(cd -- "$(dirname -- "${BASH_SOURCE[0]:-$0}")/../.." && pwd)"
cd "$repo"
base="$repo/snapshot-results"
compose_file="$repo/scripts/snapshot-db/compose.yml"
env_file="$repo/scripts/snapshot-db/snapshot.env"
action="${1:-status}"
mkdir -p "$base"
if [[ "$action" == restore ]]; then
  maria="${MARIA_DUMP:-}"
  postgres="${PG_DUMP:-}"
  if [[ -z "$maria" ]]; then
    files=("$repo"/fpfss-mariadb-dump-*.sanitized.sql.zst)
    [[ ${#files[@]} == 1 && -f "${files[0]}" ]] || { echo 'Set MARIA_DUMP to one sanitized .sql.zst snapshot.' >&2; exit 1; }
    maria="${files[0]}"
  fi
  if [[ -z "$postgres" ]]; then
    files=("$repo"/fpfss-postgres-dump-*.sql.zst)
    [[ ${#files[@]} == 1 && -f "${files[0]}" ]] || { echo 'Set PG_DUMP to one .sql.zst snapshot.' >&2; exit 1; }
    postgres="${files[0]}"
  fi
  [[ -f "$maria" && "$maria" == *.sanitized.sql.zst && -f "$postgres" ]] || { echo 'Missing dumps or MariaDB input not marked sanitized.' >&2; exit 1; }
  run_dir="$(mktemp -d "$base/run-XXXXXXXX")"
  project="fpfss-snapshot-$(basename "$run_dir" | tr '[:upper:]' '[:lower:]')"
  printf '%s\n' "$project" > "$run_dir/project"
  printf '%s\n' "$run_dir" > "$base/latest"
else
  run_dir="${SNAPSHOT_RUN:-$(cat "$base/latest")}"
  [[ "$run_dir" == "$base"/run-* && -f "$run_dir/project" ]] || { echo 'Invalid snapshot run directory.' >&2; exit 1; }
  project="$(cat "$run_dir/project")"
  [[ "$project" == fpfss-snapshot-run-* && "$project" != *[!a-z0-9-]* ]] || exit 1
fi
compose=(docker compose --env-file "$env_file" -f "$compose_file" -p "$project")
case "$action" in
 restore)
  echo "Restoring isolated snapshot project: $project"
  echo "Artifacts: $run_dir"
  git rev-parse HEAD > "$run_dir/revision.txt"
  shasum -a 256 "$maria" "$postgres" > "$run_dir/dumps.sha256"
  trap 'status=$?; printf "%s\n" "$status" > "$run_dir/restore-exit-code.txt"' EXIT
  "${compose[@]}" up -d --wait --wait-timeout 180 > "$run_dir/startup.log" 2>&1
  zstd -q -dc -- "$maria" | "${compose[@]}" exec -T mariadb sh -c 'exec mariadb --user=root --password="$MARIADB_ROOT_PASSWORD" fpfss' > "$run_dir/restore-maria.log" 2>&1
  "${compose[@]}" exec -T mariadb sh -c 'exec mariadb --batch --skip-column-names --user=root --password="$MARIADB_ROOT_PASSWORD" fpfss -e "SELECT (SELECT COUNT(*) FROM session), (SELECT COUNT(*) FROM oauth_client)"' > "$run_dir/auth-counts.tsv"
  [[ "$(cat "$run_dir/auth-counts.tsv")" == $'0\t0' ]] || { echo 'Restored auth tables are not empty.' >&2; exit 1; }
  touch "$run_dir/mariadb-restored"
  echo 'MariaDB restored; restoring PostgreSQL cluster dump...' 
  zstd -q -dc -- "$postgres" | python3 scripts/snapshot-db/prepare_postgres.py 2> "$run_dir/postgres-stream-adjustments.txt" | "${compose[@]}" exec -T postgres psql -X --set ON_ERROR_STOP=on -U snapshot_admin -d snapshot_control > "$run_dir/restore-postgres.log" 2>&1
  "${compose[@]}" images --format json > "$run_dir/images.json"
  touch "$run_dir/restores-complete"
  echo 'Both snapshots restored; auth tables verified empty.'
  ;;
 cache)
  [[ -f "$run_dir/mariadb-restored" ]] || { echo 'MariaDB restore has not completed successfully.' >&2; exit 1; }
  trap 'status=$?; printf "%s\n" "$status" > "$run_dir/cache-exit-code.txt"' EXIT
  # A fresh full logical clone preserves source data/schema, leaving fpfss untouched.
  "${compose[@]}" exec -T mariadb sh -c 'mariadb -N -u root --password="$MARIADB_ROOT_PASSWORD" -e "SELECT CONCAT(\"CREATE DATABASE snapshot_cache_work_scoped CHARACTER SET \", DEFAULT_CHARACTER_SET_NAME, \" COLLATE \", DEFAULT_COLLATION_NAME) FROM information_schema.SCHEMATA WHERE SCHEMA_NAME=\"fpfss\"" | mariadb -u root --password="$MARIADB_ROOT_PASSWORD"' > "$run_dir/clone-create.log" 2>&1
  "${compose[@]}" exec -T mariadb sh -c 'exec mariadb-dump --single-transaction --routines --triggers -u root --password="$MARIADB_ROOT_PASSWORD" fpfss' 2> "$run_dir/clone-dump.log" | "${compose[@]}" exec -T mariadb sh -c 'exec mariadb -u root --password="$MARIADB_ROOT_PASSWORD" snapshot_cache_work_scoped' > "$run_dir/clone-restore.log" 2>&1
  docker build -f scripts/snapshot-db/Dockerfile -t "$project-cache-audit" . > "$run_dir/cache-build.log" 2>&1
  docker image inspect "$project-cache-audit" --format '{{.Id}}' > "$run_dir/cache-image-id.txt"
  shasum -a 256 cmd/snapshot-cache-audit/*.go > "$run_dir/cache-tool-source.sha256"
  mkdir -p "$run_dir/cache"
  echo "Rebuilding caches on disposable clone; progress in $run_dir/cache-progress.log"
  docker run --rm --network "${project}_default" -e 'SNAPSHOT_CACHE_DSN=root:snapshot-root@tcp(mariadb:3306)/snapshot_cache_work_scoped?parseTime=true&loc=UTC&time_zone=%27%2B00%3A00%27' -v "$run_dir/cache:/results" "$project-cache-audit" -scoped-inputs -out /results > "$run_dir/cache-progress.log" 2>&1
  ;;
 search)
  [[ -f "$run_dir/restores-complete" && -f "$run_dir/cache/cache-reconciliation.json" ]] || { echo 'Restore and cache reconciliation are required first.' >&2; exit 1; }
  [[ "$(cat "$run_dir/cache-exit-code.txt")" == 0 ]] || { echo 'Cache audit must have succeeded.' >&2; exit 1; }
  python3 - "$run_dir/cache/cache-reconciliation.json" <<'PY_CHECK'
import json, sys
r=json.load(open(sys.argv[1]))
p=r.get('Passes', [])
assert len(p)>=2 and all(x['Eligible']==x['Visited']==x['Rebuilt'] and not x['Failures'] for x in p), 'Incomplete cache traversal'
assert p[-1]['Normalized']['submissions']==0, 'Rebuild is not repeatable'
PY_CHECK
  search_dir="$(mktemp -d "$run_dir/search-XXXXXXXX")"
  trap 'status=$?; printf "%s\n" "$status" > "$search_dir/exit-code.txt"' EXIT
  git rev-parse HEAD > "$search_dir/revision.txt"
  shasum -a 256 cmd/snapshot-search-baseline/*.go database/searchmonster.go > "$search_dir/source.sha256"
  git diff -- database types utils scripts/snapshot-db cmd/snapshot-search-baseline > "$search_dir/tracked-changes.patch"
  docker build -f scripts/snapshot-db/Dockerfile -t "$project-cache-audit" . > "$search_dir/build.log" 2>&1
  docker image inspect "$project-cache-audit" --format '{{.Id}}' > "$search_dir/image-id.txt"
  docker info --format '{{json .}}' > "$search_dir/docker-info.json"
  "${compose[@]}" ps -q > "$search_dir/container-ids.txt"
  "${compose[@]}" exec -T mariadb sh -c 'cat /proc/cpuinfo; cat /proc/meminfo; cat /proc/diskstats' > "$search_dir/host-before.txt"
  docker stats --no-stream --format '{{json .}}' $(cat "$search_dir/container-ids.txt") > "$search_dir/stats-before.jsonl"
  echo "Search baseline artifacts: $search_dir"
  docker run --rm --network "${project}_default" --entrypoint /snapshot-search-baseline -e 'SNAPSHOT_SEARCH_DSN=root:snapshot-root@tcp(mariadb:3306)/fpfss' -v "$search_dir:/results" "$project-cache-audit" -out /results -repetitions "${SNAPSHOT_SEARCH_REPETITIONS:-3}" -suite "${SNAPSHOT_SEARCH_SUITE:-standard}" -variants "${SNAPSHOT_SEARCH_VARIANTS:-original,rebuilt}" > "$search_dir/progress.log" 2>&1
  docker stats --no-stream --format '{{json .}}' $(cat "$search_dir/container-ids.txt") > "$search_dir/stats-after.jsonl"
  "${compose[@]}" exec -T mariadb sh -c 'cat /proc/diskstats' > "$search_dir/host-after.txt"
  echo 'Search baseline complete.'
  ;;
 status) "${compose[@]}" ps ;;
 down)
  "${compose[@]}" down --volumes --remove-orphans
  if docker image inspect "$project-cache-audit" >/dev/null 2>&1; then docker image rm "$project-cache-audit"; fi
  ;;
 audit)
  [[ -f "$run_dir/restores-complete" ]] || { echo 'Both restores must complete before the full audit.' >&2; exit 1; }
  python3 scripts/snapshot-audit/audit.py --compose "$compose_file" --env-file "$env_file" --project "$project" --output "$run_dir/audit" --postgres-user snapshot_admin
  ;;
 *) echo 'Usage: run.sh restore|status|audit|cache|search|down' >&2; exit 1 ;;
esac
