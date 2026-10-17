#!/bin/sh
# mgl001_fw LAN variant: what the gateway needs to run with no Xiaomi service at all (no cloud,
# no Mi Home, no Matter, no Xiaomi Bluetooth, no Wi-Fi). Called by rcS instead of fw_manager.sh -r.
# The quick part runs in the foreground (chip reset, names, time zone); network and time go on in
# the background so they can never hold up the boot.
#
# Variante LAN de mgl001_fw: lo que necesita la pasarela sin ningún servicio de Xiaomi (ni nube, ni
# Mi Home, ni Matter, ni el Bluetooth de Xiaomi, ni wifi). rcS la llama en lugar de fw_manager.sh -r.
# Lo rápido va en primer plano (reset del chip, nombres, zona horaria); la red y la hora siguen en
# segundo plano para que nunca retrasen el arranque.

GETPROP=agetprop

# Zigbee chip: out of its bootloader and reset, as Xiaomi's Z3GatewayHost does at start
# Chip Zigbee: fuera del bootloader y con un reset, como hace al arrancar el Z3GatewayHost de Xiaomi
/bin/zigbee_isp.sh 1 >/dev/null
/bin/zigbee_reset.sh 1 >/dev/null
sleep 0.2
/bin/zigbee_reset.sh 0 >/dev/null

# Same MAC and host name as the stock firmware / La misma MAC y el mismo nombre que el original
LAN=$($GETPROP persist.sys.lan_name); [ -n "$LAN" ] || LAN=eth0
MAC=$($GETPROP persist.sys.lan_mac)
[ -n "$MAC" ] && ifconfig "$LAN" hw ether "$MAC"
SUFFIX=$($GETPROP persist.sys.wlan_mac | cut -b 13-14,16-17 | tr '[a-f]' '[A-F]')
[ -n "$SUFFIX" ] || SUFFIX=0000
hostname "Mijia_Hub_V2-$SUFFIX"

# Time zone: Home Assistant's own (mgl001-hass keeps it in /data/mgl001/localtime when Home Assistant
# connects); until then the one Mi Home saved (as Xiaomi's fw_manager.sh does), or UTC
# Zona horaria: la de Home Assistant (mgl001-hass la guarda en /data/mgl001/localtime cuando conecta);
# hasta entonces la que guardó Mi Home (como fw_manager.sh de Xiaomi), o UTC
TZFILE=/data/mgl001/localtime
[ -s "$TZFILE" ] || TZFILE=$($GETPROP persist.app.miio_timezone)
[ -f "$TZFILE" ] || TZFILE=/usr/share/zoneinfo/UTC
ln -sf "$TZFILE" /tmp/localtime

# Log in RAM, at most 1 MB (the installer needs the memory for an update)
# Registro en RAM, 1 MB como mucho (el instalador necesita la memoria para actualizar)
syslogd -s 1024 -b0

# Zigbee bridge for ZHA: its own service, first and apart from the Home Assistant one, so restarting that
# never cuts Zigbee. TCP 6638, announced by mDNS for ZHA to discover. Options in /data/mgl001/zigbee.args.
# Puente Zigbee para ZHA: su propio servicio, el primero y aparte del de Home Assistant, para que reiniciar
# aquel nunca corte el Zigbee. TCP 6638, anunciado por mDNS para que ZHA lo descubra.
(
    while :; do
        # shellcheck disable=SC2046
        /usr/bin/mgl001-zigbee $(cat /data/mgl001/zigbee.args 2>/dev/null) 2>&1 | logger -t mgl001-zigbee
        sleep 5
    done
) &

# Time: NTP started again if it stops. busybox 1.20 ntpd exits when a server name does not resolve,
# which happens if DNS is not answering yet when the address arrives.
# Hora: NTP que vuelve a arrancar si se para. La ntpd de busybox 1.20 sale si un nombre no resuelve,
# y pasa si el DNS aún no contesta cuando llega la dirección.
(
    while :; do
        ntpd -n -p 0.pool.ntp.org -p 1.pool.ntp.org -p 2.pool.ntp.org -p 3.pool.ntp.org >/dev/null 2>&1
        sleep 30
    done
) &

(
    ifconfig "$LAN" up
    # DHCP in the background forever / DHCP en segundo plano para siempre
    udhcpc -i "$LAN" -b -S -t 10 -A 10 -x "hostname:$(hostname)" -s /usr/share/mgl001/udhcpc.script
) &

# Home Assistant: the gateway as a native ESPHome device, started again if it ever stops. Its log goes
# to syslog (/var/log/messages). Extra options, if any, in /data/mgl001/hass.args (e.g. -no-bt).
# Home Assistant: la pasarela como dispositivo ESPHome nativo, que vuelve a arrancar si se para. Su
# registro va a syslog (/var/log/messages). Opciones extra, si hacen falta, en /data/mgl001/hass.args.
(
    while :; do
        # shellcheck disable=SC2046
        /usr/bin/mgl001-hass $(cat /data/mgl001/hass.args 2>/dev/null) 2>&1 | logger -t mgl001-hass
        sleep 10
    done
) &

exit 0
