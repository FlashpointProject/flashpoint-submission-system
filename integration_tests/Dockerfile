FROM golang:1.26.1
COPY --from=migrate/migrate:v4.18.3 /usr/local/bin/migrate /usr/local/bin/migrate
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Cache compilation in the image; each execution still runs uncached tests.
RUN go test ./integration_tests -run '^$'
ENTRYPOINT ["bash", "/src/integration_tests/runner.sh"]
