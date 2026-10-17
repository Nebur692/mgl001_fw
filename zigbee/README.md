# Zigbee chip firmware / Firmware del chip Zigbee

🇬🇧 [English](#english) · 🇪🇸 [Español](#español)

## English

The Hub 2 has an EFR32MG21 Zigbee chip on `/dev/ttyS1` (115200 baud, RTS/CTS). These files let you use it as a standard EmberZNet coordinator for ZHA or Zigbee2MQTT, through the LAN variant's own bridge (`socket://GATEWAY_IP:6638`, discovered by ZHA) or, on the modified firmware, through `openmiio_agent --zigbee.tcp=8888`.

| File | What it is |
|---|---|
| `mgl001_zigbee_ncp_7.5.1.0.gbl` | EmberZNet **7.5.1.0** (Gecko SDK 4.5.0), EZSP 13. **64 end-device children**, route table 250, source route table 200. |
| `mgl001_zigbee_ncp.yaml` | The manifest it is built from. |
| `xiaomi_stock_ncp_6.6.6.0_build218.gbl` | Xiaomi's stock coordinator firmware (EmberZNet 6.6.6, EZSP 7), taken from the gateway's `Network-Co-Processor.ota`, to go back. |

**Why this build.** It is the stack configuration of Nabu Casa's stable firmware for the Home Assistant Connect ZBT-1 (release `v2026.02.23`), which uses exactly the same chip (`EFR32MG21A020F512IM32`), with two changes: 64 children instead of 32, and the Hub 2 UART pins (TX PB01, RX PB00, CTS PC01, RTS PC00) taken from [darkxst/silabs-firmware-builder](https://github.com/darkxst/silabs-firmware-builder). Built the same way, the ZBT-1 firmware comes out identical to Nabu Casa's except for the build date and its CRC.

**What it fixes compared with the stock firmware.** The stock firmware reports a trust center address that differs from the EUI64 the coordinator presents, so Zigbee 3.0 devices (for example the Mi Light Detection Sensor) join, fail to get their own link key and leave a while later. EZSP 13 lets ZHA set it right.

**Flashing.** The bootloader (Gecko 1.9.2) is not touched. It is entered from the gateway itself: `zigbee_isp.sh 0`, `zigbee_reset.sh 1`, `zigbee_reset.sh 0`, `zigbee_isp.sh 1`. Then flash over the TCP bridge with [universal-silabs-flasher](https://github.com/NabuCasa/universal-silabs-flasher) (`--probe-methods bootloader:115200 flash --firmware …`). If a flash is interrupted, enter the bootloader the same way and flash again.

**Keep your network.** Take a ZHA backup first and restore it after flashing. The new firmware starts with the chip's factory EUI64; the restore writes your coordinator EUI64 back (EZSP 13 can rewrite it). Sensors already paired keep working; Zigbee 3.0 devices that left because of the stock trust center bug need to be paired again.

**Going back to Mi Home** needs the stock coordinator firmware first: Xiaomi's software only speaks EZSP 7.

**Building.** Use [NabuCasa/silabs-firmware-builder](https://github.com/NabuCasa/silabs-firmware-builder) at tag `v2026.02.23` with this manifest under `manifests/`.

## Español

La Hub 2 tiene un chip Zigbee EFR32MG21 en `/dev/ttyS1` (115200 baudios, RTS/CTS). Estos ficheros permiten usarlo como coordinador EmberZNet estándar para ZHA o Zigbee2MQTT, a través del puente propio de la variante LAN (`socket://GATEWAY_IP:6638`, que ZHA descubre) o, en el firmware modificado, de `openmiio_agent --zigbee.tcp=8888`.

| Fichero | Qué es |
|---|---|
| `mgl001_zigbee_ncp_7.5.1.0.gbl` | EmberZNet **7.5.1.0** (Gecko SDK 4.5.0), EZSP 13. **64 hijos directos**, tabla de rutas de 250 y de rutas de origen de 200. |
| `mgl001_zigbee_ncp.yaml` | El manifiesto con el que se compila. |
| `xiaomi_stock_ncp_6.6.6.0_build218.gbl` | El firmware de fábrica del coordinador de Xiaomi (EmberZNet 6.6.6, EZSP 7), sacado del `Network-Co-Processor.ota` de la pasarela, para volver atrás. |

**Por qué esta versión.** Es la configuración de la pila del firmware estable de Nabu Casa para el Home Assistant Connect ZBT-1 (release `v2026.02.23`), que lleva exactamente el mismo chip (`EFR32MG21A020F512IM32`), con dos cambios: 64 hijos en vez de 32, y los pines de la UART de la Hub 2 (TX PB01, RX PB00, CTS PC01, RTS PC00) sacados de [darkxst/silabs-firmware-builder](https://github.com/darkxst/silabs-firmware-builder). Compilado de la misma forma, el firmware del ZBT-1 sale idéntico al de Nabu Casa salvo la fecha de compilación y su CRC.

**Qué arregla frente al de fábrica.** El de fábrica anuncia una dirección del centro de confianza distinta del EUI64 con el que se presenta el coordinador, así que los dispositivos Zigbee 3.0 (por ejemplo el sensor de luz de Xiaomi) se unen, no consiguen su clave propia y se van al rato. Con EZSP 13, ZHA la deja bien.

**Flasheo.** El bootloader (Gecko 1.9.2) no se toca. Se entra desde la propia pasarela: `zigbee_isp.sh 0`, `zigbee_reset.sh 1`, `zigbee_reset.sh 0`, `zigbee_isp.sh 1`. Después se flashea por el puente TCP con [universal-silabs-flasher](https://github.com/NabuCasa/universal-silabs-flasher) (`--probe-methods bootloader:115200 flash --firmware …`). Si un flasheo se corta, se entra igual en el bootloader y se repite.

**Conservar la red.** Haz antes una copia de ZHA y restáurala después de flashear. El firmware nuevo arranca con el EUI64 de fábrica del chip; la restauración vuelve a escribir el del coordinador (con EZSP 13 se puede reescribir). Los sensores ya emparejados siguen funcionando; los Zigbee 3.0 que se fueron por el fallo del centro de confianza hay que volver a emparejarlos.

**Volver a Mi Home** exige antes el firmware de fábrica del coordinador: el software de Xiaomi solo habla EZSP 7.

**Compilarlo.** Con [NabuCasa/silabs-firmware-builder](https://github.com/NabuCasa/silabs-firmware-builder) en la etiqueta `v2026.02.23` y este manifiesto en `manifests/`.
