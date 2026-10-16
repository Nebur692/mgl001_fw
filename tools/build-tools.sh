#!/bin/sh
# Builds the static ARMv7 (hard-float, musl) tools added by the modified firmware.
# Run inside an Alpine x86_64 container:
#   docker run --rm -v "$PWD":/w -w /w alpine:3 sh tools/build-tools.sh
# Output: build/out/bin
#
# Compila las herramientas estáticas para ARMv7 que añade el firmware modificado.
# Se ejecuta dentro de un contenedor Alpine x86_64 (orden de arriba). Salida: build/out/bin
set -eu

TARGET=armv7l-linux-musleabihf
JOBS=${JOBS:-2}

DROPBEAR=2026.94
NCURSES=6.6
HTOP=3.5.3
NANO=9.2
LIBPCAP=1.11.0
TCPDUMP=4.99.7
STRACE=7.2
SOCAT=1.8.1.3
MBEDTLS=3.6.7
CURL=8.23.0

ROOT=$(pwd)/build
SRC=$ROOT/src
PREFIX=$ROOT/sysroot
OUT=$ROOT/out/bin
mkdir -p "$SRC" "$PREFIX" "$OUT"

apk add -q build-base curl xz bzip2 perl linux-headers autoconf automake libtool pkgconf bison flex cmake python3 >/dev/null

if [ ! -x "$ROOT/toolchain/bin/$TARGET-gcc" ]; then
    curl -fsSL "https://musl.cc/$TARGET-cross.tgz" | tar -xz -C "$ROOT"
    mv "$ROOT/$TARGET-cross" "$ROOT/toolchain"
fi
export PATH="$ROOT/toolchain/bin:$PATH"
export CC=$TARGET-gcc CXX=$TARGET-g++ AR=$TARGET-ar RANLIB=$TARGET-ranlib STRIP=$TARGET-strip
export CFLAGS="-Os -march=armv7-a -mfpu=neon-vfpv4 -mfloat-abi=hard -ffunction-sections -fdata-sections"
export LDFLAGS="-static -Wl,--gc-sections"
export PKG_CONFIG_PATH="$PREFIX/lib/pkgconfig" PKG_CONFIG_LIBDIR="$PREFIX/lib/pkgconfig"
HOST="--host=$TARGET"

fetch() { # url
    f=$SRC/$(basename "$1")
    [ -f "$f" ] || curl -fsSL -o "$f" "$1"
    d=$SRC/$(basename "$1" | sed -E 's/\.tar\.(gz|xz|bz2)$//')
    [ -d "$d" ] || tar -xf "$f" -C "$SRC"
    cd "$d"
}

# dropbear: SSH server, client, scp and key generator in one binary
fetch "https://matt.ucc.asn.au/dropbear/releases/dropbear-$DROPBEAR.tar.bz2"
./configure $HOST --disable-zlib --disable-lastlog --disable-utmp --disable-utmpx --disable-wtmp --disable-wtmpx --disable-pututline --disable-pututxline --enable-static >/dev/null
make -j"$JOBS" PROGRAMS="dropbear dbclient dropbearkey scp" MULTI=1 STATIC=1 >/dev/null
cp dropbearmulti "$OUT/"

# ncurses with built-in terminal descriptions, so htop and nano need no terminfo files
fetch "https://invisible-mirror.net/archives/ncurses/ncurses-$NCURSES.tar.gz"
./configure $HOST --prefix="$PREFIX" --without-cxx --without-cxx-binding --without-ada --without-manpages --without-progs --without-tests --without-debug --disable-database --with-fallbacks=linux,vt100,xterm,xterm-256color,screen --enable-widec --enable-pc-files --with-pkg-config-libdir="$PREFIX/lib/pkgconfig" >/dev/null
make -j"$JOBS" >/dev/null && make install >/dev/null

fetch "https://github.com/htop-dev/htop/releases/download/$HTOP/htop-$HTOP.tar.xz"
./configure $HOST --enable-static --enable-unicode --disable-sensors CPPFLAGS="-I$PREFIX/include -I$PREFIX/include/ncursesw" LDFLAGS="$LDFLAGS -L$PREFIX/lib" >/dev/null
make -j"$JOBS" >/dev/null && cp htop "$OUT/"

fetch "https://www.nano-editor.org/dist/v9/nano-$NANO.tar.xz"
./configure $HOST --disable-nls --disable-libmagic --disable-speller --disable-browser --disable-help CPPFLAGS="-I$PREFIX/include -I$PREFIX/include/ncursesw" LDFLAGS="$LDFLAGS -L$PREFIX/lib" >/dev/null
make -j"$JOBS" >/dev/null && cp src/nano "$OUT/"

fetch "https://www.tcpdump.org/release/libpcap-$LIBPCAP.tar.xz"
./configure $HOST --prefix="$PREFIX" --disable-shared --disable-usb --disable-bluetooth --disable-dbus --disable-rdma --disable-netmap --without-libnl --with-pcap=linux >/dev/null
make -j"$JOBS" >/dev/null && make install >/dev/null

fetch "https://www.tcpdump.org/release/tcpdump-$TCPDUMP.tar.xz"
./configure $HOST --without-crypto --without-smi CPPFLAGS="-I$PREFIX/include" LDFLAGS="$LDFLAGS -L$PREFIX/lib" >/dev/null
make -j"$JOBS" >/dev/null && cp tcpdump "$OUT/"

fetch "https://github.com/strace/strace/releases/download/v$STRACE/strace-$STRACE.tar.xz"
./configure $HOST --enable-mpers=no --disable-gcc-Werror >/dev/null
make -j"$JOBS" >/dev/null && cp src/strace "$OUT/"

fetch "http://www.dest-unreach.org/socat/download/socat-$SOCAT.tar.gz"
./configure $HOST --disable-openssl --disable-readline --disable-libwrap >/dev/null
make -j"$JOBS" socat >/dev/null && cp socat "$OUT/"

# curl with mbedTLS, for HTTPS from the gateway
fetch "https://github.com/Mbed-TLS/mbedtls/releases/download/mbedtls-$MBEDTLS/mbedtls-$MBEDTLS.tar.bz2"
make -j"$JOBS" lib >/dev/null && make install DESTDIR="$PREFIX" >/dev/null

fetch "https://curl.se/download/curl-$CURL.tar.xz"
./configure $HOST --with-mbedtls="$PREFIX" --without-zlib --without-libpsl --without-brotli --without-zstd --without-nghttp2 --without-libidn2 --disable-ldap --disable-rtsp --disable-dict --disable-telnet --disable-tftp --disable-pop3 --disable-imap --disable-smtp --disable-gopher --disable-mqtt --disable-manual --disable-docs --enable-static --disable-shared --with-ca-bundle=/etc/ssl/cert.pem >/dev/null
make -j"$JOBS" LDFLAGS="$LDFLAGS -all-static" >/dev/null && cp src/curl "$OUT/"

cd "$OUT"
$STRIP --strip-all ./*
ls -l

# Every tool must be self-contained: the gateway has glibc, not musl
apk add -q file >/dev/null
for b in ./*; do
    file "$b" | grep -q "static" || { echo "! $b is not statically linked"; exit 1; }
done
echo "All tools are statically linked"
