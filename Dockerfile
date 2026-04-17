FROM golang:trixie AS builder

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY main.go ./
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o entrypoint .

FROM debian:trixie-slim

LABEL maintainer="jonas-stjernquist" \
      org.opencontainers.image.source="https://github.com/jonas-stjernquist/jottacloud" \
      org.opencontainers.image.description="Dockerized Jottacloud CLI backup client"

VOLUME ["/data"]

ENV JOTTA_TOKEN="**None**" \
    JOTTA_DEVICE="**docker-jottacloud**" \
    JOTTA_CONFIG_SCANINTERVAL="12h" \
    JOTTA_IGNORE_FILE="/config/ignorefile" \
    JOTTA_CONFIG_FILE="/config/jotta-config.env" \
    LOCALTIME="Europe/Stockholm" \
    STARTUP_TIMEOUT=15 \
    JOTTAD_SYSTEMD=0

RUN apt-get update && \
    apt-get upgrade -y && \
    apt-get -y install --no-install-recommends \
      curl ca-certificates psmisc && \
    curl -fsSL https://repo.jotta.cloud/jotta.gpg \
      -o /usr/share/keyrings/jotta.gpg && \
    echo "deb [signed-by=/usr/share/keyrings/jotta.gpg] https://repo.jotta.cloud/debian debian main" \
      > /etc/apt/sources.list.d/jotta-cli.list && \
    apt-get update && \
    apt-get install -y jotta-cli && \
    apt-get autoremove -y --purge && \
    apt-get clean && \
    rm -rf /var/lib/apt/lists/*

COPY --from=builder /build/entrypoint /src/entrypoint

EXPOSE 14443

ENTRYPOINT ["/src/entrypoint"]
