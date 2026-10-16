#!/bin/sh
# Builds the modified firmware from a stock one: same kernel, rootfs with remote access and tools.
#   docker run --rm -v "$PWD":/w -w /w alpine:3 sh tools/build-mod.sh 1.0.5_0016 20261016
# Needs build/out/bin from tools/build-tools.sh.
# TOOLS picks the diagnostic tools (default: all). 1.0.8 has less room: TOOLS="htop nano tcpdump curl"
# TOOLS elige las herramientas de diagnóstico (por defecto, todas). En la 1.0.8 caben menos.
#
# Construye el firmware modificado a partir de uno original: el mismo kernel y el sistema con acceso
# remoto y herramientas. Necesita build/out/bin, que genera tools/build-tools.sh.
set -eu

STOCK_VER=$1   # e.g. 1.0.5_0016
MOD_DATE=$2    # e.g. 20261016
NAME=mgl001_${STOCK_VER}_mod${MOD_DATE}
STOCK=firmware/stock/mgl001_$STOCK_VER
DEST=firmware/custom/$NAME
BIN=build/out/bin
WORK=build/rootfs-$NAME
PART_SIZE=16777216   # rootfs partition, 16 MiB
TOOLS=${TOOLS:-htop nano tcpdump strace socat curl}   # dropbear always goes in: it is the SSH access

apk add -q squashfs-tools >/dev/null

[ -s "$STOCK/kernel" ] && [ -s "$STOCK/rootfs.sqfs" ] || { echo "! Missing stock firmware in $STOCK"; exit 1; }
for b in dropbearmulti $TOOLS; do
    [ -x "$BIN/$b" ] || { echo "! Missing $BIN/$b, run tools/build-tools.sh first"; exit 1; }
done

rm -rf "$WORK"
unsquashfs -n -d "$WORK" "$STOCK/rootfs.sqfs" >/dev/null

RCS=$WORK/etc/init.d/rcS
grep -qx 'CUSTOM_POST_INIT=/data/scripts/post_init.sh' "$RCS" || { echo "! rcS anchor not found, stock firmware changed"; exit 1; }
grep -q mgl001_mod "$RCS" && { echo "! rcS already modified"; exit 1; }

# 1. Remote access before the gateway services; user script at the end of boot
sed -i 's|^CUSTOM_POST_INIT=/data/scripts/post_init.sh$|# mgl001_fw mod: telnet and SSH in the background, so they can never hold up the gateway boot\n/usr/sbin/mgl001_mod_access.sh \&\n\n&|' "$RCS"
cat >> "$RCS" <<'EOF'

# mgl001_fw mod: user script
[ -x /data/run.sh ] && /data/run.sh &
EOF
grep -qx '/usr/sbin/mgl001_mod_access.sh &' "$RCS" || { echo "! rcS was not patched (access)"; exit 1; }
grep -qx '\[ -x /data/run.sh \] && /data/run.sh &' "$RCS" || { echo "! rcS was not patched (run.sh)"; exit 1; }
sh -n "$RCS" || { echo "! rcS has a syntax error"; exit 1; }

# 2. Overlay: access script and CA certificates for curl
cp -a mod/overlay/. "$WORK/"
chmod 755 "$WORK/usr/sbin/mgl001_mod_access.sh"
chmod 644 "$WORK/etc/ssl/cert.pem"

# 3. Tools
for b in dropbearmulti $TOOLS; do cp "$BIN/$b" "$WORK/usr/bin/"; done
for l in dropbear dbclient dropbearkey scp; do ln -sf dropbearmulti "$WORK/usr/bin/$l"; done
ln -sf dbclient "$WORK/usr/bin/ssh"

# 4. root's home lives in /data, so SSH keys survive reboots
mkdir -p "$WORK/home"
rm -rf "$WORK/home/root"
ln -s /data/root "$WORK/home/root"

# 5. Profile: /data/bin and /data/lib for extra tools, /data/.profile for aliases
cat >> "$WORK/etc/profile" <<'EOF'

# mgl001_fw mod
export PATH=$PATH:/data/bin
export LD_LIBRARY_PATH=$LD_LIBRARY_PATH:/data/lib
export TERM=${TERM:-xterm}
[ -f /data/.profile ] && . /data/.profile
EOF

echo "$NAME" > "$WORK/etc/mgl001_fw_mod"

# 6. Pack like the stock image: xz, 128 KiB blocks
mkdir -p "$DEST"
rm -f "$DEST/rootfs.sqfs"
mksquashfs "$WORK" "$DEST/rootfs.sqfs" -comp xz -b 131072 -noappend -no-xattrs -quiet >/dev/null
SIZE=$(stat -c %s "$DEST/rootfs.sqfs")
if [ "$SIZE" -gt "$PART_SIZE" ]; then
    echo "! rootfs is $SIZE bytes, partition holds $PART_SIZE"
    rm -f "$DEST/rootfs.sqfs"; rmdir "$DEST" 2>/dev/null; rm -rf "$WORK"
    exit 1
fi
cp "$STOCK/kernel" "$DEST/kernel"
rm -rf "$WORK"

cd "$DEST"
echo "$NAME: rootfs $SIZE of $PART_SIZE bytes ($(( (PART_SIZE - SIZE) / 1024 )) KiB free)"
sha256sum kernel rootfs.sqfs
