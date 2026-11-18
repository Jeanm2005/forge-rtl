#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

root=$PWD
platform=$root/build/physical-platform
mkdir -p "$platform"
cd "$platform"

platform_rev=5cbd5178dc15264dbb2c6233922953347b6de92c
platform_url=https://raw.githubusercontent.com/The-OpenROAD-Project/OpenROAD-flow-scripts/$platform_rev/flow/platforms/nangate45
openroad_url=https://github.com/Precision-Innovations/OpenROAD/releases/download/2024-12-14/openroad_2.0-17598-ga008522d8_amd64-ubuntu-22.04.deb
openroad_sha=40ed178396b0276a5d5dfbbe695c9de9aac9088157a6655be02b39a0cef07207

download() {
    local url=$1 file=$2 expected=$3
    if ! printf '%s  %s\n' "$expected" "$file" | sha256sum -c --status; then
        local partial
        partial=$(mktemp "$file.XXXXXX")
        curl --fail --location --retry 3 --connect-timeout 15 --max-time 300 \
            "$url" -o "$partial"
        printf '%s  %s\n' "$expected" "$partial" | sha256sum -c
        mv "$partial" "$file"
    fi
}

while read -r digest file; do
    case "$file" in
        *.lib) directory=lib ;;
        *.lef) directory=lef ;;
        *) exit 1 ;;
    esac
    download "$platform_url/$directory/$file" "$file" "$digest"
done < "$root/physical/dot4/platform.sha256"
download "$openroad_url" openroad.deb "$openroad_sha"
