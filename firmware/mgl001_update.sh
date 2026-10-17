#!/bin/sh
# Installs a stock or modified firmware on the Xiaomi Smart Home Hub 2 (lumi.gateway.mgl001).
# It uses the gateway's own installer (/bin/fw_update.sh): kernel and rootfs go to the bank that
# is not running. The boot only switches once both are written and checked, in a single write of
# the U-Boot environment. /data is not touched, so paired devices, automations and settings are
# kept. The previous firmware stays in the other bank: option "r" goes back to it.
#
# Instala un firmware original o modificado en la Xiaomi Smart Home Hub 2 (lumi.gateway.mgl001).
# Usa el instalador de la propia pasarela (/bin/fw_update.sh): kernel y sistema van al banco que no
# está en uso. El arranque solo cambia cuando los dos están escritos y comprobados, en una única
# escritura del entorno de U-Boot. /data no se toca: se conservan los aparatos emparejados, las
# automatizaciones y los ajustes. El firmware anterior queda en el otro banco: la opción «r» vuelve a él.

#
# Without questions (Home Assistant updates on the LAN variant):
#   sh mgl001_update.sh --install <name> [--trial]
# --trial boots the new firmware once with mgl001-boot: it comes back to this one unless it is kept.
# MGL001_LOCAL=<dir> takes list.txt and <kind>/<name>/{kernel,rootfs.sqfs} from a local folder instead of GitHub.
# Sin preguntas: --install <nombre> [--trial]. --trial arranca el nuevo una vez con mgl001-boot.
# MGL001_LOCAL=<carpeta> toma la lista y las imágenes de una carpeta local en lugar de GitHub.

REPO=Nebur692/mgl001_fw
BRANCH=main
HOST=raw.githubusercontent.com
R=${MGL001_ROOT:-}   # only for tests: on the gateway it is empty / solo para pruebas: en la pasarela, vacío
WORK=$R/tmp/fwup
FWP=$R/etc/fw_printenv
FWS=$R/etc/fw_setenv
FW_UPDATE=$R/bin/fw_update.sh
BOOT=${MGL001_BOOT:-$R/usr/sbin/mgl001-boot}
LOCAL=${MGL001_LOCAL:-}

INSTALL=; TRIAL=
case "$1" in
    --install)
        INSTALL=$2
        [ -n "$INSTALL" ] || { echo "! --install needs a firmware name"; exit 1; }
        case "$3" in --trial) TRIAL=1;; '') ;; *) echo "! Unknown option $3"; exit 1;; esac ;;
    '') ;;
    *) echo "usage: sh mgl001_update.sh [--install <name> [--trial]]"; exit 2 ;;
esac

say() { echo "$*"; }
die() { echo "! $*"; rm -rf "$WORK"; exit 1; }

# One installer at a time: two would erase and write the same free bank. Held until exit or reboot;
# mgl001-hass reads it too. / Un instalador a la vez: dos borrarían y escribirían el mismo banco libre.
exec 8>"$R/tmp/mgl001_update.lock"
flock -n 8 || { echo "! Another update is already running, nothing was changed"; exit 1; }

# A stalled download must not hang the installer (nothing is written before the downloads end).
# busybox 1.20 (the gateway) wants timeout -t SECS / la busybox 1.20 quiere timeout -t
if timeout -t 1 true 2>/dev/null; then TMO="timeout -t 300"; else TMO="timeout 300"; fi

# CA bundle: the mod ships /etc/ssl/cert.pem, the stock firmware /etc/ca-certificates.crt
CA=
for c in "$R/etc/ssl/cert.pem" "$R/etc/ca-certificates.crt"; do [ -s "$c" ] && { CA=$c; break; }; done

# HTTPS download with the gateway's own openssl (its busybox wget has no HTTPS). The certificate
# and the host name are checked: the SHA-256 in list.txt is only worth what this channel is worth.
fetch() { # path-in-repo output
    if [ -n "$LOCAL" ]; then cp "$LOCAL/${1#firmware/}" "$2" 2>/dev/null; return; fi
    printf 'GET /%s/%s/%s HTTP/1.0\r\nHost: %s\r\nUser-Agent: mgl001_update\r\n\r\n' "$REPO" "$BRANCH" "$1" "$HOST" |
        $TMO openssl s_client -quiet -connect "$HOST:443" -servername "$HOST" \
            -CAfile "$CA" -verify_return_error -verify_hostname "$HOST" 2>/dev/null | {
            IFS= read -r status
            case "$status" in *" 200 "*) ;; *) exit 1;; esac
            while IFS= read -r line; do [ "$line" = "$(printf '\r')" ] && break; done
            cat > "$2"
        }
}

