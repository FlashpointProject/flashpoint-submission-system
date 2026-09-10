#!/usr/bin/env bash
set -euo pipefail
repo="$(cd -- "$(dirname -- "${BASH_SOURCE[0]:-$0}")/../.." && pwd)"
cd "$repo"
base="$repo/snapshot-results"
reference="${SNAPSHOT_RUN:-$(cat "$base/latest")}"
[[ "$reference" == "$base"/run-* && -f "$reference/restores-complete" ]] || { echo 'Verified snapshot reference required.' >&2; exit 1; }
import_run="${SUBMISSION_IMPORT_RUN:-$(cat "$reference/latest-import")}"
[[ "$import_run" == "$reference"/import-* && -f "$import_run/results/manifest.json" ]] || { echo 'Completed submission import required.' >&2; exit 1; }
python3 - "$import_run/results/manifest.json" <<'PY'
import json,sys
m=json.load(open(sys.argv[1]))
assert m['Complete'] and not m['Error'], 'Import must have succeeded'
PY
target="$(cat "$import_run/target-database")"
[[ "$target" == submission_import_* && "$target" != *[!a-z0-9_]* ]] || exit 1
project="$(cat "$reference/project")"
[[ "$project" == fpfss-snapshot-run-* && "$project" != *[!a-z0-9-]* ]] || exit 1
standard="${SUBMISSION_STANDARD_BASELINE:-search-yowm3oMb}"
repairs="${SUBMISSION_REPAIR_BASELINE:-search-4B4rfrRq}"
suites=("$standard" "$repairs")
if [[ -n "${SUBMISSION_UI_BASELINE:-}" ]]; then suites+=("$SUBMISSION_UI_BASELINE"); fi
baseline_args=()
for suite in "${suites[@]}"; do
 [[ "$suite" == search-* && "$suite" != */* && -f "$reference/$suite/complete" ]] || { echo 'Completed baseline required.' >&2; exit 1; }
 baseline_args+=(-baseline "/reference/$suite")
done
run="$(mktemp -d "$import_run/parity-XXXXXXXX")"
trap 'status=$?; printf "%s\n" "$status" > "$run/exit-code.txt"; echo "Parity artifacts: $run"' EXIT
printf '%s\n' "$run" > "$import_run/latest-parity"
compose=(docker compose --env-file "$repo/scripts/snapshot-db/snapshot.env" -f "$repo/scripts/snapshot-db/compose.yml" -p "$project")
"${compose[@]}" exec -T postgres psql -X -At -v ON_ERROR_STOP=1 -U snapshot_admin -d "$target" -c 'SELECT version::text || chr(58) || dirty::text FROM schema_migrations' > "$run/migration-state.txt"
[[ "$(cat "$run/migration-state.txt")" == '17:false' ]] || { echo 'Target must be at clean migration 17.' >&2; exit 1; }
pg_container="$("${compose[@]}" ps -q postgres)"
docker inspect "$pg_container" --format '{{.HostConfig.ShmSize}}' > "$run/postgres-shm-bytes.txt"
[[ "$(cat "$run/postgres-shm-bytes.txt")" -ge 1073741824 ]] || { echo 'Recreate the snapshot PostgreSQL container with configured 1 GiB shared memory before parity.' >&2; exit 1; }
docker inspect "$pg_container" --format '{{.Config.Image}} {{.Image}}' > "$run/postgres-image.txt"

# Freeze the code used for this build before long cache traversals/measurements.
mkdir "$run/source"
cp go.mod go.sum "$run/source/"
for directory in activityevents config constants database types utils; do cp -R "$directory" "$run/source/"; done
mkdir -p "$run/source/cmd"
cp -R cmd/submission-parity "$run/source/cmd/"
cp scripts/submission-parity/Dockerfile "$run/source/Dockerfile"
shasum -a 256 cmd/submission-parity/*.go database/*.go postgres_migrations/001[4567]*.sql > "$run/source.sha256"
git rev-parse HEAD > "$run/revision.txt"
image="$project-submission-parity"
docker build -t "$image" "$run/source" > "$run/build.log" 2>&1
docker image inspect "$image" --format '{{.Id}}' > "$run/image-id.txt"
docker info --format '{{json .}}' > "$run/docker-info.json"
"${compose[@]}" ps -q > "$run/container-ids.txt"
docker stats --no-stream --format '{{json .}}' $(cat "$run/container-ids.txt") > "$run/stats-before.jsonl"
rebuild_args=(-rebuild)
case "${SUBMISSION_PARITY_REBUILD:-1}" in
  1) ;;
  0) rebuild_args=() ;;
  *) echo 'SUBMISSION_PARITY_REBUILD must be 0 or 1.' >&2; exit 1 ;;
esac
printf '%s\n' "${SUBMISSION_PARITY_REBUILD:-1}" > "$run/rebuild-requested.txt"
echo "Rebuild and parity progress: $run/progress.log"
if command -v pmset >/dev/null 2>&1; then pmset -g batt > "$run/power-before.txt"; fi
docker run --rm --network "${project}_default" \
 -e "POSTGRES_PARITY_DSN=postgres://snapshot_admin:snapshot-postgres@postgres:5432/$target?sslmode=disable" \
 -e 'MARIADB_PARITY_CACHE_DSN=root:snapshot-root@tcp(mariadb:3306)/snapshot_cache_work_scoped' \
 -v "$reference:/reference:ro" -v "$run:/results" "$image" \
 "${baseline_args[@]}" \
 -out /results/data "${rebuild_args[@]}" -repetitions "${SUBMISSION_PARITY_REPETITIONS:-3}" > "$run/progress.log" 2>&1
docker stats --no-stream --format '{{json .}}' $(cat "$run/container-ids.txt") > "$run/stats-after.jsonl"
if command -v pmset >/dev/null 2>&1; then pmset -g batt > "$run/power-after.txt"; fi
echo 'PostgreSQL cache and search parity completed.'
