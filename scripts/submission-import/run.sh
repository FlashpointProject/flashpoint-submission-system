#!/usr/bin/env bash
# Each run creates a fresh clone. References are never restored over or reset.
set -euo pipefail
repo="$(cd -- "$(dirname -- "${BASH_SOURCE[0]:-$0}")/../.." && pwd)"
cd "$repo"
base="$repo/snapshot-results"
reference="${SNAPSHOT_RUN:-$(cat "$base/latest")}"
[[ "$reference" == "$base"/run-* && -f "$reference/restores-complete" ]] || { echo 'Completed isolated snapshot restore required.' >&2; exit 1; }
project="$(cat "$reference/project")"
[[ "$project" == fpfss-snapshot-run-* && "$project" != *[!a-z0-9-]* ]] || exit 1
run="$(mktemp -d "$reference/import-XXXXXXXX")"
target="submission_import_$(basename "$run" | tr '[:upper:]-' '[:lower:]_')"
printf '%s\n' "$target" > "$run/target-database"
printf '%s\n' "$run" > "$reference/latest-import"
trap 'status=$?; printf "%s\n" "$status" > "$run/exit-code.txt"; echo "Import artifacts: $run"' EXIT
compose=(docker compose --env-file "$repo/scripts/snapshot-db/snapshot.env" -f "$repo/scripts/snapshot-db/compose.yml" -p "$project")
# Freeze code, migrations and dependency metadata before long clone operations.
inputs="$run/inputs"
mkdir -p "$inputs/cmd/submission-import" "$inputs/scripts/submission-import" "$inputs/postgres_migrations"
cp go.mod go.sum "$inputs/"
cp cmd/submission-import/*.go "$inputs/cmd/submission-import/"
cp scripts/submission-import/Dockerfile scripts/submission-import/*.sh "$inputs/scripts/submission-import/"
for version in 0014 0015 0016 0017; do
  files=(postgres_migrations/"$version"*.up.sql)
  [[ ${#files[@]} == 1 && -f "${files[0]}" ]] || { echo "Exactly one migration $version is required." >&2; exit 1; }
  cp "${files[0]}" "$inputs/postgres_migrations/"
done
shasum -a 256 "$inputs"/cmd/submission-import/*.go "$inputs"/postgres_migrations/*.sql > "$run/source.sha256"
cp "$reference/dumps.sha256" "$run/source-dumps.sha256"
git rev-parse HEAD > "$run/revision.txt"
printf '%s\n' "Cloning existing PostgreSQL into disposable database $target"
# CREATE DATABASE refuses if the source has active connections. Never terminate
# reference sessions to force a clone: stop unrelated snapshot jobs then retry.
"${compose[@]}" exec -T postgres psql -X -v ON_ERROR_STOP=1 -U snapshot_admin -d snapshot_control -c "CREATE DATABASE $target WITH TEMPLATE fpfss OWNER snapshot_admin" > "$run/clone.log" 2>&1
# Preserve the restored catalog; only new submission extension migrations run.
for migration in "$inputs"/postgres_migrations/*.up.sql; do
  "${compose[@]}" exec -T postgres psql -X -v ON_ERROR_STOP=1 --single-transaction -U snapshot_admin -d "$target" < "$migration" >> "$run/migration.log" 2>&1
done
"${compose[@]}" exec -T postgres psql -X -v ON_ERROR_STOP=1 -U snapshot_admin -d "$target" -c 'UPDATE schema_migrations SET version=17, dirty=false' >> "$run/migration.log" 2>&1
image="$project-submission-import"
docker build -f "$inputs/scripts/submission-import/Dockerfile" -t "$image" "$inputs" > "$run/build.log" 2>&1
docker image inspect "$image" --format '{{.Id}}' > "$run/image-id.txt"
mkdir -p "$run/results"
echo "Importing; progress log: $run/import.log"
docker run --rm --network "${project}_default" \
 -e 'SUBMISSION_IMPORT_MARIA_DSN=root:snapshot-root@tcp(mariadb:3306)/fpfss' \
 -e "SUBMISSION_IMPORT_PG_DSN=postgres://snapshot_admin:snapshot-postgres@postgres:5432/$target?sslmode=disable" \
 -v "$run/results:/results" "$image" -out /results > "$run/import.log" 2>&1
# New index statistics must describe imported rows, not the pre-import emptiness.
"${compose[@]}" exec -T postgres psql -X -v ON_ERROR_STOP=1 -U snapshot_admin -d "$target" -c 'ANALYZE submission_cache; ANALYZE curation_meta; ANALYZE masterdb_game;' > "$run/analyze.log" 2>&1
echo "Import passed. Disposable target retained: $target"
