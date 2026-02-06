FROM golang:1.24-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal
COPY deploy ./deploy
COPY web ./web

RUN CGO_ENABLED=0 GOOS=linux \
	go build -trimpath -ldflags="-s -w" -o /out/hopshare ./cmd/server

FROM alpine:3.21

RUN apk add --no-cache ca-certificates curl iproute2 procps tzdata

WORKDIR /app

RUN addgroup -S hopshare && adduser -S -D -G hopshare hopshare

COPY --from=build /out/hopshare /app/hopshare
COPY --from=build /src/web/static /app/web/static

ENV HOPSHARE_ADDR=:8080
ENV HOPSHARE_ENV=production

EXPOSE 8080

USER hopshare

ENTRYPOINT ["/app/hopshare"]
