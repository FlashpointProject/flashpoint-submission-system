FROM golang:1.26.1
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go test ./cmd/submission-parity && go build -o /submission-parity ./cmd/submission-parity
ENTRYPOINT ["/submission-parity"]
