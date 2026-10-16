# Firmware

Each `stock/mgl001_<version>/` and `custom/mgl001_<version>_mod<date>/` holds a `kernel` (u-boot
uImage for the KERNEL/KERNEL_BAK partition) and a `rootfs.sqfs` (squashfs for the rootfs partition),
exactly as `/bin/fw_update.sh` on the gateway expects them. `list.txt` is the menu that
`mgl001_update.sh` reads, with the size and SHA-256 of every file.

Cada `stock/mgl001_<versión>/` y `custom/mgl001_<versión>_mod<fecha>/` lleva un `kernel` (uImage de
u-boot para la partición KERNEL/KERNEL_BAK) y un `rootfs.sqfs` (squashfs para la partición rootfs),
tal como los espera `/bin/fw_update.sh` en la pasarela. `list.txt` es el menú que lee
`mgl001_update.sh`, con el tamaño y el SHA-256 de cada fichero.
