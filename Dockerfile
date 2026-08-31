FROM --platform=$BUILDPLATFORM golang:trixie AS go-builder
WORKDIR /src
# Define the build arguments passed from GitHub Actions
ARG APP_VERSION=v0.0.0
ARG APP_COMMIT=unknown
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN set -eux;   \
    export BUILD_DATE="$(date +%Y-%m-%d)";   \
    CGO_ENABLED=1 \
        GOOS=linux \
        go build \
            -buildvcs=false \
            -trimpath \
            -tags dashboard \
            -ldflags="-s -w -X 'main.Version=${APP_VERSION}' -X 'main.Commit=${APP_COMMIT}' -X 'main.BuildDate=${BUILD_DATE}' -X 'main.Version=${VERSION}'" \
            -tags dashboard \
            -o /out/freebuff-proxy ./backend/cmd/freebuff-proxy ;  \
    chmod +x /out/freebuff-proxy

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
COPY --from=go-builder /out/freebuff-proxy /usr/local/bin/freebuff-proxy
USER app
RUN set -eux; \
    mkdir -p /app/dump /app/logs
EXPOSE 3457
ENTRYPOINT ["/usr/local/bin/freebuff-proxy"]
