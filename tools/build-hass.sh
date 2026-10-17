#!/bin/sh
# Builds mgl001-hass (hass/), the native Home Assistant device of the LAN variant: static ARMv7, no cgo.
# Runs its checks and tests first. In the official Go image:
#   docker run --rm -v "$PWD":/w -w /w golang:1.27.2 sh tools/build-hass.sh
# Output: build/out/bin/mgl001-hass (tools/build-mod.sh takes it from there for VARIANT=lan)
#
# Compila mgl001-hass (hass/), el dispositivo nativo de Home Assistant de la variante LAN: ARMv7 estático.
# Antes pasa sus comprobaciones y pruebas. Salida: build/out/bin/mgl001-hass
set -eu
GO=${GO:-go}
OUT=$(pwd)/build/out/bin
mkdir -p "$OUT"
cd hass
"$GO" vet ./...
"$GO" test -count=1 ./...
CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 "$GO" build -trimpath -ldflags="-s -w" -o "$OUT/mgl001-hass" .
ls -l "$OUT/mgl001-hass"