env_get() { "$FWP" "$1" 2>/dev/null | cut -d= -f2-; }
current_rootfs() { grep -o 'root=/dev/mtdblock[78]' "$R/proc/cmdline" | grep -o '[78]$'; }
env_rootfs() { env_get bootargs | grep -o 'root=/dev/mtdblock[78]' | grep -o '[78]$'; }
first_kernel() { env_get bootcmd | awk '{print $4}'; }   # KERNEL or KERNEL_BAK
kernel_name() { if [ "$1" = 5 ]; then echo KERNEL; else echo KERNEL_BAK; fi; }
version_of() { echo "$1" | sed -n 's/^\(mgl001_\)\{0,1\}\([0-9][0-9]*\.[0-9][0-9]*\.[0-9][0-9]*\).*/\2/p'; }
older_than() { # a b: true if version a < version b
    [ "$1" != "$2" ] && [ "$(printf '%s\n%s\n' "$1" "$2" | sort -t. -k1,1n -k2,2n -k3,3n | head -1)" = "$1" ]
}

# bootcmd and bootargs in ONE write of the U-Boot environment: a power cut can never leave the
# kernel of one bank with the rootfs of the other
set_boot() { # bootcmd bootargs
    printf 'bootcmd %s\nbootargs %s\n' "$1" "$2" | "$FWS" -s - >/dev/null 2>&1
    [ "$(env_get bootcmd)" = "$1" ] && [ "$(env_get bootargs)" = "$2" ]
}

# The boot settings Xiaomi's fw_update.sh would write to switch to the other bank, built with its
# own functions but printed instead of written: the environment is only written once, by set_boot
other_bank_boot() { # sets NEW_BOOTCMD and NEW_BOOTARGS
    sed -n '/^set_kernel_partitions()/,/^}/p;/^set_rootfs_partitions()/,/^}/p' "$FW_UPDATE" |
        sed 's#[^ ]*/etc/fw_setenv #boot_value #' > "$WORK/switch.sh"
    boot_value() { echo "$2"; }
    . "$WORK/switch.sh"
    command -v set_kernel_partitions >/dev/null && command -v set_rootfs_partitions >/dev/null || return 1
    NEW_BOOTCMD=$(set_kernel_partitions "$(kernel_name $KERNEL_NOW)")
    if [ "$ROOT_OTHER" = 8 ]; then NEW_BOOTARGS=$(set_rootfs_partitions x); else NEW_BOOTARGS=$(set_rootfs_partitions ""); fi
    # Must point to the other bank, as a pair
    echo "$NEW_BOOTARGS" | grep -q "^root=/dev/mtdblock$ROOT_OTHER " &&
        [ "$(echo "$NEW_BOOTCMD" | awk '{print $4}')" = "$(kernel_name $KERNEL_OTHER)" ]
}

# Xiaomi's fw_update.sh without its two boot switches: it only writes the image to the free bank
make_writer() {
    W=$WORK/fw_write.sh
    # fw_update.sh picks the kernel bank with `fw_printenv | grep bootcmd`: any other variable with
    # "bootcmd" in its name gives it two lines and it would write the RUNNING kernel
    # fw_update.sh elige el banco con `fw_printenv | grep bootcmd`: otra variable así le haría escribir
    # el kernel EN USO
    [ "$("$FWP" 2>/dev/null | grep -c bootcmd)" = 1 ] || return 1
    [ "$(grep -cxF '        set_kernel_partitions "$KERNEL_PARTITION"' "$FW_UPDATE")" = 1 ] &&
        [ "$(grep -cxF '        set_rootfs_partitions "$ROOTFS_PARTITION"' "$FW_UPDATE")" = 1 ] || return 1
    # check_env too: on a bad CRC it rewrites the boot with three separate writes, in the middle of the install
    grep -vxF -e '        set_kernel_partitions "$KERNEL_PARTITION"' -e '        set_rootfs_partitions "$ROOTFS_PARTITION"' \
        -e 'check_env' "$FW_UPDATE" > "$W"
    ! grep -q -e 'set_kernel_partitions "' -e 'set_rootfs_partitions "' "$W" && ! grep -qx 'check_env' "$W" && sh -n "$W"
}

# The other bank must hold a valid image before booting into it
bank_ok() { # rootfs-mtd kernel-mtd
    [ "$(dd if="$R/dev/mtd${1}ro" bs=4 count=1 2>/dev/null)" = "hsqs" ] &&
        [ "$(dd if="$R/dev/mtd${2}ro" bs=4 count=1 2>/dev/null | hexdump -e '4/1 "%02x"')" = "27051956" ]
}

