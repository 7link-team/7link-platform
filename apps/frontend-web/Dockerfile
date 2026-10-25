FROM oven/bun:1.3.12@sha256:8956c7667fa17beb6e3c664115e66bdacfe502da5d99603626e74c197bdef160 AS build
WORKDIR /src
COPY . .
RUN bun install --frozen-lockfile
# Keep browser requests same-origin; the runtime proxy selects the API server.
RUN VITE_API_URL=/api bun run --cwd apps/web build

FROM nginxinc/nginx-unprivileged:1.28-alpine@sha256:7377697a821c131a924a7105fafbe7414db4e9fcc77a6f08f776f33f141ec3f8
ARG VERSION=dev
ARG REVISION=unknown
ARG SOURCE
ARG CREATED
LABEL org.opencontainers.image.title="Web console" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${REVISION}" \
      org.opencontainers.image.source="${SOURCE}" \
      org.opencontainers.image.created="${CREATED}"
COPY --from=build /src/apps/web/dist /usr/share/nginx/html
COPY docker/nginx.conf /etc/nginx/nginx.conf
COPY docker/default.conf.template /etc/nginx/templates/default.conf.template
ENV API_UPSTREAM=http://api:8080 \
    NGINX_ENVSUBST_FILTER=API_UPSTREAM \
    NGINX_ENVSUBST_OUTPUT_DIR=/tmp
USER 101:101
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -q -O /dev/null http://127.0.0.1:8080/healthz || exit 1
