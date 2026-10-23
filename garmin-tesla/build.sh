#!/usr/bin/env bash
# Builds My Tesla for the Venu 4.
#
#   TESSIE_TOKEN=xxxx ./build.sh            # 45mm watch
#   TESSIE_TOKEN=xxxx ./build.sh venu441mm  # 41mm watch
#
# Sideloaded apps can't be configured from the Garmin Connect phone app, so the
# token (and optional TESSIE_VIN) is written into a temporary copy of the
# project and never into this folder.
set -euo pipefail

device="${1:-venu445mm}"
here="$(cd "$(dirname "$0")" && pwd)"
out="$here/bin/MyTesla-$device.prg"
key="${GARMIN_DEVELOPER_KEY:-$here/developer_key.der}"

monkeyc="monkeyc"
if [ -n "${CIQ_SDK:-}" ]; then
    monkeyc="$CIQ_SDK/bin/monkeyc"
elif ! command -v monkeyc >/dev/null; then
    sdk_root="$HOME/.Garmin/ConnectIQ/Sdks"
    [ "$(uname)" = Darwin ] && sdk_root="$HOME/Library/Application Support/Garmin/ConnectIQ/Sdks"
    latest="$(ls -d "$sdk_root"/*/ 2>/dev/null | sort -V | tail -1)"
    if [ -z "$latest" ]; then
        echo "Connect IQ SDK not found. Install it with the SDK Manager, or set CIQ_SDK." >&2
        exit 1
    fi
    monkeyc="${latest}bin/monkeyc"
fi

if [ -z "${TESSIE_TOKEN:-}" ]; then
    echo "Set TESSIE_TOKEN to your Tessie API token (tessie.com > Settings > API)." >&2
    exit 1
fi
if [[ ! "$TESSIE_TOKEN" =~ ^[A-Za-z0-9._-]+$ ]] || [[ ! "${TESSIE_VIN:-}" =~ ^[A-Za-z0-9]*$ ]]; then
    echo "TESSIE_TOKEN or TESSIE_VIN contains unexpected characters." >&2
    exit 1
fi

if [ ! -f "$key" ]; then
    echo "Creating a developer signing key at $key"
    openssl genrsa -out "$key.pem" 4096 2>/dev/null
    openssl pkcs8 -topk8 -inform PEM -outform DER -in "$key.pem" -out "$key" -nocrypt
    rm "$key.pem"
fi

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
cp -R "$here/manifest.xml" "$here/monkey.jungle" "$here/source" "$here/resources" "$work/"
cat > "$work/resources/settings/properties.xml" <<XML
<properties>
    <property id="tessieToken" type="string">$TESSIE_TOKEN</property>
    <property id="vin" type="string">${TESSIE_VIN:-}</property>
</properties>
XML

mkdir -p "$here/bin"
"$monkeyc" -d "$device" -f "$work/monkey.jungle" -o "$out" -y "$key"
echo "Built $out"