# The running kernel must be able to mount the rootfs and run its shell: if not, it would not boot
rootfs_check() { # rootfs-mtd
    m=$WORK/mnt
    mkdir -p "$m"
    mount -t squashfs -o ro "$R/dev/mtdblock$1" "$m" 2>/dev/null || return 1
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
[ "$(grep 'KERNEL\|rootfs' "$R/proc/mtd")" = "$EXPECTED_MTD" ] || die "Unexpected flash layout, nothing was changed"
[ -n "$CA" ] || [ -n "$LOCAL" ] || die "No CA certificates found to check the download, nothing was changed"
[ -z "$TRIAL" ] || [ -x "$BOOT" ] || die "A trial boot needs $BOOT (LAN variant), nothing was changed"
# A trial still open (or its leftovers) changes what the boot will do: any install waits for it
# Una prueba abierta (o sus restos) cambia lo que hará el arranque: ninguna instalación sigue
if [ -x "$BOOT" ]; then
    ! "$BOOT" status | grep -q "^trial boot" || die "A trial boot is still open: keep it or go back first. Nothing was changed"
fi

ROOT_NOW=$(current_rootfs)
[ -n "$ROOT_NOW" ] || die "Cannot tell which bank is running"
if [ "$ROOT_NOW" = 8 ]; then ROOT_OTHER=7; KERNEL_NOW=6; KERNEL_OTHER=5
else ROOT_OTHER=8; KERNEL_NOW=5; KERNEL_OTHER=6; fi

# Banks go in pairs (rootfs mtd7 + KERNEL, rootfs mtd8 + KERNEL_BAK), as Xiaomi's own updates leave
# them. If the boot settings do not match what is running, a switch is waiting for a reboot: any
# write now would land in the wrong bank.
[ "$(env_rootfs)" = "$ROOT_NOW" ] && [ "$(first_kernel)" = "$(kernel_name $KERNEL_NOW)" ] ||
    die "The boot is set to another firmware than the running one (a switch is waiting for a reboot). Reboot and run this again. Nothing was changed"

OLD_BOOTCMD=$(env_get bootcmd)
OLD_BOOTARGS=$(env_get bootargs)
RUN_VER=$(agetprop ro.sys.fw_ver)

say "  Running: $(cat "$R/etc/mgl001_fw_mod" 2>/dev/null || echo "stock ${RUN_VER}_$(agetprop ro.sys.build_num)") (rootfs mtd$ROOT_NOW, kernel mtd$KERNEL_NOW)"

rm -rf "$WORK"; mkdir -p "$WORK"
say "* Getting firmware list"
# Without network only the rollback ("r") is offered
fetch firmware/list.txt "$WORK/list.txt" || { say "! Cannot download the firmware list, check the network"; : > "$WORK/list.txt"; }

if [ -n "$INSTALL" ]; then
    LINE=$(awk -F'|' -v n="$INSTALL" '$1 == n { print; exit }' "$WORK/list.txt")
    [ -n "$LINE" ] || die "$INSTALL is not in the firmware list, nothing was changed"
    CHOICE=byname
fi

if [ -z "$INSTALL" ]; then   # menu (not indented, to keep it as it was)
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
    rootfs_check "$ROOT_OTHER" || die "The other bank does not mount, nothing was changed"
    say "  Other bank: $OTHER_VERSION"
    CONFIRM=yes
    OTHER_VER=$(version_of "$OTHER_VERSION")
    if [ -z "$OTHER_VER" ] || older_than "$OTHER_VER" "$RUN_VER"; then
        say "! The other bank holds ${OTHER_VER:-an unknown version}, older than the running $RUN_VER."
        say "! Xiaomi updates also flash the Zigbee chip: an older firmware may not work with it."
        say "! El otro banco tiene ${OTHER_VER:-una versión desconocida}, anterior a la $RUN_VER en marcha: el chip Zigbee puede no funcionar con ella."
        CONFIRM=older
    fi
    printf 'Boot from rootfs mtd%s and kernel mtd%s? Type %s / Escribe %s: ' "$ROOT_OTHER" "$KERNEL_OTHER" "$CONFIRM" "$CONFIRM"
    read OK; [ "$OK" = "$CONFIRM" ] || die "Cancelled"
    other_bank_boot || die "$FW_UPDATE has changed (unexpected boot settings), nothing was changed"
    trap '' HUP INT TERM QUIT PIPE
    set_boot "$NEW_BOOTCMD" "$NEW_BOOTARGS" || { set_boot "$OLD_BOOTCMD" "$OLD_BOOTARGS"; die "Boot was not switched, left on the current firmware"; }
    say "* Done. Rebooting in 10 seconds"
    rm -rf "$WORK"; sleep 10; reboot; exit 0
fi

case "$CHOICE" in ''|*[!0-9]*) die "Wrong choice";; esac
[ "$CHOICE" -ge 1 ] && [ "$CHOICE" -le "$N" ] || { rm -rf "$WORK"; say "Exit"; exit 0; }
LINE=$(grep -v '^#' "$WORK/list.txt" | grep -v '^$' | sed -n "${CHOICE}p")
fi   # menu
IFS='|' read -r NAME KIND KSIZE KSHA RSIZE RSHA <<EOF
$LINE
EOF

