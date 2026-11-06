FROM golang:1.27.3-trixie@sha256:af1bfc2614ebb1dbd31af514b78740fd678850e76498762b425ecee4c5c6ea80 AS build

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
