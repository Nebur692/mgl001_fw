#!/bin/sh
# mgl001_fw: remote access always open. /etc/init.d/rcS starts it in the background, so it can never
# hold up the gateway boot: if anything here failed, the gateway would still boot as stock.
# mgl001_fw: acceso remoto siempre abierto. /etc/init.d/rcS lo lanza en paralelo, así que nunca puede
# frenar el arranque de la pasarela: si algo de aquí fallara, arrancaría igual que la original.

# Telnet: root without password, as the stock firmware does once Xiaomi Gateway 3 has opened it.
[ -f /res/passwd ] && passwd -d root >/dev/null 2>&1
telnetd

# SSH: like telnet, root without password (-B). Keys in /data/root/.ssh/authorized_keys also work.
# The host key is kept in /data/dropbear, so it does not change between reboots.
mkdir -p /data/root/.ssh /data/dropbear
chmod 700 /data/root /data/root/.ssh /data/dropbear
KEY=/data/dropbear/dropbear_ed25519_host_key
[ -s "$KEY" ] || dropbearkey -t ed25519 -f "$KEY" >/dev/null 2>&1
dropbear -B -r "$KEY"
