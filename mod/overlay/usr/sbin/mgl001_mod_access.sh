#!/bin/sh
# mgl001_fw: remote access always open. /etc/init.d/rcS starts it in the background, so it can never
# hold up the gateway boot: if anything here failed, the gateway would still boot as stock.
# mgl001_fw: acceso remoto siempre abierto. /etc/init.d/rcS lo lanza en paralelo, así que nunca puede
# frenar el arranque de la pasarela: si algo de aquí fallara, arrancaría igual que la original.

# Telnet: root without password, as the stock firmware does once Xiaomi Gateway 3 has opened it.
[ -f /res/passwd ] && passwd -d root >/dev/null 2>&1

# SSH: like telnet, root without password (-B). Keys in /data/root/.ssh/authorized_keys also work.
# The host key is kept in /data/dropbear, so it does not change between reboots.
mkdir -p /data/root/.ssh /data/dropbear
chmod 700 /data/root /data/root/.ssh /data/dropbear
KEY=/data/dropbear/dropbear_ed25519_host_key
[ -s "$KEY" ] || dropbearkey -t ed25519 -f "$KEY" >/dev/null 2>&1

# Watchdog: if telnetd or dropbear stop listening (or someone else's telnetd took port 23 and then
# quit), start them again. Checked by the listening port, not by name: an open SSH session is also
# called dropbear and would hide a dead listener.
# Vigilante: si telnetd o dropbear dejan de escuchar, se vuelven a lanzar. Se mira el puerto, no el
# nombre: una sesión SSH abierta también se llama dropbear y taparía un servidor caído.
listening() { netstat -lnt 2>/dev/null | grep -q "[:.]$1 "; }
while true; do
    listening 23 || telnetd
    listening 22 || dropbear -B -r "$KEY"
    sleep 60
done
