# Statistics count benchmark

Run inside the isolated snapshot Docker network, with the repository mounted or
copied into a Go container. `POSTGRES_PARITY_DSN` must name the `postgres` service
and a disposable `submission_import_*` database. This command refuses other
hosts/databases and forces read-only transactions.

```sh
go run ./cmd/submission-stats-benchmark \
  -user-id "$BENCHMARK_UPLOADER_ID" \
  -out /results/statistics-counts.json \
  -repetitions 3
```

Choose and record one source uploader ID; it supplies all eight user-statistics
filters. The seven global filters match `GetStatisticsPageData`, and the eight
user filters match `GetUserStatistics`. The report contains counts and timings,
not submission contents. The output path must not exist.

Each workload runs count-only once and three warm repetitions, followed by a
full `SearchSubmissions` call that verifies the total. The report includes the
individual count timings, their warm median, and the single validation-call
time. That validation time is **not** a repeated warm baseline. Count and search
run within one read-only repeatable-read snapshot; any count mismatch or unstable
repetition fails the command. A complete successful report has `complete: true`.

Run separately from search benchmarks and integration tests, without concurrent
DB work. Record machine power state and Docker resource allocation alongside the
report, using the surrounding snapshot benchmark workflow.
