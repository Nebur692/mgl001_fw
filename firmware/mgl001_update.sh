#!/bin/sh
# Installs a stock or modified firmware on the Xiaomi Smart Home Hub 2 (lumi.gateway.mgl001).
# It uses the gateway's own installer (/bin/fw_update.sh): kernel and rootfs go to the bank that
# is not running and the boot switches to it. /data is not touched, so paired devices, automations
# and settings are kept. The previous firmware stays in the other bank: option "r" goes back to it.
#
# Instala un firmware original o modificado en la Xiaomi Smart Home Hub 2 (lumi.gateway.mgl001).
# Usa el instalador de la propia pasarela (/bin/fw_update.sh): kernel y sistema van al banco que no
# está en uso y el arranque cambia a él. /data no se toca: se conservan los aparatos emparejados,
# las automatizaciones y los ajustes. El firmware anterior queda en el otro banco: la opción «r»
# vuelve a él.

REPO=Nebur692/mgl001_fw
BRANCH=main
HOST=raw.githubusercontent.com
WORK=/tmp/fwup

say() { echo "$*"; }
die() { echo "! $*"; rm -rf "$WORK"; exit 1; }

# HTTPS download with the gateway's own openssl (its busybox wget has no HTTPS)
fetch() { # path-in-repo output
    printf 'GET /%s/%s/%s HTTP/1.0\r\nHost: %s\r\nUser-Agent: mgl001_update\r\n\r\n' "$REPO" "$BRANCH" "$1" "$HOST" |
        openssl s_client -quiet -connect "$HOST:443" -servername "$HOST" 2>/dev/null | {
            IFS= read -r status
            case "$status" in *" 200 "*) ;; *) exit 1;; esac
            while IFS= read -r line; do [ "$line" = "$(printf '\r')" ] && break; done
            cat > "$2"
        }
}

current_rootfs() { grep -o 'root=/dev/mtdblock[78]' /proc/cmdline | grep -o '[78]$'; }
first_kernel() { /etc/fw_printenv bootcmd | awk '{print $4}'; }   # KERNEL or KERNEL_BAK

# The other bank must hold a valid image before booting into it
bank_ok() { # rootfs-mtd kernel-mtd
    [ "$(dd if=/dev/mtd${1}ro bs=4 count=1 2>/dev/null)" = "hsqs" ] &&
        [ "$(dd if=/dev/mtd${2}ro bs=4 count=1 2>/dev/null | hexdump -e '4/1 "%02x"')" = "27051956" ]
}

# The running kernel must be able to mount the rootfs and run its shell: if not, it would not boot
rootfs_check() { # rootfs-mtd
    m=$WORK/mnt
    mkdir -p "$m"
    mount -t squashfs -o ro "/dev/mtdblock$1" "$m" 2>/dev/null || return 1
    ok=1
    [ -x "$m/linuxrc" ] && [ -x "$m/etc/init.d/rcS" ] && chroot "$m" /bin/busybox true || ok=0
    OTHER_VERSION="$(cat "$m/etc/mgl001_fw_mod" 2>/dev/null || sed -n 's/^ro.sys.fw_ver=//p;s/^ro.sys.build_num=//p' "$m/etc/build.prop" | tr '\n' '_' | sed 's/_$//')"
    umount "$m"
    [ "$ok" = 1 ]
}

say "* Checking the gateway"
[ "$(agetprop ro.sys.model)" = "lumi.gateway.mcn001" ] || die "This is not a Xiaomi Smart Home Hub 2 (model $(agetprop ro.sys.model))"
EXPECTED_MTD='mtd5: 00500000 00020000 "KERNEL"
mtd6: 00500000 00020000 "KERNEL_BAK"
mtd7: 01000000 00020000 "rootfs"
mtd8: 01000000 00020000 "rootfs_bak"'
[ "$(grep 'KERNEL\|rootfs' /proc/mtd)" = "$EXPECTED_MTD" ] || die "Unexpected flash layout, nothing was changed"

ROOT_NOW=$(current_rootfs)
[ -n "$ROOT_NOW" ] || die "Cannot tell which bank is running"
if [ "$ROOT_NOW" = 8 ]; then ROOT_OTHER=7; else ROOT_OTHER=8; fi
if [ "$(first_kernel)" = KERNEL_BAK ]; then KERNEL_NOW=6; KERNEL_OTHER=5; else KERNEL_NOW=5; KERNEL_OTHER=6; fi

say "  Running: $(cat /etc/mgl001_fw_mod 2>/dev/null || echo "stock $(agetprop ro.sys.fw_ver)_$(agetprop ro.sys.build_num)") (rootfs mtd$ROOT_NOW, kernel mtd$KERNEL_NOW)"

rm -rf "$WORK"; mkdir -p "$WORK"
say "* Getting firmware list"
# Without network only the rollback ("r") is offered
fetch firmware/list.txt "$WORK/list.txt" || { say "! Cannot download the firmware list, check the network"; : > "$WORK/list.txt"; }

say
say "Available firmware / Firmware disponible:"
say "[0] Exit / Salir"
say "[r] Go back to the firmware in the other bank / Volver al firmware del otro banco"
N=0
while IFS='|' read -r name kind ksize ksha rsize rsha; do
    case "$name" in ''|'#'*) continue;; esac
    N=$((N + 1))
    say "[$N] $kind  $name"
done < "$WORK/list.txt"

