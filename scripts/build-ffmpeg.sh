#!/bin/sh
set -eu

prefix=${1:?Usage: scripts/build-ffmpeg.sh /absolute/install/prefix}
version=7.1.5
digest=de668509caf9e35e3cd162473441fdb29538c6d96ed080292b3cf9e6fc5d558f
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
cd "$work"
archive="ffmpeg-$version.tar.xz"
curl --fail --location --proto '=https' --tlsv1.2 "https://ffmpeg.org/releases/$archive" -o "$archive"
printf '%s  %s\n' "$digest" "$archive" | sha256sum --check
tar -xf "$archive"
cd "ffmpeg-$version"

# Keep only the features used by the speech converter. The full system FFmpeg
# in the build stage supplies test fixtures and decodes test output.
./configure --disable-everything --disable-autodetect --disable-debug --disable-doc \
  --disable-network --disable-ffplay --disable-ffprobe --disable-avdevice --disable-swscale \
  --enable-libopus --enable-protocol=pipe --enable-demuxer=wav,mp3,pcm_s16le \
  --enable-decoder=mp3float,pcm_s16le,pcm_s24le,pcm_s32le,pcm_f32le,pcm_f64le,pcm_u8,pcm_alaw,pcm_mulaw \
  --enable-encoder=libopus --enable-muxer=ogg \
  --enable-filter=aresample,atempo,deesser,dynaudnorm,acompressor,alimiter
make -j"$(getconf _NPROCESSORS_ONLN)" ffmpeg
mkdir -p "$prefix/bin"
install -m755 ffmpeg "$prefix/bin/ffmpeg"
install -m644 COPYING.LGPLv2.1 "$prefix/COPYING.LGPLv2.1"
