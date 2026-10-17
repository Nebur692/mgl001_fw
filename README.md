<div align="center">

# mgl001_fw

*Stock and modified firmware for the Xiaomi Smart Home Hub 2, with telnet and SSH always open*

![Release](https://img.shields.io/github/v/release/Nebur692/mgl001_fw?label=release&color=blue)
[![Ko-fi](https://ko-fi.com/img/githubbutton_sm.svg)](https://ko-fi.com/nebur69265723)
[![PayPal](https://img.shields.io/badge/PayPal-donate-00457C?logo=paypal&logoColor=white)](https://paypal.me/0SkillS)

🇬🇧 [English](#english) · 🇪🇸 [Español](#español)

</div>

---

## English

Firmware and an installer for the **Xiaomi Smart Home Hub 2** (`lumi.gateway.mcn001` / `lumi.gateway.mgl001`, models ZNDMWG04LM, DMWG03LM, BHR6765GL). Inspired by [zvldz/mgl03_fw](https://github.com/zvldz/mgl03_fw), which does the same for the older Multimode Gateway 3 — a model this repository does **not** cover.

Each modified firmware is the matching stock Xiaomi firmware, untouched, plus telnet and SSH always open at boot and a few diagnostic tools. Nothing of the original is removed: the boot script and every original file are kept, and the modifications are started **in the background**, so they can never hold up the gateway boot. The installer writes to the firmware bank that is not running and then switches to it, so paired devices, automations and settings in `/data` are kept.

### ✨ What's new / Features

- **Telnet and SSH always open at boot**, in parallel with the gateway services (SSH with no password, like telnet; your own key in `/data/root/.ssh/authorized_keys` works too). A watchdog starts them again within a minute if they ever stop listening.
- **Block Mi Home updates when you want**: `mgl001-ota lock` makes Mi Home updates fail before anything is written, `mgl001-ota unlock` allows them again, `mgl001-ota status` tells which. It survives reboots.
- **Diagnostic tools** built static for the gateway's ARM CPU: `curl` (HTTPS), `tcpdump`, `htop`, `nano` — plus `dropbear` (SSH). The build can add `strace` and `socat` where there is room.
- **A/B install with no data loss**: writes the bank that is not in use and switches boot to it; `/data` is never touched.
- **Power-cut safe**: the boot settings are not touched while writing. They switch only at the end, in a single write of the U-Boot environment, once both images are written and checked — a power cut or a dropped SSH session at any point leaves the gateway on the firmware it was running.
- **Verified install**: checks model and flash layout, free memory, the GitHub certificate and SHA-256 of what it downloads, re-reads what it wrote, mounts the new rootfs and runs its shell before switching. It refuses to start if a boot switch is still waiting for a reboot.
- **No downgrades**: refuses to install a firmware older than the one running, because a Xiaomi update also reflashes the Zigbee chip and an older build may not work with it. Going back with `r` to an older bank warns and asks you to type `older`.
- **Reproducible**: `tools/build-tools.sh` and `tools/build-mod.sh` rebuild everything from the stock firmware; nothing is prebuilt by hand.
- **LAN variant**: no Xiaomi service at all; the gateway becomes a native Home Assistant device and a Zigbee coordinator for ZHA, updated from Home Assistant with a trial boot that comes back by itself (see [Usage](#-usage)).

Firmwares included: `1.0.5_0016` and `1.0.8_0013`, each stock and modified.

**Zigbee chip for ZHA / Zigbee2MQTT**: [`zigbee/`](zigbee/) has an EmberZNet 7.5.1 coordinator firmware for the Hub 2's EFR32MG21 (EZSP 13, 64 children), Xiaomi's stock one to go back, and how to flash them without opening the case.

### 📦 Installation

Open telnet or SSH on the gateway (for example through the [XiaomiGateway3](https://github.com/AlexxIT/XiaomiGateway3) Home Assistant integration, which opens telnet over the LAN), then run:

```sh
cd /tmp
printf 'GET /Nebur692/mgl001_fw/main/firmware/mgl001_update.sh HTTP/1.0\r\nHost: raw.githubusercontent.com\r\n\r\n' | openssl s_client -quiet -connect raw.githubusercontent.com:443 -servername raw.githubusercontent.com -CAfile /etc/ca-certificates.crt -verify_return_error -verify_hostname raw.githubusercontent.com 2>/dev/null | sed '1,/^.$/d' > mgl001_update.sh
sh mgl001_update.sh
```

The stock firmware has no `curl` and its `wget` has no HTTPS, so the download uses the gateway's own `openssl`, checking GitHub's certificate. Pick a firmware from the menu. The gateway reboots when it is done.

> **Everything you do, you do at your own risk.** Flashing a wrong firmware can leave the gateway recoverable only over UART (opening the case). Do not unplug the gateway while it is flashing.

> **Anyone who can reach the gateway over the network gets root on it, with no password.** That is how the modified firmware is meant to work: telnet and SSH open on every boot, and adding your own key does not close the passwordless login. Keep the gateway on a network you trust (an IoT VLAN, for example) and never forward ports 22 or 23 to it from the internet.

### 🧭 Usage

- **Roll back**: run the installer again and choose `r` to boot the firmware kept in the other bank.
- **Extra tools**: drop static ARM binaries in `/data/bin` (already on `PATH`) and libraries in `/data/lib`; they survive reboots and firmware changes.
- **Run your own at boot**: put an executable `/data/run.sh`; the modified firmware runs it in the background at the end of boot.
- **Mi Home updates**: `mgl001-ota lock` / `unlock` / `status`. While locked, Mi Home shows the update as failed and the downloaded package is deleted, as Xiaomi does when an update fails.
- **Factory reset** formats `/data`: the update lock, the SSH host key (your client will warn that it changed) and `/data/run.sh` are gone. Telnet and SSH stay open, they live in the firmware.
- **Root password**: it is cleared in `/res`, which no firmware change touches, so it stays empty even after going back to a stock firmware.

#### Home Assistant switch for Mi Home updates

The Home Assistant container can log in with no password, so a `command_line` switch is enough (replace `GATEWAY_IP`):

```yaml
command_line:
  - switch:
      name: Hub 2 Mi Home updates blocked
      unique_id: hub2_mi_home_updates_blocked
      command_on: ssh -o BatchMode=yes -o StrictHostKeyChecking=accept-new root@GATEWAY_IP mgl001-ota lock
      command_off: ssh -o BatchMode=yes -o StrictHostKeyChecking=accept-new root@GATEWAY_IP mgl001-ota unlock
      command_state: ssh -o BatchMode=yes -o StrictHostKeyChecking=accept-new root@GATEWAY_IP mgl001-ota status
      value_template: "{{ value == 'locked' }}"
```

#### LAN variant: a native Home Assistant device

**BurbuHub** — the firmwares named `mgl001_<version>_lan<date>` (shown in Home Assistant as **BurbuHub** and the date of the version, e.g. `BurbuHub 2026.10.17.5`) remove every Xiaomi service: cloud, Mi Home, Matter, Xiaomi Bluetooth and Wi‑Fi. The gateway becomes a Home Assistant device (ESPHome API) and a network Zigbee coordinator for ZHA, both found by Home Assistant by itself. It keeps wired Ethernet with DHCP (same host name and MAC address as the stock firmware), NTP time in the time zone Mi Home saved, telnet and SSH, and the hardware watchdog.

**Before you start**

- The gateway must be connected **by Ethernet cable** (there is no Wi‑Fi in this variant) and run a firmware of this repository (telnet or SSH open).
- **Mi Home will no longer see the gateway**, and Zigbee devices paired in Mi Home must be **paired again in ZHA**.

**Set it up, step by step**

1. **Install the LAN firmware.** Run the installer (see *Installation*) and choose the line that contains `_lan`. The gateway reboots into it; in about a minute you can log in again with telnet or SSH.
2. **Add the gateway to Home Assistant.** On the gateway, show its encryption key with `cat /data/mgl001/api_key`. In Home Assistant open **Settings → Devices & services**: the gateway appears as a discovered **ESPHome** device. Press **Add** and paste the key. If it does not appear, use **Add integration → ESPHome**, host `GATEWAY_IP`, port `6053`.
3. **Add the Zigbee coordinator to ZHA.** In the same page a discovered **Zigbee Home Automation** device appears, *Xiaomi Hub 2 Zigbee*. Press **Add** and follow the steps; to bring an existing Zigbee network, restore its ZHA backup when asked. If it does not appear: **Add integration → Zigbee Home Automation** and type `socket://GATEWAY_IP:6638` as the serial device path. The [`zigbee/`](zigbee/) folder has a newer coordinator firmware for up to 64 end devices.
4. **Optional — no Bluetooth proxy.** The Bluetooth proxy (passive, active and automatic scanning, connections, pairing) is experimental. To turn it off: `echo -no-bt > /data/mgl001/hass.args` and `reboot`.

**Coming from openmiio_agent?** The Zigbee bridge is now part of the firmware (TCP port 6638). Delete the `openmiio_agent` line from `/data/run.sh` and `reboot`. Then, in Home Assistant: **Settings → Devices & services → Zigbee Home Automation → Configure → Submit → Change the current adapter's settings**. In **Serial device path** type `socket://GATEWAY_IP:6638`, then choose **Migrate manually → Keep adapter network settings**. The network and the paired devices stay as they are.

**Updates from Home Assistant**

- The gateway's **Firmware** update entity checks `firmware/list.txt` of this repository two minutes after boot, every six hours, and when you press **Check for updates**. A new version can take up to five minutes to show after it is published (GitHub's cache).
- **Install** writes the firmware bank that is not in use and boots it **once, as a trial**. Zigbee stops for one or two minutes.
- The new firmware stays only when ZHA and Home Assistant are connected again. If they are not within ten minutes, the gateway goes back to the previous firmware by itself. Unplugging the gateway during the trial also goes back.

**On the gateway**

| Command | What it does |
|---|---|
| `mgl001-boot status` | Which bank runs and which one boots next |
| `mgl001-boot try` | Boots the other bank once, as a trial (it reboots) |
| `mgl001-boot keep` | On a trial: keeps this firmware |
| `mgl001-boot back && reboot` | Goes back to the firmware in the other bank |
| `cat /var/log/messages` | Log of the system and of its two services: `mgl001-zigbee` (the Zigbee bridge) and `mgl001-hass` (the Home Assistant device) |

Each service restarts by itself if it stops, and they are independent: restarting the Home Assistant device never cuts Zigbee. Their options go in `/data/mgl001/zigbee.args` and `/data/mgl001/hass.args`.

**Entities in Home Assistant**: RGB LED, the button (press, double press, long press), Zigbee bridge connected, Ethernet connected and its speed, IP address, CPU temperature, last reboot (power on, software or watchdog), time zone, Zigbee firmware and protocol (as the chip tells ZHA), Bluetooth firmware and version, uptime, free memory, load, free storage, firmware, firmware bank, restart button and firmware update. Without Mi Home, recovery level 2 below does not apply; telnet and SSH stay open as on the modified firmware.

**Time zone**: the gateway takes Home Assistant's own (**Settings → System → General**) every time Home Assistant connects, keeps it for the next boots and follows it if you change it there. Until Home Assistant has connected once it uses the one Mi Home saved, or UTC.

### 🩹 Recovery — if you lose access

With a modified firmware installed you have access on three levels, none needing to open the case:

1. **On the modified firmware you cannot be left without telnet or SSH**: the boot script opens both on every boot, unconditionally and in the background, and a watchdog starts them again within a minute if they stop. A **reboot** always brings them back.
2. **If you lose the modified firmware itself** (a Xiaomi update overwrote it, or the boot was switched), the XiaomiGateway3 integration reopens telnet over the LAN using your Mi Home token and the gateway **key** — this also works on stock firmware, including 1.0.8. Save your key somewhere safe.
3. **If even that is gone**, reflash from a full NAND backup you took beforehand (see the installer and `firmware/`), or from the firmware kept in the other bank.

### 🛠️ Build it yourself

```sh
docker run --rm -v "$PWD":/w -w /w alpine:3 sh tools/build-tools.sh          # tools -> build/out/bin
docker run --rm -v "$PWD":/w -w /w alpine:3 sh tools/build-mod.sh 1.0.8_0013 20261016.2
docker run --rm -v "$PWD":/w -w /w golang:1.27.2 sh tools/build-hass.sh      # LAN variant: mgl001-hass -> build/out/bin
docker run --rm -e VARIANT=lan -v "$PWD":/w -w /w alpine:3 sh tools/build-mod.sh 1.0.8_0013 20261017.2
sh tools/make-list.sh                                                        # refresh firmware/list.txt
```

To add a new stock version, read the 12 NAND partitions of a gateway running it (read-only), put its kernel and `rootfs.sqfs` under `firmware/stock/mgl001_<version>/`, and run the two scripts above.

The trial boot has its own bench (an environment file and a stock `fw_update.sh`): `sh tools/test-mgl001-boot.sh mod/lan/overlay/usr/sbin/mgl001-boot <stock rootfs>/bin/fw_update.sh <file with bootcmd= and bootargs=>`.

### 💙 Support

*None of this would be possible without the community's support. If this project has been useful to you, consider supporting it on [Ko-fi](https://ko-fi.com/nebur69265723) or [PayPal](https://paypal.me/0SkillS) — every bit helps keep it maintained.*

### ⚠️ Disclaimer

Not affiliated with Xiaomi, Aqara or Lumi United. The files under `firmware/stock/` and the stock parts inside `firmware/custom/` are Xiaomi's firmware, included only so the gateway's owner can reinstall or modify their own device; all rights belong to their owners. The scripts under `tools/` and the files under `mod/` are the original work of this repository. Use at your own risk.

---

## Español

Firmware y un instalador para la **Xiaomi Smart Home Hub 2** (`lumi.gateway.mcn001` / `lumi.gateway.mgl001`, modelos ZNDMWG04LM, DMWG03LM, BHR6765GL). Inspirado en [zvldz/mgl03_fw](https://github.com/zvldz/mgl03_fw), que hace lo mismo para la Multimode Gateway 3 anterior — un modelo que este repositorio **no** cubre.

Cada firmware modificado es el firmware original de Xiaomi que le corresponde, sin tocar, más telnet y SSH siempre abiertos en el arranque y unas herramientas de diagnóstico. No se quita nada del original: se conservan el guion de arranque y todos los ficheros originales, y las modificaciones se lanzan **en paralelo**, así que nunca pueden frenar el arranque de la pasarela. El instalador graba en la copia del firmware que no está en uso y después cambia a ella, de modo que se conservan los aparatos emparejados, las automatizaciones y los ajustes de `/data`.

### ✨ Novedades / Características

- **Telnet y SSH siempre abiertos en el arranque**, en paralelo a los servicios de la pasarela (SSH sin contraseña, como el telnet; tu propia llave en `/data/root/.ssh/authorized_keys` también vale). Un vigilante los vuelve a lanzar en menos de un minuto si dejan de escuchar.
- **Bloquear las actualizaciones de Mi Home cuando quieras**: `mgl001-ota lock` hace que fallen antes de escribir nada, `mgl001-ota unlock` las vuelve a permitir y `mgl001-ota status` dice cómo está. Aguanta los reinicios.
- **Herramientas de diagnóstico** compiladas estáticas para el procesador ARM de la pasarela: `curl` (HTTPS), `tcpdump`, `htop`, `nano` — además de `dropbear` (SSH). La construcción puede añadir `strace` y `socat` donde haya sitio.
- **Instalación A/B sin pérdida de datos**: graba la copia que no está en uso y cambia el arranque a ella; `/data` no se toca nunca.
- **A prueba de cortes de luz**: el arranque no se toca mientras se graba. Solo cambia al final, en una única escritura del entorno de U-Boot, cuando las dos imágenes están grabadas y comprobadas: un corte de luz o una sesión SSH que se cae en cualquier momento dejan la pasarela con el firmware que tenía.
- **Instalación verificada**: comprueba el modelo y la estructura de la memoria, la memoria libre, el certificado de GitHub y el SHA-256 de lo que descarga, relee lo que graba, monta el sistema nuevo y ejecuta su shell antes de cambiar. No empieza si hay un cambio de arranque pendiente de reiniciar.
- **Sin bajar de versión**: se niega a instalar un firmware más antiguo que el que está en uso, porque una actualización de Xiaomi también regraba el chip Zigbee y una versión anterior podría no entenderse con él. Volver con `r` a una copia más antigua avisa y pide escribir `older`.
- **Reproducible**: `tools/build-tools.sh` y `tools/build-mod.sh` reconstruyen todo a partir del firmware original; nada está precompilado a mano.
- **Variante LAN**: sin ningún servicio de Xiaomi; la pasarela pasa a ser un dispositivo nativo de Home Assistant y un coordinador Zigbee para ZHA, que se actualiza desde Home Assistant con un arranque de prueba que vuelve solo (ver [Uso](#-uso)).

Firmwares incluidos: `1.0.5_0016` y `1.0.8_0013`, cada uno original y modificado.

**Chip Zigbee para ZHA / Zigbee2MQTT**: en [`zigbee/`](zigbee/) hay un firmware de coordinador EmberZNet 7.5.1 para el EFR32MG21 de la Hub 2 (EZSP 13, 64 hijos), el de fábrica de Xiaomi para volver, y cómo flashearlos sin abrir la carcasa.

### 📦 Instalación

Abre telnet o SSH en la pasarela (por ejemplo con la integración de Home Assistant [XiaomiGateway3](https://github.com/AlexxIT/XiaomiGateway3), que abre el telnet por la red local) y ejecuta:

```sh
cd /tmp
printf 'GET /Nebur692/mgl001_fw/main/firmware/mgl001_update.sh HTTP/1.0\r\nHost: raw.githubusercontent.com\r\n\r\n' | openssl s_client -quiet -connect raw.githubusercontent.com:443 -servername raw.githubusercontent.com -CAfile /etc/ca-certificates.crt -verify_return_error -verify_hostname raw.githubusercontent.com 2>/dev/null | sed '1,/^.$/d' > mgl001_update.sh
sh mgl001_update.sh
```

El firmware original no trae `curl` y su `wget` no sabe HTTPS, así que la descarga usa el `openssl` de la propia pasarela, comprobando el certificado de GitHub. Elige un firmware del menú. La pasarela se reinicia al terminar.

> **Todo lo que hagas, lo haces bajo tu responsabilidad.** Grabar un firmware equivocado puede dejar la pasarela recuperable solo por UART (abriendo la carcasa). No desenchufes la pasarela mientras graba.

> **Cualquiera que llegue a la pasarela por la red entra en ella como root, sin contraseña.** Así es como funciona el firmware modificado: telnet y SSH se abren en cada arranque, y poner tu propia llave no quita la entrada sin contraseña. Ten la pasarela en una red de confianza (una VLAN para los aparatos domóticos, por ejemplo) y no redirijas nunca los puertos 22 ni 23 hacia ella desde internet.

### 🧭 Uso

- **Volver atrás**: ejecuta el instalador otra vez y elige `r` para arrancar el firmware que quedó en la otra copia.
- **Más herramientas**: deja binarios ARM estáticos en `/data/bin` (ya está en el `PATH`) y bibliotecas en `/data/lib`; sobreviven a los reinicios y a los cambios de firmware.
- **Lo tuyo al arrancar**: pon un `/data/run.sh` ejecutable; el firmware modificado lo ejecuta en paralelo al final del arranque.
- **Actualizaciones de Mi Home**: `mgl001-ota lock` / `unlock` / `status`. Mientras están bloqueadas, Mi Home da la actualización por fallida y el paquete descargado se borra, como hace Xiaomi cuando una actualización falla.
- **Restablecer de fábrica** formatea `/data`: se pierden el bloqueo de actualizaciones, la llave del equipo para SSH (tu cliente avisará de que ha cambiado) y `/data/run.sh`. Telnet y SSH siguen abiertos, porque viven en el firmware.
- **Contraseña de root**: se borra en `/res`, que ningún cambio de firmware toca, así que sigue vacía aunque vuelvas a un firmware original.

#### Interruptor en Home Assistant para las actualizaciones de Mi Home

El contenedor de Home Assistant entra sin contraseña, así que basta con un interruptor `command_line` (cambia `GATEWAY_IP`):

```yaml
command_line:
  - switch:
      name: Hub 2 Mi Home updates blocked
      unique_id: hub2_mi_home_updates_blocked
      command_on: ssh -o BatchMode=yes -o StrictHostKeyChecking=accept-new root@GATEWAY_IP mgl001-ota lock
      command_off: ssh -o BatchMode=yes -o StrictHostKeyChecking=accept-new root@GATEWAY_IP mgl001-ota unlock
      command_state: ssh -o BatchMode=yes -o StrictHostKeyChecking=accept-new root@GATEWAY_IP mgl001-ota status
      value_template: "{{ value == 'locked' }}"
```

#### Variante LAN: un dispositivo nativo de Home Assistant

**BurbuHub** — los firmwares llamados `mgl001_<versión>_lan<fecha>` (en Home Assistant se ven como **BurbuHub** y la fecha de la versión, p. ej. `BurbuHub 2026.10.17.5`) quitan todos los servicios de Xiaomi: nube, Mi Home, Matter, Bluetooth de Xiaomi y wifi. La pasarela pasa a ser un dispositivo de Home Assistant (API de ESPHome) y un coordinador Zigbee en red para ZHA, y Home Assistant encuentra los dos solo. Conserva la red por cable con DHCP (el mismo nombre y la misma dirección MAC que el firmware original), la hora por NTP en la zona horaria que guardó Mi Home, telnet y SSH, y el vigilante de hardware.

**Antes de empezar**

- La pasarela tiene que estar conectada **por cable de red** (esta variante no tiene wifi) y llevar un firmware de este repositorio (telnet o SSH abiertos).
- **Mi Home dejará de ver la pasarela**, y los dispositivos Zigbee emparejados en Mi Home hay que **volver a emparejarlos en ZHA**.

**Puesta en marcha, paso a paso**

1. **Instala el firmware LAN.** Ejecuta el instalador (ver *Instalación*) y elige la línea que contiene `_lan`. La pasarela reinicia con él; en un minuto más o menos puedes volver a entrar por telnet o SSH.
2. **Añade la pasarela a Home Assistant.** En la pasarela, muestra su clave de cifrado con `cat /data/mgl001/api_key`. En Home Assistant abre **Ajustes → Dispositivos y servicios**: la pasarela aparece como dispositivo **ESPHome** descubierto. Pulsa **Añadir** y pega la clave. Si no aparece, usa **Añadir integración → ESPHome**, host `GATEWAY_IP`, puerto `6053`.
3. **Añade el coordinador Zigbee a ZHA.** En la misma página aparece un **Zigbee Home Automation** descubierto, *Xiaomi Hub 2 Zigbee*. Pulsa **Añadir** y sigue los pasos; para traer una red Zigbee que ya tienes, restaura su copia de ZHA cuando te lo pregunte. Si no aparece: **Añadir integración → Zigbee Home Automation** y escribe `socket://GATEWAY_IP:6638` como ruta del dispositivo serie. En la carpeta [`zigbee/`](zigbee/) hay un firmware de coordinador más nuevo, para hasta 64 dispositivos finales.
4. **Opcional — sin proxy Bluetooth.** El proxy Bluetooth (escaneo pasivo, activo y automático, conexiones, emparejamiento) es experimental. Para apagarlo: `echo -no-bt > /data/mgl001/hass.args` y `reboot`.

**¿Vienes de openmiio_agent?** El puente Zigbee ya forma parte del firmware (puerto TCP 6638). Borra la línea de `openmiio_agent` de `/data/run.sh` y haz `reboot`. Después, en Home Assistant: **Ajustes → Dispositivos y servicios → Zigbee Home Automation → Configurar → Enviar → Cambiar la configuración del adaptador actual**. En **Ruta del dispositivo serie** escribe `socket://GATEWAY_IP:6638` y elige **Migrar manualmente → Mantener la configuración de red del adaptador**. La red y los dispositivos emparejados se quedan como están.

**Actualizaciones desde Home Assistant**

- La entidad de actualización **Firmware** de la pasarela consulta `firmware/list.txt` de este repositorio dos minutos después de arrancar, cada seis horas y cuando pulsas **Buscar actualizaciones**. Una versión nueva puede tardar hasta cinco minutos en verse tras publicarse (la caché de GitHub).
- **Instalar** graba el banco de firmware que no está en uso y lo arranca **una vez, de prueba**. El Zigbee se para uno o dos minutos.
- El firmware nuevo solo se queda cuando ZHA y Home Assistant vuelven a estar conectados. Si no lo están en diez minutos, la pasarela vuelve sola al firmware anterior. Desenchufarla durante la prueba también la devuelve.

**En la pasarela**

| Orden | Qué hace |
|---|---|
| `mgl001-boot status` | Qué banco corre y cuál arranca después |
| `mgl001-boot try` | Arranca una vez el otro banco, de prueba (reinicia) |
| `mgl001-boot keep` | En una prueba: se queda con este firmware |
| `mgl001-boot back && reboot` | Vuelve al firmware del otro banco |
| `cat /var/log/messages` | Registro del sistema y de sus dos servicios: `mgl001-zigbee` (el puente Zigbee) y `mgl001-hass` (el dispositivo de Home Assistant) |

Cada servicio vuelve a arrancar solo si se para, y son independientes: reiniciar el dispositivo de Home Assistant nunca corta el Zigbee. Sus opciones van en `/data/mgl001/zigbee.args` y `/data/mgl001/hass.args`.

**Entidades en Home Assistant**: LED RGB, el botón (pulsación, doble pulsación y pulsación larga), puente Zigbee conectado, red por cable conectada y su velocidad, dirección IP, temperatura del procesador, último reinicio (encendido, software o vigilante), zona horaria, firmware y protocolo del Zigbee (lo que el chip le dice a ZHA), firmware y versión del Bluetooth, tiempo encendida, memoria libre, carga, almacenamiento libre, firmware, banco de firmware, botón de reinicio y actualización de firmware. Sin Mi Home, el nivel 2 de recuperación de abajo no aplica; telnet y SSH siguen abiertos como en el firmware modificado.

**Zona horaria**: la pasarela toma la de Home Assistant (**Ajustes → Sistema → General**) cada vez que Home Assistant conecta, la guarda para los siguientes arranques y la sigue si la cambias allí. Hasta que Home Assistant conecta por primera vez usa la que guardó Mi Home, o UTC.

### 🩹 Recuperación — si pierdes el acceso

Con un firmware modificado instalado tienes acceso por tres niveles, ninguno necesita abrir la carcasa:

1. **Con el firmware modificado no puedes quedarte sin telnet ni SSH**: el guion de arranque los abre en cada encendido, de forma incondicional y en paralelo, y un vigilante los vuelve a lanzar en menos de un minuto si se caen. **Reiniciar** la pasarela siempre los devuelve.
2. **Si pierdes el propio firmware modificado** (una actualización de Xiaomi lo sobrescribió, o se cambió la copia de arranque), la integración XiaomiGateway3 vuelve a abrir el telnet por la red local con tu token de Mi Home y la **clave** de la pasarela — esto funciona también en el firmware original, incluida la 1.0.8. Guarda tu clave en un sitio seguro.
3. **Si incluso eso se pierde**, regraba desde una copia completa de la memoria NAND hecha de antemano (ver el instalador y `firmware/`), o desde el firmware que quedó en la otra copia.

### 🛠️ Compilarlo tú mismo

```sh
docker run --rm -v "$PWD":/w -w /w alpine:3 sh tools/build-tools.sh          # herramientas -> build/out/bin
docker run --rm -v "$PWD":/w -w /w alpine:3 sh tools/build-mod.sh 1.0.8_0013 20261016.2
docker run --rm -v "$PWD":/w -w /w golang:1.27.2 sh tools/build-hass.sh      # variante LAN: mgl001-hass -> build/out/bin
docker run --rm -e VARIANT=lan -v "$PWD":/w -w /w alpine:3 sh tools/build-mod.sh 1.0.8_0013 20261017.2
sh tools/make-list.sh                                                        # regenera firmware/list.txt
```

Para añadir una versión original nueva, lee las 12 particiones de la memoria NAND de una pasarela que la tenga (solo lectura), pon su kernel y su `rootfs.sqfs` en `firmware/stock/mgl001_<versión>/`, y ejecuta los dos guiones de arriba.

El arranque de prueba tiene su propio banco (un fichero de entorno y un `fw_update.sh` original): `sh tools/test-mgl001-boot.sh mod/lan/overlay/usr/sbin/mgl001-boot <rootfs original>/bin/fw_update.sh <fichero con bootcmd= y bootargs=>`.

### 💙 Apoya el proyecto

*Sin el apoyo de la comunidad estos proyectos no serían posibles. Si te ha resultado útil, puedes apoyarlo en [Ko-fi](https://ko-fi.com/nebur69265723) o [PayPal](https://paypal.me/0SkillS) — cualquier aportación ayuda a seguir manteniéndolo.*

### ⚠️ Aviso

Sin relación con Xiaomi, Aqara ni Lumi United. Los ficheros bajo `firmware/stock/` y las partes originales dentro de `firmware/custom/` son firmware de Xiaomi, incluidos solo para que el dueño de la pasarela pueda reinstalar o modificar su propio equipo; todos los derechos son de sus propietarios. Los guiones de `tools/` y los ficheros de `mod/` son obra original de este repositorio. Úsalo bajo tu responsabilidad.
