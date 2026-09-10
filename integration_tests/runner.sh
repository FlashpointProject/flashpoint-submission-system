#!/usr/bin/env bash
set -euo pipefail
cd /src
go version > /results/runtime.txt
sha256sum migrations/*.up.sql postgres_migrations/*.up.sql > /results/migrations.sha256
find . -type f -print0 | sort -z | xargs -0 sha256sum > /results/source.sha256
set +e
go test ./integration_tests -count=1 -p=1 -parallel=1 -failfast -timeout=20m \
  -coverpkg=./database,./types,./service,./transport \
  -coverprofile=/results/coverage.out -json "$@" > /results/tests.jsonl 2>&1
status=$?
set -e
if [[ $status == 0 ]] && ! grep -q '"Action":"run"' /results/tests.jsonl; then
  echo 'No tests executed; refusing a successful empty test run.' >&2
  status=1
fi
if [[ -s /results/coverage.out ]]; then
  go tool cover -func=/results/coverage.out > /results/coverage.txt
fi
cat /results/tests.jsonl
exit "$status"
