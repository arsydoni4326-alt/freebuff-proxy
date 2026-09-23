FROM --platform=$BUILDPLATFORM golang:trixie AS go-builder
WORKDIR /src
# Define the build arguments passed from GitHub Actions
ARG APP_VERSION=v0.0.0
ARG APP_COMMIT=unknown
ARG BUILD_DATE=2025-09-09
# COMMIT is the CI/docker-build.sh spelling of the same stamp (deploy.yaml
# passes build-arg COMMIT; the Dockerfile previously never declared it, so
# the commit was silently never embedded).
ARG COMMIT=unknown
COPY go.mod go.sum ./
# BuildKit cache mounts keep rebuilds fast and the build layer small:
# the module cache survives across builds (no re-download), and the
# go-build cache speeds recompiles without bloating the final image.
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build go mod download
COPY . .
# Custom build: dashboard-tagged, CGO-enabled binary (SQLite token DB) on a
# Debian trixie-slim runtime with TZ data for exact Pacific-midnight math.
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=1 \
        GOOS=linux \
        go build \
            -buildvcs=false \
            -trimpath \
            -tags dashboard \
            -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X 'main.BuildDate=${BUILD_DATE}'" \
            -tags dashboard \
            -o /out/freebucks-proxy ./backend/cmd/freebucks-proxy ;  \
    chmod +x /out/freebucks-proxy

FROM debian:trixie-slim
SHELL ["/bin/bash", "-c"]
ENV TZ="Asia/Jakarta"
RUN set -eux; 	\
    [ ! -f /etc/localtime ] && ln -s /usr/share/zoneinfo/$TZ /etc/localtime; 	\
    echo $TZ > /etc/timezone
    
RUN set -eux; \
    useradd -s /bin/bash -d /app -m app

WORKDIR /app
COPY --from=go-builder /out/freebucks-proxy /usr/local/bin/freebucks-proxy
USER app
RUN set -eux; \
    mkdir -p /app/dump /app/logs
EXPOSE 3457
HEALTHCHECK --interval=30s --timeout=5s --retries=3 --start-period=10s CMD wget -qO- http://127.0.0.1:3457/healthz || exit 1
ENTRYPOINT ["/usr/local/bin/freebucks-proxy"]
