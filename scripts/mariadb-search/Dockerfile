FROM golang:1.26.1
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY activityevents ./activityevents
COPY database ./database
COPY config ./config
COPY constants ./constants
COPY types ./types
COPY utils ./utils
COPY cmd/snapshot-search-baseline ./cmd/snapshot-search-baseline
RUN go build -o /snapshot-search-baseline ./cmd/snapshot-search-baseline
ENTRYPOINT ["/snapshot-search-baseline"]
