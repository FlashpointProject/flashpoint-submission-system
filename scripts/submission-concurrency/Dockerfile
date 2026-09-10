FROM golang:1.26.1
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go test ./cmd/submission-concurrency && go build -o /probe ./cmd/submission-concurrency
ENTRYPOINT ["/probe"]
