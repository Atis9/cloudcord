# Official multi-arch image manifests are pinned; APT uses an immutable snapshot.
FROM golang:1.27.1-trixie@sha256:3b77fc618ec235a1ab412de7737f120dd507c57e8d87de4cbb7994fb94275ed5 AS build
ARG DEBIAN_SNAPSHOT=20261003T000000Z
RUN rm -f /etc/apt/sources.list.d/debian.sources \
 && printf 'deb [check-valid-until=no] http://snapshot.debian.org/archive/debian/%s/ trixie main\n' "$DEBIAN_SNAPSHOT" > /etc/apt/sources.list \
 && apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl unzip pkg-config ffmpeg \
 && rm -rf /var/lib/apt/lists/*
WORKDIR /src
COPY scripts/install-libdave.sh /src/scripts/install-libdave.sh
RUN /src/scripts/install-libdave.sh /opt/libdave
ENV CGO_ENABLED=1 PKG_CONFIG_PATH=/opt/libdave/lib/pkgconfig FFMPEG_PATH=/usr/bin/ffmpeg
COPY go.mod go.sum ./
RUN go mod download && go mod verify
COPY cmd ./cmd
COPY internal ./internal
FROM build AS test
RUN go test -race -timeout 60s ./... && go vet ./...
FROM test AS binary
RUN go build -trimpath -buildvcs=false -o /out/cloudcord ./cmd/cloudcord

FROM debian:trixie-slim@sha256:a99cfc517144bc59b1978475ec53b46ecabec7e43635402ee5b77cc54cd1b20a AS runtime
LABEL org.opencontainers.image.source=https://github.com/Atis9/cloudcord
ARG DEBIAN_SNAPSHOT=20261003T000000Z
RUN rm -f /etc/apt/sources.list.d/debian.sources \
 && printf 'deb [check-valid-until=no] http://snapshot.debian.org/archive/debian/%s/ trixie main\n' "$DEBIAN_SNAPSHOT" > /etc/apt/sources.list \
 && apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates ffmpeg \
 && rm -rf /var/lib/apt/lists/*
COPY --from=binary /out/cloudcord /usr/local/bin/cloudcord
COPY --from=build /opt/libdave /opt/libdave
RUN ldd /usr/local/bin/cloudcord && ffmpeg -version
ENV FFMPEG_PATH=/usr/bin/ffmpeg
USER 65532:65532
STOPSIGNAL SIGTERM
ENTRYPOINT ["/usr/local/bin/cloudcord"]
