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

Firmwares included: `1.0.5_0016` and `1.0.8_0013`, each stock and modified.

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

### 🩹 Recovery — if you lose access

With a modified firmware installed you have access on three levels, none needing to open the case:

1. **On the modified firmware you cannot be left without telnet or SSH**: the boot script opens both on every boot, unconditionally and in the background, and a watchdog starts them again within a minute if they stop. A **reboot** always brings them back.
2. **If you lose the modified firmware itself** (a Xiaomi update overwrote it, or the boot was switched), the XiaomiGateway3 integration reopens telnet over the LAN using your Mi Home token and the gateway **key** — this also works on stock firmware, including 1.0.8. Save your key somewhere safe.
3. **If even that is gone**, reflash from a full NAND backup you took beforehand (see the installer and `firmware/`), or from the firmware kept in the other bank.

### 🛠️ Build it yourself

```sh
docker run --rm -v "$PWD":/w -w /w alpine:3 sh tools/build-tools.sh          # tools -> build/out/bin
docker run --rm -v "$PWD":/w -w /w alpine:3 sh tools/build-mod.sh 1.0.8_0013 20261016.2
sh tools/make-list.sh                                                        # refresh firmware/list.txt
```

To add a new stock version, read the 12 NAND partitions of a gateway running it (read-only), put its kernel and `rootfs.sqfs` under `firmware/stock/mgl001_<version>/`, and run the two scripts above.

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

Firmwares incluidos: `1.0.5_0016` y `1.0.8_0013`, cada uno original y modificado.

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

### 🩹 Recuperación — si pierdes el acceso

Con un firmware modificado instalado tienes acceso por tres niveles, ninguno necesita abrir la carcasa:

1. **Con el firmware modificado no puedes quedarte sin telnet ni SSH**: el guion de arranque los abre en cada encendido, de forma incondicional y en paralelo, y un vigilante los vuelve a lanzar en menos de un minuto si se caen. **Reiniciar** la pasarela siempre los devuelve.
2. **Si pierdes el propio firmware modificado** (una actualización de Xiaomi lo sobrescribió, o se cambió la copia de arranque), la integración XiaomiGateway3 vuelve a abrir el telnet por la red local con tu token de Mi Home y la **clave** de la pasarela — esto funciona también en el firmware original, incluida la 1.0.8. Guarda tu clave en un sitio seguro.
3. **Si incluso eso se pierde**, regraba desde una copia completa de la memoria NAND hecha de antemano (ver el instalador y `firmware/`), o desde el firmware que quedó en la otra copia.

### 🛠️ Compilarlo tú mismo

```sh
docker run --rm -v "$PWD":/w -w /w alpine:3 sh tools/build-tools.sh          # herramientas -> build/out/bin
docker run --rm -v "$PWD":/w -w /w alpine:3 sh tools/build-mod.sh 1.0.8_0013 20261016.2
sh tools/make-list.sh                                                        # regenera firmware/list.txt
```

Para añadir una versión original nueva, lee las 12 particiones de la memoria NAND de una pasarela que la tenga (solo lectura), pon su kernel y su `rootfs.sqfs` en `firmware/stock/mgl001_<versión>/`, y ejecuta los dos guiones de arriba.

### 💙 Apoya el proyecto

*Sin el apoyo de la comunidad estos proyectos no serían posibles. Si te ha resultado útil, puedes apoyarlo en [Ko-fi](https://ko-fi.com/nebur69265723) o [PayPal](https://paypal.me/0SkillS) — cualquier aportación ayuda a seguir manteniéndolo.*

### ⚠️ Aviso

Sin relación con Xiaomi, Aqara ni Lumi United. Los ficheros bajo `firmware/stock/` y las partes originales dentro de `firmware/custom/` son firmware de Xiaomi, incluidos solo para que el dueño de la pasarela pueda reinstalar o modificar su propio equipo; todos los derechos son de sus propietarios. Los guiones de `tools/` y los ficheros de `mod/` son obra original de este repositorio. Úsalo bajo tu responsabilidad.
