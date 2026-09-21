FROM --platform=$BUILDPLATFORM golang:trixie AS go-builder
WORKDIR /src
# Define the build arguments passed from GitHub Actions
ARG APP_VERSION=v0.0.0
ARG APP_COMMIT=unknown
# COMMIT is the CI/docker-build.sh spelling of the same stamp (deploy.yaml
# passes build-arg COMMIT; the Dockerfile previously never declared it, so
# the commit was silently never embedded).
ARG COMMIT=unknown
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
# Custom build: dashboard-tagged, CGO-enabled binary (SQLite token DB) on a
# Debian trixie-slim runtime with TZ data for exact Pacific-midnight math.
RUN set -eux;   \
    export BUILD_DATE="$(date +%Y-%m-%d)";   \
    CGO_ENABLED=1 \
        GOOS=linux \
        go build \
            -buildvcs=false \
            -trimpath \
            -tags dashboard \
            -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" \
            -tags dashboard \
            -o /out/freebucks-proxy ./backend/cmd/freebucks-proxy ;  \
    chmod +x /out/freebucks-proxy

FROM debian:trixie-slim
SHELL ["/bin/bash", "-c"]
ENV TZ="Asia/Jakarta"
RUN set -eux; 	\
    [ ! -f /etc/localtime ] && ln -s /usr/share/zoneinfo/$TZ /etc/localtime; 	\
    echo $TZ > /etc/timezone; 	\
    apt-get update
RUN set -eux;     \
    apt install -y --no-install-recommends \
        tzdata ca-certificates;     \
    apt-mark showmanual > /savedAptMark.txt
RUN set -eux;   \
    apt-mark auto '.*' > /dev/null ;	\
    apt-mark manual $(cat /savedAptMark.txt) > /dev/null; 	\
    apt-get purge -y --auto-remove -o APT::AutoRemove::RecommendsImportant=false;     \
    apt-get clean;     \
    apt-get autoclean;     \
    rm -rf /var/lib/apt/lists/*
    
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
