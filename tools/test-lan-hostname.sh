#!/bin/sh
# Bench for the names block of mgl001_lan_init.sh (ESPHome device name kept once, host name from
# Home Assistant or BurbuHub-XXXX). Takes the block from the script itself; writes only in a temporary
# folder and prints the name instead of setting it. Run it also ON the gateway (its busybox 1.20).
# Banco del bloque de nombres de mgl001_lan_init.sh. Saca el bloque del propio guion; solo escribe en una
# carpeta temporal e imprime el nombre en vez de ponerlo. Pasarlo también EN la pasarela (su busybox 1.20).
#   sh tools/test-lan-hostname.sh mod/lan/overlay/usr/sbin/mgl001_lan_init.sh
INIT=${1:?mgl001_lan_init.sh}
D=$(mktemp -d) || exit 1
BLOCK=$(sed -n '/^if \[ ! -s \/data\/mgl001\/node_name \]/,/^hostname "\$HOST"$/p' "$INIT" |
    sed "s|/data/mgl001|$D|g; s|^hostname \"\\\$HOST\"\$|echo \"\$HOST\|\$(cat $D/node_name 2>/dev/null)\"|")
[ -n "$BLOCK" ] || { echo "BAD: names block not found in $INIT"; exit 1; }
SUFFIX=A1B2; ok=0; bad=0
run() { # node_name api_key hostname
    rm -f "$D/node_name" "$D/api_key" "$D/hostname"
    [ -n "$1" ] && echo "$1" > "$D/node_name"
    [ -n "$2" ] && echo k > "$D/api_key"
    [ -n "$3" ] && printf '%s\n' "$3" > "$D/hostname"
    eval "$BLOCK"
}
chk() { if [ "$1" = "$2" ]; then ok=$((ok+1)); else bad=$((bad+1)); echo "BAD: $3: «$1» want «$2»"; fi; }
L63=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
chk "$(run '' k '')" "BurbuHub-A1B2|mijia-hub-v2-a1b2" "paired, first boot"
chk "$(run 'otro-nombre' k '')" "BurbuHub-A1B2|otro-nombre" "name already kept"
chk "$(run '' '' '')" "BurbuHub-A1B2|" "fresh install"
chk "$(run '' k 'Casa-Rack2')" "Casa-Rack2|mijia-hub-v2-a1b2" "set from HA"
chk "$(run '' k '-mal')" "BurbuHub-A1B2|mijia-hub-v2-a1b2" "leading hyphen"
chk "$(run '' k 'mal-')" "BurbuHub-A1B2|mijia-hub-v2-a1b2" "trailing hyphen"
chk "$(run '' k 'a_b')" "BurbuHub-A1B2|mijia-hub-v2-a1b2" "underscore"
chk "$(run '' k 'con espacio')" "BurbuHub-A1B2|mijia-hub-v2-a1b2" "space"
chk "$(run '' k "${L63}a")" "BurbuHub-A1B2|mijia-hub-v2-a1b2" "64 chars"
chk "$(run '' k "$L63")" "$L63|mijia-hub-v2-a1b2" "63 chars"
chk "$(run '' k 'x')" "x|mijia-hub-v2-a1b2" "one char"
rm -f "$D/node_name" "$D/api_key" "$D/hostname"; rmdir "$D"
echo "ok=$ok bad=$bad total=$((ok+bad)) expected=11"
[ "$bad" = 0 ] && [ $((ok+bad)) = 11 ]
