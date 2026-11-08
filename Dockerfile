# The build image and APT snapshot are pinned. The runtime contains only the copied files.
FROM golang:1.27.1-trixie@sha256:3b77fc618ec235a1ab412de7737f120dd507c57e8d87de4cbb7994fb94275ed5 AS build
ARG DEBIAN_SNAPSHOT=20261003T000000Z
RUN rm -f /etc/apt/sources.list.d/debian.sources \
 && printf 'deb [check-valid-until=no] http://snapshot.debian.org/archive/debian/%s/ trixie main\n' "$DEBIAN_SNAPSHOT" > /etc/apt/sources.list \
 && apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl unzip pkg-config ffmpeg libopus-dev nasm xz-utils \
 && rm -rf /var/lib/apt/lists/*
WORKDIR /src
COPY scripts/build-ffmpeg.sh /src/scripts/build-ffmpeg.sh
RUN sh /src/scripts/build-ffmpeg.sh /opt/ffmpeg
COPY scripts/install-libdave.sh /src/scripts/install-libdave.sh
RUN /src/scripts/install-libdave.sh /opt/libdave
ENV CGO_ENABLED=1 PKG_CONFIG_PATH=/opt/libdave/lib/pkgconfig FFMPEG_PATH=/opt/ffmpeg/bin/ffmpeg FFMPEG_FIXTURE_PATH=/usr/bin/ffmpeg
COPY go.mod go.sum ./
RUN go mod download && go mod verify
COPY cmd ./cmd
COPY internal ./internal
FROM build AS test
RUN go test -race -timeout 60s ./... && go vet ./...
FROM test AS binary
RUN go build -trimpath -buildvcs=false -ldflags='-s -w' -o /out/cloudcord ./cmd/cloudcord
RUN set -eu; \
    root=/out/rootfs; \
    install -D /out/cloudcord "$root/usr/local/bin/cloudcord"; \
    install -D /opt/ffmpeg/bin/ffmpeg "$root/usr/bin/ffmpeg"; \
    install -D /usr/bin/test "$root/usr/bin/test"; \
    install -D /opt/libdave/lib/libdave.so "$root/opt/libdave/lib/libdave.so"; \
    strip --strip-unneeded "$root/opt/libdave/lib/libdave.so"; \
    cp -a /opt/libdave/licenses "$root/opt/libdave/"; \
    install -Dm644 /opt/ffmpeg/COPYING.LGPLv2.1 "$root/usr/share/doc/ffmpeg/COPYING.LGPLv2.1"; \
    cp -L --parents /etc/ssl/certs/ca-certificates.crt /etc/nsswitch.conf "$root"; \
    for pkg in ca-certificates coreutils libc6 libgcc-s1 libopus0 libstdc++6; do \
      cp -L --parents "/usr/share/doc/$pkg/copyright" "$root"; \
    done; \
    ldd /out/cloudcord /opt/ffmpeg/bin/ffmpeg /usr/bin/test > /out/runtime-deps; \
    if grep -q 'not found' /out/runtime-deps; then cat /out/runtime-deps; exit 1; fi; \
    awk '$2 == "=>" && $3 ~ /^\/(usr\/)?lib/ { print $3 } $1 ~ /^\/(usr\/)?lib/ { print $1 }' /out/runtime-deps \
      | sort -u | xargs -r cp -L --parents -t "$root"; \
    install -d -m1777 "$root/tmp"; \
    chroot --userspec=65532:65532 "$root" /usr/bin/ffmpeg -version; \
    chroot --userspec=65532:65532 "$root" /usr/bin/test -x /usr/local/bin/cloudcord

FROM scratch AS runtime
LABEL org.opencontainers.image.source=https://github.com/Atis9/cloudcord
COPY --from=binary /out/rootfs/ /
ENV PATH=/usr/local/bin:/usr/bin FFMPEG_PATH=/usr/bin/ffmpeg
USER 65532:65532
STOPSIGNAL SIGTERM
ENTRYPOINT ["/usr/local/bin/cloudcord"]
