FROM golang:1.26.1 as build

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . /app

ARG GIT_COMMIT="git rev-list -1 HEAD"
ENV GIT_COMMIT=$GIT_COMMIT

RUN CGO_ENABLED=0 GOOS=linux go build -o /fpfss ./main/*.go


FROM alpine:3.23 as deploy
ENV GIT_COMMIT="its fucked"
COPY --from=build /fpfss /fpfss
COPY /templates /templates
COPY /static /static
COPY /docs /docs
CMD ["/fpfss"]
