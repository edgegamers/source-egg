FROM golang:1.27.2-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/sourceegg ./cmd/sourceegg \
 && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/sourcenode ./cmd/sourcenode

FROM debian:bookworm-slim
LABEL org.opencontainers.image.source="https://github.com/edgegamers/source-egg"
LABEL org.opencontainers.image.licenses="GPL-3.0-or-later"
RUN dpkg --add-architecture i386 \
 && apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl tar bzip2 xz-utils \
    lib32gcc-s1 lib32stdc++6 libc6:i386 libcurl4 libcurl3-gnutls:i386 libtinfo6:i386 \
 && rm -rf /var/lib/apt/lists/* \
 && useradd --create-home --home-dir /home/container --uid 1000 --shell /bin/bash container \
 && mkdir -p /opt/steamcmd /opt/source-shared /opt/source-frameworks /run/source-node \
 && curl -fsSL https://steamcdn-a.akamaihd.net/client/installer/steamcmd_linux.tar.gz -o /tmp/steamcmd.tar.gz \
 && tar -xzf /tmp/steamcmd.tar.gz -C /opt/steamcmd \
 && rm /tmp/steamcmd.tar.gz \
 && chown -R container:container /opt/steamcmd
USER container
ENV USER=container HOME=/home/container SOURCE_GAME="CS:S" SOURCE_MODE=auto SERVER_PORT=27015
RUN /opt/steamcmd/steamcmd.sh +quit
COPY --from=build /out/sourceegg /out/sourcenode /usr/local/bin/
WORKDIR /home/container
STOPSIGNAL SIGTERM
ENTRYPOINT ["/usr/local/bin/sourceegg"]
