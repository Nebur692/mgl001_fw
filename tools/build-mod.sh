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
# VARIANT=lan: no Xiaomi service at all (see mod/lan/). VARIANT=lan: sin ningún servicio de Xiaomi.
VARIANT=${VARIANT:-mod}
case "$VARIANT" in mod|lan) ;; *) echo "! VARIANT must be mod or lan"; exit 1 ;; esac
NAME=mgl001_${STOCK_VER}_${VARIANT}${MOD_DATE}
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

# 1b. Mi Home updates can be blocked: check at the top of updater(), before anything is prepared
FWM=$WORK/bin/fw_manager.sh
[ "$(grep -cx 'updater()' "$FWM")" = 1 ] || { echo "! fw_manager.sh updater() anchor not found"; exit 1; }
grep -qx '    if \[ \$# -lt 3 \]; then green_echo "Lost params."; return 1; fi' "$FWM" || { echo "! fw_manager.sh updater() body changed"; exit 1; }
awk -v hook=mod/ota-lock.sh.in '
    { print }
    prev == "updater()" && $0 == "{" { while ((getline l < hook) > 0) print l }
    { prev = $0 }
' "$FWM" > "$FWM.new"
mv "$FWM.new" "$FWM"
chmod 755 "$FWM"
grep -qx '    if \[ -f /data/mgl001_mod/ota.lock \]; then' "$FWM" || { echo "! fw_manager.sh was not patched (OTA lock)"; exit 1; }
sh -n "$FWM" || { echo "! fw_manager.sh has a syntax error"; exit 1; }

# 2. Overlay: access script, OTA lock command and CA certificates for curl
cp -a mod/overlay/. "$WORK/"
chmod 755 "$WORK/usr/sbin/mgl001_mod_access.sh" "$WORK/usr/sbin/mgl001-ota"
chmod 644 "$WORK/etc/ssl/cert.pem"

# 2b. LAN variant: no Xiaomi service. The trial-boot check runs right after the last mdev (it needs
# /dev/mtd for the U-Boot environment), our init replaces fw_manager.sh -r, Wi-Fi is not loaded and
# the Xiaomi service binaries are removed so nothing can start them.
# Variante LAN: la comprobación del arranque de prueba va justo tras el último mdev, nuestro arranque
# sustituye a fw_manager.sh -r, la wifi no se carga y se quitan los binarios de los servicios de Xiaomi.
if [ "$VARIANT" = lan ]; then
    [ "$(grep -cx '/sbin/mdev -s' "$RCS")" = 2 ] || { echo "! rcS mdev anchor changed"; exit 1; }
    awk 'BEGIN { n = 0 }
         { print }
         $0 == "/sbin/mdev -s" { n++; if (n == 2) print "/usr/sbin/mgl001-boot boot" }' "$RCS" > "$RCS.new"
    mv "$RCS.new" "$RCS"
    [ "$(grep -c '^    fw_manager.sh -r$' "$RCS")" = 1 ] || { echo "! rcS fw_manager anchor changed"; exit 1; }
    awk '
        $0 == "if [ -x ${CUSTOM_POST_INIT} ]; then" { skip = 1; next }
        skip && $0 == "fi" { skip = 0; print "# mgl001_fw lan: no Xiaomi services"; print "/usr/sbin/mgl001_lan_init.sh"; next }
        !skip { print }' "$RCS" > "$RCS.new"
    mv "$RCS.new" "$RCS"
    chmod 755 "$RCS"
    grep -qx '/usr/sbin/mgl001-boot boot' "$RCS" || { echo "! rcS was not patched (trial boot)"; exit 1; }
    grep -qx '/usr/sbin/mgl001_lan_init.sh' "$RCS" || { echo "! rcS was not patched (lan init)"; exit 1; }
    ! grep -q 'fw_manager.sh -r' "$RCS" || { echo "! rcS still starts fw_manager.sh"; exit 1; }
    sh -n "$RCS" || { echo "! rcS has a syntax error"; exit 1; }

    DRV=$WORK/bin/driver_modules.sh
    for m in cfg80211 mac80211 ssw105at; do
        grep -q "^insmod /lib/modules/4.9.84/$m.ko" "$DRV" || { echo "! driver_modules.sh: $m not found"; exit 1; }
        sed -i "s|^insmod /lib/modules/4.9.84/$m.ko|# mgl001_fw lan, no Wi-Fi: &|" "$DRV"
    done
    sh -n "$DRV" || { echo "! driver_modules.sh has a syntax error"; exit 1; }

    for b in miio_client miio_agent mha_basis mha_master mijia_automation miio_bt mZ3GatewayHost_MQTT \
             mzigbee_agent ot_service http_service matter_gateway_service translation_service central_lite \
             miio_client_helper_nomqtt.sh app_monitor.sh log_monitor.sh query_zgb.sh wifi_start.sh \
             miio_recv_line mdnsd; do
        [ -f "$WORK/bin/$b" ] || { echo "! /bin/$b not found"; exit 1; }
        rm -f "$WORK/bin/$b"
    done

    cp -a mod/lan/overlay/. "$WORK/"
    # The native Home Assistant device (tools/build-hass.sh) and the installer it runs for updates
    # El dispositivo nativo de Home Assistant y el instalador que usa para las actualizaciones
    [ -s "$BIN/mgl001-hass" ] || { echo "! $BIN/mgl001-hass missing: run tools/build-hass.sh"; exit 1; }
    cp "$BIN/mgl001-hass" "$WORK/usr/bin/mgl001-hass"
    ln -sf mgl001-hass "$WORK/usr/bin/mgl001-zigbee"   # the Zigbee bridge service / el servicio del puente Zigbee
    cp firmware/mgl001_update.sh "$WORK/usr/sbin/mgl001_update.sh"
    chmod 755 "$WORK/usr/sbin/mgl001_lan_init.sh" "$WORK/usr/sbin/mgl001-boot" "$WORK/usr/share/mgl001/udhcpc.script" \
        "$WORK/usr/bin/mgl001-hass" "$WORK/usr/sbin/mgl001_update.sh"
    for f in usr/sbin/mgl001_lan_init.sh usr/sbin/mgl001-boot usr/share/mgl001/udhcpc.script usr/sbin/mgl001_update.sh; do
        sh -n "$WORK/$f" || { echo "! $f has a syntax error"; exit 1; }
    done
fi

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
