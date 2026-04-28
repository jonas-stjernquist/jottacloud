# Pin Go toolchain to match go.mod (see go 1.26.2 directive). Bump the tag
# and go.mod together; Dependabot covers the go module graph but not this
# FROM line, so the version lives here explicitly.
FROM golang:1.26.2-trixie AS builder

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY main.go ./
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o entrypoint .

# Runtime base: pinned to the trixie-slim point release currently supported
# by repo.jotta.cloud's apt repo. The weekly scheduled rebuild in
# .github/workflows/build.yml picks up OS patches.
FROM debian:trixie-slim

LABEL maintainer="jonas-stjernquist" \
      org.opencontainers.image.source="https://github.com/jonas-stjernquist/jottacloud" \
      org.opencontainers.image.description="Dockerized Jottacloud CLI backup client"

VOLUME ["/data"]

ENV JOTTA_DEVICE="**docker-jottacloud**" \
    LOCALTIME="Europe/Stockholm" \
    STARTUP_TIMEOUT=60 \
    BOOTSTRAP_TIMEOUT=60 \
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

HEALTHCHECK --interval=30s --timeout=10s --start-period=45s --retries=3 \
  CMD /src/entrypoint healthcheck

ENTRYPOINT ["/src/entrypoint"]
