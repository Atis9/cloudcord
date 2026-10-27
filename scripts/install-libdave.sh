#!/bin/sh
# Install only into the explicit prefix; do not edit shell profiles or use sudo.
set -eu
version=1.1.0
prefix=${1:?Usage: scripts/install-libdave.sh /absolute/install/prefix}
case "$prefix" in /*) ;; *) echo 'Prefix must be absolute' >&2; exit 1;; esac
case "$(uname -s)" in Linux) platform=Linux;library=libdave.so;; Darwin) platform=macOS;library=libdave.dylib;; *) echo 'Linux/macOS only' >&2;exit 1;; esac
case "$(uname -m)" in aarch64|arm64) arch=ARM64;; x86_64) arch=X64;; *) echo 'Unsupported architecture' >&2;exit 1;; esac
archive=$(mktemp)
staging=$(mktemp -d)
trap 'rm -f "$archive"; rm -rf "$staging"' EXIT HUP INT TERM
url="https://github.com/discord/libdave/releases/download/v${version}/cpp/libdave-${platform}-${arch}-boringssl.zip"
curl --fail --location --proto '=https' --tlsv1.2 "$url" -o "$archive"
case "$platform-$arch" in
 Linux-ARM64) digest=fc119512aefac9cb7652634c16ecb0833a3c4bf91d87b9ec8d6acda03d454349;;
 Linux-X64) digest=33157b8cbadcdd3c6cb4df3be18e8bbe7b86e3c65d57d0f1bc9dadc62a768d6a;;
 macOS-ARM64) digest=05166ca29afb30b4bcb7dd1d320b63ff14f2f4a179050ced8b91305ff86cc991;;
 macOS-X64) digest=996dad1e812ef76ba7b73fdd26cff30af5dbc1fc1f2389a1107752212c41950b;;
esac
if command -v sha256sum >/dev/null 2>&1; then
 actual=$(sha256sum "$archive" | cut -d ' ' -f 1)
else
 actual=$(shasum -a 256 "$archive" | cut -d ' ' -f 1)
fi
[ "$actual" = "$digest" ] || { echo 'libdave checksum mismatch' >&2; exit 1; }
unzip -q "$archive" -d "$staging"
mkdir -p "$prefix/include" "$prefix/lib/pkgconfig" "$prefix/licenses"
cp "$staging/include/dave/dave.h" "$prefix/include/dave.h"
cp "$staging/lib/$library" "$prefix/lib/$library"
cp "$staging/licenses/"* "$prefix/licenses/"
cat > "$prefix/lib/pkgconfig/dave.pc" <<PC
prefix=$prefix
libdir=\${prefix}/lib
includedir=\${prefix}/include

Name: dave
Description: Discord libdave
Version: $version
Libs: -L\${libdir} -ldave -Wl,-rpath,\${libdir}
Cflags: -I\${includedir}
PC
printf 'Installed libdave %s (%s %s) to %s\n' "$version" "$platform" "$arch" "$prefix"
printf 'Set PKG_CONFIG_PATH=%s/lib/pkgconfig before CGO build.\n' "$prefix"
