#!/bin/sh
# Writes firmware/list.txt, the menu of mgl001_update.sh: name|kind|kernel size|kernel SHA-256|rootfs size|rootfs SHA-256
# Escribe firmware/list.txt, el menú de mgl001_update.sh.
set -eu
cd "$(dirname "$0")/../firmware"
{
    echo "# name|kind|kernel size|kernel sha256|rootfs size|rootfs sha256"
    for kind in custom stock; do
        for d in "$kind"/mgl001_*; do
            [ -s "$d/kernel" ] && [ -s "$d/rootfs.sqfs" ] || continue
            printf '%s|%s|%s|%s|%s|%s\n' "$(basename "$d")" "$kind" \
                "$(stat -c %s "$d/kernel")" "$(sha256sum "$d/kernel" | cut -d' ' -f1)" \
                "$(stat -c %s "$d/rootfs.sqfs")" "$(sha256sum "$d/rootfs.sqfs" | cut -d' ' -f1)"
        done
    done
} > list.txt
cat list.txt
