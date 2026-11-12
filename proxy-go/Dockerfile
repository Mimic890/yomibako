# syntax=docker/dockerfile:1
#
# Yomibako — runtime proxy for BookOrbit.
#
# The binary itself is a static Go program, but it shells out to two things, so the
# runtime image is Alpine + Node instead of distroless:
#   * `docker`     — to start/stop the miwayomi container through the mounted socket;
#   * `lnreader`   — @lnreader/cli, one process per LNReader search/download.

# ---- Stage 1: build the static binary ----
FROM golang:1.27-alpine AS build

WORKDIR /src
ENV CGO_ENABLED=0 \
    GOOS=linux

# No third-party modules (see go.mod), so there is nothing to download here.
COPY go.mod ./
RUN go mod download

COPY *.go ./
RUN go build -trimpath -ldflags="-s -w" -o /out/yomibako .

# ---- Stage 2: runtime ----
FROM node:22-alpine AS runtime

# docker-cli: talks to the Docker socket mounted by compose.
# tini:        PID 1 that reaps the lnreader children.
# ca-certs:    HTTPS to sources and to miwayomi.
RUN apk add --no-cache ca-certificates docker-cli tini \
    && npm install --global --no-audit --no-fund @lnreader/cli@0.1.1 \
    && npm cache clean --force \
    && lnreader --version

# Non-root runtime user; the code inside is fine but the sources are third-party.
ARG UID=10001
ARG GID=10001
RUN addgroup -g ${GID} -S yomibako \
    && adduser -u ${UID} -S -D -G yomibako -h /home/yomibako yomibako \
    && mkdir -p /etc/yomibako /home/yomibako/.lnreader \
    && chown -R ${UID}:${GID} /home/yomibako

COPY --from=build /out/yomibako /usr/local/bin/yomibako
# Container defaults: listen on 0.0.0.0, reach miwayomi by service name.
# Mount your own file at /config/config.json and point YOMIBAKO_CONFIG at it to override.
COPY config.docker.json /etc/yomibako/config.json

ENV YOMIBAKO_CONFIG=/etc/yomibako/config.json \
    YOMIBAKO_HOST=0.0.0.0 \
    YOMIBAKO_PORT=7345 \
    HOME=/home/yomibako \
    NODE_ENV=production

WORKDIR /home/yomibako
USER ${UID}:${GID}

EXPOSE 7345

# /health starts nothing, so it is safe to poll and needs no token.
HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
    CMD wget -q -O /dev/null http://127.0.0.1:7345/health || exit 1

ENTRYPOINT ["/sbin/tini", "--"]
CMD ["/usr/local/bin/yomibako"]