# No downgrades: Xiaomi updates also flash the Zigbee chip, and an older firmware may not work with it
NEW_VER=$(version_of "$NAME")
[ -n "$NEW_VER" ] || die "Cannot read the version of $NAME, nothing was changed"
older_than "$NEW_VER" "$RUN_VER" && die "$NAME is older than the running firmware ($RUN_VER): the Zigbee chip may not work with it. Nothing was changed"

# Both images are downloaded to RAM (/tmp): leave room for the gateway services, as fw_manager.sh does
NEED_KB=$(( (KSIZE + RSIZE) / 1024 + 16384 ))
AVAIL_KB=$(awk '/^MemAvailable:/{print $2}' "$R/proc/meminfo")
[ -n "$AVAIL_KB" ] && [ "$AVAIL_KB" -ge "$NEED_KB" ] || die "Not enough free memory (${AVAIL_KB:-?} KB, need $NEED_KB KB), nothing was changed"

say "* Downloading $NAME"
fetch "firmware/$KIND/$NAME/kernel" "$WORK/kernel" || die "Download failed (kernel)"
fetch "firmware/$KIND/$NAME/rootfs.sqfs" "$WORK/rootfs.sqfs" || die "Download failed (rootfs)"
[ "$(wc -c < "$WORK/kernel")" -eq "$KSIZE" ] && [ "$(sha256sum "$WORK/kernel" | cut -d' ' -f1)" = "$KSHA" ] || die "Kernel is damaged (size or SHA-256), nothing was changed"
[ "$(wc -c < "$WORK/rootfs.sqfs")" -eq "$RSIZE" ] && [ "$(sha256sum "$WORK/rootfs.sqfs" | cut -d' ' -f1)" = "$RSHA" ] || die "Rootfs is damaged (size or SHA-256), nothing was changed"
say "  SHA-256 OK"

say
say "It will be written to rootfs mtd$ROOT_OTHER and kernel mtd$KERNEL_OTHER (the bank not in use)."
say "Paired devices and settings in /data are kept. Do not unplug the gateway."
if [ -z "$INSTALL" ]; then
    printf 'Type yes to continue / Escribe yes para seguir: '
    read OK; [ "$OK" = yes ] || die "Cancelled"
fi

make_writer || die "$FW_UPDATE has changed, or the boot environment has more than one bootcmd variable (fw_printenv | grep bootcmd). Nothing was changed"
other_bank_boot || die "$FW_UPDATE has changed (unexpected boot settings), nothing was changed"

# From here on a closed SSH session or Ctrl-C must not stop the script half way
trap '' HUP INT TERM QUIT PIPE

# The boot is not touched while writing: until the final switch the gateway keeps booting the
# running firmware, so a power cut at any point leaves it as it was
say "* Flashing kernel"
sh "$W" "$WORK/kernel" >/dev/null 2>&1 || die "Kernel write failed, boot left on the current firmware"
say "* Flashing rootfs"
sh "$W" "$WORK/rootfs.sqfs" >/dev/null 2>&1 || die "Rootfs write failed, boot left on the current firmware"
[ "$(env_get bootcmd)" = "$OLD_BOOTCMD" ] && [ "$(env_get bootargs)" = "$OLD_BOOTARGS" ] ||
    { set_boot "$OLD_BOOTCMD" "$OLD_BOOTARGS"; die "The boot settings changed while writing (put back), left on the current firmware"; }

say "* Verifying what was written"
[ "$(head -c "$KSIZE" "$R/dev/mtd${KERNEL_OTHER}ro" | sha256sum | cut -d' ' -f1)" = "$KSHA" ] || die "Kernel verify failed, boot left on the current firmware"
[ "$(head -c "$RSIZE" "$R/dev/mtd${ROOT_OTHER}ro" | sha256sum | cut -d' ' -f1)" = "$RSHA" ] || die "Rootfs verify failed, boot left on the current firmware"
rootfs_check "$ROOT_OTHER" || die "The new rootfs does not mount, boot left on the current firmware"
say "  Verified: $OTHER_VERSION"

if [ -n "$TRIAL" ]; then
    # One boot of the new firmware: unless it is kept there, the next boot comes back to this one
    say "* Trial boot"
    rm -rf "$WORK"
    exec "$BOOT" try
fi

say "* Switching the boot"
set_boot "$NEW_BOOTCMD" "$NEW_BOOTARGS" || { set_boot "$OLD_BOOTCMD" "$OLD_BOOTARGS"; die "Boot was not switched, left on the current firmware"; }

rm -rf "$WORK"
say
say "*** Done / Hecho ***  Rebooting in 10 seconds"
sleep 10
reboot
