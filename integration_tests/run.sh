#!/usr/bin/env bash
set -euo pipefail
SUBMISSION_DB_ENGINE="${SUBMISSION_DB_ENGINE:-mariadb}"
case "$SUBMISSION_DB_ENGINE" in mariadb|postgres) ;; *) echo 'SUBMISSION_DB_ENGINE must be mariadb or postgres' >&2; exit 2;; esac
export SUBMISSION_DB_ENGINE
test_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
mkdir -p "$test_dir/results"
FPFSS_TEST_RESULTS="$(mktemp -d "$test_dir/results/run-XXXXXXXX")"
run_id="$(basename "$FPFSS_TEST_RESULTS" | tr '[:upper:]' '[:lower:]' | tr -cd '[:alnum:]')"
FPFSS_TEST_DB_NAME="fpfss_test_$run_id"
export FPFSS_TEST_RESULTS FPFSS_TEST_DB_NAME
project="fpfss-test-$run_id"
# --env-file prevents Compose from loading the developer's .env.
compose=(docker compose --env-file "$test_dir/testenv.env" -f "$test_dir/dc-db.yml" -p "$project")
step_pid=""
run_step() {
  "$@" &
  step_pid=$!
  wait "$step_pid"
  step_pid=""
}
cleanup() {
  status=$?
  trap - EXIT INT TERM
  if [[ -n "$step_pid" ]]; then
    kill "$step_pid" 2>/dev/null || true
    wait "$step_pid" 2>/dev/null || true
  fi
  "${compose[@]}" logs --no-color > "$FPFSS_TEST_RESULTS/containers.log" 2>&1 || true
  "${compose[@]}" ps -a --format json > "$FPFSS_TEST_RESULTS/containers.json" 2>&1 || true
  if ! "${compose[@]}" down --volumes --remove-orphans > "$FPFSS_TEST_RESULTS/cleanup.log" 2>&1; then
    echo "Cleanup failed; see $FPFSS_TEST_RESULTS/cleanup.log" >&2
    if [[ $status == 0 ]]; then status=1; fi
  fi
  # Only remove this run's built image, never shared database images/caches.
  if docker image inspect "$FPFSS_TEST_DB_NAME-runner" >/dev/null 2>&1; then
    if ! docker image rm "$FPFSS_TEST_DB_NAME-runner" >> "$FPFSS_TEST_RESULTS/cleanup.log" 2>&1; then
      echo "Image cleanup failed; see $FPFSS_TEST_RESULTS/cleanup.log" >&2
      if [[ $status == 0 ]]; then status=1; fi
    fi
  fi
  printf '%s\n' "$status" > "$FPFSS_TEST_RESULTS/exit-code.txt"
  echo "Test artifacts: $FPFSS_TEST_RESULTS"
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
{
  printf 'project=%s\ndatabase=%s\nsubmission_engine=%s\n' "$project" "$FPFSS_TEST_DB_NAME" "$SUBMISSION_DB_ENGINE"
  git -C "$test_dir/.." rev-parse HEAD
  git -C "$test_dir/.." status --short
  printf 'go test arguments:'
  printf ' %q' "$@"
  printf '\n'
  docker version --format '{{.Server.Version}}'
  docker compose version
} > "$FPFSS_TEST_RESULTS/manifest.txt"
echo "Running isolated integration tests: $project"
run_step "${compose[@]}" build tests > "$FPFSS_TEST_RESULTS/build.log" 2>&1
run_step "${compose[@]}" up -d --wait --wait-timeout 150 database postgres > "$FPFSS_TEST_RESULTS/startup.log" 2>&1
run_step "${compose[@]}" run --rm -T migrate-maria > "$FPFSS_TEST_RESULTS/migrate-maria.log" 2>&1
# Source migrations inherited historical defaults. Match the audited production
# OAuth exception explicitly in this disposable harness, without rewriting them.
run_step "${compose[@]}" exec -T database mariadb -ufpfss -ppassword "$FPFSS_TEST_DB_NAME" \
  -e 'ALTER TABLE oauth_client MODIFY client_id VARCHAR(36) CHARACTER SET utf8mb4 COLLATE utf8mb4_general_ci NOT NULL, MODIFY client_secret TEXT CHARACTER SET utf8mb4 COLLATE utf8mb4_general_ci NOT NULL' \
  > "$FPFSS_TEST_RESULTS/source-collations.log" 2>&1
run_step "${compose[@]}" run --rm -T migrate-postgres > "$FPFSS_TEST_RESULTS/migrate-postgres.log" 2>&1
"${compose[@]}" images --format json > "$FPFSS_TEST_RESULTS/images.json"
docker image inspect mariadb:11.1.5 postgres:15 "$FPFSS_TEST_DB_NAME-runner" > "$FPFSS_TEST_RESULTS/image-metadata.json"
run_step "${compose[@]}" run --rm -T tests "$@" > "$FPFSS_TEST_RESULTS/runner.log" 2>&1
echo "Integration tests passed."
