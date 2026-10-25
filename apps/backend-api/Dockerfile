FROM golang:1.26.2-alpine@sha256:f85330846cde1e57ca9ec309382da3b8e6ae3ab943d2739500e08c86393a21b1 AS build
RUN apk add --no-cache build-base ca-certificates tzdata
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY pkg ./pkg
ARG VERSION=dev
# gorm.io/driver/sqlite uses mattn/go-sqlite3; CGO is required.
RUN CGO_ENABLED=1 go build -p 1 -trimpath -buildvcs=false \
    -ldflags="-s -w -X 7link.com/platform/cmd/7link-api/cmd.buildVersion=${VERSION}" \
    -o /out/api ./cmd/7link-api

FROM alpine:3.23@sha256:ff7f0c727dd796aa36787bfcd9a64bf35a6ce72a73451b3359fde849b6703d63
ARG VERSION=dev
ARG REVISION=unknown
ARG SOURCE
ARG CREATED
LABEL org.opencontainers.image.title="API service" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${REVISION}" \
      org.opencontainers.image.source="${SOURCE}" \
      org.opencontainers.image.created="${CREATED}"
RUN addgroup -S -g 10001 service && adduser -S -D -H -u 10001 -G service service \
    && mkdir /data && chown 10001:10001 /data
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=build /out/api /usr/local/bin/api
WORKDIR /data
USER 10001:10001
ENV HTTP_SERVER_PORT=8080
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=60s --retries=3 \
    CMD wget -q -O /dev/null "http://127.0.0.1:${HTTP_SERVER_PORT}/healthz" || exit 1
ENTRYPOINT ["/usr/local/bin/api"]
CMD ["serve"]
