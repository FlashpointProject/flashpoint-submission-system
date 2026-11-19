FROM golang:1.27.4-trixie@sha256:f18c3d73a184fb0582828510722e723b63a93f7188d11dd223d9be6f6880e65d AS build

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . /app

ARG GIT_COMMIT=unknown
ENV GIT_COMMIT=$GIT_COMMIT

RUN CGO_ENABLED=0 GOOS=linux go build -o /fpfss ./main/*.go


FROM alpine:3.24.4@sha256:93b41ef40b1677fce8fcc64ff4af09daddfacf5040afde618bc46c1e2d1cc7d4 AS deploy
ARG GIT_COMMIT=unknown
ENV GIT_COMMIT=$GIT_COMMIT
COPY --from=build /fpfss /fpfss
COPY /templates /templates
COPY /static /static
COPY /docs /docs
COPY /linkpreview/fonts/LICENSE /licenses/liberation-fonts.txt
COPY /linkpreview/fonts/LICENSE-NotoSansCJK /licenses/noto-sans-cjk-fonts.txt
CMD ["/fpfss"]
