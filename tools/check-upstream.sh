#!/bin/sh
# Opens a GitHub issue when the Zigbee (NCP) or Bluetooth (Telink SDK) firmware has something new upstream
# (firmware/upstream.txt). One issue per new version: if one with the same title exists, nothing is opened.
# Run by .github/workflows/upstream-firmware.yml. DRY_RUN=1 only prints. Needs curl and jq.
#
# Abre una incidencia en GitHub cuando el firmware Zigbee o Bluetooth tiene algo nuevo arriba. Una por
# versión: si ya existe una con el mismo título, no abre otra. DRY_RUN=1 solo lo escribe.
set -eu
REPO=${GITHUB_REPOSITORY:-Nebur692/mgl001_fw}
API=https://api.github.com
auth() { if [ -n "${GH_TOKEN:-}" ]; then printf 'Authorization: Bearer %s' "$GH_TOKEN"; else printf 'X-No-Auth: 1'; fi; }
get() { curl -fsS -H "$(auth)" -H 'Accept: application/vnd.github+json' "$API/$1"; }

new=0
while IFS='|' read -r name upstream kind ours; do
    case "$name" in ''|'#'*) continue;; esac
    case "$kind" in
        release) latest=$(get "repos/$upstream/releases/latest" | jq -r .tag_name)
                 url="https://github.com/$upstream/releases/tag/$latest" ;;
        commit)  latest=$(get "repos/$upstream/commits/HEAD" | jq -r .sha)
                 url="https://github.com/$upstream/commit/$latest" ;;
        *) echo "! $name: unknown kind $kind"; exit 1 ;;
    esac
    [ -n "$latest" ] && [ "$latest" != null ] || { echo "! $name: could not read $upstream"; exit 1; }
    if [ "$latest" = "$ours" ]; then
        echo "$name: up to date ($ours)"
        continue
    fi
    short=$(echo "$latest" | cut -c1-12)
    title="Upstream $name firmware: $short"
    echo "$name: NEW $latest (we use $ours)"
    new=$((new + 1))
    # Already reported (open or closed)? / ¿Ya avisado (abierta o cerrada)?
    found=$(get "search/issues?q=$(printf 'repo:%s in:title "%s"' "$REPO" "$title" | jq -sRr @uri)" | jq -r .total_count)
    if [ "$found" != 0 ]; then
        echo "  already reported"
        continue
    fi
    body=$(printf 'New upstream for the %s firmware.\n\n- Upstream: %s\n- We use: `%s`\n- New: `%s`\n\nRebuild, test on the bench and on a gateway, then update `firmware/upstream.txt`.\n\n---\n\nHay una versión nueva del firmware %s. Recompilar, probar en el banco y en una pasarela, y actualizar `firmware/upstream.txt`.' \
        "$name" "$url" "$ours" "$latest" "$name")
    if [ "${DRY_RUN:-}" = 1 ]; then
        echo "  would open: $title"
        continue
    fi
    jq -n --arg t "$title" --arg b "$body" '{title: $t, body: $b}' |
        curl -fsS -X POST -H "$(auth)" -H 'Accept: application/vnd.github+json' --data @- "$API/repos/$REPO/issues" >/dev/null
    echo "  issue opened: $title"
done < "$(dirname "$0")/../firmware/upstream.txt"
echo "new upstream versions: $new"
