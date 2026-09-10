#!/usr/bin/env bash
set -euo pipefail
repo="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$repo"
reference="$(cat snapshot-results/latest)"
[[ "$reference" == "$repo"/snapshot-results/run-* ]] || exit 1
import="$(cat "$reference/latest-import")"
[[ "$import" == "$reference"/import-* ]] || exit 1
parity="$(cat "$import/latest-parity")"
[[ "$parity" == "$import"/parity-* ]] || exit 1
python3 - "$parity/data/report.json" <<'PY'
import json,sys
r=json.load(open(sys.argv[1])); assert r['Complete'] and not r['Error']
PY
project="$(cat "$reference/project")"
target="$(cat "$import/target-database")"
[[ "$project" == fpfss-snapshot-run-* && "$project" != *[!a-z0-9-]* ]] || exit 1
[[ "$target" == submission_import_* && "$target" != *[!a-z0-9_]* ]] || exit 1
run="$(mktemp -d "$import/concurrency-XXXXXXXX")"
trap 'status=$?; printf "%s\n" "$status" > "$run/exit-code.txt"; echo "Concurrency artifacts: $run"' EXIT
mkdir -p "$run/source/cmd"
cp go.mod go.sum "$run/source/"
for directory in activityevents config constants database types utils; do cp -R "$directory" "$run/source/"; done
cp -R cmd/submission-concurrency "$run/source/cmd/"
cp scripts/submission-concurrency/Dockerfile "$run/source/Dockerfile"
cp scripts/submission-concurrency/run.sh "$run/runner.sh"
cp "$reference/search-s620fWEK/workloads.json" "$run/workloads.json"
shasum -a 256 database/*.go cmd/submission-concurrency/*.go postgres_migrations/0017*.sql > "$run/source.sha256"
image="$project-concurrency"
docker build -t "$image" "$run/source" > "$run/build.log" 2>&1
docker image inspect "$image" --format '{{.Id}}' > "$run/image-id.txt"
if command -v pmset >/dev/null 2>&1; then pmset -g batt > "$run/power-before.txt"; fi
docker stats --no-stream --format '{{json .}}' > "$run/stats-before.jsonl"
docker run --rm --network "${project}_default" \
 -e "POSTGRES_PARITY_DSN=postgres://snapshot_admin:snapshot-postgres@postgres:5432/$target?sslmode=disable" \
 -v "$run:/results" "$image" -out /results/report.json -workloads /results/workloads.json -seconds "${CONCURRENCY_SECONDS:-12}" > "$run/progress.log" 2>&1
docker stats --no-stream --format '{{json .}}' > "$run/stats-after.jsonl"
if command -v pmset >/dev/null 2>&1; then pmset -g batt > "$run/power-after.txt"; fi
