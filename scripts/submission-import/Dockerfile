FROM golang:1.26.1 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/submission-import ./cmd/submission-import
RUN go test ./cmd/submission-import && CGO_ENABLED=0 go build -o /submission-import ./cmd/submission-import
FROM debian:bookworm-slim
COPY --from=build /submission-import /submission-import
ENTRYPOINT ["/submission-import"]
