#!/bin/sh
# Bench for mod/lan/overlay/usr/sbin/mgl001-boot: the U-Boot environment in a file, Xiaomi's REAL fw_update.sh,
# and fw_printenv/fw_setenv doubles as strict as the real ones (lock in /var/lock taken as they start, fw_setenv
# before reading its input; no /var/lock at the start of rcS; no argument = the whole environment).
#   sh tools/test-mgl001-boot.sh <mgl001-boot> <fw_update.sh from a stock rootfs> <env: fw_printenv bootcmd bootargs>
# Banco de mgl001-boot: el entorno de U-Boot en un fichero, el fw_update.sh REAL de Xiaomi y dobles de
# fw_printenv/fw_setenv tan estrictos como los reales.
BOOT=$1; FWU=$2; ENV0=$3
ok=0; mal=0; ESPERADAS=47
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT

mk() { # a root with the initial environment, running bank $1; bank 7/8 contents in $T/r/banks/<n>
    rm -rf "$T/r"; mkdir -p "$T/r/etc" "$T/r/bin" "$T/r/proc" "$T/r/tmp" "$T/r/dev" "$T/r/var/lock"
    cp "$FWU" "$T/r/bin/fw_update.sh"
    cp "$ENV0" "$T/r/env"
    # The environment points to the bank that runs: for 7, root=mtdblock7 and KERNEL read first (as values_for 7)
    [ "$1" = 7 ] && sed -i '/^bootargs=/s#mtdblock8#mtdblock7#; /^bootcmd=/{s/KERNEL_BAK/__B__/g; s/KERNEL /KERNEL_BAK /g; s/__B__/KERNEL/g}' "$T/r/env"
    cat > "$T/r/etc/fw_printenv" <<'EOF'
#!/bin/sh
D=$(dirname "$0")/..; E=$D/env
[ -d "$D/var/lock" ] || { echo "Error opening lock file /var/lock/fw_printenv.lock" >&2; exit 1; }
exec 9>"$D/var/lock/fw_printenv.lock"; i=0
until flock -n 9; do i=$((i+1)); [ $i -lt 50 ] || { echo "## lock busy (deadlock)" >&2; exit 9; }; sleep 0.1; done
[ $# = 0 ] && { cat "$E"; exit 0; }
for n in "$@"; do grep "^$n=" "$E" || { echo "## Error: \"$n\" not defined" >&2; exit 1; }; done
EOF
    cat > "$T/r/etc/fw_setenv" <<'EOF'
#!/bin/sh
D=$(dirname "$0")/..; E=$D/env
[ -d "$D/var/lock" ] || { echo "Error opening lock file /var/lock/fw_printenv.lock" >&2; exit 1; }
exec 9>"$D/var/lock/fw_printenv.lock"; i=0   # the lock BEFORE reading the input, as the real one
until flock -n 9; do i=$((i+1)); [ $i -lt 50 ] || { echo "## lock busy (deadlock)" >&2; exit 9; }; sleep 0.1; done
[ "$1" = -s ] && [ "$2" = - ] || exit 1
cp "$E" "$E.new"
while IFS= read -r l; do
    n=${l%% *}; [ "$n" = "$l" ] && v="" || v=${l#* }
    grep -v "^$n=" "$E.new" > "$E.tmp"; mv "$E.tmp" "$E.new"
    [ -n "$v" ] && echo "$n=$v" >> "$E.new"
done
mv "$E.new" "$E"
echo written >> "$D/writes"
EOF
    chmod +x "$T/r/etc/fw_printenv" "$T/r/etc/fw_setenv"
    sed -n 's/^bootargs=//p' "$ENV0" | sed "s#root=/dev/mtdblock[78]#root=/dev/mtdblock$1#" > "$T/r/proc/cmdline"
    echo "mgl001_x_lan" > "$T/r/etc/mgl001_fw_mod"
    for b in 7 8; do lan_bank $b; done
    : > "$T/r/writes"
}
lan_bank() { # bank $1 holds a LAN firmware: its rcS runs mgl001-boot boot
    rm -rf "$T/r/banks/$1"; mkdir -p "$T/r/banks/$1/etc/init.d" "$T/r/banks/$1/usr/sbin"
    printf '#!/bin/sh\n/sbin/mdev -s\n/usr/sbin/mgl001-boot boot\n' > "$T/r/banks/$1/etc/init.d/rcS"
    cp "$BOOT" "$T/r/banks/$1/usr/sbin/mgl001-boot"; chmod +x "$T/r/banks/$1/usr/sbin/mgl001-boot"
}
mod_bank() { # bank $1 holds a stock/mod firmware: no mgl001-boot
    rm -rf "$T/r/banks/$1"; mkdir -p "$T/r/banks/$1/etc/init.d"
    printf '#!/bin/sh\n/sbin/mdev -s\nfw_manager.sh -r\n' > "$T/r/banks/$1/etc/init.d/rcS"
}
v() { grep "^$1=" "$T/r/env" | cut -d= -f2-; }
writes() { wc -l < "$T/r/writes"; }
# What Xiaomi's fw_update.sh sees (get_kernel_partitions): must be ONE word, KERNEL or KERNEL_BAK
xiaomi_kernel() { "$T/r/etc/fw_printenv" | grep bootcmd | awk '{print $4}' | tr '\n' ' '; }
# busybox 1.20 (the gateway) wants timeout -t SECS; newer ones take SECS / la busybox 1.20 quiere -t
if timeout -t 1 true 2>/dev/null; then TO="timeout -t 20"; else TO="timeout 20"; fi
run() { MGL001_ROOT=$T/r PATH="$T/fake:$PATH" $TO sh "$BOOT" "$@"; r=$?
    [ $r != 124 ] || { echo "BAD: $1 hung (deadlock)"; mal=$((mal+1)); }; return $r; }
chk() { if [ "$2" = "$3" ]; then ok=$((ok+1)); else mal=$((mal+1)); echo "BAD: $1 (expected «$3», got «$2»)"; fi; }
running() { sed -i "s#mtdblock[78]#mtdblock$1#" "$T/r/proc/cmdline"; }   # a reboot into bank $1

mkdir -p "$T/fake"
printf '#!/bin/sh\necho REBOOT >> "%s/r/writes"\n' "$T" > "$T/fake/reboot"
printf '#!/bin/sh\n:\n' > "$T/fake/sync"
# mount -t squashfs -o ro <root>/dev/mtdblockN DIR: serves bank N from $T/r/banks/N
cat > "$T/fake/mount" <<EOF
#!/bin/sh
eval d=\\\${\$#}; dev=\$(eval echo \\\${\$((\$# - 1))}); n=\${dev##*mtdblock}
[ -d "$T/r/banks/\$n" ] || exit 1
cp -a "$T/r/banks/\$n/." "\$d/"
EOF
printf '#!/bin/sh\nrm -rf "$1"/* "$1"/.[!.]* 2>/dev/null; :\n' > "$T/fake/umount"
chmod +x "$T/fake/"*

CMD8=$(sed -n 's/^bootcmd=//p' "$ENV0"); ARGS8=$(sed -n 's/^bootargs=//p' "$ENV0")

# 1. Normal boot: writes nothing / Arranque normal: no escribe nada
mk 8; run boot; chk "boot with nothing pending writes nothing" "$(writes)" 0
chk "fw_update.sh sees one kernel" "$(xiaomi_kernel)" "KERNEL_BAK "

# 2. try towards a bank without mgl001-boot (stock/mod): refused, nothing written
mod_bank 7; run try >/dev/null 2>&1; chk "try to a mod bank is refused" "$?" 1
chk "try to a mod bank writes nothing" "$(writes)" 0
lan_bank 7

# 3. try from bank 8: next boot is 7 with KERNEL, the running one saved to come back
run try >/dev/null
chk "try: rootfs 7" "$(v bootargs | grep -o 'mtdblock[78]')" mtdblock7
chk "try: KERNEL first" "$(v bootcmd | awk '{print $4}')" KERNEL
chk "try: saves old bootcmd" "$(v mgl001_tcmd)" "$CMD8"
chk "try: saves old bootargs" "$(v mgl001_targs)" "$ARGS8"
chk "try: one write, then reboot" "$(tr '\n' ' ' < "$T/r/writes")" "written REBOOT "
chk "try: new bootargs = old ones with mtdblock7" "$(v bootargs)" "$(echo "$ARGS8" | sed 's#mtdblock8#mtdblock7#')"
chk "try: fw_update.sh still sees one kernel" "$(xiaomi_kernel)" "KERNEL "
NEWCMD=$(v bootcmd); NEWARGS=$(v bootargs)

# 4. First boot of bank 7 (the trial), early in rcS without /var/lock: next boot back to 8, keep values saved
running 7; : > "$T/r/writes"; rm -rf "$T/r/var/lock"
run boot
chk "trial boot: back to 8" "$(v bootargs)" "$ARGS8"
chk "trial boot: old bootcmd" "$(v bootcmd)" "$CMD8"
chk "trial boot: keep values" "$(v mgl001_kargs)" "$NEWARGS"
chk "trial boot: keep bootcmd" "$(v mgl001_kcmd)" "$NEWCMD"
chk "trial boot: trial mark gone" "$(v mgl001_tcmd)" ""
chk "trial boot: one write" "$(writes)" 1
chk "trial boot: fw_update.sh sees one kernel" "$(xiaomi_kernel)" "KERNEL_BAK "
# 4b. A second boot of 7 writes nothing more / un segundo arranque del 7 no escribe más
: > "$T/r/writes"; run boot; chk "second boot of 7 writes nothing" "$(writes)" 0
# 4c. try with a trial open: refused
run try >/dev/null 2>&1; chk "try with a trial open is refused" "$?" 1

# 5. keep: bank 7 stays
run keep >/dev/null
chk "keep: boots 7" "$(v bootargs)" "$NEWARGS"
chk "keep: bootcmd of 7" "$(v bootcmd)" "$NEWCMD"
chk "keep: clears keep values" "$(v mgl001_kcmd)$(v mgl001_kargs)" ""
chk "keep: fw_update.sh sees one kernel" "$(xiaomi_kernel)" "KERNEL "

# 6. back from 7: bank 8 again with the same values it had
run back >/dev/null
chk "back: bootargs of 8" "$(v bootargs)" "$ARGS8"
chk "back: bootcmd of 8" "$(v bootcmd)" "$CMD8"

# 7. A trial that went back without keep (power cut, watchdog, not healthy): leftovers removed on the old bank
mk 8; run try >/dev/null; running 7; run boot          # trial armed, trial boot of 7
running 8; : > "$T/r/writes"; rm -rf "$T/r/var/lock"     # power cut: back on 8 by itself
run boot
chk "back on 8: keep leftovers removed" "$(v mgl001_kcmd)$(v mgl001_kargs)" ""
chk "back on 8: one write" "$(writes)" 1
chk "back on 8: still boots 8" "$(v bootargs)" "$ARGS8"
chk "back on 8: status clean" "$(run status | grep -c 'trial boot')" 0
: > "$T/r/writes"; run boot; chk "next boot of 8 writes nothing" "$(writes)" 0
run try >/dev/null; chk "try allowed again" "$(v bootargs | grep -o 'mtdblock[78]')" mtdblock7

# 8. keep on the wrong bank (leftovers present, boot not run yet): refused, nothing changed
mk 8; printf 'mgl001_kcmd %s\nmgl001_kargs %s\n' "x" "$(echo "$ARGS8" | sed 's#mtdblock8#mtdblock7#')" | "$T/r/etc/fw_setenv" -s -
: > "$T/r/writes"; run keep >/dev/null 2>&1; chk "keep for another bank is refused" "$?" 1
chk "keep for another bank writes nothing" "$(writes)" 0
chk "keep for another bank: still boots 8" "$(v bootargs)" "$ARGS8"

# 9. The same, from bank 7 (try from 7 towards 8)
mk 7; run try >/dev/null; chk "try from 7: rootfs 8" "$(v bootargs | grep -o 'mtdblock[78]')" mtdblock8
chk "try from 7: KERNEL_BAK first" "$(v bootcmd | awk '{print $4}')" KERNEL_BAK
running 8; run boot; chk "trial boot of 8: back to 7" "$(v bootargs | grep -o 'mtdblock[78]')" mtdblock7
run keep >/dev/null; chk "keep 8" "$(v bootargs | grep -o 'mtdblock[78]')" mtdblock8

# 10. A trial armed by the first mgl001-boot (old names, "bootcmd" inside them), booted by this one:
#     the first update from Home Assistant of a gateway that runs the 17-10-2026 build
mk 8
CMD7=$(echo "$CMD8" | sed 's/KERNEL_BAK/__B__/g; s/KERNEL /KERNEL_BAK /g; s/__B__/KERNEL/g'); ARGS7=$(echo "$ARGS8" | sed 's#mtdblock8#mtdblock7#')
printf 'mgl001_trial_bootcmd %s\nmgl001_trial_bootargs %s\nbootcmd %s\nbootargs %s\n' "$CMD8" "$ARGS8" "$CMD7" "$ARGS7" | "$T/r/etc/fw_setenv" -s -
running 7; : > "$T/r/writes"; rm -rf "$T/r/var/lock"
run boot
chk "old-name trial: back to 8" "$(v bootargs)" "$ARGS8"
chk "old-name trial: keep values under the new names" "$(v mgl001_kargs)" "$ARGS7"
chk "old-name trial: old names removed" "$(grep -c '^mgl001_\(trial\|keep\)_' "$T/r/env")" 0
chk "old-name trial: one write" "$(writes)" 1
chk "old-name trial: fw_update.sh sees one kernel" "$(xiaomi_kernel)" "KERNEL_BAK "
run keep >/dev/null; chk "old-name trial: keep 7" "$(v bootargs)" "$ARGS7"
# 11. Old-name leftovers only (no trial): removed once, then nothing more is written
mk 8; printf 'mgl001_keep_bootcmd x\nmgl001_keep_bootargs y\n' | "$T/r/etc/fw_setenv" -s -
: > "$T/r/writes"; run boot
chk "old-name leftovers removed" "$(grep -c '^mgl001_' "$T/r/env")" 0
: > "$T/r/writes"; run boot; chk "then a normal boot writes nothing" "$(writes)" 0

echo "ok=$ok bad=$mal total=$((ok+mal)) expected=$ESPERADAS"
[ "$mal" = 0 ] && [ "$((ok+mal))" = "$ESPERADAS" ]
