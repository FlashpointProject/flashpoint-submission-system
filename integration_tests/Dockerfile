FROM golang:1.27.3-trixie@sha256:af1bfc2614ebb1dbd31af514b78740fd678850e76498762b425ecee4c5c6ea80
COPY --from=migrate/migrate:v4.20.1@sha256:76cc2074cb6642631f34a898ced71e6aeaa6b1a4d78c4daa743275a22e0c5be7 /usr/local/bin/migrate /usr/local/bin/migrate
COPY --from=node:22-bookworm-slim@sha256:5ec23b7c622b00547753bc6ffd78085dd868a68aeb41c8e0a1418f033e13d96d /usr/local/ /usr/local/
WORKDIR /src
COPY integration_tests/browser/package*.json integration_tests/browser/
RUN cd integration_tests/browser && npm ci --ignore-scripts --no-audit --no-fund
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Cache compilation in the image; each execution still runs uncached tests.
RUN go test ./integration_tests -run '^$'
ENTRYPOINT ["bash", "/src/integration_tests/runner.sh"]