printf '\nChoose / Elige: '
read CHOICE

if [ "$CHOICE" = r ]; then
    bank_ok "$ROOT_OTHER" "$KERNEL_OTHER" || die "The other bank has no valid firmware, nothing was changed"
    if [ "$ROOT_OTHER" = 8 ]; then fw_root=x; else fw_root=; fi
    rootfs_check "$ROOT_OTHER" || die "The other bank does not mount, nothing was changed"
    say "  Other bank: $OTHER_VERSION"
    printf 'Boot from rootfs mtd%s and kernel mtd%s? Type yes / Escribe yes: ' "$ROOT_OTHER" "$KERNEL_OTHER"
    read OK; [ "$OK" = yes ] || die "Cancelled"
    # Same switch fw_update.sh does after writing
    sed -n '/^set_kernel_partitions()/,/^}/p;/^set_rootfs_partitions()/,/^}/p' /bin/fw_update.sh > "$WORK/switch.sh"
    . "$WORK/switch.sh"
    if [ "$KERNEL_OTHER" = 5 ]; then set_kernel_partitions KERNEL_BAK; else set_kernel_partitions KERNEL; fi
    set_rootfs_partitions "$fw_root"
    say "* Done. Rebooting in 10 seconds"
    rm -rf "$WORK"; sleep 10; reboot; exit 0
fi

case "$CHOICE" in ''|*[!0-9]*) die "Wrong choice";; esac
[ "$CHOICE" -ge 1 ] && [ "$CHOICE" -le "$N" ] || { rm -rf "$WORK"; say "Exit"; exit 0; }
LINE=$(grep -v '^#' "$WORK/list.txt" | grep -v '^$' | sed -n "${CHOICE}p")
IFS='|' read -r NAME KIND KSIZE KSHA RSIZE RSHA <<EOF
$LINE
EOF

# No downgrades: Xiaomi updates also flash the Zigbee chip, and an older firmware may not work with it
NEW_VER=$(echo "$NAME" | sed -n 's/^mgl001_\([0-9.]*\)_.*/\1/p')
RUN_VER=$(agetprop ro.sys.fw_ver)
if [ "$(printf '%s\n%s\n' "$NEW_VER" "$RUN_VER" | sort -t. -k1,1n -k2,2n -k3,3n | head -1)" != "$RUN_VER" ]; then
    die "$NAME is older than the running firmware ($RUN_VER): the Zigbee chip may not work with it. Nothing was changed"
fi

say "* Downloading $NAME"
fetch "firmware/$KIND/$NAME/kernel" "$WORK/kernel" || die "Download failed (kernel)"
fetch "firmware/$KIND/$NAME/rootfs.sqfs" "$WORK/rootfs.sqfs" || die "Download failed (rootfs)"
[ "$(wc -c < "$WORK/kernel")" -eq "$KSIZE" ] && [ "$(sha256sum "$WORK/kernel" | cut -d' ' -f1)" = "$KSHA" ] || die "Kernel is damaged (size or SHA-256), nothing was changed"
[ "$(wc -c < "$WORK/rootfs.sqfs")" -eq "$RSIZE" ] && [ "$(sha256sum "$WORK/rootfs.sqfs" | cut -d' ' -f1)" = "$RSHA" ] || die "Rootfs is damaged (size or SHA-256), nothing was changed"
say "  SHA-256 OK"

say
say "It will be written to rootfs mtd$ROOT_OTHER and kernel mtd$KERNEL_OTHER (the bank not in use)."
say "Paired devices and settings in /data are kept. Do not unplug the gateway."
printf 'Type yes to continue / Escribe yes para seguir: '
read OK; [ "$OK" = yes ] || die "Cancelled"

OLD_BOOTCMD=$(/etc/fw_printenv bootcmd | cut -d= -f2-)
OLD_BOOTARGS=$(/etc/fw_printenv bootargs | cut -d= -f2-)
restore_env() {
    /etc/fw_setenv bootcmd "$OLD_BOOTCMD"
    /etc/fw_setenv bootargs "$OLD_BOOTARGS"
}

say "* Flashing kernel"
/bin/fw_update.sh "$WORK/kernel" || { restore_env; die "Kernel write failed, boot left on the current firmware"; }
say "* Flashing rootfs"
/bin/fw_update.sh "$WORK/rootfs.sqfs" || { restore_env; die "Rootfs write failed, boot left on the current firmware"; }

say "* Verifying what was written"
[ "$(head -c "$KSIZE" /dev/mtd${KERNEL_OTHER}ro | sha256sum | cut -d' ' -f1)" = "$KSHA" ] || { restore_env; die "Kernel verify failed, boot left on the current firmware"; }
[ "$(head -c "$RSIZE" /dev/mtd${ROOT_OTHER}ro | sha256sum | cut -d' ' -f1)" = "$RSHA" ] || { restore_env; die "Rootfs verify failed, boot left on the current firmware"; }
rootfs_check "$ROOT_OTHER" || { restore_env; die "The new rootfs does not mount, boot left on the current firmware"; }
/etc/fw_printenv bootargs | grep -q "mtdblock$ROOT_OTHER" || { restore_env; die "Boot was not switched, left on the current firmware"; }
say "  Verified: $OTHER_VERSION"

rm -rf "$WORK"
say
say "*** Done / Hecho ***  Rebooting in 10 seconds"
sleep 10
reboot
