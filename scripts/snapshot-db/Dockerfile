FROM golang:1.26.1
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o /snapshot-cache-audit ./cmd/snapshot-cache-audit && go build -o /snapshot-search-baseline ./cmd/snapshot-search-baseline
ENTRYPOINT ["/snapshot-cache-audit"]
