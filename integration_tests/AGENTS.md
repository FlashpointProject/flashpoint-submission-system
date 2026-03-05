# Integration Tests Documentation

## Prerequisites

1. **Docker**: Docker must be installed and running.
   - Note: The tests use `docker compose` which requires access to the Docker daemon. If you are not in the `docker` group, you will need to run tests with `sudo`.

## Running Tests

To run the integration tests, navigate to the `integration_tests` directory and use `go test`.

### Command

If you have Docker permissions (user is in `docker` group):
```bash
cd integration_tests
go test -v ./
```

If you require root/sudo for Docker:
```bash
cd integration_tests
sudo -E $(which go) test -v ./
```
*Note: `-E` preserves environment variables, and specifying the full path to `go` might be necessary if it's not in the secure path.*

## Tips for AI Agents

- **Saving test output**: Tests produce very verbose output. Pipe to a temp file and grep over it:
  ```bash
  go test -v ./ 2>&1 | tee /tmp/integration_test_output.txt | grep -E "PASS|FAIL|Error|Messages:"
  ```
  Then inspect specific failures with `grep -B5 -A5 "FAIL" /tmp/integration_test_output.txt`.

## Troubleshooting

- **Permission Denied (Docker)**: Ensure you are using `sudo` if your user lacks direct Docker access.
